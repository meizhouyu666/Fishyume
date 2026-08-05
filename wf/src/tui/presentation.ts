import type {AttemptSnapshot, NodeDiagnostic, NodeSummary, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {designTokens, statusBadgeText, statusBadgeWidth, statusForNode, statusForRun, type SemanticStatus} from './design-tokens.js';
import {fitText, padDisplay, terminalSize, type TerminalSize} from './layout.js';
import {formatElapsed} from './text-reporter.js';

export interface FormattedRow {status: SemanticStatus; lines: string[]}
function nodeDetail(node: NodeSummary): string[] {return [node.type, node.currentAttempt ? `attempt ${node.currentAttempt}` : undefined, node.reason].filter((value): value is string => Boolean(value))}

export function formatNodeRow(node: NodeSummary, width: number): FormattedRow {
  const status = statusForNode(node); const prefix = `${statusBadgeText(status)} `; const contentWidth = Math.max(1, width - statusBadgeWidth - 1); const size = terminalSize(width); const detail = nodeDetail(node);
  if (size === 'narrow') {
    const lines = [`${prefix}${fitText(node.id, contentWidth)}`];
    if (detail.length) lines.push(`${' '.repeat(statusBadgeWidth + 1)}${fitText(detail.join(' · '), contentWidth)}`);
    if (node.diagnostic) lines.push(`${' '.repeat(statusBadgeWidth + 1)}${fitText(node.diagnostic, contentWidth)}`);
    return {status, lines};
  }
  const idWidth = size === 'wide' ? 32 : 24; const primary = `${padDisplay(node.id, Math.min(idWidth, contentWidth))} ${detail.join(' · ')}`.trimEnd(); const diagnostic = node.diagnostic ? ` · ${node.diagnostic}` : '';
  return {status, lines: [`${prefix}${fitText(`${primary}${diagnostic}`, contentWidth)}`]};
}

export function formatAttemptRow(attempt: AttemptSnapshot, width: number): FormattedRow {
  const node: NodeSummary = {id: attempt.nodeId, type: 'agent', phase: attempt.phase, conclusion: attempt.conclusion, reason: attempt.reason, currentAttempt: attempt.number};
  const status = statusForNode(node); const prefix = `${statusBadgeText(status)} `; const contentWidth = Math.max(1, width - statusBadgeWidth - 1);
  const execution = attempt.execution?.id ? ` · execution ${attempt.execution.id}` : ''; const launch = attempt.launchState ? ` · ${attempt.launchState}` : '';
  return {status, lines: [`${prefix}${fitText(`${attempt.nodeId} · attempt ${attempt.number} · ${attempt.backend}${launch}${execution}`, contentWidth)}`]};
}

export function progressText(run: WorkflowSnapshot): string {
  const nodes = Object.values(run.nodes); const completed = nodes.filter(node => node.phase === 'completed' || node.phase === 'skipped').length; const running = nodes.filter(node => node.phase === 'running').length; const waiting = nodes.filter(node => node.phase === 'waiting').length; const failed = nodes.filter(node => node.conclusion === 'failed' || node.conclusion === 'indeterminate').length; const skipped = nodes.filter(node => node.phase === 'skipped').length;
  return [`${completed}/${nodes.length} settled`, running ? `${running} active` : undefined, waiting ? `${waiting} waiting` : undefined, failed ? `${failed} failed` : undefined, skipped ? `${skipped} skipped` : undefined].filter((value): value is string => Boolean(value)).join('  |  ');
}

export function headerLines(run: WorkflowSnapshot, width: number, elapsedMs: number): string[] {
  const size = terminalSize(width); const badge = statusBadgeText(statusForRun(run)); const concurrency = run.effectiveConcurrency ? ` · capacity ${run.effectiveConcurrency}` : '';
  if (size === 'narrow') return [fitText(`${designTokens.emphasis.brand} · ${run.workflowName}`, width), fitText(`${badge} · ${formatElapsed(elapsedMs)}${concurrency}`, width), fitText(`run ${run.id} · backend ${run.backend}`, width)];
  return [fitText(`${designTokens.emphasis.brand} · ${run.workflowName}  ${badge}  ${formatElapsed(elapsedMs)}`, width), fitText(`run ${run.id} · backend ${run.backend}${concurrency} · state ${run.stateDir}`, width)];
}

export function footerLines(view: RunStatusView, width: number): string[] {
  const run = view.run; if (!run) return [];
  const approvals = view.waitingApprovals ?? Object.values(run.nodes).filter(node => node.type === 'approval' && node.phase === 'waiting');
  const commands = approvals.length ? approvals.flatMap(node => [`approve: fishyume resume ${run.id} --approve ${node.id}`, `reject:  fishyume resume ${run.id} --reject ${node.id} --reason "..."`]) : run.phase === 'running' ? ['ctrl+c detach · workflow continues in the Engine', `inspect: fishyume status ${run.id}`] : [`inspect: fishyume status ${run.id}`, `state: ${run.stateDir}`];
  return commands.map(command => fitText(command, width));
}

function diagnosticLines(diagnostics: readonly NodeDiagnostic[], width: number): string[] {return diagnostics.map(item => fitText(`${item.nodeId}${item.reason ? ` · ${item.reason}` : ''}${item.message ? ` · ${item.message}` : ''}`, width))}
export interface TextSection {title: string; lines: string[]}
export interface RunTextPresentation {size: TerminalSize; header: string[]; progress: string; sections: TextSection[]; footer: string[]}

export function buildRunTextPresentation(view: RunStatusView, width: number, elapsedMs: number): RunTextPresentation {
  if (!view.run) throw new Error('TUI requires a current run'); const run = view.run; const nodes = run.topologicalOrder.map(id => run.nodes[id]).filter((node): node is NodeSummary => Boolean(node));
  const sections: TextSection[] = [{title: 'WORKFLOW', lines: nodes.flatMap(node => formatNodeRow(node, width).lines)}];
  const attempts = view.activeAttempts ?? (view.activeAttempt ? [view.activeAttempt] : []); if (attempts.length) sections.push({title: `ACTIVE ATTEMPTS (${attempts.length})`, lines: attempts.flatMap(attempt => formatAttemptRow(attempt, width).lines)});
  const approvals = view.waitingApprovals ?? nodes.filter(node => node.type === 'approval' && node.phase === 'waiting'); if (approvals.length) sections.push({title: `APPROVALS (${approvals.length})`, lines: approvals.flatMap(node => formatNodeRow(node, width).lines)});
  const diagnostics = view.diagnostics ?? nodes.filter(node => node.diagnostic).map(node => ({nodeId: node.id, reason: node.reason, message: node.diagnostic})); if (diagnostics.length) sections.push({title: `DIAGNOSTICS (${diagnostics.length})`, lines: diagnosticLines(diagnostics, width)});
  if (run.summary || run.reason) sections.push({title: 'SUMMARY', lines: [fitText(run.summary ?? run.reason ?? '', width)]});
  return {size: terminalSize(width), header: headerLines(run, width, elapsedMs), progress: fitText(progressText(run), width), sections, footer: footerLines(view, width)};
}

export function renderRunText(view: RunStatusView, width: number, elapsedMs: number): string {
  const presentation = buildRunTextPresentation(view, width, elapsedMs); const lines = [...presentation.header, presentation.progress];
  for (const section of presentation.sections) lines.push(`${designTokens.emphasis.sectionPrefix} ${section.title}`, ...section.lines); lines.push(...presentation.footer); return lines.join('\n');
}
