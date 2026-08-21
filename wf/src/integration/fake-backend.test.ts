import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {mkdtemp, readFile, readdir, rm, stat} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
import {applicationRunToStatus, callApplication, type RunGetResponse, type RunStartResponse} from '../bridge/application.js';
import {EngineBridge} from '../bridge/engine.js';
import type {RunStatusView, WorkflowSnapshot} from '../bridge/types.js';

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

async function withTimeout<T>(promise: Promise<T>, label: string, timeoutMs = 10_000): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<T>((_, reject) => {timer = setTimeout(() => reject(new Error(`${label} timeout`)), timeoutMs)}),
    ]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}

async function closeBridge(bridge: EngineBridge): Promise<void> {
  await bridge.close();
}

async function stopTemporaryControlPlane(stateDir: string, signal: NodeJS.Signals = 'SIGTERM'): Promise<void> {
  let pid: number;
  try {
    const owner = JSON.parse(await readFile(join(stateDir, 'control-plane.json'), 'utf8')) as {pid?: number};
    if (!owner.pid) return;
    pid = owner.pid;
  } catch {return}
  try {process.kill(pid, signal)} catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ESRCH') return;
    throw error;
  }
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    try {process.kill(pid, 0)} catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ESRCH') return;
      throw error;
    }
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  throw new Error(`temporary Control Plane ${pid} did not exit`);
}

async function waitForRun(bridge: EngineBridge, runId: string, accept: (view: RunStatusView) => boolean, label: string): Promise<RunStatusView> {
  const deadline = Date.now() + 10_000;
  let latest: RunStatusView | undefined;
  while (Date.now() < deadline) {
    latest = applicationRunToStatus(await callApplication(bridge, 'run.get', {runId}) as RunGetResponse);
    if (accept(latest)) return latest;
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  throw new Error(`${label} timeout: ${JSON.stringify(latest)}`);
}

function adHocStart(project: string, task: string, requestId: string): Record<string, unknown> {
  return {project, workflow: {document: {apiVersion: 'fishyume/v2', name: 'ad-hoc', defaults: {agent: {driver: 'codex', target: 'local'}}, execution: {maxConcurrency: 1}, nodes: {'agent-1': {type: 'agent', task}}}}, clientRequestId: requestId};
}

async function waitForPersistedExecution(stateDir: string, runId: string): Promise<WorkflowSnapshot> {
  const runDir = join(stateDir, 'runs', runId);
  const deadline = Date.now() + 10_000;
  while (Date.now() < deadline) {
    try {
      const snapshot = JSON.parse(await readFile(join(runDir, 'run.json'), 'utf8')) as WorkflowSnapshot;
      const attempt = JSON.parse(await readFile(join(runDir, 'nodes', 'agent-1', 'attempts', '1', 'attempt.json'), 'utf8')) as {execution?: unknown};
      if (attempt.execution) return snapshot;
    } catch { /* persistence is not complete yet */ }
    await new Promise(resolve => setTimeout(resolve, 20));
  }
  throw new Error(`active Attempt persistence timeout for ${runId}`);
}

test('CLI bridge fake integration closes completed, waiting, failed, and error-path engines', {timeout: 45_000}, async () => {
  const testDir = dirname(fileURLToPath(import.meta.url));
  const projectRoot = join(testDir, '..', '..', '..');
  const engineRoot = join(projectRoot, 'wf-engine');
  const temporary = await mkdtemp(join(tmpdir(), 'wf-e2e-'));
  const enginePath = join(temporary, process.platform === 'win32' ? 'wf-engine.exe' : 'wf-engine');
  const codexPath = join(temporary, process.platform === 'win32' ? 'fake-codex.exe' : 'fake-codex');
  const stateDir = join(temporary, 'state');
  const bridges: EngineBridge[] = [];
  const environment = {
    FISHYUME_CODEX_PATH: codexPath,
    WF_STATE_DIR: stateDir,
  };
  let testError: unknown;
  try {
    for (const [output, target] of [[enginePath, './cmd/wf-engine'], [codexPath, './internal/driver/codexprocess/testdata/fake-agent']] as const) {
      const built = spawnSync('go', ['build', '-o', output, target], {cwd: engineRoot, encoding: 'utf8'});
      assert.equal(built.status, 0, built.stderr);
    }

    const bridge = bridgeWithEnvironment(enginePath, environment, bridges);
    const phases: string[] = [];
    let resolveTerminal!: () => void;
    const terminal = new Promise<void>(resolve => {resolveTerminal = resolve});
    bridge.onRunEvent(event => {
      phases.push(event.phase);
      if (event.phase === 'completed') resolveTerminal();
    });
    const hello = await bridge.hello(projectRoot);
    assert.equal(hello.backendReady, true);
    assert.equal(hello.projectReady, true);
    const started = await callApplication(bridge, 'run.start', adHocStart(projectRoot, 'fixture task', 'fake-integration-start')) as RunStartResponse;
    assert.match(started.runId, /^run-/);
    await withTimeout(terminal, 'terminal event');
    const view = applicationRunToStatus(await callApplication(bridge, 'run.get', {runId: started.runId}) as RunGetResponse);
    const snapshot = view.run as WorkflowSnapshot;
    assert.equal(snapshot.conclusion, 'succeeded');
    assert.ok(phases.includes('running'));
    assert.equal(phases.at(-1), 'completed');
    const firstRunDir = join(stateDir, 'runs', started.runId);
    const persistedSnapshot = JSON.parse(await readFile(join(firstRunDir, 'run.json'), 'utf8')) as WorkflowSnapshot;
    assert.equal(persistedSnapshot.conclusion, 'succeeded');
    const events = (await readFile(join(firstRunDir, 'events.jsonl'), 'utf8')).trim().split('\n').map(line => JSON.parse(line));
    assert.ok(events.length >= 4);
    assert.match(await readFile(join(firstRunDir, 'nodes', 'agent-1', 'attempts', '1', 'output.log'), 'utf8'), /turn.completed/);
    await closeBridge(bridge);

    const workflow = `apiVersion: fishyume/v2
name: fake-integration
inputs: {goal: {required: true}}
defaults: {agent: {driver: codex, target: local}}
execution: {maxConcurrency: 1}
nodes:
  plan: {type: agent, task: "Plan {{ inputs.goal }}", context: {dependencies: []}}
  approve: {type: approval, dependsOn: [plan], prompt: "Approve {{ nodes.plan.result.summary }}"}
  implement:
    type: agent
    dependsOn: [approve]
    when: {node: approve, field: result.decision, equals: approved}
    task: "Implement {{ nodes.plan.result.summary }}"
    context: {dependencies: [plan]}
  rejected:
    type: agent
    dependsOn: [approve]
    when: {node: approve, field: result.decision, equals: rejected}
    task: "Record {{ nodes.approve.result.reason }}"
    context: {dependencies: [approve]}
`;
    const workflowBridge = bridgeWithEnvironment(enginePath, environment, bridges);
    let resolveApproval!: () => void;
    const approval = new Promise<void>(resolve => {resolveApproval = resolve});
    workflowBridge.onRunEvent(event => {if (event.phase === 'waiting' && event.reason === 'approval_required') resolveApproval()});
    const workflowRun = await callApplication(workflowBridge, 'run.start', {project: projectRoot, workflow: {source: {filename: 'workflow.yaml', content: workflow}}, inputs: {goal: 'ship'}, clientRequestId: 'fake-workflow-start'}) as RunStartResponse;
    await withTimeout(approval, 'approval');
    await closeBridge(workflowBridge);

    const resumeBridge = bridgeWithEnvironment(enginePath, environment, bridges);
    let resolveWorkflowTerminal!: () => void;
    const workflowTerminal = new Promise<void>(resolve => {resolveWorkflowTerminal = resolve});
    resumeBridge.onRunEvent(event => {if (event.runId === workflowRun.runId && event.phase === 'completed') resolveWorkflowTerminal()});
    const waiting = applicationRunToStatus(await callApplication(resumeBridge, 'run.get', {runId: workflowRun.runId}) as RunGetResponse);
    await callApplication(resumeBridge, 'run.action', {actionId: 'fake-workflow-approve', runId: workflowRun.runId, type: 'approve', nodeId: 'approve', expectedStateVersion: waiting.run?.stateVersion});
    await withTimeout(workflowTerminal, 'workflow terminal');
    const workflowView = applicationRunToStatus(await callApplication(resumeBridge, 'run.get', {runId: workflowRun.runId}) as RunGetResponse);
    assert.equal(workflowView.run?.conclusion, 'succeeded');
    const rejected = workflowView.nodes?.find(node => node.id === 'rejected');
    assert.equal(rejected?.phase, 'skipped');
    assert.equal(rejected?.reason, 'condition_false');
    assert.equal((await readFile(join(stateDir, 'runs', workflowRun.runId, 'workflow.json'), 'utf8')).includes('topologicalOrder'), true);
    await closeBridge(resumeBridge);

    const failedBridge = bridgeWithEnvironment(enginePath, environment, bridges);
    let resolveFailure!: () => void;
    const failure = new Promise<void>(resolve => {resolveFailure = resolve});
    failedBridge.onRunEvent(event => {if (event.phase === 'completed') resolveFailure()});
    const failedRun = await callApplication(failedBridge, 'run.start', adHocStart(projectRoot, 'scenario:terminal-failed', 'fake-failed-start')) as RunStartResponse;
    await withTimeout(failure, 'failure terminal');
    const failedView = applicationRunToStatus(await callApplication(failedBridge, 'run.get', {runId: failedRun.runId}) as RunGetResponse);
    assert.equal(failedView.run?.conclusion, 'failed');
    await closeBridge(failedBridge);

    const errorBridge = bridgeWithEnvironment(enginePath, environment, bridges);
    try {
      await assert.rejects(errorBridge.call('unknown.method', {}), /method not found/);
    } finally {
      await closeBridge(errorBridge);
    }

    // Closing the client immediately after run.start must not own the Run
    // lifecycle. A second client observes the same durable Attempt to terminal.
    const starter = bridgeWithEnvironment(enginePath, environment, bridges);
    const detachedRun = await callApplication(starter, 'run.start', adHocStart(projectRoot, 'scenario:delayed-succeeded', 'fake-detached-start')) as RunStartResponse;
    await closeBridge(starter);
    const observer = bridgeWithEnvironment(enginePath, environment, bridges);
    const detachedFinal = await waitForRun(observer, detachedRun.runId, view => view.run?.phase === 'completed', 'detached Run completion');
    assert.equal(detachedFinal.run?.conclusion, 'succeeded');
    assert.deepEqual(await readdir(join(stateDir, 'runs', detachedRun.runId, 'nodes', 'agent-1', 'attempts')), ['1']);
    await closeBridge(observer);

    // Crash the Control Plane while the external Agent remains active. The
    // replacement must reconcile Attempt 1 and never dispatch Attempt 2.
    const crashStarter = bridgeWithEnvironment(enginePath, environment, bridges);
    const crashRun = await callApplication(crashStarter, 'run.start', adHocStart(projectRoot, 'scenario:active', 'fake-crash-start')) as RunStartResponse;
    const active = await waitForPersistedExecution(stateDir, crashRun.runId);
    await closeBridge(crashStarter);
    await stopTemporaryControlPlane(stateDir, 'SIGKILL');
    const recovery = bridgeWithEnvironment(enginePath, environment, bridges);
    const recovered = await waitForRun(recovery, crashRun.runId, view => view.activeAttempt?.number === 1, 'Control Plane recovery');
    assert.equal(recovered.activeAttempt?.number, 1);
    assert.deepEqual(await readdir(join(active.stateDir, 'nodes', 'agent-1', 'attempts')), ['1']);
    const expectedStateVersion = recovered.run?.stateVersion;
    await callApplication(recovery, 'run.action', {actionId: 'fake-recovered-cancel', runId: crashRun.runId, type: 'cancel', expectedStateVersion});
    const cancelled = await waitForRun(recovery, crashRun.runId, view => view.run?.conclusion === 'cancelled', 'recovered Run cancellation');
    assert.equal(cancelled.run?.phase, 'completed');
    assert.deepEqual(await readdir(join(active.stateDir, 'nodes', 'agent-1', 'attempts')), ['1']);
    await closeBridge(recovery);
  } catch (error) {
    testError = error;
    throw error;
  } finally {
    const cleanupErrors: unknown[] = [];
    for (const bridge of bridges.reverse()) {
      try {
        await closeBridge(bridge);
      } catch (error) {
        cleanupErrors.push(error);
      }
    }
    try {await stopTemporaryControlPlane(stateDir)} catch (error) {cleanupErrors.push(error)}
    try {
      await rm(temporary, {recursive: true, force: true, maxRetries: 3, retryDelay: 100});
      await assert.rejects(stat(temporary), (error: NodeJS.ErrnoException) => error.code === 'ENOENT');
    } catch (error) {
      cleanupErrors.push(error);
    }
    if (cleanupErrors.length > 0 && testError === undefined) throw new AggregateError(cleanupErrors, 'fake integration cleanup failed');
  }
});
