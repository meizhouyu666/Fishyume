import type {Conclusion, NodeSummary, RunPhase, WorkflowSnapshot} from '../bridge/types.js';

export type SemanticStatus = 'running' | 'waiting' | 'approval' | 'failed' | 'indeterminate' | 'cancelled' | 'succeeded' | 'skipped' | 'rejected' | 'cancelling' | 'paused' | 'ready' | 'pending';
export type SymbolMode = 'unicode' | 'ascii';
export type ColorMode = 'mono' | 'ansi16' | 'ansi256' | 'truecolor';
export type ColorRole = 'brand' | 'strong' | 'muted' | 'running' | 'waiting' | 'approval' | 'danger' | 'success' | 'neutral';

interface StatusToken {
  unicode: string;
  ascii: string;
  rowLabel: string;
  headerLabel: string;
  role: ColorRole;
}

export const designTokens = {
  spacing: {inline: 1, section: 1},
  divider: {unicode: '─', ascii: '-'},
  separator: {unicode: ' · ', ascii: ' | '},
  selection: {unicode: '›', ascii: '>'},
  emphasis: {brand: 'FISHYUME'},
  status: {
    running: {unicode: '●', ascii: '>', rowLabel: 'run', headerLabel: 'RUNNING', role: 'running'},
    waiting: {unicode: '◌', ascii: '.', rowLabel: 'wait', headerLabel: 'WAITING', role: 'waiting'},
    approval: {unicode: '◆', ascii: '?', rowLabel: 'approve', headerLabel: 'APPROVAL', role: 'approval'},
    succeeded: {unicode: '✓', ascii: '+', rowLabel: 'done', headerLabel: 'SUCCEEDED', role: 'success'},
    failed: {unicode: '!', ascii: '!', rowLabel: 'fail', headerLabel: 'FAILED', role: 'danger'},
    indeterminate: {unicode: '?', ascii: '?', rowLabel: 'unknown', headerLabel: 'INDETERMINATE', role: 'danger'},
    cancelled: {unicode: '×', ascii: 'x', rowLabel: 'cancel', headerLabel: 'CANCELLED', role: 'neutral'},
    rejected: {unicode: '×', ascii: 'x', rowLabel: 'reject', headerLabel: 'REJECTED', role: 'danger'},
    cancelling: {unicode: '◍', ascii: '~', rowLabel: 'stop', headerLabel: 'CANCELLING', role: 'waiting'},
    paused: {unicode: 'Ⅱ', ascii: '|', rowLabel: 'pause', headerLabel: 'PAUSED', role: 'waiting'},
    ready: {unicode: '◇', ascii: '>', rowLabel: 'ready', headerLabel: 'READY', role: 'strong'},
    pending: {unicode: '○', ascii: 'o', rowLabel: 'pending', headerLabel: 'PENDING', role: 'muted'},
    skipped: {unicode: '·', ascii: '-', rowLabel: 'skip', headerLabel: 'SKIPPED', role: 'muted'},
  } satisfies Record<SemanticStatus, StatusToken>,
} as const;

const ansiPalette: Record<ColorRole, string | undefined> = {
  brand: 'cyan', strong: undefined, muted: 'gray', running: 'cyan', waiting: 'yellow', approval: 'magenta', danger: 'red', success: 'green', neutral: undefined,
};
const trueColorPalette: Record<ColorRole, string | undefined> = {
  brand: '#2dd4bf', strong: undefined, muted: 'gray', running: '#38bdf8', waiting: '#f59e0b', approval: '#d946ef', danger: '#fb7185', success: '#4ade80', neutral: undefined,
};

export function colorFor(role: ColorRole, mode: ColorMode): string | undefined {
  if (mode === 'mono') return undefined;
  return mode === 'truecolor' ? trueColorPalette[role] : ansiPalette[role];
}

export function detectColorMode(output: {getColorDepth?: () => number}, environment: NodeJS.ProcessEnv = process.env): ColorMode {
  if ('NO_COLOR' in environment || environment.NODE_DISABLE_COLORS || environment.TERM === 'dumb' || environment.FISHYUME_THEME === 'mono') return 'mono';
  const depth = output.getColorDepth?.() ?? 1;
  if (depth >= 24) return 'truecolor';
  if (depth >= 8) return 'ansi256';
  if (depth >= 4) return 'ansi16';
  return 'mono';
}

export function detectSymbolMode(environment: NodeJS.ProcessEnv = process.env): SymbolMode {
  return environment.TERM === 'dumb' || environment.FISHYUME_ASCII === '1' ? 'ascii' : 'unicode';
}

export function symbolForStatus(status: SemanticStatus, mode: SymbolMode): string {
  return designTokens.status[status][mode];
}

export function rowStatusText(status: SemanticStatus, mode: SymbolMode): string {
  const token = designTokens.status[status];
  return `${symbolForStatus(status, mode)} ${token.rowLabel.padEnd(7)}`;
}

export function headerStatusText(status: SemanticStatus): string {
  return designTokens.status[status].headerLabel;
}

export function dividerCharacter(mode: SymbolMode): string {return designTokens.divider[mode]}
export function separatorText(mode: SymbolMode): string {return designTokens.separator[mode]}
export function selectionText(selected: boolean, mode: SymbolMode): string {return selected ? designTokens.selection[mode] : ' '}

export function statusForConclusion(conclusion: Conclusion): SemanticStatus {return conclusion}
export function statusForNode(node: NodeSummary): SemanticStatus {
  if (node.type === 'approval' && node.phase === 'waiting') return 'approval';
  if (node.conclusion) return statusForConclusion(node.conclusion);
  if (node.phase === 'skipped') return 'skipped';
  if (node.phase === 'running') return 'running';
  if (node.phase === 'waiting') return 'waiting';
  if (node.phase === 'ready') return 'ready';
  if (node.phase === 'completed') return 'indeterminate';
  return 'pending';
}
export function statusForRun(run: Pick<WorkflowSnapshot, 'phase' | 'conclusion' | 'reason'>): SemanticStatus {
  if (run.conclusion) return statusForConclusion(run.conclusion);
  if (run.reason === 'approval_required') return 'approval';
  const phase: RunPhase = run.phase;
  if (phase === 'running') return 'running';
  if (phase === 'waiting') return 'waiting';
  if (phase === 'paused') return 'paused';
  if (phase === 'cancelling') return 'cancelling';
  if (phase === 'completed') return 'indeterminate';
  return 'pending';
}
