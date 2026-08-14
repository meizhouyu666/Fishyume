import React, {useEffect, useState} from 'react';
import {render, Text, useInput, useStdout, type Instance} from 'ink';
import type {EngineClient} from '../bridge/engine.js';
import {callApplication, type RunListResponse, type RunSummary} from '../bridge/application.js';
import {fitText, joinColumns} from './layout.js';

const activePhases = new Set(['created', 'running', 'waiting', 'paused', 'cancelling']);

export interface DashboardSnapshot {runs: RunSummary[]; selectedRunId?: string; loading?: boolean; error?: string}
export type DashboardResult = {kind: 'attach'; runId: string} | {kind: 'exit'};

export function orderDashboardRuns(runs: readonly RunSummary[]): RunSummary[] {
  return [...runs].sort((left, right) => {
    const leftActive = activePhases.has(left.phase) ? 0 : 1;
    const rightActive = activePhases.has(right.phase) ? 0 : 1;
    return leftActive - rightActive || Date.parse(right.updatedAt) - Date.parse(left.updatedAt) || left.runId.localeCompare(right.runId);
  });
}

function statusLabel(run: RunSummary): string {
  return (run.conclusion ?? run.phase).toUpperCase();
}

function elapsedLabel(updatedAt: string, now: number): string {
  const elapsed = Math.max(0, now - Date.parse(updatedAt));
  if (!Number.isFinite(elapsed)) return 'unknown';
  if (elapsed < 60_000) return `${Math.floor(elapsed / 1000)}s ago`;
  if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)}m ago`;
  if (elapsed < 86_400_000) return `${Math.floor(elapsed / 3_600_000)}h ago`;
  return `${Math.floor(elapsed / 86_400_000)}d ago`;
}

export function dashboardLines(snapshot: DashboardSnapshot, width: number, now = Date.now()): string[] {
  const boundedWidth = Math.max(40, width);
  const runs = orderDashboardRuns(snapshot.runs);
  const lines = [joinColumns('FISHYUME / RUNS', `${runs.filter(run => activePhases.has(run.phase)).length} active · ${runs.length} total`, boundedWidth), '─'.repeat(boundedWidth)];
  if (snapshot.error) lines.push(fitText(`connection · ${snapshot.error}`, boundedWidth));
  if (snapshot.loading && runs.length === 0) lines.push('Loading durable Runs…');
  else if (runs.length === 0) {
    lines.push('No durable Runs yet.', '', fitText('Ask your Host Agent to use Fishyume, or start one with: fishyume run "<task>"', boundedWidth));
  } else {
    for (const run of runs) {
      const selected = run.runId === snapshot.selectedRunId;
      const left = `${selected ? '›' : ' '} ${statusLabel(run).padEnd(12)} ${run.workflowName}`;
      const right = boundedWidth < 100 ? elapsedLabel(run.updatedAt, now) : `${run.driver}/${run.target} · ${elapsedLabel(run.updatedAt, now)}`;
      lines.push(joinColumns(left, right, boundedWidth));
      if (selected) lines.push(fitText(`  ${run.runId} · ${run.project}`, boundedWidth));
    }
  }
  lines.push('─'.repeat(boundedWidth), runs.length > 0 ? '↑/↓ or j/k select  Enter attach  r refresh  q exit' : 'r refresh  q exit');
  return lines;
}

export function dashboardText(runs: readonly RunSummary[], width = 120, now = Date.now()): string {
  const ordered = orderDashboardRuns(runs);
  const lines = dashboardLines({runs: ordered, selectedRunId: ordered[0]?.runId}, width, now);
  if (ordered.length > 0) lines.push('', `Open Dashboard: fishyume`, `Attach directly: fishyume attach ${ordered[0].runId}`);
  else lines.push('', 'Check readiness: fishyume doctor');
  return `${lines.join('\n')}\n`;
}

interface DashboardAppProps extends DashboardSnapshot {onAttach(runId: string): void; onRefresh(): void; onExit(): void; width?: number}

export function DashboardApp({runs: sourceRuns, selectedRunId: initialSelection, loading, error, onAttach, onRefresh, onExit, width: fixedWidth}: DashboardAppProps) {
  const {stdout} = useStdout();
  const runs = orderDashboardRuns(sourceRuns);
  const [selectedRunId, setSelectedRunId] = useState(initialSelection ?? runs[0]?.runId);
  useEffect(() => {
    if (selectedRunId && runs.some(run => run.runId === selectedRunId)) return;
    setSelectedRunId(runs[0]?.runId);
  }, [runs.map(run => run.runId).join('|'), selectedRunId]);
  useInput((input, key) => {
    if (input === 'q' || key.escape) {onExit(); return}
    if (input === 'r') {onRefresh(); return}
    if (key.return && selectedRunId) {onAttach(selectedRunId); return}
    if (runs.length === 0) return;
    const current = Math.max(0, runs.findIndex(run => run.runId === selectedRunId));
    if (key.upArrow || input === 'k') setSelectedRunId(runs[(current - 1 + runs.length) % runs.length].runId);
    if (key.downArrow || input === 'j') setSelectedRunId(runs[(current + 1) % runs.length].runId);
  });
  return <Text>{dashboardLines({runs, selectedRunId, loading, error}, Math.max(40, fixedWidth ?? stdout.columns ?? 80)).join('\n')}</Text>;
}

export async function runDashboard(client: EngineClient, limit = 50): Promise<DashboardResult> {
  let runs: RunSummary[] = [];
  let ink: Instance | undefined;
  let refreshing = false;
  let finished = false;
  let settle!: (result: DashboardResult) => void;
  const result = new Promise<DashboardResult>(resolve => {settle = resolve});
  const finish = (outcome: DashboardResult): void => {if (finished) return; finished = true; settle(outcome)};
  const draw = (snapshot: Omit<DashboardSnapshot, 'runs'> = {}): void => {
    if (finished) return;
    const props = {runs, ...snapshot, onAttach: (runId: string) => finish({kind: 'attach', runId}), onRefresh: () => {void refresh()}, onExit: () => finish({kind: 'exit'})};
    if (ink) ink.rerender(<DashboardApp {...props}/>); else ink = render(<DashboardApp {...props}/>, {exitOnCtrlC: false});
  };
  const refresh = async (): Promise<void> => {
    if (refreshing || finished) return;
    refreshing = true; draw({loading: runs.length === 0});
    try {
      const response = await callApplication(client, 'run.list', {limit}) as RunListResponse;
      runs = orderDashboardRuns(response.items); draw();
    } catch (error) {
      draw({error: error instanceof Error ? error.message : String(error)});
    } finally {refreshing = false}
  };
  const onInterrupt = (): void => finish({kind: 'exit'});
  process.once('SIGINT', onInterrupt);
  try {
    await refresh();
    const timer = setInterval(() => {void refresh()}, 2000);
    try {return await result} finally {clearInterval(timer)}
  } finally {
    process.off('SIGINT', onInterrupt);
    ink?.unmount();
  }
}
