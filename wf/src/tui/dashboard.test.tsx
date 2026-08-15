import assert from 'node:assert/strict';
import test from 'node:test';
import type {EngineClient} from '../bridge/engine.js';
import type {RunSummary} from '../bridge/application.js';
import {showDashboard} from '../commands/dashboard.js';
import {assertWidth, displayWidth} from './layout.js';
import {dashboardLines, dashboardText, orderDashboardRuns} from './dashboard.js';

const now = Date.parse('2026-08-15T00:00:00Z');
function run(overrides: Partial<RunSummary>): RunSummary {
  return {runId: 'run-1', workflowName: 'release', project: 'E:\\project', driver: 'codex', target: 'local', phase: 'completed', conclusion: 'succeeded', stateVersion: 8, createdAt: '2026-08-14T23:00:00Z', updatedAt: '2026-08-14T23:59:00Z', ...overrides};
}

test('Dashboard prioritizes active work and keeps 80/120/160 column output bounded', () => {
  const runs = [
    run({runId: 'run-old', workflowName: 'old result', updatedAt: '2026-08-14T23:59:50Z'}),
    run({runId: 'run-waiting', workflowName: 'approval', phase: 'waiting', conclusion: undefined, updatedAt: '2026-08-14T23:58:00Z'}),
    run({runId: 'run-running', workflowName: 'implementation', phase: 'running', conclusion: undefined, updatedAt: '2026-08-14T23:59:00Z'}),
  ];
  assert.deepEqual(orderDashboardRuns(runs).map(item => item.runId), ['run-running', 'run-waiting', 'run-old']);
  for (const width of [80, 120, 160]) {
    const lines = dashboardLines({runs, selectedRunId: 'run-running'}, width, now);
    assert.equal(assertWidth(lines, width), true, `${width} columns`);
    assert.match(lines.join('\n'), /2 个进行中 · 共 3 个/);
    assert.match(lines.join('\n'), /Enter 打开/);
  }
  assert.match(dashboardLines({runs, selectedRunId: 'run-waiting'}, 80, now).join('\n'), /等待你的处理.*Enter 打开/);
  const workflowNames = ['implementation', 'approval', 'old result'];
  const statusColumns = dashboardLines({runs, selectedRunId: 'run-running'}, 120, now)
    .map(line => ({line, workflowName: workflowNames.find(name => line.includes(name))}))
    .filter((item): item is {line: string; workflowName: string} => Boolean(item.workflowName))
    .map(({line, workflowName}) => displayWidth(line.slice(0, line.indexOf(workflowName))));
  assert.deepEqual(statusColumns, [15, 15, 15]);
});

test('Dashboard empty and non-interactive surfaces give executable next steps', () => {
  assert.match(dashboardLines({runs: []}, 80, now).join('\n'), /目前没有可显示的任务/);
  assert.match(dashboardText([], 80, now), /fishyume doctor/);
  assert.match(dashboardText([run({})], 80, now), /fishyume attach run-1/);
});

test('non-interactive Dashboard lists Runs once and closes its client', async () => {
  let output = ''; let closed = false;
  const client = {
    async call<T>(method: string): Promise<T> {
      assert.equal(method, 'run.list');
      return {apiVersion: 'fishyume.application/v1', items: [run({})]} as T;
    },
    async close(): Promise<void> {closed = true},
  } as unknown as EngineClient;
  assert.equal(await showDashboard(client, false, {write(text) {output += text}}, 10), 0);
  assert.match(output, /run-1/);
  assert.equal(closed, true);
});
