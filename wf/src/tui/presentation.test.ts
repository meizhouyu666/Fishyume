import assert from 'node:assert/strict';
import test from 'node:test';
import type {NodeSummary, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {detectColorMode, statusBadgeText, statusForNode, type SemanticStatus} from './design-tokens.js';
import {assertWidth, displayWidth, fitText, terminalSize} from './layout.js';
import {renderRunText} from './presentation.js';

const createdAt = '2026-08-06T00:00:00Z';
const nodes: Record<string, NodeSummary> = {
  plan: {id: 'plan', type: 'agent', phase: 'running', currentAttempt: 1},
  research: {id: 'research-with-a-long-readable-node-name', type: 'agent', phase: 'running', currentAttempt: 2},
  approve: {id: 'approve', type: 'approval', phase: 'waiting', reason: 'approval_required', diagnostic: 'Approve the implementation plan before continuing.'},
  implement: {id: 'implement', type: 'agent', phase: 'pending'},
  verify: {id: 'verify', type: 'agent', phase: 'skipped', reason: 'upstream_failed'},
};
const run: WorkflowSnapshot = {protocolVersion: 2, id: 'run-0123456789', workflowName: 'parallel-productization', project: 'E:/very/long/project/path', backend: 'direct', phase: 'running', effectiveConcurrency: 2, topologicalOrder: Object.keys(nodes), nodes, cancelRequested: false, stateDir: 'E:/state/fishyume/run-0123456789', createdAt, updatedAt: createdAt};
const view: RunStatusView = {protocolVersion: 2, legacy: false, run, activeAttempts: [
  {protocolVersion: 2, runId: run.id, nodeId: 'plan', number: 1, phase: 'running', backend: 'direct', launchState: 'handle_persisted', execution: {backend: 'direct', schemaVersion: 1, id: 'pid:12345'}, promptHash: 'a', startedAt: createdAt, updatedAt: createdAt},
  {protocolVersion: 2, runId: run.id, nodeId: 'research-with-a-long-readable-node-name', number: 2, phase: 'running', backend: 'direct', launchState: 'handle_persisted', execution: {backend: 'direct', schemaVersion: 1, id: 'pid:67890'}, promptHash: 'b', startedAt: createdAt, updatedAt: createdAt},
], waitingApprovals: [nodes.approve], diagnostics: [{nodeId: 'approve', reason: 'approval_required', message: 'Approve the implementation plan before continuing.'}]};

test('status tokens encode meaning without relying on color', () => {
  const fixtures: Array<[NodeSummary, SemanticStatus, string]> = [
    [{id: 'run', type: 'agent', phase: 'running'}, 'running', '>>'], [{id: 'wait', type: 'agent', phase: 'waiting'}, 'waiting', '..'],
    [{id: 'approval', type: 'approval', phase: 'waiting'}, 'approval', '??'], [{id: 'failed', type: 'agent', phase: 'completed', conclusion: 'failed'}, 'failed', '!!'],
    [{id: 'unknown', type: 'agent', phase: 'completed', conclusion: 'indeterminate'}, 'indeterminate', '!?'], [{id: 'cancelled', type: 'agent', phase: 'completed', conclusion: 'cancelled'}, 'cancelled', '[]'],
    [{id: 'ok', type: 'agent', phase: 'completed', conclusion: 'succeeded'}, 'succeeded', 'OK'], [{id: 'skip', type: 'agent', phase: 'skipped'}, 'skipped', '--'],
  ];
  for (const [node, status, symbol] of fixtures) {assert.equal(statusForNode(node), status); assert.match(statusBadgeText(status), new RegExp(symbol.replace(/[\[\]?*!]/g, '\\$&')))}
});

test('run presentation remains bounded and informative at 80, 120, and 160 columns', () => {
  for (const width of [80, 120, 160]) {
    const text = renderRunText(view, width, 65_000); const lines = text.split('\n');
    assert.equal(assertWidth(lines, width), true, `${width}: ${lines.find(line => displayWidth(line) > width)}`);
    assert.match(text, /ACTIVE ATTEMPTS \(2\)/); assert.match(text, /APPROVALS \(1\)/); assert.match(text, /DIAGNOSTICS \(1\)/); assert.match(text, /2 active/); assert.match(text, /capacity 2/);
  }
  assert.equal(terminalSize(80), 'narrow'); assert.equal(terminalSize(120), 'medium'); assert.equal(terminalSize(160), 'wide');
});

test('narrow rows wrap diagnostics instead of overflowing or hiding status', () => {
  const text = renderRunText(view, 80, 1000); assert.match(text, /\[\?\? APPROVAL/); assert.match(text, /approval_required/); assert.match(text, /approve: fishyume resume/); assert.equal(assertWidth(text.split('\n'), 80), true);
});

test('monochrome detection and unicode truncation degrade predictably', () => {
  assert.equal(detectColorMode({getColorDepth: () => 24}, {NO_COLOR: '1'}), 'mono'); assert.equal(detectColorMode({getColorDepth: () => 8}, {}), 'ansi256'); assert.equal(detectColorMode({getColorDepth: () => 4}, {}), 'ansi16');
  const text = fitText('并行工作流 diagnostic', 12); assert.equal(displayWidth(text), 12); assert.match(text, /…$/);
});
