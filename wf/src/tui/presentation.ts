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

export interface DetailPresentation {title: string; role: ColorRole; lines: string[]}
export interface RunTextPresentation {
  size: TerminalSize;
  header: string[];
  divider: string;
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
  const attempts = view.activeAttempts ?? (view.activeAttempt ? [view.activeAttempt] : []);
  return new Map(attempts.map(attempt => [attempt.nodeId, attempt]));
}

function nodeSnapshotMap(view: RunStatusView): Map<string, NodeSnapshot> {
  return new Map((view.nodes ?? []).map(node => [node.id, node]));
}

function diagnosticsFor(view: RunStatusView, node: NodeSummary): NodeDiagnostic[] {
  const diagnostics = (view.diagnostics ?? []).filter(item => item.nodeId === node.id);
  if (diagnostics.length) return diagnostics;
  return node.diagnostic ? [{nodeId: node.id, reason: node.reason, message: node.diagnostic}] : [];
}

export function dividerLine(width: number, symbolMode: SymbolMode, title?: string): string {
  const character = dividerCharacter(symbolMode);
  if (!title) return character.repeat(Math.max(0, width));
  if (width < 8) return character.repeat(Math.max(0, width));
  const fittedTitle = fitText(title, Math.max(1, width - 6));
  const prefix = `${character.repeat(2)} `; const suffixWidth = Math.max(1, width - displayWidth(prefix) - displayWidth(fittedTitle) - 1);
  return fitText(`${prefix}${fittedTitle} ${character.repeat(suffixWidth)}`, width);
}

export function headerLines(run: WorkflowSnapshot, width: number, elapsedMs: number, symbolMode: SymbolMode): string[] {
  const size = terminalSize(width); const separator = separatorText(symbolMode);
  const status = headerStatusText(statusForRun(run)); const settled = settledText(run);
  const capacity = run.effectiveConcurrency ? `capacity ${run.effectiveConcurrency}` : undefined;
  const identity = [`run ${run.id}`, run.backend, capacity].filter((value): value is string => Boolean(value)).join(separator);
  if (size === 'narrow') return [
    fitText(`${designTokens.emphasis.brand} / ${run.workflowName}`, width),
    fitText([status, formatElapsed(elapsedMs), settled, capacity].filter((value): value is string => Boolean(value)).join(separator), width),
    fitText(identity, width),
  ];
  const result = [
    joinColumns(`${designTokens.emphasis.brand} / ${run.workflowName}`, `${status}  ${formatElapsed(elapsedMs)}`, width),
    joinColumns(identity, settled, width),
  ];
  if (size === 'wide') result.push(fitText(`state ${run.stateDir}`, width));
  return result;
}

function settledText(run: WorkflowSnapshot): string {
  const nodes = Object.values(run.nodes); const settled = nodes.filter(node => node.phase === 'completed' || node.phase === 'skipped').length;
  return `${settled}/${nodes.length} settled`;
}

function nodeTail(node: NodeSummary, attempt: AttemptSnapshot | undefined, size: TerminalSize, separator: string): string[] {
  const attemptText = attempt ? `a${attempt.number}` : node.currentAttempt ? `a${node.currentAttempt}` : undefined;
  const backend = attempt?.backend;
  const execution = attempt?.execution?.id ? `exec ${attempt.execution.id}` : undefined;
  const launch = attempt?.launchState?.replaceAll('_', ' ');
  const primary = size === 'narrow' ? [attemptText, backend] : [node.type, attemptText, backend, launch, execution];
  return [...primary, node.reason, node.diagnostic].filter((value): value is string => Boolean(value)).map(value => value.replaceAll(' · ', separator));
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
  if (result.summary) lines.push(`result${separator}${result.summary}`);
  if (result.decision) lines.push(`decision${separator}${result.decision}${result.reason ? `${separator}${result.reason}` : ''}`);
  if (result.warnings?.length) lines.push(`warnings${separator}${result.warnings.join(separator)}`);
  if (result.checks?.length) lines.push(`checks${separator}${result.checks.join(separator)}`);
  if (result.artifacts?.length) lines.push(`artifacts${separator}${result.artifacts.join(separator)}`);
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
  lines.push([node.type, node.phase, node.conclusion, node.reason].filter(Boolean).join(separator));
  if (attempt) {
    lines.push([
      `attempt ${attempt.number}`, attempt.backend, attempt.launchState?.replaceAll('_', ' '), attempt.execution ? `execution ${attempt.execution.id}` : undefined,
    ].filter((value): value is string => Boolean(value)).join(separator));
  }
  for (const diagnostic of diagnosticsFor(view, node)) {
    const text = [diagnostic.reason, diagnostic.message].filter((value): value is string => Boolean(value)).join(separator);
    if (text) lines.push(`diagnostic${separator}${text}`);
  }
  appendResult(lines, snapshot, separator);
  const run = view.run;
  if (run?.phase === 'completed') {
    if (run.summary) lines.push(`summary${separator}${run.summary}`);
    lines.push(`next${separator}fishyume status ${run.id}`);
    lines.push(`state${separator}${run.stateDir}`);
  }
  const attemptTitle = attempt ? ` / attempt ${attempt.number}` : '';
  return {title: `${node.id}${attemptTitle}`, role: designTokens.status[status].role, lines: lines.filter(Boolean).map(line => fitText(line, width))};
}

function actionDetail(view: RunStatusView, context: PresentationActionContext, width: number, symbolMode: SymbolMode): DetailPresentation {
  const separator = separatorText(symbolMode); const state = context.interaction;
  const target = context.pendingTarget ?? state.actionTarget;
  const label = target ? `${target.nodeId}${separator}${target.kind}${target.duplicateRisk ? `${separator}duplicate-risk` : ''}` : 'run cancellation';
  const lines: string[] = [];
  if (context.pending) lines.push(`working${separator}target fixed${target ? `${separator}${target.nodeId}` : ''}`);
  else if (state.mode === 'reject') lines.push(`reason${separator}${state.rejectReason || '(empty; rejection will still be submitted)'}`);
  else if (state.mode === 'retry-risk-confirm') lines.push('Retry may repeat external effects. Explicit duplicate-risk acknowledgement is required.');
  else if (state.mode === 'retry-confirm') lines.push('Retry this node using the Engine current actionable identity?');
  else if (state.mode === 'cancel-confirm') lines.push('Cancel this run? Active executions may be stopped; cancellation is not complete until confirmed by the Engine.');
  if (context.message) lines.push(`action${separator}${context.message}`);
  if (view.run?.phase === 'cancelling') lines.push('Engine is still confirming active executions; status remains CANCELLING.');
  return {title: `ACTION / ${label}`, role: target?.duplicateRisk || state.mode === 'cancel-confirm' ? 'danger' : 'approval', lines: lines.map(line => fitText(line, width))};
}

function helpDetail(width: number): DetailPresentation {
  return {title: 'HELP', role: 'brand', lines: [
    'Select any Workflow node with ↑/↓ or j/k; action keys only apply to the selected Engine-actionable node.',
    'Enter folds or expands Focus Detail. a approves; r rejects with a reason; R retries after confirmation.',
    'd/q/Ctrl+C detach or stop observing. They never cancel; c is the explicit run cancellation action.',
    'Action input and confirmation stay bound to nodeId, kind, and duplicate-risk identity.',
  ].map(line => fitText(line, width))};
}

export function statusStripText(run: WorkflowSnapshot, symbolMode: SymbolMode): string {
  const nodes = Object.values(run.nodes); const separator = separatorText(symbolMode);
  const active = nodes.filter(node => node.phase === 'running').length;
  const waiting = nodes.filter(node => node.phase === 'waiting').length;
  const failed = nodes.filter(node => node.conclusion === 'failed' || node.conclusion === 'indeterminate').length;
  const skipped = nodes.filter(node => node.phase === 'skipped').length;
  return [active ? `${active} active` : undefined, waiting ? `${waiting} waiting` : undefined, failed ? `${failed} failed` : undefined, skipped ? `${skipped} skipped` : undefined, run.effectiveConcurrency ? `capacity ${run.effectiveConcurrency}` : undefined]
    .filter((value): value is string => Boolean(value)).join(separator);
}

function footerItems(view: RunStatusView, options: RunPresentationOptions, selectedNode: NodeSummary | undefined): string[] {
  const run = view.run; if (!run) return [];
  const action = options.action; const state = action?.interaction;
  if (action?.pending) return [];
  if (state && state.mode !== 'idle') return ['Enter confirm', 'Esc discard'];
  if (run.phase === 'completed') return [`status fishyume status ${run.id}`, `state ${run.stateDir}`, 'q exit'];
  if (!options.interactive) return [`status fishyume status ${run.id}`];
  const visibleNodeCount = run.topologicalOrder.filter(nodeId => Boolean(run.nodes[nodeId])).length; const items: string[] = [];
  if (visibleNodeCount > 1) items.push('↑↓/j/k select');
  if (selectedNode) items.push(`Enter ${state?.detailExpanded === false ? 'details' : 'fold'}`);
  if (selectedNode && action) {
    const target = action.actionable.find(item => item.nodeId === selectedNode.id);
    if (target?.kind === 'approval') items.push('a approve', 'r reject');
    if (target?.kind === 'retry') items.push('R retry');
    if (run.phase !== 'cancelling') items.push('c cancel');
    items.push(state?.helpVisible ? '? close' : '? help', 'q detach');
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
  let detail: DetailPresentation | undefined;
  const actionState = options.action?.interaction;
  if (options.action && (options.action.pending || actionState?.mode !== 'idle' || options.action.message)) detail = actionDetail(view, options.action, width, symbolMode);
  else if (actionState?.helpVisible) detail = helpDetail(width);
  else if (options.detailExpanded !== false && selectedNode) detail = nodeDetail(view, selectedNode, width, symbolMode);
  return {
    size: terminalSize(width),
    header: headerLines(run, width, elapsedMs, symbolMode),
    divider: dividerLine(width, symbolMode),
    workflow,
    detail,
    statusStrip: fitText(statusStripText(run, symbolMode), width),
    footer: footerLines(view, width, options, selectedNode),
  };
}

export function renderRunText(view: RunStatusView, width: number, elapsedMs: number, options: RunPresentationOptions = {}): string {
  const presentation = buildRunTextPresentation(view, width, elapsedMs, options); const lines = [...presentation.header, presentation.divider];
  lines.push(...presentation.workflow.map(row => row.text));
  if (presentation.detail) lines.push(dividerLine(width, options.symbolMode ?? 'unicode', presentation.detail.title), ...presentation.detail.lines, presentation.divider);
  if (presentation.statusStrip) lines.push(presentation.statusStrip);
  lines.push(...presentation.footer);
  return lines.join('\n');
}
