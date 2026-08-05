import React, {useEffect, useState} from 'react';
import {Box, Text} from 'ink';
import type {WorkflowSnapshot} from '../bridge/types.js';
import {formatElapsed} from './text-reporter.js';

export function RunApp({snapshot, startedAt}: {snapshot: WorkflowSnapshot; startedAt: number}) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {const timer = setInterval(() => setNow(Date.now()), 250); return () => clearInterval(timer)}, []);
  const activeNodes = snapshot.topologicalOrder.map(id => snapshot.nodes[id]).filter(node => node && (node.phase === 'running' || node.phase === 'waiting'));
  return <Box flexDirection="column">
    <Text>run: {snapshot.id}</Text>
    <Text>workflow: {snapshot.workflowName}</Text>
    <Text>phase: {snapshot.phase}{snapshot.conclusion ? ` / ${snapshot.conclusion}` : ''}{snapshot.reason ? ` (${snapshot.reason})` : ''}</Text>
    {activeNodes.map(node => <Text key={node.id}>node: {node.id} / {node.phase}{node.reason ? ` (${node.reason})` : ''}{node.diagnostic ? ` — ${node.diagnostic}` : ''}</Text>)}
    <Text>elapsed: {formatElapsed(now - startedAt)}</Text>
    <Text>state: {snapshot.stateDir}</Text>
    {snapshot.summary ? <Text>summary: {snapshot.summary}</Text> : null}
  </Box>;
}
