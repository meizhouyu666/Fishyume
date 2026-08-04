import React, {useEffect, useState} from 'react';
import {Box, Text} from 'ink';
import type {RunSnapshot} from '../bridge/types.js';
import {formatElapsed} from './text-reporter.js';

export function RunApp({snapshot, startedAt}: {snapshot: RunSnapshot; startedAt: number}) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 250);
    return () => clearInterval(timer);
  }, []);
  return <Box flexDirection="column">
    <Text>run: {snapshot.id}</Text>
    <Text>status: {snapshot.status} / {snapshot.nodeStatus}</Text>
    <Text>elapsed: {formatElapsed(now - startedAt)}</Text>
    <Text>state: {snapshot.stateDir}</Text>
    {snapshot.summary ? <Text>summary: {snapshot.summary}</Text> : null}
  </Box>;
}
