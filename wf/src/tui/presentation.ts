import type {AttemptSnapshot, NodeDiagnostic, NodeSnapshot, NodeSummary, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {
  designTokens,
  dividerCharacter,
  headerStatusText,
  rowStatusText,
  selectionText,
  separatorText,
  statusForNode,
  statusForRun,
  type ColorRole,
  type SemanticStatus,
  type SymbolMode,
} from './design-tokens.js';
import {displayWidth, fitText, joinColumns, padDisplay, terminalSize, wrapItems, type TerminalSize} from './layout.js';
import {formatElapsed} from './text-reporter.js';
import type {ActionableNode, ConsoleInteractionState} from './interaction.js';

export interface WorkflowRowPresentation {
  nodeId: string;
  status: SemanticStatus;
  role: ColorRole;
  selected: boolean;
  marker: string;
  statusText: string;
  content: string;
  text: string;
}

export interface StyledTextSegment {text: string; role?: ColorRole; bold?: boolean}
export interface HeaderLinePresentation {text: string; segments: StyledTextSegment[]}
export interface DetailPresentation {title: string; role: ColorRole; lines: string[]}
export interface AttentionPresentation {role: ColorRole; lines: string[]}
export interface TopologyLinePresentation {text: string; role?: ColorRole; bold?: boolean}
export interface RunTextPresentation {
  size: TerminalSize;
  header: HeaderLinePresentation[];
  divider: string;
  attention?: AttentionPresentation;
  topology: TopologyLinePresentation[];
  workflow: WorkflowRowPresentation[];
  detail?: DetailPresentation;
  statusStrip: string;
  footer: string[];
}

export interface PresentationActionContext {
  interaction: ConsoleInteractionState;
  actionable: readonly ActionableNode[];
  pending: boolean;
  pendingTarget?: ActionableNode;
  message?: string;
}

export interface RunPresentationOptions {
  selectedNodeId?: string;
  detailExpanded?: boolean;
  symbolMode?: SymbolMode;
  interactive?: boolean;
  action?: PresentationActionContext;
}

function attemptMap(view: RunStatusView): Map<string, AttemptSnapshot> {
  const attempts = view.attempts ?? view.activeAttempts ?? (view.activeAttempt ? [view.activeAttempt] : []);
  return new Map(attempts.map(attempt => [attempt.nodeId, attempt]));
}

function nodeSnapshotMap(view: RunStatusView): Map<string, NodeSnapshot> {
  return new Map((view.nodes ?? []).map(node => [node.id, node]));
}

function topologyLayers(view: RunStatusView, nodes: NodeSummary[]): NodeSummary[][] {
  const run = view.run;
  if (run?.parallelLayers?.length) {
    const byID = new Map(nodes.map(node => [node.id, node]));
    return run.parallelLayers.map(layer => layer.map(nodeID => byID.get(nodeID)).filter((node): node is NodeSummary => Boolean(node))).filter(layer => layer.length > 0);
  }
  const grouped = new Map<number, NodeSummary[]>();
  if (nodes.some(node => node.parallelLayer !== undefined)) {
    for (const node of nodes) {
      const layer = node.parallelLayer ?? 0;
      const current = grouped.get(layer) ?? [];
      current.push(node); grouped.set(layer, current);
    }
    return [...grouped.keys()].sort((left, right) => left - right).map(layer => grouped.get(layer)!);
  }
  return nodes.map(node => [node]);
}

function buildTopologyLines(view: RunStatusView, nodes: NodeSummary[], attempts: Map<string, AttemptSnapshot>, width: number, selectedNodeId: string | undefined, symbolMode: SymbolMode): TopologyLinePresentation[] {
  const layers = topologyLayers(view, nodes); const lines: TopologyLinePresentation[] = [];
  layers.forEach((layer, layerIndex) => {
    const label = layer.length > 1 ? `阶段 ${layerIndex + 1} · 并行 ${layer.length}` : `阶段 ${layerIndex + 1}`;
    lines.push({text: fitText(label, width), role: 'muted', bold: true});
    layer.forEach((node, nodeIndex) => {
      const row = formatWorkflowRow(node, attempts.get(node.id), Math.max(20, width - 6), node.id === selectedNodeId, symbolMode);
      const connector = symbolMode === 'ascii'
        ? (layer.length > 1 && nodeIndex < layer.length - 1 ? '|-' : '`-')
        : (layer.length > 1 && nodeIndex < layer.length - 1 ? '├─' : '└─');
      const dependencyText = node.dependsOn?.length ? ` · 依赖 ${node.dependsOn.join(', ')}` : '';
      lines.push({text: fitText(`  ${connector} ${row.text}${dependencyText}`, width), role: row.selected ? 'brand' : row.role, bold: row.selected});
    });
    if (layerIndex < layers.length - 1) lines.push({text: fitText(symbolMode === 'ascii' ? '  |' : '  │', width), role: 'muted'});
  });
  return lines;
}

function diagnosticsFor(view: RunStatusView, node: NodeSummary): NodeDiagnostic[] {
  const diagnostics = (view.diagnostics ?? []).filter(item => item.nodeId === node.id);
  if (diagnostics.length) return diagnostics;
  return node.diagnostic ? [{nodeId: node.id, reason: node.reason, message: node.diagnostic}] : [];
}

const humanLabels: Record<string, string> = {
  agent: '智能体', approval: '人工审批', pending: '未开始', ready: '准备就绪', running: '运行中', waiting: '等待处理', paused: '已暂停', cancelling: '正在取消', completed: '已结束', skipped: '已跳过',
  succeeded: '成功', failed: '失败', rejected: '已拒绝', cancelled: '已取消', indeterminate: '结果待确认',
  approval_required: '需要人工审批', agent_waiting_input: '需要你的回答', invalid_result: '结果格式无效', completion_missing: '未确认执行完成', user_requested: '用户操作', cancel_failed: '取消尚未确认',
  handle_persisted: '执行句柄已保存', session_persisted: '会话已保存', result_consumed: '结果已接收',
};

function human(value: string | undefined): string | undefined {return value ? humanLabels[value] ?? value.replaceAll('_', ' ') : undefined}

export function dividerLine(width: number, symbolMode: SymbolMode, title?: string): string {
  const character = dividerCharacter(symbolMode);
  if (!title) return character.repeat(Math.max(0, width));
  if (width < 8) return character.repeat(Math.max(0, width));
  const fittedTitle = fitText(title, Math.max(1, width - 6));
  const prefix = `${character.repeat(2)} `; const suffixWidth = Math.max(1, width - displayWidth(prefix) - displayWidth(fittedTitle) - 1);
  return fitText(`${prefix}${fittedTitle} ${character.repeat(suffixWidth)}`, width);
}

function styledLine(text: string, highlights: readonly {start: number; length: number; role?: ColorRole; bold?: boolean}[] = []): HeaderLinePresentation {
  const segments: StyledTextSegment[] = []; let cursor = 0;
  for (const highlight of [...highlights].sort((left, right) => left.start - right.start)) {
    if (highlight.start < cursor || highlight.start >= text.length || highlight.length <= 0) continue;
    if (highlight.start > cursor) segments.push({text: text.slice(cursor, highlight.start)});
    const end = Math.min(text.length, highlight.start + highlight.length);
    segments.push({text: text.slice(highlight.start, end), role: highlight.role, bold: highlight.bold}); cursor = end;
  }
  if (cursor < text.length) segments.push({text: text.slice(cursor)});
  if (!segments.length) segments.push({text});
  return {text, segments};
}

export function headerLines(run: WorkflowSnapshot, width: number, elapsedMs: number, symbolMode: SymbolMode): HeaderLinePresentation[] {
  const size = terminalSize(width); const separator = separatorText(symbolMode);
  const runStatus = statusForRun(run); const status = headerStatusText(runStatus); const semanticRole = designTokens.status[runStatus].role;
  const statusRole = semanticRole === 'danger' || semanticRole === 'approval' ? semanticRole : undefined; const settled = settledText(run);
  const capacity = run.effectiveConcurrency ? `并发上限 ${run.effectiveConcurrency}` : undefined;
  const identity = [`任务 ${run.id}`, run.resolvedDriver ?? run.backend, ...(size === 'narrow' ? [] : [capacity])].filter((value): value is string => Boolean(value)).join(separator);
  if (size === 'narrow') {
    const brandLine = fitText(`${designTokens.emphasis.brand} / ${run.workflowName}`, width);
    const statusLine = fitText([status, formatElapsed(elapsedMs), settled, capacity].filter((value): value is string => Boolean(value)).join(separator), width);
    return [
      styledLine(brandLine, [{start: 0, length: Math.min(designTokens.emphasis.brand.length, brandLine.length), role: 'brand', bold: true}]),
      styledLine(statusLine, [{start: 0, length: Math.min(status.length, statusLine.length), role: statusRole, bold: true}]),
      styledLine(fitText(identity, width)),
    ];
  }
  const primary = joinColumns(`${designTokens.emphasis.brand} / ${run.workflowName}`, `${status}  ${formatElapsed(elapsedMs)}`, width);
  const statusStart = primary.lastIndexOf(status);
  const result = [
    styledLine(primary, [
      {start: 0, length: designTokens.emphasis.brand.length, role: 'brand', bold: true},
      ...(statusStart >= 0 ? [{start: statusStart, length: status.length, role: statusRole, bold: true} as const] : []),
    ]),
    styledLine(joinColumns(identity, settled, width)),
  ];
  if (size === 'wide') result.push(styledLine(fitText(`状态目录 ${run.stateDir}`, width), [{start: 0, length: Math.min(width, `状态目录 ${run.stateDir}`.length), role: 'muted'}]));
  return result;
}

function settledText(run: WorkflowSnapshot): string {
  const nodes = Object.values(run.nodes); const settled = nodes.filter(node => node.phase === 'completed' || node.phase === 'skipped').length;
  return `已结束 ${settled}/${nodes.length}`;
}

function nodeTail(node: NodeSummary, attempt: AttemptSnapshot | undefined, size: TerminalSize, separator: string): string[] {
  const attemptText = attempt ? `第${attempt.number}次` : node.currentAttempt ? `第${node.currentAttempt}次` : undefined;
  const backend = attempt?.resolvedDriver ?? attempt?.backend;
  const execution = attempt?.execution?.id ? `执行 ${attempt.execution.id}` : undefined;
  const launch = human(attempt?.launchState);
  const primary = size === 'narrow' ? [attemptText, backend] : [human(node.type), attemptText, backend, launch, execution];
  return [...primary, attempt?.activity?.summary ? `活动 ${attempt.activity.summary}` : undefined, human(node.reason), node.diagnostic].filter((value): value is string => Boolean(value)).map(value => value.replaceAll(' · ', separator));
}

export function formatWorkflowRow(
  node: NodeSummary,
  attempt: AttemptSnapshot | undefined,
  width: number,
  selected: boolean,
  symbolMode: SymbolMode,
): WorkflowRowPresentation {
  const status = statusForNode(node); const token = designTokens.status[status]; const marker = selectionText(selected, symbolMode);
  const statusText = rowStatusText(status, symbolMode); const prefix = `${marker} ${statusText} `;
  const available = Math.max(1, width - displayWidth(prefix)); const size = terminalSize(width); const separator = separatorText(symbolMode);
  const tail = nodeTail(node, attempt, size, separator);
  const idealIdWidth = size === 'wide' ? 34 : size === 'medium' ? 28 : 30;
  const idWidth = Math.min(idealIdWidth, available); const separatorWidth = displayWidth(separator);
  const tailBudget = Math.max(0, available - idWidth - separatorWidth);
  const fittedTail = tail.length && tailBudget > 0 ? `${separator}${fitText(tail.join(separator), tailBudget)}` : '';
  const nodeId = padDisplay(node.id, idWidth); const content = fitText(`${nodeId}${fittedTail}`, available);
  return {nodeId: node.id, status, role: token.role, selected, marker, statusText, content, text: `${prefix}${content}`};
}

function appendResult(lines: string[], snapshot: NodeSnapshot | undefined, separator: string): void {
  const result = snapshot?.result;
  if (!result) return;
  if (result.summary) lines.push(`结果摘要${separator}${result.summary}`);
  if (result.decision) lines.push(`决策${separator}${result.decision}${result.reason ? `${separator}${result.reason}` : ''}`);
  if (result.warnings?.length) lines.push(`警告${separator}${result.warnings.join(separator)}`);
  if (result.checks?.length) lines.push(`检查项${separator}${result.checks.join(separator)}`);
  if (result.artifacts?.length) lines.push(`产物${separator}${result.artifacts.join(separator)}`);
  for (const question of result.questions ?? []) {
    lines.push(`问题 ${question.id}${separator}${question.required ? '必填' : '可选'}${separator}${question.prompt}`);
    if (question.choices?.length) lines.push(`可选答案${separator}${question.choices.join(separator)}`);
  }
}

function nodeDetail(
  view: RunStatusView,
  node: NodeSummary,
  width: number,
  symbolMode: SymbolMode,
): DetailPresentation {
  const separator = separatorText(symbolMode); const attempts = attemptMap(view); const snapshots = nodeSnapshotMap(view);
  const attempt = attempts.get(node.id); const snapshot = snapshots.get(node.id); const status = statusForNode(node);
  const lines: string[] = [];
  lines.push([human(node.type), human(node.phase), human(node.conclusion), human(node.reason)].filter(Boolean).join(separator));
  if (attempt) {
    lines.push([
      `第 ${attempt.number} 次尝试`, attempt.resolvedDriver ?? attempt.backend, human(attempt.launchState), attempt.execution ? `执行标识 ${attempt.execution.id}` : undefined,
    ].filter((value): value is string => Boolean(value)).join(separator));
    if (attempt.context) {
      const context = attempt.context;
      const components = context.components.map(component => `${component.id}:${component.kind} ${component.includedBytes ?? 0}/${component.originalBytes ?? component.includedBytes ?? 0}B`).join(',');
      const omissions = context.omissions?.length ? ` omissions=${context.omissions.map(omission => typeof omission === 'string' ? omission : `${omission.id}:${omission.reason ?? 'omitted'}`).join(',')}` : '';
      const memory = context.memoryUsage ? ` memory=${context.memoryUsage.recordIds.join(',')} ${context.memoryUsage.committed ? 'committed' : 'pending'}` : '';
      lines.push(`context ${context.compilerVersion}${context.hash ? ` hash=${context.hash}` : ''} usage=${context.usage.totalBytes ?? 0}/${context.budget.totalBytes ?? 0} components=${components}${omissions}${memory}${context.truncated ? ' truncated' : ''}`);
    }
    if (attempt.activity) {
      lines.push(`当前活动${separator}${attempt.activity.summary ?? 'Codex 正在工作'}`);
      for (const item of attempt.activity.items.slice(-6)) {
        lines.push(`活动${separator}${item.status === 'completed' ? '已完成' : '进行中'}${separator}${item.message}`);
      }
      if (attempt.activity.truncated) lines.push(`活动${separator}较早记录已截断`);
    }
  }
  for (const diagnostic of diagnosticsFor(view, node)) {
    const text = [human(diagnostic.reason), diagnostic.message].filter((value): value is string => Boolean(value)).join(separator);
    if (text) lines.push(`提示${separator}${text}`);
  }
  appendResult(lines, snapshot, separator);
  const run = view.run;
  if (run?.phase === 'completed') {
    if (run.summary) lines.push(`任务总结${separator}${run.summary}`);
    lines.push(`再次查看${separator}fishyume status ${run.id}`);
    lines.push(`状态目录${separator}${run.stateDir}`);
  }
  const attemptTitle = attempt ? ` / 第 ${attempt.number} 次` : '';
  return {title: `节点：${node.id}${attemptTitle}`, role: designTokens.status[status].role, lines: lines.filter(Boolean).map(line => fitText(line, width))};
}

function actionDetail(view: RunStatusView, context: PresentationActionContext, width: number, symbolMode: SymbolMode): DetailPresentation {
  const separator = separatorText(symbolMode); const state = context.interaction;
  const target = context.pendingTarget ?? state.actionTarget;
  const actionKind = target?.kind === 'approval' ? '人工审批' : target?.kind === 'answer' ? '回答问题' : target?.kind === 'retry' ? '重试节点' : undefined;
  const label = target ? `${target.nodeId}${separator}${actionKind}${target.duplicateRisk ? `${separator}存在重复副作用风险` : ''}` : '取消整个任务';
  const lines: string[] = [];
  if (context.pending) lines.push(`正在提交${separator}操作目标已固定${target ? `${separator}${target.nodeId}` : ''}`);
  else if (state.mode === 'reject') lines.push(`拒绝原因${separator}${state.rejectReason || '尚未填写（可直接提交）'}`);
  else if (state.mode === 'answer') lines.push(`${target?.questionIds?.length === 1 ? '回答' : '批量回答'}${separator}${state.answerText || '请输入内容'}`);
  else if (state.mode === 'retry-risk-confirm') lines.push('重试可能再次产生外部副作用。确认你已了解重复执行风险后再继续。');
  else if (state.mode === 'retry-confirm') lines.push('确认重新执行这个节点吗？Fishyume 会锁定当前可操作的节点身份。');
  else if (state.mode === 'cancel-confirm') lines.push('确认取消整个任务吗？活动中的执行会收到停止请求，Engine 确认后才算取消完成。');
  if (context.message) lines.push(`操作结果${separator}${context.message}`);
  if (view.run?.phase === 'cancelling') lines.push('Engine 正在确认活动执行已经停止，请稍候。');
  return {title: `操作确认 / ${label}`, role: target?.duplicateRisk || state.mode === 'cancel-confirm' ? 'danger' : 'approval', lines: lines.map(line => fitText(line, width))};
}

function helpDetail(width: number): DetailPresentation {
  return {title: '操作帮助', role: 'brand', lines: [
    '↑/↓ 或 J/K：选择节点。操作只会作用于当前选中且可操作的节点。',
    'Enter：展开或收起详情。A/Y：批准或回答。X/N：拒绝并填写原因。T：确认后重试。',
    'Q、D 或 Ctrl+C：退出观察，不会取消任务。C：明确取消整个任务。',
    '输入和确认始终绑定到节点身份；状态变化时不会误操作到其他节点。',
  ].map(line => fitText(line, width))};
}

function attentionFor(view: RunStatusView, actionable: readonly ActionableNode[], width: number, selectedNodeId: string | undefined): AttentionPresentation | undefined {
  const approvals = actionable.filter(item => item.kind === 'approval');
  if (approvals.length) {
    const selected = approvals.some(item => item.nodeId === selectedNodeId);
    const ids = approvals.map(item => item.nodeId).join('、');
    return {role: 'approval', lines: [
      fitText(`⚠ 需要人工审批：${ids}`, width),
      fitText(selected ? '请先阅读下方审批说明，然后按 A/Y 批准，或按 X/N 拒绝。' : '请用 ↑/↓ 选择审批节点，然后按 A/Y 批准，或按 X/N 拒绝。', width),
    ]};
  }
  const answers = actionable.filter(item => item.kind === 'answer');
  if (answers.length) return {role: 'waiting', lines: [
    fitText(`⚠ 智能体需要你的回答：${answers.map(item => item.nodeId).join('、')}`, width),
    fitText('选择对应节点后按 A/Y，输入答案并按 Enter 提交。', width),
  ]};
  const retries = actionable.filter(item => item.kind === 'retry');
  if (retries.length) return {role: retries.some(item => item.duplicateRisk) ? 'danger' : 'waiting', lines: [
    fitText(`⚠ 有节点需要决定是否重试：${retries.map(item => item.nodeId).join('、')}`, width),
    fitText('选择节点后按 T；存在外部副作用风险时会再次确认。', width),
  ]};
  return undefined;
}

export function statusStripText(run: WorkflowSnapshot, symbolMode: SymbolMode): string {
  const nodes = Object.values(run.nodes); const separator = separatorText(symbolMode);
  const active = nodes.filter(node => node.phase === 'running').length;
  const waiting = nodes.filter(node => node.phase === 'waiting').length;
  const failed = nodes.filter(node => node.conclusion === 'failed' || node.conclusion === 'indeterminate').length;
  const skipped = nodes.filter(node => node.phase === 'skipped').length;
  return [active ? `${active} 个运行中` : undefined, waiting ? `${waiting} 个等待处理` : undefined, failed ? `${failed} 个失败` : undefined, skipped ? `${skipped} 个已跳过` : undefined, run.effectiveConcurrency ? `并发上限 ${run.effectiveConcurrency}` : undefined]
    .filter((value): value is string => Boolean(value)).join(separator);
}

function footerItems(view: RunStatusView, options: RunPresentationOptions, selectedNode: NodeSummary | undefined): string[] {
  const run = view.run; if (!run) return [];
  const action = options.action; const state = action?.interaction;
  if (action?.pending) return [];
  if (state && state.mode !== 'idle') return ['Enter 确认', 'Esc 放弃'];
  if (run.phase === 'completed') return [`再次查看 fishyume status ${run.id}`, `状态目录 ${run.stateDir}`, 'Q 退出'];
  if (!options.interactive) return [`再次查看 fishyume status ${run.id}`];
  const visibleNodeCount = run.topologicalOrder.filter(nodeId => Boolean(run.nodes[nodeId])).length; const items: string[] = [];
  if (visibleNodeCount > 1) items.push('↑↓/J/K 选择节点');
  if (selectedNode) items.push(`Enter ${state?.detailExpanded === false ? '查看详情' : '收起详情'}`);
  if (selectedNode && action) {
    const target = action.actionable.find(item => item.nodeId === selectedNode.id);
    if (target?.kind === 'approval') items.push('A/Y 批准', 'X/N 拒绝');
    if (target?.kind === 'answer') items.push('A/Y 回答');
    if (target?.kind === 'retry') items.push('T 重试');
    if (run.phase !== 'cancelling') items.push('C 取消任务');
    items.push(state?.helpVisible ? '? 关闭帮助' : '? 操作帮助', 'Q 退出观察');
  }
  return items;
}

export function footerLines(view: RunStatusView, width: number, options: RunPresentationOptions, selectedNode: NodeSummary | undefined): string[] {
  return wrapItems(footerItems(view, options, selectedNode), width);
}

export function buildRunTextPresentation(view: RunStatusView, width: number, elapsedMs: number, options: RunPresentationOptions = {}): RunTextPresentation {
  if (!view.run) throw new Error('TUI requires a current run');
  const run = view.run; const symbolMode = options.symbolMode ?? 'unicode';
  const nodes = run.topologicalOrder.map(id => run.nodes[id]).filter((node): node is NodeSummary => Boolean(node));
  const selectedNodeId = options.selectedNodeId && run.nodes[options.selectedNodeId] ? options.selectedNodeId : nodes[0]?.id;
  const selectedNode = selectedNodeId ? run.nodes[selectedNodeId] : undefined; const attempts = attemptMap(view);
  const workflow = nodes.map(node => formatWorkflowRow(node, attempts.get(node.id), width, node.id === selectedNodeId, symbolMode));
  const topologyWidth = width >= 120 ? Math.max(48, Math.floor(width * 0.58)) : width;
  const topology = buildTopologyLines(view, nodes, attempts, topologyWidth, selectedNodeId, symbolMode);
  let detail: DetailPresentation | undefined;
  const actionState = options.action?.interaction;
  if (options.action && (options.action.pending || actionState?.mode !== 'idle' || options.action.message)) detail = actionDetail(view, options.action, width, symbolMode);
  else if (actionState?.helpVisible) detail = helpDetail(width);
  else if (options.detailExpanded !== false && selectedNode) detail = nodeDetail(view, selectedNode, width, symbolMode);
  return {
    size: terminalSize(width),
    header: headerLines(run, width, elapsedMs, symbolMode),
    divider: dividerLine(width, symbolMode),
    attention: attentionFor(view, options.action?.actionable ?? [], width, selectedNodeId),
    topology,
    workflow,
    detail,
    statusStrip: fitText(statusStripText(run, symbolMode), width),
    footer: footerLines(view, width, options, selectedNode),
  };
}

function wideContentLines(presentation: RunTextPresentation, width: number, symbolMode: SymbolMode): string[] {
  if (width < 120 || !presentation.detail) return presentation.topology.map(line => line.text);
  const leftWidth = Math.max(48, Math.floor(width * 0.58));
  const rightWidth = Math.max(24, width - leftWidth - 3);
  const right = [dividerLine(rightWidth, symbolMode, presentation.detail.title), ...presentation.detail.lines.map(line => fitText(line, rightWidth))];
  const count = Math.max(presentation.topology.length, right.length);
  return Array.from({length: count}, (_, index) => joinColumns(fitText(presentation.topology[index]?.text ?? '', leftWidth), right[index] ?? '', width));
}

export function renderRunText(view: RunStatusView, width: number, elapsedMs: number, options: RunPresentationOptions = {}): string {
  const presentation = buildRunTextPresentation(view, width, elapsedMs, options); const lines = [...presentation.header.map(line => line.text), presentation.divider];
  if (presentation.attention) lines.push(...presentation.attention.lines, presentation.divider);
  lines.push(...wideContentLines(presentation, width, options.symbolMode ?? 'unicode'));
  if (presentation.detail && width < 120) lines.push(dividerLine(width, options.symbolMode ?? 'unicode', presentation.detail.title), ...presentation.detail.lines, presentation.divider);
  if (presentation.statusStrip) lines.push(presentation.statusStrip);
  lines.push(...presentation.footer);
  return lines.join('\n');
}
