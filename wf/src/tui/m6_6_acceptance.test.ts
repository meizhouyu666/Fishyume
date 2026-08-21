import assert from 'node:assert/strict';
import test from 'node:test';
import type {AttemptSnapshot, RunStatusView} from '../bridge/types.js';
import {displayWidth} from './layout.js';
import {renderRunText} from './presentation.js';
import {writeStatus} from './text-reporter.js';

const attempt: AttemptSnapshot = {
  protocolVersion: 2, runId: 'run-m66', nodeId: 'work', number: 2, phase: 'completed', conclusion: 'succeeded', resolvedDriver: 'codex', resolvedTarget: 'local',
  routingDecision: {
    schemaVersion: 'fishyume.routing-decision/v1', catalogHash: 'a'.repeat(64),
    requirement: {schemaVersion: 'fishyume.routing-requirement/v1', capabilities: ['repo_edit', 'repo_read', 'structured_output', 'tool_use'], complexity: 'standard', quality: 'balanced', latency: 'balanced', maxCostUnits: 101, maxContextBytes: 131072, maxOutputBytes: 32768, allowModelFallback: true},
    selected: {driver: 'codex', provider: 'local', model: 'gpt-5.6'}, reasonCodes: ['capability_match', 'fallback_declared', 'fallback_selected'],
    budget: {maxCostUnits: 101, contextBytes: 131072, outputBytes: 32768}, fallbackPolicy: {mode: 'eligible', maxAttempts: 2, requireNoSideEffect: true, requireApproval: true},
  },
  routingUsage: {schemaVersion: 'fishyume.routing-usage/v1', target: {driver: 'codex', provider: 'local', model: 'gpt-5.6'}, routeIndex: 1, costUnits: 100, cumulativeCostUnits: 101},
  sideEffectStatus: 'unknown', startedAt: '2026-08-21T00:00:00Z', updatedAt: '2026-08-21T00:00:01Z', completedAt: '2026-08-21T00:00:01Z',
};

const view: RunStatusView = {
  protocolVersion: 2, legacy: false,
  run: {protocolVersion: 2, id: 'run-m66', workflowName: 'routing-release', project: 'C:/project', resolvedDriver: 'codex', resolvedTarget: 'local', phase: 'completed', conclusion: 'succeeded', summary: 'done', topologicalOrder: ['work'], nodes: {work: {id: 'work', type: 'agent', phase: 'completed', conclusion: 'succeeded', currentAttempt: 2}}, cancelRequested: false, stateDir: 'C:/state', createdAt: '2026-08-21T00:00:00Z', updatedAt: '2026-08-21T00:00:01Z'},
  nodes: [{protocolVersion: 2, runId: 'run-m66', id: 'work', type: 'agent', phase: 'completed', conclusion: 'succeeded', currentAttempt: 2, createdAt: '2026-08-21T00:00:00Z', updatedAt: '2026-08-21T00:00:01Z'}],
  attempts: [attempt],
};

test('M6.6 Chinese topology detail exposes bounded routing evidence', () => {
  for (const width of [80, 120, 160]) {
    const text = renderRunText(view, width, 1000, {selectedNodeId: 'work', detailExpanded: true});
    assert.match(text, /路由.*codex\/local\/gpt-5\.6.*路径 2\/2/);
    assert.match(text, /路由原因/);
    if (width >= 120) assert.match(text, /fallback_selected/);
    assert.match(text, /路由预算.*成本 101\/101.*本次 100/);
    assert.match(text, /回退/);
    if (width === 160) {
      assert.match(text, /eligible/);
      assert.match(text, /需显式批准/);
      assert.match(text, /副作用 unknown/);
    }
    for (const line of text.split('\n')) assert.ok(displayWidth(line) <= width, `unbounded routing line: ${line}`);
  }
});

test('M6.6 plain status emits a stable routing record', () => {
  let output = '';
  writeStatus(view, {write(text) {output += text}});
  assert.match(output, /routing node=work attempt=2 model=gpt-5\.6 route=2\/2 cost=101\/101/);
  assert.match(output, /reasons=capability_match,fallback_declared,fallback_selected fallback=none approval=true sideEffect=unknown/);
});
