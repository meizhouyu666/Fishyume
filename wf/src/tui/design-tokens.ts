import type {Conclusion, NodeSummary, RunPhase, WorkflowSnapshot} from '../bridge/types.js';

export type SemanticStatus = 'running' | 'waiting' | 'approval' | 'failed' | 'indeterminate' | 'cancelled' | 'succeeded' | 'skipped' | 'rejected' | 'cancelling' | 'paused' | 'ready' | 'pending';
export type ColorMode = 'mono' | 'ansi16' | 'ansi256' | 'truecolor';
export type ColorRole = 'brand' | 'strong' | 'muted' | 'running' | 'waiting' | 'approval' | 'danger' | 'success' | 'neutral';

export const designTokens = {
  spacing: {inline: 1, section: 1, panelX: 1},
  border: {horizontal: '-', vertical: '|', corner: '+'},
  emphasis: {brand: 'FISHYUME', sectionPrefix: '::'},
  status: {
    running: {symbol: '>>', label: 'RUNNING', role: 'running'}, waiting: {symbol: '..', label: 'WAITING', role: 'waiting'},
    approval: {symbol: '??', label: 'APPROVAL', role: 'approval'}, failed: {symbol: '!!', label: 'FAILED', role: 'danger'},
    indeterminate: {symbol: '!?', label: 'UNKNOWN', role: 'danger'}, cancelled: {symbol: '[]', label: 'CANCELLED', role: 'neutral'},
    succeeded: {symbol: 'OK', label: 'SUCCEEDED', role: 'success'}, skipped: {symbol: '--', label: 'SKIPPED', role: 'muted'},
    rejected: {symbol: 'NO', label: 'REJECTED', role: 'danger'}, cancelling: {symbol: '[]', label: 'STOPPING', role: 'waiting'},
    paused: {symbol: '||', label: 'PAUSED', role: 'waiting'}, ready: {symbol: '->', label: 'READY', role: 'strong'},
    pending: {symbol: ' .', label: 'PENDING', role: 'muted'},
  } satisfies Record<SemanticStatus, {symbol: string; label: string; role: ColorRole}>,
} as const;

const ansiPalette: Record<ColorRole, string | undefined> = {brand: 'cyan', strong: 'white', muted: 'gray', running: 'cyan', waiting: 'yellow', approval: 'magenta', danger: 'red', success: 'green', neutral: 'gray'};
const trueColorPalette: Record<ColorRole, string | undefined> = {brand: '#67e8f9', strong: '#f8fafc', muted: '#94a3b8', running: '#38bdf8', waiting: '#fbbf24', approval: '#c084fc', danger: '#fb7185', success: '#4ade80', neutral: '#cbd5e1'};

export function colorFor(role: ColorRole, mode: ColorMode): string | undefined {if (mode === 'mono') return undefined; return mode === 'truecolor' ? trueColorPalette[role] : ansiPalette[role]}
export function detectColorMode(output: {getColorDepth?: () => number}, environment: NodeJS.ProcessEnv = process.env): ColorMode {
  if ('NO_COLOR' in environment || environment.NODE_DISABLE_COLORS || environment.TERM === 'dumb') return 'mono';
  const depth = output.getColorDepth?.() ?? 1; if (depth >= 24) return 'truecolor'; if (depth >= 8) return 'ansi256'; if (depth >= 4) return 'ansi16'; return 'mono';
}
export function statusForConclusion(conclusion: Conclusion): SemanticStatus {return conclusion}
export function statusForNode(node: NodeSummary): SemanticStatus {
  if (node.type === 'approval' && node.phase === 'waiting') return 'approval'; if (node.conclusion) return statusForConclusion(node.conclusion);
  if (node.phase === 'skipped') return 'skipped'; if (node.phase === 'running') return 'running'; if (node.phase === 'waiting') return 'waiting';
  if (node.phase === 'ready') return 'ready'; if (node.phase === 'completed') return 'indeterminate'; return 'pending';
}
export function statusForRun(run: Pick<WorkflowSnapshot, 'phase' | 'conclusion' | 'reason'>): SemanticStatus {
  if (run.conclusion) return statusForConclusion(run.conclusion); if (run.reason === 'approval_required') return 'approval';
  const phase: RunPhase = run.phase; if (phase === 'running') return 'running'; if (phase === 'waiting') return 'waiting'; if (phase === 'paused') return 'paused'; if (phase === 'cancelling') return 'cancelling'; if (phase === 'completed') return 'indeterminate'; return 'pending';
}
export const statusBadgeWidth = 15;
export function statusBadgeText(status: SemanticStatus): string {const token = designTokens.status[status]; return `[${token.symbol} ${token.label.padEnd(10)}]`}
