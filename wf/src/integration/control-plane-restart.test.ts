import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {mkdtemp, readFile, readdir, rm, stat} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
import {EngineBridge} from '../bridge/engine.js';
import type {ApplicationRunView, RunActionRequest, RunActionResponse, RunEventsResponse, RunGetResponse, RunStartResponse} from '../bridge/application.js';

const workflow = {
  apiVersion: 'fishyume/v1',
  name: 'control-plane-restart-acceptance',
  defaults: {agent: {driver: 'codex', target: 'local'}},
  execution: {maxConcurrency: 1},
  nodes: {
    approve: {type: 'approval', prompt: 'Start the active Agent?'},
    execute: {type: 'agent', dependsOn: ['approve'], task: 'scenario:active\nRemain active across Control Plane restart.'},
  },
};

function bridgeWithEnvironment(enginePath: string, environment: Record<string, string>, bridges: EngineBridge[]): EngineBridge {
  const previous = new Map<string, string | undefined>();
  for (const [key, value] of Object.entries(environment)) {
    previous.set(key, process.env[key]);
    process.env[key] = value;
  }
  try {
    const bridge = new EngineBridge(enginePath);
    bridges.push(bridge);
    return bridge;
  } finally {
    for (const [key, value] of previous) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
}

async function waitForRun(bridge: EngineBridge, runId: string, accept: (run: ApplicationRunView) => boolean, label: string): Promise<ApplicationRunView> {
  let afterSequence = 0;
  let latest: ApplicationRunView | undefined;
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    const events = await bridge.call<RunEventsResponse>('run.events', {runId, afterSequence, limit: 100, waitMs: 250});
    afterSequence = events.nextAfterSequence;
    const view = await bridge.call<RunGetResponse>('run.get', {runId});
    latest = view.run;
    if (accept(view.run)) return view.run;
  }
  throw new Error(`${label} timeout: ${JSON.stringify(latest)}`);
}

async function waitForPersistedExecution(stateDir: string, runId: string, nodeId: string, attempt: number): Promise<void> {
  const path = join(stateDir, 'runs', runId, 'nodes', nodeId, 'attempts', String(attempt), 'attempt.json');
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    try {
      const snapshot = JSON.parse(await readFile(path, 'utf8')) as {execution?: unknown};
      if (snapshot.execution) return;
    } catch { /* launch is still committing its durable handle */ }
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  throw new Error(`persisted execution handle timeout: ${path}`);
}

async function submitCancel(bridge: EngineBridge, runId: string, actionPrefix: string): Promise<void> {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    const current = await bridge.call<RunGetResponse>('run.get', {runId});
    if (current.run.phase === 'completed') return;
    const request: RunActionRequest = {actionId: `${actionPrefix}-${current.run.stateVersion}`, runId, type: 'cancel', expectedStateVersion: current.run.stateVersion};
    try {
      await bridge.call<RunActionResponse>('run.action', request);
      await waitForRun(bridge, runId, run => run.phase === 'completed', 'cancellation');
      return;
    } catch (error) {
      if (!(error instanceof Error) || !error.message.includes('conflict')) throw error;
    }
  }
  throw new Error(`cancellation for ${runId} did not converge`);
}

async function stopTemporaryControlPlane(stateDir: string, signal: NodeJS.Signals = 'SIGTERM'): Promise<void> {
  let pid: number | undefined;
  try {pid = (JSON.parse(await readFile(join(stateDir, 'control-plane.json'), 'utf8')) as {pid?: number}).pid} catch {return}
  if (!pid) return;
  try {process.kill(pid, signal)} catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ESRCH') return;
    throw error;
  }
  const deadline = Date.now() + 5_000;
  while (Date.now() < deadline) {
    try {process.kill(pid, 0)} catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ESRCH') return;
      throw error;
    }
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  throw new Error(`temporary Control Plane ${pid} did not exit`);
}

test('Application Run survives Control Plane crash/restart without duplicate Attempt or lost action receipt', {timeout: 120_000}, async () => {
  const testDir = dirname(fileURLToPath(import.meta.url));
  const projectRoot = join(testDir, '..', '..', '..');
  const engineRoot = join(projectRoot, 'wf-engine');
  const temporary = await mkdtemp(join(tmpdir(), 'fishyume-control-restart-'));
  const enginePath = join(temporary, process.platform === 'win32' ? 'wf-engine.exe' : 'wf-engine');
  const codexPath = join(temporary, process.platform === 'win32' ? 'fake-codex.exe' : 'fake-codex');
  const stateDir = join(temporary, 'state');
  const bridges: EngineBridge[] = [];
  const environment = {FISHYUME_CODEX_PATH: codexPath, FISHYUME_STATE_DIR: stateDir, WF_STATE_DIR: stateDir};
  const engineBuild = spawnSync('go', ['build', '-o', enginePath, './cmd/wf-engine'], {cwd: engineRoot, encoding: 'utf8'});
  assert.equal(engineBuild.status, 0, engineBuild.stderr);
  const codexBuild = spawnSync('go', ['build', '-o', codexPath, './internal/backend/directcli/testdata/fake-agent'], {cwd: engineRoot, encoding: 'utf8'});
  assert.equal(codexBuild.status, 0, codexBuild.stderr);

  let starter: EngineBridge | undefined;
  let recovery: EngineBridge | undefined;
  let runId: string | undefined;
  let cleanupBridge: EngineBridge | undefined;
  let controlPlaneCrashed = false;
  let bodyFailure: unknown;
  try {
    starter = bridgeWithEnvironment(enginePath, environment, bridges);
    cleanupBridge = starter;
    const started = await starter.call<RunStartResponse>('run.start', {project: projectRoot, workflow: {document: workflow}, clientRequestId: 'control-restart-1'});
    runId = started.runId;
    const waiting = await waitForRun(starter, runId, run => run.phase === 'waiting' && run.nodes.some(node => node.nodeId === 'approve' && node.phase === 'waiting'), 'approval');
    const approveRequest: RunActionRequest = {actionId: 'control-restart-approve-1', runId, type: 'approve', expectedStateVersion: waiting.stateVersion, nodeId: 'approve'};
    const approved = await starter.call<RunActionResponse>('run.action', approveRequest);
    const active = await waitForRun(starter, runId, run => run.phase === 'running' && run.nodes.some(node => node.nodeId === 'execute' && node.phase === 'running'), 'active Attempt');
    assert.equal(active.nodes.find(node => node.nodeId === 'execute')?.currentAttempt, 1);
    await waitForPersistedExecution(stateDir, runId, 'execute', 1);

    await starter.close();
    await stopTemporaryControlPlane(stateDir, 'SIGKILL');
    controlPlaneCrashed = true;
    recovery = bridgeWithEnvironment(enginePath, environment, bridges);
    cleanupBridge = recovery;
    await recovery.hello(projectRoot);
    const recovered = await waitForRun(recovery, runId, run => run.phase === 'running' && run.nodes.some(node => node.nodeId === 'execute' && node.phase === 'running'), 'recovered active Attempt');
    assert.equal(recovered.nodes.find(node => node.nodeId === 'execute')?.currentAttempt, 1);
    assert.deepEqual(await readdir(join(stateDir, 'runs', runId, 'nodes', 'execute', 'attempts')), ['1'], 'restart must not dispatch Attempt 2');

    const replayed = await recovery.call<RunActionResponse>('run.action', approveRequest);
    assert.deepEqual(replayed, approved, 'durable action receipt must replay after Control Plane restart');
    await submitCancel(recovery, runId, 'control-restart-cancel');
    const cancelled = await recovery.call<RunGetResponse>('run.get', {runId});
    assert.equal(cancelled.run.phase, 'completed');
    assert.equal(cancelled.run.conclusion, 'cancelled');
    assert.deepEqual(await readdir(join(stateDir, 'runs', runId, 'nodes', 'execute', 'attempts')), ['1']);
  } catch (error) {
    bodyFailure = error;
  }

  const cleanupFailures: unknown[] = [];
  const cleanupStep = async (step: () => Promise<unknown>): Promise<void> => {
    try {await step()} catch (error) {cleanupFailures.push(error)}
  };
  if (runId) {
    await cleanupStep(async () => {
      if (controlPlaneCrashed || !cleanupBridge) cleanupBridge = bridgeWithEnvironment(enginePath, environment, bridges);
      await submitCancel(cleanupBridge!, runId!, 'cleanup-cancel');
    });
  }
  const closes = await Promise.allSettled(bridges.reverse().map(bridge => bridge.close()));
  cleanupFailures.push(...closes.filter((result): result is PromiseRejectedResult => result.status === 'rejected').map(result => result.reason));
  await cleanupStep(() => stopTemporaryControlPlane(stateDir));
  await cleanupStep(() => rm(temporary, {recursive: true, force: true, maxRetries: 10, retryDelay: 100}));
  await cleanupStep(() => assert.rejects(stat(temporary), (error: NodeJS.ErrnoException) => error.code === 'ENOENT'));
  if (cleanupFailures.length > 0) throw new AggregateError([...(bodyFailure === undefined ? [] : [bodyFailure]), ...cleanupFailures], 'acceptance or cleanup failed');
  if (bodyFailure !== undefined) throw bodyFailure;
});
