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
    [{id: 'run', type: 'agent', phase: 'running'}, 'running', '● 运行中', '运行中'],
    [{id: 'wait', type: 'agent', phase: 'waiting'}, 'waiting', '◌ 等待中', '等待处理'],
    [{id: 'approval', type: 'approval', phase: 'waiting'}, 'approval', '◆ 待审批', '需要审批'],
    [{id: 'ok', type: 'agent', phase: 'completed', conclusion: 'succeeded'}, 'succeeded', '✓ 已完成', '已完成'],
    [{id: 'failed', type: 'agent', phase: 'completed', conclusion: 'failed'}, 'failed', '! 已失败', '已失败'],
    [{id: 'unknown', type: 'agent', phase: 'completed', conclusion: 'indeterminate'}, 'indeterminate', '? 待确认', '结果待确认'],
    [{id: 'cancelled', type: 'agent', phase: 'completed', conclusion: 'cancelled'}, 'cancelled', '× 已取消', '已取消'],
    [{id: 'skip', type: 'agent', phase: 'skipped'}, 'skipped', '· 已跳过', '已跳过'],
  ];
  for (const [node, status, row, header] of fixtures) {
    assert.equal(statusForNode(node), status); assert.match(rowStatusText(status, 'unicode'), new RegExp(row.replace(/[?]/g, '\\?'))); assert.equal(headerStatusText(status), header);
  }
  assert.match(rowStatusText('running', 'ascii'), /^> 运行中/); assert.match(rowStatusText('succeeded', 'ascii'), /^\+ 已完成/);
});

test('all six canonical scenes remain bounded at 80, 120, and 160 columns in Unicode and ASCII', () => {
  for (const fixture of canonicalVisualFixtures) {
    for (const width of [80, 120, 160]) {
      for (const symbolMode of ['unicode', 'ascii'] as const) {
        const text = renderRunText(fixture.view, width, 138_000, optionsFor(fixture, symbolMode)); const lines = text.split('\n');
        assert.equal(assertWidth(lines, width), true, `${fixture.id}/${width}/${symbolMode}: ${lines.find(line => displayWidth(line) > width)}`);
        assert.match(text, /FISHYUME \/ /); assert.match(text, /已结束/); assert.match(text, new RegExp(fixture.selectedNodeId.slice(0, 8)));
        assert.doesNotMatch(text, /ACTIVE ATTEMPTS|APPROVALS \(|DIAGNOSTICS \(|:: WORKFLOW/, 'repeated panel sections must stay removed');
      }
    }
  }
  assert.equal(terminalSize(80), 'narrow'); assert.equal(terminalSize(120), 'medium'); assert.equal(terminalSize(160), 'wide');
});

test('canonical scenes expose their defining operator evidence', () => {
  const concurrent = renderRunText(canonicalFixture('concurrent').view, 120, 138_000, optionsFor(canonicalFixture('concurrent')));
  assert.match(concurrent, /第2次.*direct/); assert.match(concurrent, /第1次.*codex/); assert.match(concurrent, /2 个运行中/);

  const approval = renderRunText(canonicalFixture('approval').view, 120, 138_000, optionsFor(canonicalFixture('approval')));
  assert.match(approval, /需要人工审批/); assert.match(approval, /Approve production deploy/); assert.match(approval, /A\/Y 批准/); assert.match(approval, /X\/N 拒绝/); assert.doesNotMatch(approval, /T 重试/);

  const retryable = renderRunText(canonicalFixture('retryable').view, 120, 138_000, optionsFor(canonicalFixture('retryable')));
  assert.match(retryable, /Expected junit\.xml/); assert.match(retryable, /T 重试/); assert.match(retryable, /产物/);

  const indeterminate = renderRunText(canonicalFixture('indeterminate').view, 120, 138_000, optionsFor(canonicalFixture('indeterminate')));
  assert.match(indeterminate, /操作确认 \/ publish-artifact/); assert.match(indeterminate, /重试可能再次产生外部副作用/); assert.match(indeterminate, /外部副作用/); assert.match(indeterminate, /Enter 确认.*Esc 放弃/);

  const cancelling = renderRunText(canonicalFixture('cancelling').view, 120, 138_000, optionsFor(canonicalFixture('cancelling')));
  assert.match(cancelling, /正在取消/); assert.match(cancelling, /codex/); assert.match(cancelling, /Remote session still repo/); assert.doesNotMatch(cancelling, /C 取消任务/); assert.doesNotMatch(cancelling, /已取消/);

  const terminal = renderRunText(canonicalFixture('terminal').view, 120, 138_000, optionsFor(canonicalFixture('terminal')));
  for (const label of ['已完成', '已失败', '已取消', '已拒绝']) assert.match(terminal, new RegExp(label));
  assert.match(terminal, /任务总结.*Release stopped/); assert.match(terminal, /再次查看.*fishyume status/); assert.match(terminal, /Q 退出/); assert.doesNotMatch(terminal, /C 取消任务|A\/Y 批准|T 重试/);
});

test('topology-first console makes fan-out and fan-in visible', () => {
  const fixture = canonicalFixture('concurrent');
  const options = optionsFor(fixture);
  const presentation = buildRunTextPresentation(fixture.view, 120, 138_000, options);
  assert.deepEqual(fixture.view.run?.parallelLayers, [
    ['plan'],
    ['实现-operator-console', 'windows-pty-check'],
    ['review'],
    ['publish'],
  ]);
  assert.equal(presentation.topology.filter(line => line.text.includes('阶段')).length, 4);
  assert.ok(presentation.topology.some(line => line.text.includes('并行 2')));
  const renderedWide = renderRunText(fixture.view, 120, 138_000, options);
  assert.ok(renderedWide.includes('\u251c\u2500') || renderedWide.includes('\u2514\u2500'));
  assert.ok(renderedWide.includes('实现-operator-console'));
  assert.ok(renderedWide.includes('节点：实现-operator-console'));
  assert.ok(renderedWide.includes('依赖 plan'));
  const renderedNarrow = renderRunText(fixture.view, 80, 138_000, options);
  assert.equal(assertWidth(renderedNarrow.split('\n'), 80), true);
  assert.ok(renderedNarrow.includes('并行 2'));
});

test('waiting Agent detail exposes structured needs_input questions and choices', () => {
  const fixture = structuredClone(canonicalFixture('retryable'));
  const selected = fixture.view.nodes?.find(node => node.id === fixture.selectedNodeId);
  assert.ok(selected);
  selected.result = {summary: 'approval required', questions: [{id: 'approval', prompt: 'Proceed with deployment?', choices: ['yes', 'no'], required: true}]};
  const text = renderRunText(fixture.view, 120, 138_000, optionsFor(fixture));
  assert.match(text, /问题 approval.*必填.*Proceed with deployment\?/);
  assert.match(text, /可选答案.*yes.*no/);
});

test('active Agent activity is visible in Chinese and bounded at supported widths', () => {
  const fixture = structuredClone(canonicalFixture('concurrent'));
  fixture.selectedNodeId = fixture.view.activeAttempts?.[0]?.nodeId ?? fixture.selectedNodeId;
  const attempt = fixture.view.activeAttempts?.find(item => item.nodeId === fixture.selectedNodeId);
  assert.ok(attempt);
  attempt.activity = {schemaVersion: 'fishyume.attempt-activity/v1', summary: '正在执行命令：go test ./...', items: [
    {kind: 'turn', status: 'running', message: 'Codex 正在处理任务'},
    {kind: 'command', status: 'running', message: '正在执行命令：go test ./...'},
  ], truncated: false};
  for (const width of [80, 120, 160]) {
    const text = renderRunText(fixture.view, width, 138_000, optionsFor(fixture));
    assert.match(text, /当前活动.*正在执行命令/);
    assert.match(text, /活动.*进行中.*Codex 正在处理任务/);
    assert.equal(assertWidth(text.split('\n'), width), true);
  }
});

test('Focus Detail folds locally while action detail overrides the folded node view', () => {
  const fixture = canonicalFixture('retryable');
  const folded = buildRunTextPresentation(fixture.view, 80, 138_000, {...optionsFor(fixture), detailExpanded: false});
  assert.equal(folded.detail, undefined);
  const action = {...optionsFor(fixture), detailExpanded: false};
  action.action = {...action.action!, interaction: {...interactionFor(fixture), mode: 'retry-confirm', actionTarget: {nodeId: 'integration-tests', kind: 'retry', duplicateRisk: false}}};
  const focused = buildRunTextPresentation(fixture.view, 80, 138_000, action);
  assert.match(focused.detail?.title ?? '', /操作确认 \/ integration-tests/);
});

test('Header colors only brand and exceptional status while essential text inherits terminal foreground', () => {
  const fixture = canonicalFixture('concurrent');
  const medium = buildRunTextPresentation(fixture.view, 120, 138_000, optionsFor(fixture));
  const primary = medium.header[0]!; const identity = medium.header[1]!;
  assert.deepEqual(primary.segments.filter(segment => segment.role).map(segment => [segment.text, segment.role]), [['FISHYUME', 'brand']]);
  assert.ok(primary.segments.some(segment => !segment.role && segment.text.includes(fixture.view.run!.workflowName)));
  assert.ok(primary.segments.some(segment => !segment.role && segment.text.includes('2m18s')));
  assert.ok(primary.segments.some(segment => segment.text === '运行中' && segment.role === undefined && segment.bold));
  assert.equal(identity.segments.every(segment => segment.role === undefined), true);
  assert.match(identity.text, /任务 run-concurrent-a91f/); assert.match(identity.text, /已结束 1\/5/);

  const approval = buildRunTextPresentation(canonicalFixture('approval').view, 120, 138_000, optionsFor(canonicalFixture('approval')));
  assert.ok(approval.header[0]!.segments.some(segment => segment.text === '需要审批' && segment.role === 'approval' && segment.bold));

  const narrow = buildRunTextPresentation(fixture.view, 80, 138_000, optionsFor(fixture));
  const narrowText = narrow.header.map(line => line.text).join('\n');
  assert.equal((narrowText.match(/并发上限 3/g) ?? []).length, 1);
  assert.doesNotMatch(narrow.header[2]!.text, /并发上限/);
  assert.equal(narrow.header[2]!.segments.every(segment => segment.role === undefined), true);
});

test('color capability levels preserve semantic roles and mono removes color only', () => {
  assert.equal(detectColorMode({getColorDepth: () => 24}, {NO_COLOR: '1'}), 'mono');
  assert.equal(detectColorMode({getColorDepth: () => 24}, {FISHYUME_THEME: 'mono'}), 'mono');
  assert.equal(detectColorMode({getColorDepth: () => 24}, {}), 'truecolor');
  assert.equal(detectColorMode({getColorDepth: () => 8}, {}), 'ansi256');
  assert.equal(detectColorMode({getColorDepth: () => 4}, {}), 'ansi16');
  for (const mode of ['ansi16', 'ansi256', 'truecolor'] as ColorMode[]) assert.ok(colorFor('danger', mode));
  assert.equal(colorFor('strong', 'truecolor'), undefined); assert.equal(colorFor('neutral', 'truecolor'), undefined);
  assert.equal(colorFor('strong', 'ansi16'), undefined); assert.equal(colorFor('neutral', 'ansi256'), undefined);
  assert.equal(colorFor('muted', 'truecolor'), 'gray'); assert.equal(colorFor('muted', 'ansi256'), 'gray');
  assert.equal(colorFor('danger', 'mono'), undefined);
  assert.equal(detectSymbolMode({TERM: 'dumb'}), 'ascii'); assert.equal(detectSymbolMode({FISHYUME_ASCII: '1'}), 'ascii'); assert.equal(detectSymbolMode({}), 'unicode');
});

test('CJK, emoji, combining marks, and long paths truncate by display width', () => {
  for (const value of ['并行工作流 diagnostic', 'operator 🐟 console', 'Cafe\u0301 / E:/团队/很长的路径']) {
    const text = fitText(value, 12); assert.ok(displayWidth(text) <= 12); assert.match(text, /…$/);
  }
});
