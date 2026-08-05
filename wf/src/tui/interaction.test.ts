import assert from 'node:assert/strict';
import test from 'node:test';
import type {NodeSummary, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {
  actionableNodes,
  approveAction,
  basicConsoleKeyEvent,
  boundActionableNode,
  initialConsoleInteractionState,
  resumeActionForMode,
  selectedActionableNode,
  selectedNodeId,
  transitionConsoleState,
  type ActionableNode,
} from './interaction.js';

const createdAt = '2026-08-06T00:00:00Z';
const nodes: Record<string, NodeSummary> = {
  approve: {id: 'approve', type: 'approval', phase: 'waiting', reason: 'approval_required'},
  input: {id: 'input', type: 'agent', phase: 'waiting', reason: 'agent_waiting_input', currentAttempt: 1},
  failed: {id: 'failed', type: 'agent', phase: 'completed', conclusion: 'failed', currentAttempt: 2},
  unknown: {id: 'unknown', type: 'agent', phase: 'completed', conclusion: 'indeterminate', currentAttempt: 3},
  pending: {id: 'pending', type: 'agent', phase: 'pending'},
};
const nodeIds = Object.keys(nodes);
const run: WorkflowSnapshot = {protocolVersion: 2, id: 'run-1', workflowName: 'fixture', project: 'p', backend: 'direct', phase: 'waiting', topologicalOrder: nodeIds, nodes, cancelRequested: false, stateDir: 'state', createdAt, updatedAt: createdAt};
const view: RunStatusView = {protocolVersion: 2, legacy: false, run};

function reconcile(state = initialConsoleInteractionState, actionTargets = actionableNodes(view)) {
  return transitionConsoleState(state, {type: 'reconcile', nodeIds, actionTargets});
}

test('actionable nodes are derived only from Engine retry and approval state', () => {
  assert.deepEqual(actionableNodes(view), [
    {nodeId: 'approve', kind: 'approval', duplicateRisk: false},
    {nodeId: 'input', kind: 'retry', duplicateRisk: false},
    {nodeId: 'failed', kind: 'retry', duplicateRisk: false},
    {nodeId: 'unknown', kind: 'retry', duplicateRisk: true},
  ]);
});

test('visual navigation traverses every workflow node and Enter folds detail', () => {
  const targets = actionableNodes(view); let state = reconcile();
  assert.equal(selectedNodeId(state, nodeIds), 'approve');
  for (const expected of ['input', 'failed', 'unknown', 'pending', 'approve']) {
    state = transitionConsoleState(state, basicConsoleKeyEvent('j', {upArrow: false, downArrow: false, escape: false}, nodeIds)!);
    assert.equal(state.selectedNodeId, expected);
  }
  state = transitionConsoleState(state, basicConsoleKeyEvent('k', {upArrow: false, downArrow: false, escape: false}, nodeIds)!);
  assert.equal(state.selectedNodeId, 'pending');
  assert.equal(selectedActionableNode(state, targets), undefined, 'a visually selected node need not be actionable');
  state = transitionConsoleState(state, basicConsoleKeyEvent('', {upArrow: false, downArrow: false, escape: false, return: true}, nodeIds)!);
  assert.equal(state.detailExpanded, false);
});

test('reject input stays pinned by nodeId, kind, and duplicateRisk while visual data changes', () => {
  const targets = actionableNodes(view); let state = reconcile();
  state = transitionConsoleState(state, {type: 'begin-reject', target: targets[0]!});
  state = transitionConsoleState(state, {type: 'append-reason', text: '需要\n调整'});
  assert.equal(state.rejectReason, '需要调整');
  const inserted: ActionableNode = {nodeId: 'new-retry', kind: 'retry', duplicateRisk: false};
  state = transitionConsoleState(state, {type: 'reconcile', nodeIds: ['new-node', ...nodeIds], actionTargets: [inserted, ...targets]});
  assert.equal(state.mode, 'reject'); assert.equal(state.selectedNodeId, 'approve');
  assert.deepEqual(boundActionableNode(state, [inserted, ...targets]), targets[0]);
  assert.deepEqual(resumeActionForMode(state, [inserted, ...targets]), {type: 'reject', nodeId: 'approve', reason: '需要调整'});
  const attemptedMove = transitionConsoleState(state, {type: 'move', delta: 1, nodeIds});
  assert.equal(attemptedMove.selectedNodeId, 'approve', 'action input cannot be redirected by visual navigation');
});

test('disappearing or identity-changed targets cancel action mode without retargeting', () => {
  const targets = actionableNodes(view); let state = reconcile();
  state = transitionConsoleState(state, {type: 'begin-retry', target: targets[3]!});
  assert.deepEqual(resumeActionForMode(state, targets), {type: 'retry', nodeId: 'unknown', acknowledgeDuplicateRisk: true});
  const changed = targets.map(target => target.nodeId === 'unknown' ? {...target, duplicateRisk: false} : target);
  assert.equal(resumeActionForMode(state, changed), undefined);
  state = transitionConsoleState(state, {type: 'reconcile', nodeIds, actionTargets: changed});
  assert.equal(state.mode, 'idle'); assert.equal(state.actionTarget, undefined);
});

test('selection reconciliation preserves identity and chooses a nearby node when it disappears', () => {
  let state = reconcile();
  state = {...state, selectedIndex: 3, selectedNodeId: 'unknown'};
  state = transitionConsoleState(state, {type: 'reconcile', nodeIds: ['approve', 'input', 'failed', 'pending'], actionTargets: actionableNodes(view)});
  assert.equal(state.selectedIndex, 3); assert.equal(state.selectedNodeId, 'pending');
  state = transitionConsoleState(state, {type: 'reconcile', nodeIds: [], actionTargets: []});
  assert.equal(state.selectedIndex, 0); assert.equal(state.selectedNodeId, undefined);
});

test('approve, reject, retry, cancel, Escape, and backspace remain pure transitions', () => {
  const targets = actionableNodes(view);
  assert.deepEqual(approveAction(targets[0]), {type: 'approve', nodeId: 'approve'}); assert.equal(approveAction(targets[1]), undefined);
  let state = transitionConsoleState(reconcile(), {type: 'begin-reject', target: targets[0]!});
  state = transitionConsoleState(state, {type: 'append-reason', text: 'not yet'}); state = transitionConsoleState(state, {type: 'backspace'});
  assert.equal(state.rejectReason, 'not ye');
  state = transitionConsoleState(state, {type: 'escape'}); assert.equal(state.mode, 'idle'); assert.equal(state.rejectReason, '');
  state = transitionConsoleState(state, {type: 'begin-cancel'}); assert.equal(state.mode, 'cancel-confirm');
  state = transitionConsoleState(state, {type: 'submitted'}); assert.equal(state.mode, 'idle');
});
