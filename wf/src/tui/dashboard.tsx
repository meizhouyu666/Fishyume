import React, {useEffect, useState} from 'react';
import {render, Text, useInput, useStdout, type Instance} from 'ink';
import type {EngineClient} from '../bridge/engine.js';
import {callApplication, type RunListResponse, type RunSummary} from '../bridge/application.js';
import {displayWidth, fitText, joinColumns, padDisplay} from './layout.js';

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
  const labels: Record<string, string> = {
    created: '刚创建', running: '运行中', waiting: '需要处理', paused: '已暂停', cancelling: '取消中', completed: '已结束',
    succeeded: '已完成', failed: '已失败', rejected: '已拒绝', cancelled: '已取消', indeterminate: '待确认',
  };
  const value = run.conclusion ?? run.phase;
  return labels[value] ?? value;
}

function elapsedLabel(updatedAt: string, now: number): string {
  const elapsed = Math.max(0, now - Date.parse(updatedAt));
  if (!Number.isFinite(elapsed)) return '时间未知';
  if (elapsed < 10_000) return '刚刚更新';
  if (elapsed < 60_000) return `${Math.floor(elapsed / 1000)} 秒前`;
  if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)} 分钟前`;
  if (elapsed < 86_400_000) return `${Math.floor(elapsed / 3_600_000)} 小时前`;
  return `${Math.floor(elapsed / 86_400_000)} 天前`;
}

function runRow(run: RunSummary, selected: boolean, width: number, now: number): string {
  const prefix = `${selected ? '›' : ' '} ${padDisplay(statusLabel(run), 12)} `;
  const rawRight = width < 100 ? elapsedLabel(run.updatedAt, now) : `${run.driver}/${run.target} · ${elapsedLabel(run.updatedAt, now)}`;
  const right = fitText(rawRight, Math.max(1, width - displayWidth(prefix) - 3));
  const workflowWidth = Math.max(1, width - displayWidth(prefix) - displayWidth(right) - 2);
  const left = `${prefix}${fitText(run.workflowName, workflowWidth)}`;
  return `${left}${' '.repeat(Math.max(2, width - displayWidth(left) - displayWidth(right)))}${right}`;
}

export function dashboardLines(snapshot: DashboardSnapshot, width: number, now = Date.now()): string[] {
  const boundedWidth = Math.max(40, width);
  const runs = orderDashboardRuns(snapshot.runs);
  const lines = [joinColumns('FISHYUME / 任务面板', `${runs.filter(run => activePhases.has(run.phase)).length} 个进行中 · 共 ${runs.length} 个`, boundedWidth), '─'.repeat(boundedWidth)];
  if (snapshot.error) lines.push(fitText(`连接失败 · ${snapshot.error}`, boundedWidth));
  if (snapshot.loading && runs.length === 0) lines.push('正在读取任务…');
  else if (runs.length === 0) {
    lines.push('目前没有可显示的任务。', '', fitText('请让 Codex、Claude 等 Host Agent 使用 Fishyume 创建任务；也可以运行：fishyume run "<任务>"', boundedWidth));
  } else {
    for (const run of runs) {
      const selected = run.runId === snapshot.selectedRunId;
      lines.push(runRow(run, selected, boundedWidth, now));
      if (selected) {
        lines.push(fitText(`  任务标识 ${run.runId} · 项目 ${run.project}`, boundedWidth));
        if (run.phase === 'waiting' || run.phase === 'paused') lines.push(fitText('  ⚠ 这个任务正在等待你的处理，按 Enter 打开。', boundedWidth));
      }
    }
  }
  lines.push('─'.repeat(boundedWidth), runs.length > 0 ? '↑/↓ 或 J/K 选择 · Enter 打开 · R 刷新 · Q 退出' : 'R 刷新 · Q 退出');
  return lines;
}

export function dashboardText(runs: readonly RunSummary[], width = 120, now = Date.now()): string {
  const ordered = orderDashboardRuns(runs);
  const lines = dashboardLines({runs: ordered, selectedRunId: ordered[0]?.runId}, width, now);
  if (ordered.length > 0) lines.push('', '打开任务面板：fishyume', `直接打开任务：fishyume attach ${ordered[0].runId}`);
  else lines.push('', '检查运行环境：fishyume doctor');
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
    const normalized = input.toLowerCase();
    if (normalized === 'q' || key.escape) {onExit(); return}
    if (normalized === 'r') {onRefresh(); return}
    if (key.return && selectedRunId) {onAttach(selectedRunId); return}
    if (runs.length === 0) return;
    const current = Math.max(0, runs.findIndex(run => run.runId === selectedRunId));
    if (key.upArrow || normalized === 'k') setSelectedRunId(runs[(current - 1 + runs.length) % runs.length].runId);
    if (key.downArrow || normalized === 'j') setSelectedRunId(runs[(current + 1) % runs.length].runId);
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
