import React, {useEffect, useState} from 'react';
import {Box, Text, useStdout} from 'ink';
import type {NodeSummary, RunStatusView} from '../bridge/types.js';
import {AttemptRow, Header, HelpFooter, NodeRow, ProgressSummary, Section} from './components.js';
import {detectColorMode, type ColorMode} from './design-tokens.js';
import {fitText} from './layout.js';

export interface RunAppProps {view: RunStatusView; startedAt: number; width?: number; now?: number; colorMode?: ColorMode}

export function RunApp({view, startedAt, width: fixedWidth, now: fixedNow, colorMode: fixedColorMode}: RunAppProps) {
  const {stdout} = useStdout(); const [clock, setClock] = useState(fixedNow ?? Date.now());
  useEffect(() => {if (fixedNow !== undefined) return; const timer = setInterval(() => setClock(Date.now()), 1000); return () => clearInterval(timer)}, [fixedNow]);
  const run = view.run; if (!run) return <Text color="red">Fishyume TUI cannot render a missing run.</Text>;
  const width = Math.max(40, fixedWidth ?? stdout.columns ?? 80); const colorMode = fixedColorMode ?? detectColorMode(stdout);
  const nodes = run.topologicalOrder.map(id => run.nodes[id]).filter((node): node is NodeSummary => Boolean(node));
  const attempts = view.activeAttempts ?? (view.activeAttempt ? [view.activeAttempt] : []); const approvals = view.waitingApprovals ?? nodes.filter(node => node.type === 'approval' && node.phase === 'waiting');
  const diagnostics = view.diagnostics ?? nodes.filter(node => node.diagnostic).map(node => ({nodeId: node.id, reason: node.reason, message: node.diagnostic})); const contentWidth = width >= 100 ? width - 4 : width;
  return <Box flexDirection="column" width={width}>
    <Header run={run} width={width} elapsedMs={(fixedNow ?? clock) - startedAt} colorMode={colorMode}/>
    <ProgressSummary run={run} width={width} colorMode={colorMode}/>
    <Section title="WORKFLOW" width={width} colorMode={colorMode}>{nodes.map(node => <NodeRow key={node.id} node={node} width={contentWidth} colorMode={colorMode}/>)}</Section>
    {attempts.length ? <Section title={`ACTIVE ATTEMPTS (${attempts.length})`} width={width} colorMode={colorMode}>{attempts.map(attempt => <AttemptRow key={`${attempt.nodeId}:${attempt.number}`} attempt={attempt} width={contentWidth} colorMode={colorMode}/>)}</Section> : null}
    {approvals.length ? <Section title={`APPROVALS (${approvals.length})`} width={width} colorMode={colorMode}>{approvals.map(node => <NodeRow key={node.id} node={node} width={contentWidth} colorMode={colorMode}/>)}</Section> : null}
    {diagnostics.length ? <Section title={`DIAGNOSTICS (${diagnostics.length})`} width={width} colorMode={colorMode}>{diagnostics.map((diagnostic, index) => <Text key={`${diagnostic.nodeId}:${index}`}>{fitText(`${diagnostic.nodeId}${diagnostic.reason ? ` · ${diagnostic.reason}` : ''}${diagnostic.message ? ` · ${diagnostic.message}` : ''}`, contentWidth)}</Text>)}</Section> : null}
    {run.summary || run.reason ? <Section title="SUMMARY" width={width} colorMode={colorMode}><Text>{fitText(run.summary ?? run.reason ?? '', contentWidth)}</Text></Section> : null}
    <HelpFooter view={view} width={width} colorMode={colorMode}/>
  </Box>;
}
