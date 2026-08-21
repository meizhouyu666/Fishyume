import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {mkdtemp, readFile, readdir, rm, stat} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
import {Client} from '@modelcontextprotocol/sdk/client/index.js';
import {InMemoryTransport} from '@modelcontextprotocol/sdk/inMemory.js';
import {EngineBridge} from '../bridge/engine.js';
import type {ApplicationRunView, RunActionResponse, RunEventsResponse, RunGetResponse, RunStartResponse} from '../bridge/application.js';
import {createMCPServer} from '../mcp/server.js';
import {LiveConsoleController} from '../tui/live-console.js';

const workflow = {
  apiVersion: 'fishyume/v2',
  name: 'mcp-tui-concurrency-acceptance',
  defaults: {agent: {driver: 'codex', target: 'local'}},
  context: {projectInstructions: ['README.md']},
  execution: {maxConcurrency: 1},
  nodes: {
    approve: {type: 'approval', prompt: 'Start the active Agent?'},
    execute: {type: 'agent', dependsOn: ['approve'], task: 'scenario:active\nRemain active until explicitly cancelled.', context: {dependencies: []}},
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

function textContent(value: unknown): string {
  const content = (value as {content?: Array<{type?: string; text?: string}>}).content ?? [];
  const text = content.find(item => item.type === 'text')?.text;
  assert.ok(text, `MCP response did not contain text content: ${JSON.stringify(value)}`);
  return text;
}

async function callTool<T>(client: Client, name: string, args: Record<string, unknown>): Promise<T> {
  const response = await client.callTool({name, arguments: args});
  assert.notEqual(response.isError, true, `${name} returned an MCP error: ${textContent(response)}`);
  return JSON.parse(textContent(response)) as T;
}

async function submitCancel(client: Client, runId: string, actionPrefix: string): Promise<RunActionResponse | undefined> {
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    const current = await callTool<RunGetResponse>(client, 'run.get', {runId});
    if (current.run.phase === 'completed') return undefined;
    const response = await client.callTool({
      name: 'run.action',
      arguments: {actionId: `${actionPrefix}-${current.run.stateVersion}`, runId, type: 'cancel', expectedStateVersion: current.run.stateVersion},
    });
    if (response.isError !== true) return JSON.parse(textContent(response)) as RunActionResponse;
    const body = JSON.parse(textContent(response)) as {error?: {code?: string}};
    if (body.error?.code !== 'conflict') throw new Error(`cancellation failed: ${textContent(response)}`);
  }
  throw new Error(`cancellation for ${runId} did not converge`);
}

async function cancelForCleanup(client: Client, runId: string): Promise<void> {
  await submitCancel(client, runId, 'cleanup-cancel');
  await waitForRun(client, runId, run => run.phase === 'completed', 'cleanup cancellation');
}

async function waitForRun(client: Client, runId: string, accept: (run: ApplicationRunView) => boolean, label: string): Promise<ApplicationRunView> {
  let afterSequence = 0;
  let latest: ApplicationRunView | undefined;
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    const events = await callTool<RunEventsResponse>(client, 'run.events', {runId, afterSequence, limit: 50, waitMs: 250});
    afterSequence = events.nextAfterSequence;
    const view = await callTool<RunGetResponse>(client, 'run.get', {runId});
    latest = view.run;
    if (accept(view.run)) return view.run;
  }
  throw new Error(`${label} timeout: ${JSON.stringify(latest)}`);
}

async function stopTemporaryControlPlane(stateDir: string): Promise<void> {
  let pid: number | undefined;
  try {pid = (JSON.parse(await readFile(join(stateDir, 'control-plane.json'), 'utf8')) as {pid?: number}).pid} catch {return}
  if (!pid) return;
  try {process.kill(pid, 'SIGTERM')} catch (error) {
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

test('MCP Host and TUI controller share state, conflict safely, and detach without cancelling', {timeout: 90_000}, async () => {
  const testDir = dirname(fileURLToPath(import.meta.url));
  const projectRoot = join(testDir, '..', '..', '..');
  const engineRoot = join(projectRoot, 'wf-engine');
  const temporary = await mkdtemp(join(tmpdir(), 'fishyume-mcp-tui-'));
  const enginePath = join(temporary, process.platform === 'win32' ? 'wf-engine.exe' : 'wf-engine');
  const codexPath = join(temporary, process.platform === 'win32' ? 'fake-codex.exe' : 'fake-codex');
  const stateDir = join(temporary, 'state');
  const bridges: EngineBridge[] = [];
  const environment = {FISHYUME_CODEX_PATH: codexPath, FISHYUME_STATE_DIR: stateDir, WF_STATE_DIR: stateDir};
  const engineBuild = spawnSync('go', ['build', '-o', enginePath, './cmd/wf-engine'], {cwd: engineRoot, encoding: 'utf8'});
  assert.equal(engineBuild.status, 0, engineBuild.stderr);
  const codexBuild = spawnSync('go', ['build', '-o', codexPath, './internal/driver/codexprocess/testdata/fake-agent'], {cwd: engineRoot, encoding: 'utf8'});
  assert.equal(codexBuild.status, 0, codexBuild.stderr);

  const mcpBridge = bridgeWithEnvironment(enginePath, environment, bridges);
  const tuiBridge = bridgeWithEnvironment(enginePath, environment, bridges);
  const server = createMCPServer(mcpBridge);
  const host = new Client({name: 'fishyume-concurrent-host', version: '1.0.0'});
  const [hostTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  let controller: LiveConsoleController | undefined;
  let hostConnected = false;
  let startedRunId: string | undefined;
  let bodyFailure: unknown;
  try {
    await Promise.all([server.connect(serverTransport), host.connect(hostTransport)]);
    hostConnected = true;
    const started = await callTool<RunStartResponse>(host, 'run.start', {
      project: projectRoot,
      workflow: {document: workflow},
      clientRequestId: 'mcp-tui-concurrency-1',
    });
    startedRunId = started.runId;
    const waiting = await waitForRun(host, started.runId, run => run.phase === 'waiting' && run.nodes.some(node => node.nodeId === 'approve' && node.phase === 'waiting'), 'approval');
    const tuiViews: number[] = [];
    controller = new LiveConsoleController(tuiBridge, started.runId, {
      mode: 'run',
      onView(view) {if (view.run?.stateVersion !== undefined) tuiViews.push(view.run.stateVersion)},
    });
    const tuiInitial = await controller.start();
    assert.equal(tuiInitial.run?.stateVersion, waiting.stateVersion, 'MCP and TUI must observe the same durable state version');
    assert.equal(tuiInitial.waitingApprovals?.[0]?.id, 'approve');

    const mcpAction = host.callTool({
      name: 'run.action',
      arguments: {actionId: 'mcp-concurrent-approve', runId: started.runId, type: 'approve', expectedStateVersion: waiting.stateVersion, nodeId: 'approve'},
    });
    const tuiAction = controller.resume({type: 'approve', nodeId: 'approve'});
    const [mcpOutcome, tuiOutcome] = await Promise.all([mcpAction, tuiAction]);
    const mcpSucceeded = mcpOutcome.isError !== true;
    assert.notEqual(mcpSucceeded, tuiOutcome.ok, 'exactly one stale-version concurrent action must succeed');
    if (!mcpSucceeded) {
      const body = JSON.parse(textContent(mcpOutcome)) as {error?: {code?: string}};
      assert.equal(body.error?.code, 'conflict');
    } else {
      const response = JSON.parse(textContent(mcpOutcome)) as RunActionResponse;
      assert.equal(response.type, 'approve');
      assert.match(tuiOutcome.message, /state version conflict/);
    }

    const active = await waitForRun(host, started.runId, run => run.phase === 'running' && run.nodes.some(node => node.nodeId === 'execute' && node.phase === 'running'), 'active Agent');
    await controller.refresh();
    assert.equal(controller.view?.run?.stateVersion, active.stateVersion, 'TUI refresh must converge on the Host-observed state');
    assert.ok(tuiViews.some(version => version > waiting.stateVersion), 'TUI must receive a post-action state update');

    await controller.detach();
    await controller.close();
    controller = undefined;
    await tuiBridge.close();
    const afterDetach = await callTool<RunGetResponse>(host, 'run.get', {runId: started.runId});
    assert.equal(afterDetach.run.phase, 'running');
    assert.equal(afterDetach.run.cancelRequested, false, 'TUI detach/connection close must not cancel the Run');
    assert.equal(afterDetach.run.nodes.find(node => node.nodeId === 'execute')?.currentAttempt, 1);

    const cancelled = await submitCancel(host, started.runId, 'mcp-explicit-cancel');
    assert.equal(cancelled?.type, 'cancel');
    const terminal = await waitForRun(host, started.runId, run => run.phase === 'completed', 'explicit cancellation');
    assert.equal(terminal.conclusion, 'cancelled');
    assert.deepEqual(await readdir(join(stateDir, 'runs', started.runId, 'nodes', 'execute', 'attempts')), ['1'], 'multi-client actions must not duplicate the Agent Attempt');
    const events = await callTool<RunEventsResponse>(host, 'run.events', {runId: started.runId, limit: 100});
    assert.ok(events.events.every((event, index) => index === 0 || event.sequence > events.events[index - 1]!.sequence));
    assert.equal(events.events.filter(event => event.nodeId === 'execute' && event.type === 'node.running').length, 1);
  } catch (error) {bodyFailure = error}

  const cleanupFailures: unknown[] = [];
  const cleanupStep = async (step: () => Promise<unknown>): Promise<void> => {
    try {await step()} catch (error) {cleanupFailures.push(error)}
  };
  if (hostConnected && startedRunId) await cleanupStep(() => cancelForCleanup(host, startedRunId));
  if (controller) await cleanupStep(() => controller!.close());
  const cleanup = await Promise.allSettled([host.close(), server.close(), ...bridges.reverse().map(bridge => bridge.close())]);
  cleanupFailures.push(...cleanup.filter((result): result is PromiseRejectedResult => result.status === 'rejected').map(result => result.reason));
  await cleanupStep(() => stopTemporaryControlPlane(stateDir));
  await cleanupStep(() => rm(temporary, {recursive: true, force: true, maxRetries: 10, retryDelay: 100}));
  await cleanupStep(() => assert.rejects(stat(temporary), (error: NodeJS.ErrnoException) => error.code === 'ENOENT'));
  if (cleanupFailures.length > 0) throw new AggregateError([...(bodyFailure === undefined ? [] : [bodyFailure]), ...cleanupFailures], 'acceptance or cleanup failed');
  if (bodyFailure !== undefined) throw bodyFailure;
});
