import assert from 'node:assert/strict';
import test from 'node:test';
import {applicationRunToStatus} from './application.js';
import type {RunGetResponse} from './application.js';

test('M5.4 application projection exposes bounded context metadata only', () => {
  const marker = 'M54-RPC-CONTENT-MARKER';
  const response: RunGetResponse = {
    apiVersion: 'fishyume.application/v1',
    run: {
      runId: 'run-m54', workflowName: 'm54', project: 'C:/project', driver: 'codex', target: 'local', phase: 'running',
      stateVersion: 4, createdAt: '2026-08-19T00:00:00Z', updatedAt: '2026-08-19T00:00:01Z', cancelRequested: false,
      effectiveConcurrency: 1, topologicalOrder: ['work'], deprecationWarnings: [],
      nodes: [{nodeId: 'work', type: 'agent', phase: 'running', currentAttempt: 1, attempt: {
        number: 1, phase: 'running', driver: 'codex', target: 'local', contextHash: 'a'.repeat(64),
        context: {schemaVersion: 'fishyume.context-manifest/v2', compilerVersion: 'context-compiler/v2', hash: 'a'.repeat(64),
          budget: {totalBytes: 1024}, usage: {totalBytes: 128}, components: [{id: 'node-task', kind: 'node_task', tier: 'required', truncation: 'none'}], omissions: ['memory-secret'], truncated: false},
        startedAt: '2026-08-19T00:00:00Z', updatedAt: '2026-08-19T00:00:01Z',
      }, diagnostic: marker}],
    },
  };
  const view = applicationRunToStatus(response);
  const encoded = JSON.stringify(view);
  assert.match(encoded, /context-compiler\/v2/);
  assert.doesNotMatch(encoded, /content|contentHash|provenance|promptHash/);
  assert.match(encoded, /node_task/);
  assert.match(encoded, new RegExp(marker));
  assert.equal(view.activeAttempts?.[0]?.context?.components.length, 1);
});
