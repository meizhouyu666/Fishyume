import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {mkdtemp, readFile, rm, stat} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
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

function assertExited(bridge: EngineBridge): void {
  assert.ok(bridge.child.exitCode !== null || bridge.child.signalCode !== null, `child ${bridge.child.pid ?? 'unknown'} is still running`);
}

async function closeAndAssert(bridge: EngineBridge): Promise<void> {
  await bridge.close();
  assertExited(bridge);
}

test('CLI bridge fake integration closes completed, waiting, failed, and error-path engines', {timeout: 45_000}, async () => {
  const testDir = dirname(fileURLToPath(import.meta.url));
  const projectRoot = join(testDir, '..', '..', '..');
  const engineRoot = join(projectRoot, 'wf-engine');
  const temporary = await mkdtemp(join(tmpdir(), 'wf-e2e-'));
  const enginePath = join(temporary, process.platform === 'win32' ? 'wf-engine.exe' : 'wf-engine');
  const ctlPath = join(temporary, process.platform === 'win32' ? 'cc-panes-ctl.exe' : 'cc-panes-ctl');
  const stateDir = join(temporary, 'state');
  const bridges: EngineBridge[] = [];
  const environment = {
    WF_CCPANES_CTL: ctlPath,
    FISHYUME_CCPANES_PROFILE_ID: 'fishyume-test-profile',
    WF_STATE_DIR: stateDir,
    WF_FAKE_PROJECT: projectRoot,
  };
  let testError: unknown;
  try {
    for (const [output, target] of [[enginePath, './cmd/wf-engine'], [ctlPath, './internal/backend/ccpanes/testdata/fake-cc-panes-ctl']] as const) {
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
    const started = await bridge.call<{runId: string}>('run.start', {project: projectRoot, tool: 'codex', runtime: 'local', task: 'fixture task'});
    assert.match(started.runId, /^run-/);
    await withTimeout(terminal, 'terminal event');
    const view = await bridge.call<RunStatusView>('run.status', {runId: started.runId});
    const snapshot = view.run as WorkflowSnapshot;
    assert.equal(snapshot.conclusion, 'succeeded');
    assert.ok(phases.includes('running'));
    assert.equal(phases.at(-1), 'completed');
    const persistedSnapshot = JSON.parse(await readFile(join(snapshot.stateDir, 'run.json'), 'utf8')) as WorkflowSnapshot;
    assert.equal(persistedSnapshot.conclusion, 'succeeded');
    const events = (await readFile(join(snapshot.stateDir, 'events.jsonl'), 'utf8')).trim().split('\n').map(line => JSON.parse(line));
    assert.ok(events.length >= 4);
    assert.match(await readFile(join(snapshot.stateDir, 'nodes', 'agent-1', 'attempts', '1', 'output.log'), 'utf8'), /fixture agent output/);
    await closeAndAssert(bridge);

    const workflow = `apiVersion: wf/v1
name: fake-integration
inputs: {goal: {required: true}}
defaults: {tool: codex, runtime: local}
execution: {maxConcurrency: 1}
nodes:
  plan: {type: agent, task: "Plan {{ inputs.goal }}"}
  approve: {type: approval, dependsOn: [plan], prompt: "Approve {{ nodes.plan.result.summary }}"}
  implement:
    type: agent
    dependsOn: [approve]
    when: {node: approve, field: result.decision, equals: approved}
    task: "Implement {{ nodes.plan.result.summary }}"
  rejected:
    type: agent
    dependsOn: [approve]
    when: {node: approve, field: result.decision, equals: rejected}
    task: "Record {{ nodes.approve.result.reason }}"
`;
    const workflowBridge = bridgeWithEnvironment(enginePath, environment, bridges);
    let resolveApproval!: () => void;
    const approval = new Promise<void>(resolve => {resolveApproval = resolve});
    workflowBridge.onRunEvent(event => {if (event.phase === 'waiting' && event.reason === 'approval_required') resolveApproval()});
    const workflowRun = await workflowBridge.call<{runId: string}>('run.startWorkflow', {project: projectRoot, filename: 'workflow.yaml', content: workflow, inputs: {goal: 'ship'}});
    await withTimeout(approval, 'approval');
    await closeAndAssert(workflowBridge);

    const resumeBridge = bridgeWithEnvironment(enginePath, environment, bridges);
    let resolveWorkflowTerminal!: () => void;
    const workflowTerminal = new Promise<void>(resolve => {resolveWorkflowTerminal = resolve});
    resumeBridge.onRunEvent(event => {if (event.runId === workflowRun.runId && event.phase === 'completed') resolveWorkflowTerminal()});
    await resumeBridge.call('run.resume', {runId: workflowRun.runId, action: {type: 'approve', nodeId: 'approve'}});
    await withTimeout(workflowTerminal, 'workflow terminal');
    const workflowView = await resumeBridge.call<RunStatusView>('run.status', {runId: workflowRun.runId});
    assert.equal(workflowView.run?.conclusion, 'succeeded');
    const rejected = workflowView.nodes?.find(node => node.id === 'rejected');
    assert.equal(rejected?.phase, 'skipped');
    assert.equal(rejected?.reason, 'condition_false');
    assert.equal((await readFile(join(workflowView.run!.stateDir, 'workflow.json'), 'utf8')).includes('topologicalOrder'), true);
    await closeAndAssert(resumeBridge);

    const failedBridge = bridgeWithEnvironment(enginePath, {...environment, WF_FAKE_BINDING_STATUS: 'failed'}, bridges);
    let resolveFailure!: () => void;
    const failure = new Promise<void>(resolve => {resolveFailure = resolve});
    failedBridge.onRunEvent(event => {if (event.phase === 'completed') resolveFailure()});
    const failedRun = await failedBridge.call<{runId: string}>('run.start', {project: projectRoot, tool: 'codex', runtime: 'local', task: 'fixture failure'});
    await withTimeout(failure, 'failure terminal');
    const failedView = await failedBridge.call<RunStatusView>('run.status', {runId: failedRun.runId});
    assert.equal(failedView.run?.conclusion, 'failed');
    await closeAndAssert(failedBridge);

    const errorBridge = bridgeWithEnvironment(enginePath, environment, bridges);
    try {
      await assert.rejects(errorBridge.call('unknown.method', {}), /method not found/);
    } finally {
      await closeAndAssert(errorBridge);
    }
  } catch (error) {
    testError = error;
    throw error;
  } finally {
    const cleanupErrors: unknown[] = [];
    for (const bridge of bridges.reverse()) {
      try {
        await closeAndAssert(bridge);
      } catch (error) {
        cleanupErrors.push(error);
      }
    }
    try {
      await rm(temporary, {recursive: true, force: true, maxRetries: 3, retryDelay: 100});
      await assert.rejects(stat(temporary), (error: NodeJS.ErrnoException) => error.code === 'ENOENT');
    } catch (error) {
      cleanupErrors.push(error);
    }
    if (cleanupErrors.length > 0 && testError === undefined) throw new AggregateError(cleanupErrors, 'fake integration cleanup failed');
  }
});
