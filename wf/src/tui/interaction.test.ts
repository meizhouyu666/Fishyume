import assert from 'node:assert/strict';
import test from 'node:test';
import type {NodeSummary, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {assertWidth} from './layout.js';
import {actionableNodes, approveAction, basicConsoleKeyEvent, consolePanelLines, initialConsoleInteractionState, resumeActionForMode, transitionConsoleState} from './interaction.js';

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
  let state = initialConsoleInteractionState;
  for (const [input, key, expected] of [
    ['k', {upArrow: false, downArrow: false, escape: false}, 3],
    ['', {upArrow: false, downArrow: true, escape: false}, 0],
    ['j', {upArrow: false, downArrow: false, escape: false}, 1],
    ['', {upArrow: true, downArrow: false, escape: false}, 0],
  ] as const) {
    state = transitionConsoleState(state, basicConsoleKeyEvent(input, key, 4)!); assert.equal(state.selectedIndex, expected);
  }
  state = transitionConsoleState(state, basicConsoleKeyEvent('?', {upArrow: false, downArrow: false, escape: false}, 4)!); assert.equal(state.helpVisible, true);
  state = transitionConsoleState(state, {type: 'begin-reject'});
  state = transitionConsoleState(state, {type: 'append-reason', text: '需要\n调整'});
  state = transitionConsoleState(state, {type: 'backspace'}); assert.equal(state.rejectReason, '需要调');
  state = transitionConsoleState(state, basicConsoleKeyEvent('', {upArrow: false, downArrow: false, escape: true}, 4)!); assert.equal(state.mode, 'idle'); assert.equal(state.rejectReason, '');
  state = {...state, selectedIndex: 3};
  state = transitionConsoleState(state, {type: 'reconcile', count: 2}); assert.equal(state.selectedIndex, 1);
  assert.equal(state.mode, 'idle');
  state = transitionConsoleState(state, {type: 'reconcile', count: 0}); assert.equal(state.selectedIndex, 0);
});

test('reject and retry confirmations build exact RPC actions', () => {
  const targets = actionableNodes(view);
  assert.deepEqual(approveAction(targets[0]), {type: 'approve', nodeId: 'approve'});
  assert.equal(approveAction(targets[1]), undefined);
  let reject = transitionConsoleState(initialConsoleInteractionState, {type: 'begin-reject'});
  assert.deepEqual(resumeActionForMode(reject, targets[0]!), {type: 'reject', nodeId: 'approve', reason: ''});
  reject = transitionConsoleState(reject, {type: 'append-reason', text: 'not yet'});
  assert.deepEqual(resumeActionForMode(reject, targets[0]!), {type: 'reject', nodeId: 'approve', reason: 'not yet'});
  const retry = transitionConsoleState(initialConsoleInteractionState, {type: 'begin-retry', duplicateRisk: false});
  assert.deepEqual(resumeActionForMode(retry, targets[2]!), {type: 'retry', nodeId: 'failed', acknowledgeDuplicateRisk: false});
  const risky = transitionConsoleState(initialConsoleInteractionState, {type: 'begin-retry', duplicateRisk: true});
  assert.deepEqual(resumeActionForMode(risky, targets[3]!), {type: 'retry', nodeId: 'unknown', acknowledgeDuplicateRisk: true});
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
