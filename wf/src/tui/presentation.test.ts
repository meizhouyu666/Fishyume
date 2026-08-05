import assert from 'node:assert/strict';
import test from 'node:test';
import {canonicalFixture, canonicalVisualFixtures} from './fixtures.js';
import {actionableNodes, initialConsoleInteractionState, type ConsoleInteractionState} from './interaction.js';
import {assertWidth, displayWidth, fitText, terminalSize} from './layout.js';
import {buildRunTextPresentation, renderRunText, type RunPresentationOptions} from './presentation.js';
import {
  colorFor,
  detectColorMode,
  detectSymbolMode,
  headerStatusText,
  rowStatusText,
  statusForNode,
  type ColorMode,
  type SemanticStatus,
} from './design-tokens.js';

function interactionFor(fixture: (typeof canonicalVisualFixtures)[number]): ConsoleInteractionState {
  if (fixture.interaction) return fixture.interaction;
  const index = fixture.view.run?.topologicalOrder.indexOf(fixture.selectedNodeId) ?? 0;
  return {...initialConsoleInteractionState, selectedIndex: Math.max(0, index), selectedNodeId: fixture.selectedNodeId};
}

function optionsFor(fixture: (typeof canonicalVisualFixtures)[number], symbolMode: 'unicode' | 'ascii' = 'unicode'): RunPresentationOptions {
  return {
    selectedNodeId: fixture.selectedNodeId,
    detailExpanded: true,
    symbolMode,
    interactive: true,
    action: {interaction: interactionFor(fixture), actionable: actionableNodes(fixture.view), pending: false},
  };
}

test('compact status syntax carries symbol, short label, and complete Header meaning', () => {
  const fixtures: Array<[Parameters<typeof statusForNode>[0], SemanticStatus, string, string]> = [
    [{id: 'run', type: 'agent', phase: 'running'}, 'running', '● run', 'RUNNING'],
    [{id: 'wait', type: 'agent', phase: 'waiting'}, 'waiting', '◌ wait', 'WAITING'],
    [{id: 'approval', type: 'approval', phase: 'waiting'}, 'approval', '◆ approve', 'APPROVAL'],
    [{id: 'ok', type: 'agent', phase: 'completed', conclusion: 'succeeded'}, 'succeeded', '✓ done', 'SUCCEEDED'],
    [{id: 'failed', type: 'agent', phase: 'completed', conclusion: 'failed'}, 'failed', '! fail', 'FAILED'],
    [{id: 'unknown', type: 'agent', phase: 'completed', conclusion: 'indeterminate'}, 'indeterminate', '? unknown', 'INDETERMINATE'],
    [{id: 'cancelled', type: 'agent', phase: 'completed', conclusion: 'cancelled'}, 'cancelled', '× cancel', 'CANCELLED'],
    [{id: 'skip', type: 'agent', phase: 'skipped'}, 'skipped', '· skip', 'SKIPPED'],
  ];
  for (const [node, status, row, header] of fixtures) {
    assert.equal(statusForNode(node), status); assert.match(rowStatusText(status, 'unicode'), new RegExp(row.replace(/[?]/g, '\\?'))); assert.equal(headerStatusText(status), header);
  }
  assert.match(rowStatusText('running', 'ascii'), /^> run/); assert.match(rowStatusText('succeeded', 'ascii'), /^\+ done/);
});

test('all six canonical scenes remain bounded at 80, 120, and 160 columns in Unicode and ASCII', () => {
  for (const fixture of canonicalVisualFixtures) {
    for (const width of [80, 120, 160]) {
      for (const symbolMode of ['unicode', 'ascii'] as const) {
        const text = renderRunText(fixture.view, width, 138_000, optionsFor(fixture, symbolMode)); const lines = text.split('\n');
        assert.equal(assertWidth(lines, width), true, `${fixture.id}/${width}/${symbolMode}: ${lines.find(line => displayWidth(line) > width)}`);
        assert.match(text, /FISHYUME \/ /); assert.match(text, /settled/); assert.match(text, new RegExp(fixture.selectedNodeId.slice(0, 8)));
        assert.doesNotMatch(text, /ACTIVE ATTEMPTS|APPROVALS \(|DIAGNOSTICS \(|:: WORKFLOW/, 'repeated panel sections must stay removed');
      }
    }
  }
  assert.equal(terminalSize(80), 'narrow'); assert.equal(terminalSize(120), 'medium'); assert.equal(terminalSize(160), 'wide');
});

test('canonical scenes expose their defining operator evidence', () => {
  const concurrent = renderRunText(canonicalFixture('concurrent').view, 120, 138_000, optionsFor(canonicalFixture('concurrent')));
  assert.match(concurrent, /a2.*direct/); assert.match(concurrent, /a1.*ccpanes/); assert.match(concurrent, /2 active/);

  const approval = renderRunText(canonicalFixture('approval').view, 120, 138_000, optionsFor(canonicalFixture('approval')));
  assert.match(approval, /Approve production deployment/); assert.match(approval, /a approve/); assert.match(approval, /r reject/); assert.doesNotMatch(approval, /R retry/);

  const retryable = renderRunText(canonicalFixture('retryable').view, 120, 138_000, optionsFor(canonicalFixture('retryable')));
  assert.match(retryable, /Expected junit\.xml/); assert.match(retryable, /R retry/); assert.match(retryable, /artifacts/);

  const indeterminate = renderRunText(canonicalFixture('indeterminate').view, 120, 138_000, optionsFor(canonicalFixture('indeterminate')));
  assert.match(indeterminate, /ACTION \/ publish-artifact.*duplicate-risk/); assert.match(indeterminate, /repeat external effects/); assert.match(indeterminate, /Enter confirm.*Esc discard/);

  const cancelling = renderRunText(canonicalFixture('cancelling').view, 120, 138_000, optionsFor(canonicalFixture('cancelling')));
  assert.match(cancelling, /CANCELLING/); assert.match(cancelling, /session:remote-9/); assert.doesNotMatch(cancelling, /c cancel/); assert.doesNotMatch(cancelling, /CANCELLED/);

  const terminal = renderRunText(canonicalFixture('terminal').view, 120, 138_000, optionsFor(canonicalFixture('terminal')));
  for (const label of ['done', 'fail', 'cancel', 'reject']) assert.match(terminal, new RegExp(label));
  assert.match(terminal, /summary.*Release stopped/); assert.match(terminal, /next.*fishyume status/); assert.match(terminal, /q exit/); assert.doesNotMatch(terminal, /c cancel|a approve|R retry/);
});

test('Focus Detail folds locally while action detail overrides the folded node view', () => {
  const fixture = canonicalFixture('retryable');
  const folded = buildRunTextPresentation(fixture.view, 80, 138_000, {...optionsFor(fixture), detailExpanded: false});
  assert.equal(folded.detail, undefined);
  const action = {...optionsFor(fixture), detailExpanded: false};
  action.action = {...action.action!, interaction: {...interactionFor(fixture), mode: 'retry-confirm', actionTarget: {nodeId: 'integration-tests', kind: 'retry', duplicateRisk: false}}};
  const focused = buildRunTextPresentation(fixture.view, 80, 138_000, action);
  assert.match(focused.detail?.title ?? '', /ACTION \/ integration-tests/);
});

test('color capability levels preserve semantic roles and mono removes color only', () => {
  assert.equal(detectColorMode({getColorDepth: () => 24}, {NO_COLOR: '1'}), 'mono');
  assert.equal(detectColorMode({getColorDepth: () => 24}, {FISHYUME_THEME: 'mono'}), 'mono');
  assert.equal(detectColorMode({getColorDepth: () => 24}, {}), 'truecolor');
  assert.equal(detectColorMode({getColorDepth: () => 8}, {}), 'ansi256');
  assert.equal(detectColorMode({getColorDepth: () => 4}, {}), 'ansi16');
  for (const mode of ['ansi16', 'ansi256', 'truecolor'] as ColorMode[]) assert.ok(colorFor('danger', mode));
  assert.equal(colorFor('danger', 'mono'), undefined);
  assert.equal(detectSymbolMode({TERM: 'dumb'}), 'ascii'); assert.equal(detectSymbolMode({FISHYUME_ASCII: '1'}), 'ascii'); assert.equal(detectSymbolMode({}), 'unicode');
});

test('CJK, emoji, combining marks, and long paths truncate by display width', () => {
  for (const value of ['并行工作流 diagnostic', 'operator 🐟 console', 'Cafe\u0301 / E:/团队/很长的路径']) {
    const text = fitText(value, 12); assert.ok(displayWidth(text) <= 12); assert.match(text, /…$/);
  }
});
