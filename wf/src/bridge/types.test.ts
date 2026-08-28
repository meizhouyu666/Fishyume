import assert from 'node:assert/strict';
import test from 'node:test';
import type {AttemptSnapshot, NodeSnapshot, RunStatusView} from './types.js';

test('node and attempt snapshots accept generic handles and missing legacy state schema versions', () => {
  const legacyNode: NodeSnapshot = {
    protocolVersion: 2, runId: 'run-1', id: 'plan', type: 'agent', phase: 'pending',
    createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z',
  };
  const currentNode: NodeSnapshot = {...legacyNode, stateSchemaVersion: 1};
  const legacyAttempt: AttemptSnapshot = {
    protocolVersion: 2, runId: 'run-1', nodeId: 'plan', number: 1, phase: 'running',
    backend: 'codex', promptHash: 'hash', launchState: 'handle_persisted',
    startedAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z',
  };
  const currentAttempt: AttemptSnapshot = {
    protocolVersion: 2, stateSchemaVersion: 1, runId: 'run-2', nodeId: 'plan', number: 1, phase: 'running', backend: 'codex',
    launchState: 'handle_persisted', execution: {backend: 'codex', schemaVersion: 1, id: 'execution-2', data: {processId: 'process-2'}},
    resultConsumed: false, promptHash: 'hash', startedAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z',
  };

  assert.equal(legacyNode.stateSchemaVersion, undefined);
  assert.equal(currentNode.stateSchemaVersion, 1);
  assert.equal(legacyAttempt.stateSchemaVersion, undefined);
  assert.equal(currentAttempt.stateSchemaVersion, 1);
  assert.equal(legacyAttempt.launchState, 'handle_persisted');
  assert.equal(currentAttempt.launchState, 'handle_persisted');
  assert.equal(currentAttempt.execution?.backend, 'codex');

  const parallel: RunStatusView = {
    protocolVersion: 2, legacy: false,
    activeNodes: [legacyNode, {...legacyNode, id: 'review'}],
    activeAttempts: [currentAttempt],
    waitingApprovals: [],
    diagnostics: [{nodeId: 'plan', reason: 'agent_waiting_input', message: 'needs input'}],
  };
  assert.equal(parallel.activeNodes?.length, 2);
});
