import React, {useEffect, useState} from 'react';
import {Box, Text} from 'ink';
import type {WorkflowSnapshot} from '../bridge/types.js';
import {formatElapsed} from './text-reporter.js';

export function RunApp({snapshot, startedAt}: {snapshot: WorkflowSnapshot; startedAt: number}) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {const timer = setInterval(() => setNow(Date.now()), 250); return () => clearInterval(timer)}, []);
  const active = snapshot.activeNodeId ? snapshot.nodes[snapshot.activeNodeId] : undefined;
  return <Box flexDirection="column">
    <Text>run: {snapshot.id}</Text>
    <Text>workflow: {snapshot.workflowName}</Text>
    <Text>phase: {snapshot.phase}{snapshot.conclusion ? ` / ${snapshot.conclusion}` : ''}{snapshot.reason ? ` (${snapshot.reason})` : ''}</Text>
    {active ? <Text>node: {active.id} / {active.phase}</Text> : null}
    <Text>elapsed: {formatElapsed(now - startedAt)}</Text>
    <Text>state: {snapshot.stateDir}</Text>
    {snapshot.summary ? <Text>summary: {snapshot.summary}</Text> : null}
  </Box>;
}
