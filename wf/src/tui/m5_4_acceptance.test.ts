import assert from 'node:assert/strict';
import test from 'node:test';
import {displayWidth} from './layout.js';
import {renderRunText} from './presentation.js';
import type {RunStatusView} from '../bridge/types.js';

test('M5.4 TUI context detail remains a bounded metadata projection', () => {
  const view: RunStatusView = {
    protocolVersion: 2, legacy: false,
    run: {
      protocolVersion: 2, id: 'run-m54-tui', workflowName: 'm54', project: 'C:/project', resolvedDriver: 'codex', resolvedTarget: 'local',
      phase: 'running', topologicalOrder: ['work'], nodes: {work: {id: 'work', type: 'agent', phase: 'running', currentAttempt: 1}},
      cancelRequested: false, stateDir: 'C:/state', createdAt: '2026-08-19T00:00:00Z', updatedAt: '2026-08-19T00:00:01Z',
    },
    nodes: [{protocolVersion: 2, runId: 'run-m54-tui', id: 'work', type: 'agent', phase: 'running', currentAttempt: 1, createdAt: '2026-08-19T00:00:00Z', updatedAt: '2026-08-19T00:00:01Z'}],
    activeAttempts: [{
      protocolVersion: 2, runId: 'run-m54-tui', nodeId: 'work', number: 1, phase: 'running', resolvedDriver: 'codex', resolvedTarget: 'local',
      context: {schemaVersion: 'fishyume.context-manifest/v2', compilerVersion: 'context-compiler/v2', hash: 'a'.repeat(64), budget: {totalBytes: 131072}, usage: {totalBytes: 1200},
        components: Array.from({length: 128}, (_, index) => ({id: `component-${index.toString().padStart(3, '0')}`, kind: 'node_task', tier: 'required', truncation: 'none'})),
        omissions: Array.from({length: 128}, (_, index) => `omission-${index.toString().padStart(3, '0')}`), truncated: true},
      startedAt: '2026-08-19T00:00:00Z', updatedAt: '2026-08-19T00:00:01Z',
    }],
  };
  const text = renderRunText(view, 80, 1000);
  for (const line of text.split('\n')) assert.ok(displayWidth(line) <= 80, `unbounded TUI line: ${line}`);
  assert.match(text, /context context-compiler\/v2/);
  assert.doesNotMatch(text, /M54-TUI-CONTENT-MARKER|contentHash|provenance/);
});
