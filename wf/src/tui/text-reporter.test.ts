import assert from 'node:assert/strict';
import test from 'node:test';
import type {RunEvent, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {exitCodeForSnapshot, TextReporter, writeStatus} from './text-reporter.js';

const snapshot: WorkflowSnapshot = {protocolVersion: 2, id: 'run-1', workflowName: 'sample', phase: 'completed', conclusion: 'succeeded', project: 'p', backend: 'codex', summary: 'done', topologicalOrder: ['a'], nodes: {a: {id: 'a', type: 'agent', phase: 'completed', conclusion: 'succeeded'}}, cancelRequested: false, stateDir: 'state', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:01Z'};

test('plain reporter includes lifecycle, elapsed, summary, and state directory', () => {let output = ''; const reporter = new TextReporter({write(text) {output += text}}); const event: RunEvent = {protocolVersion: 2, runId: 'run-1', sequence: 2, type: 'node.running', phase: 'running', nodeId: 'a', nodePhase: 'running', message: 'working', timestamp: snapshot.updatedAt}; reporter.started({...snapshot, phase: 'created', conclusion: undefined}); reporter.event(event); reporter.finished(snapshot, 2500); assert.match(output, /run run-1/); assert.match(output, /conclusion=succeeded/); assert.match(output, /elapsed=2s/); assert.match(output, /summary=done/); assert.match(output, /state=state/)})

test('exit codes distinguish terminal conclusions and waiting', () => {assert.equal(exitCodeForSnapshot(snapshot), 0); for (const [conclusion, code] of [['failed', 1], ['rejected', 2], ['cancelled', 3], ['indeterminate', 5]] as const) assert.equal(exitCodeForSnapshot({...snapshot, conclusion}), code); assert.equal(exitCodeForSnapshot({...snapshot, phase: 'waiting', conclusion: undefined}), 4)})

test('status renders parallel active attempts, approvals, and diagnostics in supplied order', () => {
  let output = '';
  const view: RunStatusView = {protocolVersion: 2, legacy: false, run: {...snapshot, phase: 'running', effectiveConcurrency: 2}, nodes: [
    {protocolVersion: 2, runId: 'run-1', id: 'a', type: 'agent', phase: 'running', currentAttempt: 1, createdAt: snapshot.createdAt, updatedAt: snapshot.updatedAt},
    {protocolVersion: 2, runId: 'run-1', id: 'b', type: 'agent', phase: 'waiting', reason: 'cancel_failed', diagnostic: 'not confirmed', currentAttempt: 1, createdAt: snapshot.createdAt, updatedAt: snapshot.updatedAt},
  ], activeAttempts: [
    {protocolVersion: 2, runId: 'run-1', nodeId: 'a', number: 1, phase: 'running', backend: 'direct', promptHash: 'a', startedAt: snapshot.createdAt, updatedAt: snapshot.updatedAt},
    {protocolVersion: 2, runId: 'run-1', nodeId: 'b', number: 1, phase: 'waiting', reason: 'cancel_failed', backend: 'direct', promptHash: 'b', startedAt: snapshot.createdAt, updatedAt: snapshot.updatedAt},
  ], waitingApprovals: [{id: 'approve', type: 'approval', phase: 'waiting', reason: 'approval_required', diagnostic: 'approve?'}], diagnostics: [{nodeId: 'b', reason: 'cancel_failed', message: 'not confirmed'}]};
  writeStatus(view, {write(text) {output += text}});
  assert.match(output, /driver=codex target=local capacity=2/);
  assert.match(output, /active node=a/);
  assert.match(output, /active node=b/);
  assert.match(output, /approval node=approve/);
  assert.match(output, /diagnostic node=b reason=cancel_failed/);
});
