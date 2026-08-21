import assert from 'node:assert/strict';
import test from 'node:test';
import {applicationRunToStatus} from './application.js';
import type {RoutingPreview, RunGetResponse, WorkflowExplainResponse, WorkflowValidateResponse} from './application.js';
import type {ModelCapability} from './types.js';

const preview: RoutingPreview = {nodeId: 'work', driver: 'codex', target: 'local', requirement: {
  schemaVersion: 'fishyume.routing-requirement/v1' as const, capabilities: ['repo_edit', 'repo_read', 'structured_output', 'tool_use'] as ModelCapability[],
  complexity: 'standard' as const, quality: 'balanced' as const, latency: 'balanced' as const,
  maxCostUnits: 20, maxContextBytes: 131072, maxOutputBytes: 32768, allowModelFallback: false,
}, decision: {schemaVersion: 'fishyume.routing-decision/v1' as const, catalogHash: 'a'.repeat(64), requirement: {
  schemaVersion: 'fishyume.routing-requirement/v1' as const, capabilities: ['repo_edit', 'repo_read', 'structured_output', 'tool_use'] as ModelCapability[],
  complexity: 'standard' as const, quality: 'balanced' as const, latency: 'balanced' as const,
  maxCostUnits: 20, maxContextBytes: 131072, maxOutputBytes: 32768, allowModelFallback: false,
}, selected: {driver: 'codex', provider: 'local', model: 'gpt-5.6-luna'}, reasonCodes: ['capability_match'], budget: {maxCostUnits: 20, contextBytes: 131072, outputBytes: 32768}, fallbackPolicy: {mode: 'none' as const, maxAttempts: 1, requireNoSideEffect: false, requireApproval: false}}};

test('M6.7 workflow projections carry identical deterministic routing previews', () => {
  const validate: WorkflowValidateResponse = {apiVersion: 'fishyume.application/v1', workflowSchemaVersion: 'fishyume/v2', valid: true, issues: [], capabilityGaps: [], routingPreviews: [preview], warnings: [], routingRequirements: [{nodeId: 'work', requirement: preview.requirement}]};
  const explain: WorkflowExplainResponse = {apiVersion: 'fishyume.application/v1', workflowSchemaVersion: 'fishyume/v2', name: 'preview', topologicalOrder: ['work'], parallelLayers: [['work']], nodes: [], capabilityGaps: [], routingPreviews: [preview], warnings: []};
  assert.deepEqual(validate.routingPreviews, explain.routingPreviews);
  assert.equal(validate.routingPreviews[0]?.decision?.selected.model, 'gpt-5.6-luna');
});

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
        routingDecision: {schemaVersion: 'fishyume.routing-decision/v1', catalogHash: 'b'.repeat(64),
          requirement: {schemaVersion: 'fishyume.routing-requirement/v1', capabilities: ['repo_edit', 'repo_read', 'structured_output', 'tool_use'], complexity: 'standard', quality: 'balanced', latency: 'balanced', maxCostUnits: 101, maxContextBytes: 131072, maxOutputBytes: 32768, allowModelFallback: true},
          selected: {driver: 'codex', provider: 'local', model: 'gpt-5.6-luna'}, reasonCodes: ['capability_match', 'fallback_declared'], budget: {maxCostUnits: 101, contextBytes: 131072, outputBytes: 32768},
          fallback: [{driver: 'codex', provider: 'local', model: 'gpt-5.6'}], fallbackPolicy: {mode: 'eligible', maxAttempts: 2, requireNoSideEffect: true, requireApproval: true}},
        routingUsage: {schemaVersion: 'fishyume.routing-usage/v1', target: {driver: 'codex', provider: 'local', model: 'gpt-5.6-luna'}, routeIndex: 0, costUnits: 1, cumulativeCostUnits: 1}, sideEffectStatus: 'unknown',
        context: {schemaVersion: 'fishyume.context-manifest/v2', compilerVersion: 'context-compiler/v2', hash: 'a'.repeat(64),
          budget: {totalBytes: 1024}, usage: {totalBytes: 128}, components: [{id: 'node-task', kind: 'node_task', tier: 'required', truncation: 'none'}], omissions: ['memory-secret'], truncated: false},
        activity: {schemaVersion: 'fishyume.attempt-activity/v1', summary: '正在执行命令：go test ./...', items: [{kind: 'command', status: 'running', message: '正在执行命令：go test ./...'}], truncated: false},
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
  assert.equal(view.activeAttempts?.[0]?.activity?.summary, '正在执行命令：go test ./...');
  assert.equal(view.attempts?.[0]?.routingDecision?.selected.model, 'gpt-5.6-luna');
  assert.equal(view.attempts?.[0]?.routingDecision?.fallback?.[0]?.model, 'gpt-5.6');
  assert.equal(view.attempts?.[0]?.routingUsage?.cumulativeCostUnits, 1);
  assert.equal(view.attempts?.[0]?.sideEffectStatus, 'unknown');
});
