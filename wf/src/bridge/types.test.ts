import assert from 'node:assert/strict';
import test from 'node:test';
import type {AttemptSnapshot, NodeSnapshot} from './types.js';

test('node and attempt snapshots accept explicit and legacy state schema fields', () => {
  const legacyNode: NodeSnapshot = {
    protocolVersion: 2, runId: 'run-1', id: 'plan', type: 'agent', phase: 'pending',
    createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z',
  };
  const currentNode: NodeSnapshot = {...legacyNode, stateSchemaVersion: 1};
  const legacyAttempt: AttemptSnapshot = {
    protocolVersion: 2, runId: 'run-1', nodeId: 'plan', number: 1, phase: 'running',
    backend: 'ccpanes', promptHash: 'hash', bindingConsumed: false,
    startedAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z',
  };
  const currentAttempt: AttemptSnapshot = {...legacyAttempt, stateSchemaVersion: 1, launchState: 'session_persisted'};

  assert.equal(legacyNode.stateSchemaVersion, undefined);
  assert.equal(currentNode.stateSchemaVersion, 1);
  assert.equal(legacyAttempt.stateSchemaVersion, undefined);
  assert.equal(currentAttempt.stateSchemaVersion, 1);
  assert.equal(currentAttempt.launchState, 'session_persisted');
});
