import type {NodeSummary, ResumeAction, RunStatusView} from '../bridge/types.js';

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
  detailExpanded: boolean;
  helpVisible: boolean;
  mode: ConsoleMode;
  rejectReason: string;
  actionTarget?: ActionableNode;
}

interface ReconcileEvent {type: 'reconcile'; nodeIds: readonly string[]; actionTargets: readonly ActionableNode[]}
export type ConsoleInteractionEvent =
  | {type: 'move'; delta: -1 | 1; nodeIds: readonly string[]}
  | ReconcileEvent
  | {type: 'toggle-detail'}
  | {type: 'toggle-help'}
  | {type: 'escape'}
  | {type: 'begin-reject'; target: ActionableNode}
  | {type: 'begin-retry'; target: ActionableNode}
  | {type: 'begin-cancel'}
  | {type: 'append-reason'; text: string}
  | {type: 'backspace'}
  | {type: 'submitted'};

export const initialConsoleInteractionState: ConsoleInteractionState = {
  selectedIndex: 0, detailExpanded: true, helpVisible: false, mode: 'idle', rejectReason: '',
};

export function basicConsoleKeyEvent(
  input: string,
  key: {upArrow: boolean; downArrow: boolean; escape: boolean; return?: boolean},
  nodeIds: readonly string[],
): ConsoleInteractionEvent | undefined {
  if (key.escape) return {type: 'escape'};
  if (key.downArrow || input === 'j') return {type: 'move', delta: 1, nodeIds};
  if (key.upArrow || input === 'k') return {type: 'move', delta: -1, nodeIds};
  if (key.return) return {type: 'toggle-detail'};
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

export function selectedNodeId(state: ConsoleInteractionState, nodeIds: readonly string[]): string | undefined {
  if (state.selectedNodeId && nodeIds.includes(state.selectedNodeId)) return state.selectedNodeId;
  return nodeIds[reconcileSelection(state.selectedIndex, nodeIds.length)];
}

export function selectedActionableNode(state: ConsoleInteractionState, targets: readonly ActionableNode[]): ActionableNode | undefined {
  return targets.find(target => target.nodeId === state.selectedNodeId);
}

export function boundActionableNode(state: ConsoleInteractionState, targets: readonly ActionableNode[]): ActionableNode | undefined {
  if (!state.actionTarget) return undefined;
  return targets.find(target => sameActionableNode(target, state.actionTarget!));
}

export function transitionConsoleState(state: ConsoleInteractionState, event: ConsoleInteractionEvent): ConsoleInteractionState {
  switch (event.type) {
    case 'move': {
      if (event.nodeIds.length <= 0 || state.mode !== 'idle') return state;
      const currentIndex = state.selectedNodeId ? event.nodeIds.indexOf(state.selectedNodeId) : state.selectedIndex;
      const base = currentIndex < 0 ? reconcileSelection(state.selectedIndex, event.nodeIds.length) : currentIndex;
      const selectedIndex = (base + event.delta + event.nodeIds.length) % event.nodeIds.length;
      return {...state, selectedIndex, selectedNodeId: event.nodeIds[selectedIndex], helpVisible: false};
    }
    case 'reconcile': {
      const preservedIndex = state.selectedNodeId ? event.nodeIds.indexOf(state.selectedNodeId) : -1;
      const selectedIndex = preservedIndex >= 0 ? preservedIndex : reconcileSelection(state.selectedIndex, event.nodeIds.length);
      const selectedNodeId = event.nodeIds[selectedIndex];
      const actionTarget = boundActionableNode(state, event.actionTargets);
      if (state.actionTarget && !actionTarget) return {...state, selectedIndex, selectedNodeId, mode: 'idle', rejectReason: '', actionTarget: undefined};
      return {...state, selectedIndex, selectedNodeId, ...(actionTarget ? {actionTarget} : {})};
    }
    case 'toggle-detail':
      return state.mode === 'idle' ? {...state, detailExpanded: !state.detailExpanded, helpVisible: false} : state;
    case 'toggle-help':
      return state.mode === 'idle' ? {...state, helpVisible: !state.helpVisible} : state;
    case 'escape':
      return state.mode === 'idle' ? {...state, helpVisible: false} : {...state, mode: 'idle', rejectReason: '', actionTarget: undefined};
    case 'begin-reject':
      return state.mode === 'idle' && event.target.kind === 'approval' ? {...state, mode: 'reject', rejectReason: '', actionTarget: {...event.target}, helpVisible: false} : state;
    case 'begin-retry':
      return state.mode === 'idle' && event.target.kind === 'retry' ? {...state, mode: event.target.duplicateRisk ? 'retry-risk-confirm' : 'retry-confirm', actionTarget: {...event.target}, helpVisible: false} : state;
    case 'begin-cancel':
      return state.mode === 'idle' ? {...state, mode: 'cancel-confirm', actionTarget: undefined, helpVisible: false} : state;
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
