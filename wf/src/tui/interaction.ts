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
  selectedNodeId?: string;
  helpVisible: boolean;
  mode: ConsoleMode;
  rejectReason: string;
  actionTarget?: ActionableNode;
}

export type ConsoleInteractionEvent =
  | {type: 'move'; delta: -1 | 1; targets: readonly ActionableNode[]}
  | {type: 'reconcile'; targets: readonly ActionableNode[]}
  | {type: 'toggle-help'}
  | {type: 'escape'}
  | {type: 'begin-reject'; target: ActionableNode}
  | {type: 'begin-retry'; target: ActionableNode}
  | {type: 'begin-cancel'}
  | {type: 'append-reason'; text: string}
  | {type: 'backspace'}
  | {type: 'submitted'};

export const initialConsoleInteractionState: ConsoleInteractionState = {selectedIndex: 0, helpVisible: false, mode: 'idle', rejectReason: ''};

export function basicConsoleKeyEvent(input: string, key: {upArrow: boolean; downArrow: boolean; escape: boolean}, targets: readonly ActionableNode[]): ConsoleInteractionEvent | undefined {
  if (key.escape) return {type: 'escape'};
  if (key.downArrow || input === 'j') return {type: 'move', delta: 1, targets};
  if (key.upArrow || input === 'k') return {type: 'move', delta: -1, targets};
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

function sameActionableNode(left: ActionableNode, right: ActionableNode): boolean {
  return left.nodeId === right.nodeId && left.kind === right.kind && left.duplicateRisk === right.duplicateRisk;
}

export function selectedActionableNode(state: ConsoleInteractionState, targets: readonly ActionableNode[]): ActionableNode | undefined {
  if (state.selectedNodeId) return targets.find(target => target.nodeId === state.selectedNodeId);
  return targets[reconcileSelection(state.selectedIndex, targets.length)];
}

export function boundActionableNode(state: ConsoleInteractionState, targets: readonly ActionableNode[]): ActionableNode | undefined {
  if (!state.actionTarget) return undefined;
  return targets.find(target => sameActionableNode(target, state.actionTarget!));
}

export function transitionConsoleState(state: ConsoleInteractionState, event: ConsoleInteractionEvent): ConsoleInteractionState {
  switch (event.type) {
    case 'move': {
      if (event.targets.length <= 0 || state.mode !== 'idle') return state;
      const currentIndex = state.selectedNodeId ? event.targets.findIndex(target => target.nodeId === state.selectedNodeId) : state.selectedIndex;
      const selectedIndex = ((currentIndex < 0 ? reconcileSelection(state.selectedIndex, event.targets.length) : currentIndex) + event.delta + event.targets.length) % event.targets.length;
      return {...state, selectedIndex, selectedNodeId: event.targets[selectedIndex]?.nodeId};
    }
    case 'reconcile': {
      const preservedIndex = state.selectedNodeId ? event.targets.findIndex(target => target.nodeId === state.selectedNodeId) : -1;
      const selectedIndex = preservedIndex >= 0 ? preservedIndex : reconcileSelection(state.selectedIndex, event.targets.length);
      const selectedNodeId = event.targets[selectedIndex]?.nodeId;
      const actionTarget = boundActionableNode(state, event.targets);
      if (state.actionTarget && !actionTarget) return {...state, selectedIndex, selectedNodeId, mode: 'idle', rejectReason: '', actionTarget: undefined};
      return {...state, selectedIndex, selectedNodeId, ...(actionTarget ? {actionTarget} : {})};
    }
    case 'toggle-help':
      return state.mode === 'idle' ? {...state, helpVisible: !state.helpVisible} : state;
    case 'escape':
      return state.mode === 'idle' ? state : {...state, mode: 'idle', rejectReason: '', actionTarget: undefined};
    case 'begin-reject':
      return state.mode === 'idle' && event.target.kind === 'approval' ? {...state, mode: 'reject', rejectReason: '', actionTarget: {...event.target}} : state;
    case 'begin-retry':
      return state.mode === 'idle' && event.target.kind === 'retry' ? {...state, mode: event.target.duplicateRisk ? 'retry-risk-confirm' : 'retry-confirm', actionTarget: {...event.target}} : state;
    case 'begin-cancel':
      return state.mode === 'idle' ? {...state, mode: 'cancel-confirm', actionTarget: undefined} : state;
    case 'append-reason':
      return state.mode === 'reject' ? {...state, rejectReason: state.rejectReason + event.text.replace(/[\u0000-\u001f\u007f]/g, '')} : state;
    case 'backspace':
      return state.mode === 'reject' ? {...state, rejectReason: Array.from(state.rejectReason).slice(0, -1).join('')} : state;
    case 'submitted':
      return {...state, mode: 'idle', rejectReason: '', actionTarget: undefined};
  }
}

export function resumeActionForMode(state: ConsoleInteractionState, targets: readonly ActionableNode[]): ResumeAction | undefined {
  const target = boundActionableNode(state, targets);
  if (!target) return undefined;
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
