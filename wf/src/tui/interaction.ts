import type {NodeSummary, ResumeAction, RunStatusView} from '../bridge/types.js';
import {fitText} from './layout.js';

const retryableWaitingReasons = new Set(['agent_waiting_input', 'completion_missing', 'invalid_result']);

export interface ActionableNode {
  nodeId: string;
  kind: 'approval' | 'retry';
  duplicateRisk: boolean;
}

export type ConsoleMode = 'idle' | 'reject' | 'retry-confirm' | 'retry-risk-confirm' | 'cancel-confirm';
export interface ConsoleInteractionState {
  selectedIndex: number;
  helpVisible: boolean;
  mode: ConsoleMode;
  rejectReason: string;
}

export type ConsoleInteractionEvent =
  | {type: 'move'; delta: -1 | 1; count: number}
  | {type: 'reconcile'; count: number}
  | {type: 'toggle-help'}
  | {type: 'escape'}
  | {type: 'begin-reject'}
  | {type: 'begin-retry'; duplicateRisk: boolean}
  | {type: 'begin-cancel'}
  | {type: 'append-reason'; text: string}
  | {type: 'backspace'}
  | {type: 'submitted'};

export const initialConsoleInteractionState: ConsoleInteractionState = {selectedIndex: 0, helpVisible: false, mode: 'idle', rejectReason: ''};

export function basicConsoleKeyEvent(input: string, key: {upArrow: boolean; downArrow: boolean; escape: boolean}, count: number): ConsoleInteractionEvent | undefined {
  if (key.escape) return {type: 'escape'};
  if (key.downArrow || input === 'j') return {type: 'move', delta: 1, count};
  if (key.upArrow || input === 'k') return {type: 'move', delta: -1, count};
  if (input === '?') return {type: 'toggle-help'};
  return undefined;
}

export function isWaitingApproval(node: NodeSummary): boolean {
  return node.type === 'approval' && node.phase === 'waiting' && node.reason === 'approval_required';
}

export function isRetryableNode(node: NodeSummary): boolean {
  if (!node.currentAttempt || node.currentAttempt < 1) return false;
  if (node.phase === 'waiting') return Boolean(node.reason && retryableWaitingReasons.has(node.reason));
  return node.phase === 'completed' && (node.conclusion === 'failed' || node.conclusion === 'indeterminate');
}

export function actionableNodes(view: RunStatusView): ActionableNode[] {
  const run = view.run;
  if (!run) return [];
  const result: ActionableNode[] = [];
  for (const nodeId of run.topologicalOrder) {
    const node = run.nodes[nodeId];
    if (!node) continue;
    if (isWaitingApproval(node)) result.push({nodeId, kind: 'approval', duplicateRisk: false});
    else if (isRetryableNode(node)) result.push({nodeId, kind: 'retry', duplicateRisk: node.conclusion === 'indeterminate'});
  }
  return result;
}

export function reconcileSelection(selectedIndex: number, count: number): number {
  if (count <= 0) return 0;
  return Math.min(Math.max(0, selectedIndex), count - 1);
}

export function transitionConsoleState(state: ConsoleInteractionState, event: ConsoleInteractionEvent): ConsoleInteractionState {
  switch (event.type) {
    case 'move': {
      if (event.count <= 0 || state.mode !== 'idle') return state;
      const selectedIndex = (state.selectedIndex + event.delta + event.count) % event.count;
      return {...state, selectedIndex};
    }
    case 'reconcile':
      return {...state, selectedIndex: reconcileSelection(state.selectedIndex, event.count), mode: 'idle', rejectReason: ''};
    case 'toggle-help':
      return state.mode === 'idle' ? {...state, helpVisible: !state.helpVisible} : state;
    case 'escape':
      return state.mode === 'idle' ? state : {...state, mode: 'idle', rejectReason: ''};
    case 'begin-reject':
      return state.mode === 'idle' ? {...state, mode: 'reject', rejectReason: ''} : state;
    case 'begin-retry':
      return state.mode === 'idle' ? {...state, mode: event.duplicateRisk ? 'retry-risk-confirm' : 'retry-confirm'} : state;
    case 'begin-cancel':
      return state.mode === 'idle' ? {...state, mode: 'cancel-confirm'} : state;
    case 'append-reason':
      return state.mode === 'reject' ? {...state, rejectReason: state.rejectReason + event.text.replace(/[\u0000-\u001f\u007f]/g, '')} : state;
    case 'backspace':
      return state.mode === 'reject' ? {...state, rejectReason: Array.from(state.rejectReason).slice(0, -1).join('')} : state;
    case 'submitted':
      return {...state, mode: 'idle', rejectReason: ''};
  }
}

export function resumeActionForMode(state: ConsoleInteractionState, target: ActionableNode): ResumeAction | undefined {
  if (state.mode === 'reject' && target.kind === 'approval') return {type: 'reject', nodeId: target.nodeId, reason: state.rejectReason};
  if (state.mode === 'retry-confirm' && target.kind === 'retry') return {type: 'retry', nodeId: target.nodeId, acknowledgeDuplicateRisk: false};
  if (state.mode === 'retry-risk-confirm' && target.kind === 'retry') return {type: 'retry', nodeId: target.nodeId, acknowledgeDuplicateRisk: true};
  return undefined;
}

export function approveAction(target: ActionableNode | undefined): ResumeAction | undefined {
  return target?.kind === 'approval' ? {type: 'approve', nodeId: target.nodeId} : undefined;
}

export function consolePanelLines(state: ConsoleInteractionState, target: ActionableNode | undefined, width: number, pending: boolean, message?: string): string[] {
  const lines: string[] = [];
  if (target) lines.push(`> selected ${target.nodeId} [${target.kind}${target.duplicateRisk ? ' · duplicate-risk' : ''}]`);
  else lines.push('> no actionable nodes; observation and cancel/detach remain available');
  if (pending) lines.push('ACTION pending · duplicate submissions are locked');
  else if (state.mode === 'reject') lines.push(`REJECT ${target?.nodeId ?? ''} · reason: ${state.rejectReason || '(empty; rejection will still be submitted)'}`, 'Enter submit · Esc discard');
  else if (state.mode === 'retry-confirm') lines.push(`RETRY ${target?.nodeId ?? ''}?`, 'y/Enter confirm · Esc discard');
  else if (state.mode === 'retry-risk-confirm') lines.push(`DUPLICATE-RISK: retrying ${target?.nodeId ?? ''} may repeat external effects.`, 'y/Enter explicitly acknowledge and retry · Esc discard');
  else if (state.mode === 'cancel-confirm') lines.push('CANCEL this run? Active work may be stopped.', 'y/Enter confirm · Esc discard');
  else lines.push('j/k or ↑/↓ select · a approve · r reject · R retry · c cancel · d/q detach · ? help');
  if (state.helpVisible && state.mode === 'idle') lines.push(
    'approve: a submits immediately for a waiting approval',
    'reject: r edits a reason; empty reason is allowed; Enter submits',
    'retry: R requires confirmation; indeterminate retries acknowledge duplicate risk',
    'leave: d/q/Ctrl+C never cancels; run detaches, status --watch only stops observing',
    'Esc closes the active input or confirmation mode',
  );
  if (message) lines.push(`ACTION ${message}`);
  return lines.map(line => fitText(line, width));
}
