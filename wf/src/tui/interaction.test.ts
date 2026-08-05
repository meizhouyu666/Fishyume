import assert from 'node:assert/strict';
import test from 'node:test';
import type {NodeSummary, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {assertWidth} from './layout.js';
import {actionableNodes, approveAction, basicConsoleKeyEvent, boundActionableNode, consolePanelLines, initialConsoleInteractionState, resumeActionForMode, selectedActionableNode, transitionConsoleState, type ActionableNode} from './interaction.js';

const createdAt = '2026-08-06T00:00:00Z';
const nodes: Record<string, NodeSummary> = {
  approve: {id: 'approve', type: 'approval', phase: 'waiting', reason: 'approval_required'},
  input: {id: 'input', type: 'agent', phase: 'waiting', reason: 'agent_waiting_input', currentAttempt: 1},
  failed: {id: 'failed', type: 'agent', phase: 'completed', conclusion: 'failed', currentAttempt: 2},
  unknown: {id: 'unknown', type: 'agent', phase: 'completed', conclusion: 'indeterminate', currentAttempt: 3},
  pending: {id: 'pending', type: 'agent', phase: 'pending'},
};
const run: WorkflowSnapshot = {protocolVersion: 2, id: 'run-1', workflowName: 'fixture', project: 'p', backend: 'direct', phase: 'waiting', topologicalOrder: Object.keys(nodes), nodes, cancelRequested: false, stateDir: 'state', createdAt, updatedAt: createdAt};
const view: RunStatusView = {protocolVersion: 2, legacy: false, run};

test('actionable nodes are derived only from Engine retry and approval state', () => {
  assert.deepEqual(actionableNodes(view), [
    {nodeId: 'approve', kind: 'approval', duplicateRisk: false},
    {nodeId: 'input', kind: 'retry', duplicateRisk: false},
    {nodeId: 'failed', kind: 'retry', duplicateRisk: false},
    {nodeId: 'unknown', kind: 'retry', duplicateRisk: true},
  ]);
});

test('navigation, help, Escape, reject input, and disappearing selections are pure transitions', () => {
  const targets = actionableNodes(view); let state = transitionConsoleState(initialConsoleInteractionState, {type: 'reconcile', targets});
  for (const [input, key, expected] of [
    ['k', {upArrow: false, downArrow: false, escape: false}, 3],
    ['', {upArrow: false, downArrow: true, escape: false}, 0],
    ['j', {upArrow: false, downArrow: false, escape: false}, 1],
    ['', {upArrow: true, downArrow: false, escape: false}, 0],
  ] as const) {
    state = transitionConsoleState(state, basicConsoleKeyEvent(input, key, targets)!); assert.equal(state.selectedIndex, expected);
  }
  state = transitionConsoleState(state, basicConsoleKeyEvent('?', {upArrow: false, downArrow: false, escape: false}, targets)!); assert.equal(state.helpVisible, true);
  state = transitionConsoleState(state, {type: 'begin-reject', target: targets[0]!});
  state = transitionConsoleState(state, {type: 'append-reason', text: '需要\n调整'});
  state = transitionConsoleState(state, {type: 'backspace'}); assert.equal(state.rejectReason, '需要调');
  state = transitionConsoleState(state, basicConsoleKeyEvent('', {upArrow: false, downArrow: false, escape: true}, targets)!); assert.equal(state.mode, 'idle'); assert.equal(state.rejectReason, '');
  state = {...state, selectedIndex: 3, selectedNodeId: 'unknown'};
  state = transitionConsoleState(state, {type: 'reconcile', targets: targets.slice(2)}); assert.equal(state.selectedIndex, 1); assert.equal(state.selectedNodeId, 'unknown');
  state = transitionConsoleState(state, {type: 'reconcile', targets: []}); assert.equal(state.selectedIndex, 0); assert.equal(state.selectedNodeId, undefined);
});

test('reject and retry confirmations build exact RPC actions', () => {
  const targets = actionableNodes(view);
  assert.deepEqual(approveAction(targets[0]), {type: 'approve', nodeId: 'approve'});
  assert.equal(approveAction(targets[1]), undefined);
  let reject = transitionConsoleState(initialConsoleInteractionState, {type: 'begin-reject', target: targets[0]!});
  assert.deepEqual(resumeActionForMode(reject, targets), {type: 'reject', nodeId: 'approve', reason: ''});
  reject = transitionConsoleState(reject, {type: 'append-reason', text: 'not yet'});
  assert.deepEqual(resumeActionForMode(reject, targets), {type: 'reject', nodeId: 'approve', reason: 'not yet'});
  const retry = transitionConsoleState(initialConsoleInteractionState, {type: 'begin-retry', target: targets[2]!});
  assert.deepEqual(resumeActionForMode(retry, targets), {type: 'retry', nodeId: 'failed', acknowledgeDuplicateRisk: false});
  const risky = transitionConsoleState(initialConsoleInteractionState, {type: 'begin-retry', target: targets[3]!});
  assert.deepEqual(resumeActionForMode(risky, targets), {type: 'retry', nodeId: 'unknown', acknowledgeDuplicateRisk: true});
});

test('reject input stays bound while unrelated actionable nodes change', () => {
  const targets = actionableNodes(view);
  let state = transitionConsoleState(initialConsoleInteractionState, {type: 'reconcile', targets});
  state = transitionConsoleState(state, {type: 'begin-reject', target: targets[0]!});
  state = transitionConsoleState(state, {type: 'append-reason', text: 'keep this reason'});
  const inserted: ActionableNode = {nodeId: 'new-retry', kind: 'retry', duplicateRisk: false};
  state = transitionConsoleState(state, {type: 'reconcile', targets: [inserted, ...targets]});
  assert.equal(state.mode, 'reject'); assert.equal(state.rejectReason, 'keep this reason'); assert.equal(state.selectedNodeId, 'approve'); assert.equal(state.selectedIndex, 1);
  assert.deepEqual(boundActionableNode(state, [inserted, ...targets]), targets[0]);
  assert.deepEqual(resumeActionForMode(state, [inserted, ...targets]), {type: 'reject', nodeId: 'approve', reason: 'keep this reason'});
  state = transitionConsoleState(state, {type: 'reconcile', targets: [inserted, targets[0]!, ...targets.slice(2)]});
  assert.equal(state.mode, 'reject'); assert.equal(state.rejectReason, 'keep this reason');
});

test('disappearing or same-index replacement targets cannot receive a pinned reject', () => {
  const targets = actionableNodes(view);
  let state = transitionConsoleState(initialConsoleInteractionState, {type: 'reconcile', targets});
  state = transitionConsoleState(state, {type: 'begin-reject', target: targets[0]!});
  state = transitionConsoleState(state, {type: 'append-reason', text: 'private reason'});
  const replacement: ActionableNode = {nodeId: 'replacement', kind: 'approval', duplicateRisk: false};
  const replaced = [replacement, ...targets.slice(1)];
  assert.equal(resumeActionForMode(state, replaced), undefined, 'effect window must not retarget the action');
  assert.equal(selectedActionableNode(state, replaced), undefined, 'selection must not drift before reconciliation');
  state = transitionConsoleState(state, {type: 'reconcile', targets: replaced});
  assert.equal(state.mode, 'idle'); assert.equal(state.rejectReason, ''); assert.equal(state.actionTarget, undefined);
  assert.equal(selectedActionableNode(state, replaced)?.nodeId, 'replacement');
});

test('retry risk confirmation is pinned and cancelled if identity or risk changes', () => {
  const targets = actionableNodes(view); const unknown = targets[3]!;
  let state = transitionConsoleState(initialConsoleInteractionState, {type: 'reconcile', targets});
  state = {...state, selectedIndex: 3, selectedNodeId: unknown.nodeId};
  state = transitionConsoleState(state, {type: 'begin-retry', target: unknown});
  const unrelated: ActionableNode = {nodeId: 'another', kind: 'approval', duplicateRisk: false};
  state = transitionConsoleState(state, {type: 'reconcile', targets: [unrelated, ...targets]});
  assert.equal(state.mode, 'retry-risk-confirm'); assert.equal(state.selectedNodeId, 'unknown'); assert.equal(state.selectedIndex, 4);
  assert.deepEqual(resumeActionForMode(state, [unrelated, ...targets]), {type: 'retry', nodeId: 'unknown', acknowledgeDuplicateRisk: true});
  const replacement = [unrelated, ...targets.slice(0, 3), {nodeId: 'replacement-risk', kind: 'retry' as const, duplicateRisk: true}];
  assert.equal(resumeActionForMode(state, replacement), undefined, 'same-index risky replacement must not receive confirmation');
  const replacedState = transitionConsoleState(state, {type: 'reconcile', targets: replacement});
  assert.equal(replacedState.mode, 'idle'); assert.equal(replacedState.actionTarget, undefined);
  const riskChanged = [unrelated, ...targets.slice(0, 3), {...unknown, duplicateRisk: false}];
  assert.equal(resumeActionForMode(state, riskChanged), undefined);
  state = transitionConsoleState(state, {type: 'reconcile', targets: riskChanged});
  assert.equal(state.mode, 'idle'); assert.equal(state.actionTarget, undefined);
});

test('console selection, input, confirmation, help, and mono-safe text stay bounded at supported widths', () => {
  const target = actionableNodes(view)[3];
  const states = [
    {...initialConsoleInteractionState, helpVisible: true},
    {...initialConsoleInteractionState, mode: 'reject' as const, rejectReason: 'a reason with 中文'},
    {...initialConsoleInteractionState, mode: 'retry-risk-confirm' as const},
    {...initialConsoleInteractionState, mode: 'cancel-confirm' as const},
  ];
  for (const width of [80, 120, 160]) for (const state of states) {
    const lines = consolePanelLines(state, target, width, false, 'fixture message');
    assert.equal(assertWidth(lines, width), true);
    assert.match(lines.join('\n'), /> selected unknown/);
  }
  assert.match(consolePanelLines(states[2]!, target, 80, false).join('\n'), /DUPLICATE-RISK/);
  assert.match(consolePanelLines(states[0]!, target, 80, false).join('\n'), /Ctrl\+C never cancels/);
});
