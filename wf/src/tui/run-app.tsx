import React, {useEffect, useReducer, useState} from 'react';
import {Box, Text, useInput, useStdout} from 'ink';
import type {NodeSummary, ResumeAction, RunStatusView} from '../bridge/types.js';
import {AttemptRow, Header, HelpFooter, NodeRow, ProgressSummary, Section} from './components.js';
import {detectColorMode, type ColorMode} from './design-tokens.js';
import type {ActionResult} from './live-console.js';
import {actionableNodes, approveAction, basicConsoleKeyEvent, consolePanelLines, initialConsoleInteractionState, resumeActionForMode, transitionConsoleState} from './interaction.js';
import {fitText} from './layout.js';

export interface RunAppProps {
  view: RunStatusView;
  startedAt: number;
  width?: number;
  now?: number;
  colorMode?: ColorMode;
  onResume?: (action: ResumeAction) => Promise<ActionResult>;
  onCancel?: () => Promise<ActionResult>;
  onExit?: () => void;
}

export function RunApp({view, startedAt, width: fixedWidth, now: fixedNow, colorMode: fixedColorMode, onResume, onCancel, onExit}: RunAppProps) {
  const {stdout} = useStdout(); const [clock, setClock] = useState(fixedNow ?? Date.now());
  const [interaction, dispatch] = useReducer(transitionConsoleState, initialConsoleInteractionState);
  const [pending, setPending] = useState(false); const [message, setMessage] = useState<string>();
  useEffect(() => {if (fixedNow !== undefined) return; const timer = setInterval(() => setClock(Date.now()), 1000); return () => clearInterval(timer)}, [fixedNow]);
  const run = view.run;
  const width = Math.max(40, fixedWidth ?? stdout.columns ?? 80); const colorMode = fixedColorMode ?? detectColorMode(stdout);
  const targets = actionableNodes(view); const target = targets[interaction.selectedIndex]; const interactive = Boolean(onResume && onCancel && onExit);
  const targetSignature = targets.map(item => `${item.nodeId}:${item.kind}:${item.duplicateRisk}`).join('|');
  useEffect(() => {dispatch({type: 'reconcile', count: targets.length})}, [targetSignature]);
  const submitResume = async (action: ResumeAction): Promise<void> => {
    if (!onResume || pending) return;
    setPending(true); setMessage(undefined);
    try {const result = await onResume(action); setMessage(result.message); dispatch({type: 'submitted'})}
    finally {setPending(false)}
  };
  const submitCancel = async (): Promise<void> => {
    if (!onCancel || pending) return;
    setPending(true); setMessage(undefined);
    try {const result = await onCancel(); setMessage(result.message); dispatch({type: 'submitted'})}
    finally {setPending(false)}
  };
  useInput((input, key) => {
    if (!run || !interactive || pending) return;
    const basicEvent = basicConsoleKeyEvent(input, key, targets.length);
    if (basicEvent?.type === 'escape') {dispatch(basicEvent); return}
    if (interaction.mode === 'reject') {
      if (key.return) {const action = target ? resumeActionForMode(interaction, target) : undefined; if (action) void submitResume(action); return}
      if (key.backspace || key.delete) {dispatch({type: 'backspace'}); return}
      if (!key.ctrl && !key.meta && input) dispatch({type: 'append-reason', text: input});
      return;
    }
    if (interaction.mode === 'retry-confirm' || interaction.mode === 'retry-risk-confirm') {
      if (key.return || input.toLowerCase() === 'y') {const action = target ? resumeActionForMode(interaction, target) : undefined; if (action) void submitResume(action)}
      return;
    }
    if (interaction.mode === 'cancel-confirm') {if (key.return || input.toLowerCase() === 'y') void submitCancel(); return}
    if (key.ctrl && input.toLowerCase() === 'c') {onExit?.(); return}
    if (basicEvent) {dispatch(basicEvent); return}
    if (input === 'a') {const action = approveAction(target); if (action) void submitResume(action); return}
    if (input === 'r' && target?.kind === 'approval') {dispatch({type: 'begin-reject'}); return}
    if (input === 'R' && target?.kind === 'retry') {dispatch({type: 'begin-retry', duplicateRisk: target.duplicateRisk}); return}
    if (input === 'c' && run.phase !== 'completed') {dispatch({type: 'begin-cancel'}); return}
    if (input === 'd' || input === 'q') onExit?.();
  });
  if (!run) return <Text color="red">Fishyume TUI cannot render a missing run.</Text>;
  const nodes = run.topologicalOrder.map(id => run.nodes[id]).filter((node): node is NodeSummary => Boolean(node));
  const attempts = view.activeAttempts ?? (view.activeAttempt ? [view.activeAttempt] : []); const approvals = view.waitingApprovals ?? nodes.filter(node => node.type === 'approval' && node.phase === 'waiting');
  const diagnostics = view.diagnostics ?? nodes.filter(node => node.diagnostic).map(node => ({nodeId: node.id, reason: node.reason, message: node.diagnostic})); const contentWidth = width >= 100 ? width - 4 : width;
  return <Box flexDirection="column" width={width}>
    <Header run={run} width={width} elapsedMs={(fixedNow ?? clock) - startedAt} colorMode={colorMode}/>
    <ProgressSummary run={run} width={width} colorMode={colorMode}/>
    <Section title="WORKFLOW" width={width} colorMode={colorMode}>{nodes.map(node => {const targetIndex = targets.findIndex(item => item.nodeId === node.id); const marker = interactive ? targetIndex === interaction.selectedIndex && targetIndex >= 0 ? '> ' : targetIndex >= 0 ? '· ' : '  ' : ''; return <NodeRow key={node.id} node={node} width={contentWidth} colorMode={colorMode} marker={marker}/>})}</Section>
    {attempts.length ? <Section title={`ACTIVE ATTEMPTS (${attempts.length})`} width={width} colorMode={colorMode}>{attempts.map(attempt => <AttemptRow key={`${attempt.nodeId}:${attempt.number}`} attempt={attempt} width={contentWidth} colorMode={colorMode}/>)}</Section> : null}
    {approvals.length ? <Section title={`APPROVALS (${approvals.length})`} width={width} colorMode={colorMode}>{approvals.map(node => <NodeRow key={node.id} node={node} width={contentWidth} colorMode={colorMode}/>)}</Section> : null}
    {diagnostics.length ? <Section title={`DIAGNOSTICS (${diagnostics.length})`} width={width} colorMode={colorMode}>{diagnostics.map((diagnostic, index) => <Text key={`${diagnostic.nodeId}:${index}`}>{fitText(`${diagnostic.nodeId}${diagnostic.reason ? ` · ${diagnostic.reason}` : ''}${diagnostic.message ? ` · ${diagnostic.message}` : ''}`, contentWidth)}</Text>)}</Section> : null}
    {run.summary || run.reason ? <Section title="SUMMARY" width={width} colorMode={colorMode}><Text>{fitText(run.summary ?? run.reason ?? '', contentWidth)}</Text></Section> : null}
    {interactive ? <Section title="RUN CONSOLE" width={width} colorMode={colorMode}>{consolePanelLines(interaction, target, contentWidth, pending, message).map((line, index) => <Text key={index}>{line}</Text>)}</Section> : <HelpFooter view={view} width={width} colorMode={colorMode}/>}
  </Box>;
}
