import React, {useEffect, useReducer, useState} from 'react';
import {Text, useInput, useStdout} from 'ink';
import type {ResumeAction, RunStatusView} from '../bridge/types.js';
import {CalmConsole} from './components.js';
import {detectColorMode, detectSymbolMode, type ColorMode, type SymbolMode} from './design-tokens.js';
import type {ActionResult} from './live-console.js';
import {
  actionableNodes,
  approveAction,
  basicConsoleKeyEvent,
  boundActionableNode,
  initialConsoleInteractionState,
  resumeActionForMode,
  selectedNodeId,
  transitionConsoleState,
  type ActionableNode,
} from './interaction.js';
import {buildRunTextPresentation} from './presentation.js';

export interface RunAppProps {
  view: RunStatusView;
  startedAt: number;
  width?: number;
  now?: number;
  colorMode?: ColorMode;
  symbolMode?: SymbolMode;
  onResume?: (action: ResumeAction) => Promise<ActionResult>;
  onCancel?: () => Promise<ActionResult>;
  onExit?: () => void;
}

export type OperatorCommand = 'approve' | 'reject' | 'retry' | 'cancel' | 'detach';
export function operatorCommand(input: string): OperatorCommand | undefined {
  const normalized = input.toLowerCase();
  if (normalized === 'a' || normalized === 'y') return 'approve';
  if (input === 'r' || normalized === 'x' || normalized === 'n') return 'reject';
  if (input === 'R' || normalized === 't') return 'retry';
  if (normalized === 'c') return 'cancel';
  if (normalized === 'd' || normalized === 'q') return 'detach';
  return undefined;
}

export function RunApp({view, startedAt, width: fixedWidth, now: fixedNow, colorMode: fixedColorMode, symbolMode: fixedSymbolMode, onResume, onCancel, onExit}: RunAppProps) {
  const {stdout} = useStdout(); const [clock, setClock] = useState(fixedNow ?? Date.now());
  const initialNodeIds = view.run?.topologicalOrder.filter(id => Boolean(view.run?.nodes[id])) ?? [];
  const initialTarget = actionableNodes(view)[0]; const initialSelectedNodeId = initialTarget?.nodeId ?? initialNodeIds[0];
  const [interaction, dispatch] = useReducer(transitionConsoleState, {
    ...initialConsoleInteractionState,
    selectedIndex: Math.max(0, initialSelectedNodeId ? initialNodeIds.indexOf(initialSelectedNodeId) : 0),
    selectedNodeId: initialSelectedNodeId,
  });
  const [pending, setPending] = useState(false); const [message, setMessage] = useState<string>();
  const [pendingTarget, setPendingTarget] = useState<ActionableNode>();
  useEffect(() => {
    if (fixedNow !== undefined) return;
    const timer = setInterval(() => setClock(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [fixedNow]);

  const run = view.run;
  const width = Math.max(40, fixedWidth ?? stdout.columns ?? 80);
  const colorMode = fixedColorMode ?? detectColorMode(stdout); const symbolMode = fixedSymbolMode ?? detectSymbolMode();
  const targets = actionableNodes(view); const nodeIds = run?.topologicalOrder.filter(id => Boolean(run.nodes[id])) ?? [];
  const visualNodeId = selectedNodeId(interaction, nodeIds); const target = targets.find(item => item.nodeId === visualNodeId);
  const boundTarget = boundActionableNode(interaction, targets); const interactive = Boolean(onResume && onCancel && onExit);
  const targetSignature = targets.map(item => `${item.nodeId}:${item.kind}:${item.duplicateRisk}:${item.expectedAttempt ?? ''}:${item.questionIds?.join(',') ?? ''}`).join('|');
  const nodeSignature = nodeIds.join('|');
  useEffect(() => {dispatch({type: 'reconcile', nodeIds, actionTargets: targets})}, [nodeSignature, targetSignature]);

  const submitResume = async (action: ResumeAction, actionTarget: ActionableNode): Promise<void> => {
    if (!onResume || pending) return;
    setPendingTarget({...actionTarget}); setPending(true); setMessage(undefined);
    try {const result = await onResume(action); setMessage(result.message); dispatch({type: 'submitted'})}
    finally {setPending(false)}
  };
  const submitCancel = async (): Promise<void> => {
    if (!onCancel || pending) return;
    setPendingTarget(undefined); setPending(true); setMessage(undefined);
    try {const result = await onCancel(); setMessage(result.message); dispatch({type: 'submitted'})}
    finally {setPending(false)}
  };
  const currentAction = (): {action: ResumeAction; target: ActionableNode} | undefined => {
    const action = resumeActionForMode(interaction, targets); const actionTarget = boundActionableNode(interaction, targets);
    if (!action || !actionTarget) {setMessage('目标状态已经变化，操作已安全取消'); dispatch({type: 'reconcile', nodeIds, actionTargets: targets}); return undefined}
    return {action, target: actionTarget};
  };

  useInput((input, key) => {
    if (!run || !interactive || pending) return;
    if (key.escape) {dispatch({type: 'escape'}); return}
    if (interaction.mode === 'reject') {
      if (key.return) {const current = currentAction(); if (current) void submitResume(current.action, current.target); return}
      if (key.backspace || key.delete) {dispatch({type: 'backspace'}); return}
      if (!key.ctrl && !key.meta && input) dispatch({type: 'append-reason', text: input});
      return;
    }
    if (interaction.mode === 'answer') {
      if (key.return) {const current = currentAction(); if (current) void submitResume(current.action, current.target); return}
      if (key.backspace || key.delete) {dispatch({type: 'backspace'}); return}
      if (!key.ctrl && !key.meta && input) dispatch({type: 'append-answer', text: input});
      return;
    }
    if (interaction.mode === 'retry-confirm' || interaction.mode === 'retry-risk-confirm') {
      if (key.return || input.toLowerCase() === 'y') {const current = currentAction(); if (current) void submitResume(current.action, current.target)}
      return;
    }
    if (interaction.mode === 'cancel-confirm') {if (key.return || input.toLowerCase() === 'y') void submitCancel(); return}
    if (key.ctrl && input.toLowerCase() === 'c') {onExit?.(); return}
    const basicEvent = basicConsoleKeyEvent(input, key, nodeIds);
    if (basicEvent) {setMessage(undefined); setPendingTarget(undefined); dispatch(basicEvent); return}
    const command = operatorCommand(input);
    if (command === 'approve') {
      if (target?.kind === 'answer') {dispatch({type: 'begin-answer', target}); return}
      const action = approveAction(target); if (action && target) void submitResume(action, target); return
    }
    if (command === 'reject' && target?.kind === 'approval') {dispatch({type: 'begin-reject', target}); return}
    if (command === 'retry' && target?.kind === 'retry') {dispatch({type: 'begin-retry', target}); return}
    if (command === 'cancel' && run.phase !== 'completed' && run.phase !== 'cancelling') {dispatch({type: 'begin-cancel'}); return}
    if (command === 'detach') onExit?.();
  });

  if (!run) return <Text color="red">Fishyume 无法显示这个任务：缺少 Run 数据。</Text>;
  const terminalUpdatedAt = Date.parse(run.updatedAt); const elapsedNow = run.phase === 'completed' && Number.isFinite(terminalUpdatedAt) ? terminalUpdatedAt : (fixedNow ?? clock);
  const presentation = buildRunTextPresentation(view, width, Math.max(0, elapsedNow - startedAt), {
    selectedNodeId: visualNodeId,
    detailExpanded: interaction.detailExpanded,
    symbolMode,
    interactive,
    action: interactive ? {interaction, actionable: targets, pending, pendingTarget: pendingTarget ?? boundTarget, message} : undefined,
  });
  return <CalmConsole presentation={presentation} width={width} colorMode={colorMode} symbolMode={symbolMode}/>;
}
