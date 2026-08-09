import assert from 'node:assert/strict';
import test from 'node:test';
import type {EngineClient, EventListener} from '../bridge/engine.js';
import type {RunActionResponse, RunGetResponse} from '../bridge/application.js';
import type {EngineHello, ResumeAction, RunEvent, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {LiveConsoleController} from './live-console.js';

const createdAt = '2026-08-06T00:00:00Z';
function view(phase: WorkflowSnapshot['phase'], updatedAt = createdAt): RunStatusView {
  return {protocolVersion: 2, legacy: false, run: {protocolVersion: 2, stateVersion: 7, id: 'run-1', workflowName: 'fixture', project: 'p', backend: 'direct', phase, ...(phase === 'completed' ? {conclusion: 'succeeded' as const} : {}), topologicalOrder: ['node', 'approve', 'failed', 'unknown'], nodes: {node: {id: 'node', type: 'agent', phase: phase === 'completed' ? 'completed' : 'running', ...(phase === 'completed' ? {conclusion: 'succeeded' as const} : {})}, approve: {id: 'approve', type: 'approval', phase: 'waiting', reason: 'approval_required'}, failed: {id: 'failed', type: 'agent', phase: 'completed', conclusion: 'failed', currentAttempt: 2}, unknown: {id: 'unknown', type: 'agent', phase: 'completed', conclusion: 'indeterminate', currentAttempt: 3}}, cancelRequested: false, stateDir: 'state', createdAt, updatedAt}};
}

function applicationView(status: RunStatusView): RunGetResponse {
  const run = status.run!;
  return {apiVersion: 'fishyume.application/v1', run: {runId: run.id, workflowName: run.workflowName, project: run.project, driver: run.resolvedDriver ?? 'codex', target: run.resolvedTarget ?? 'local', phase: run.phase, conclusion: run.conclusion, stateVersion: run.stateVersion ?? 0, createdAt: run.createdAt, updatedAt: run.updatedAt, summary: run.summary, cancelRequested: run.cancelRequested, effectiveConcurrency: run.effectiveConcurrency ?? 1, topologicalOrder: run.topologicalOrder, nodes: Object.values(run.nodes).map(node => ({nodeId: node.id, type: node.type, phase: node.phase, conclusion: node.conclusion, reason: node.reason, diagnostic: node.diagnostic, currentAttempt: node.currentAttempt})), deprecationWarnings: []}};
}

class FakeClient implements EngineClient {
  calls: Array<{method: string; params?: unknown}> = [];
  listener?: EventListener;
  closed = false;
  statusViews: Array<RunStatusView | Promise<RunStatusView>> = [view('running')];
  mutations: Array<Promise<RunActionResponse> | RunActionResponse> = [];
  async hello(): Promise<EngineHello> {throw new Error('not used')}
  async call<T>(method: string, params?: unknown): Promise<T> {
    this.calls.push({method, params});
    if (method === 'run.get') {
      const next = this.statusViews.shift() ?? view('running');
      return applicationView(await next) as T;
    }
    if (method === 'run.action') {
      const next = this.mutations.shift() ?? {apiVersion: 'fishyume.application/v1', actionId: 'fixture', runId: 'run-1', type: 'approve', stateVersion: 8, phase: 'running'};
      return await next as T;
    }
    if (method === 'run.detach') return view('paused').run as T;
    throw new Error(`unexpected ${method}`);
  }
  onRunEvent(listener: EventListener): () => void {this.listener = listener; return () => {this.listener = undefined}}
  onDiagnostic(): () => void {return () => undefined}
  async close(): Promise<void> {this.closed = true}
  emit(event: RunEvent): void {this.listener?.(event)}
}

function deferred<T>() {let resolve!: (value: T) => void; const promise = new Promise<T>(done => {resolve = done}); return {promise, resolve}}
const tick = () => new Promise<void>(resolve => setImmediate(resolve));

test('event generations prevent an older status response from replacing a newer terminal state', async () => {
  const client = new FakeClient(); const older = deferred<RunStatusView>();
  client.statusViews = [view('running', '2026-08-06T00:00:01Z'), older.promise, view('completed', '2026-08-06T00:00:03Z')];
  const seen: RunStatusView[] = [];
  const controller = new LiveConsoleController(client, 'run-1', {mode: 'run', onView: item => seen.push(item)});
  await controller.start();
  const refresh = controller.refresh();
  client.emit({protocolVersion: 2, runId: 'run-1', sequence: 2, type: 'run.completed', phase: 'completed', conclusion: 'succeeded', timestamp: '2026-08-06T00:00:03Z'});
  older.resolve(view('running', '2026-08-06T00:00:02Z'));
  await refresh; await tick(); await tick();
  assert.equal(controller.view?.run?.phase, 'completed');
  assert.equal(seen.at(-1)?.run?.updatedAt, '2026-08-06T00:00:03Z');
  await controller.close();
});

test('resume and cancel bind Application action identity and observed state', async () => {
  const client = new FakeClient(); client.statusViews = Array.from({length: 6}, () => view('waiting'));
  const controller = new LiveConsoleController(client, 'run-1', {mode: 'run', onView() {}}); await controller.start();
  const actions: ResumeAction[] = [
    {type: 'approve', nodeId: 'approve'},
    {type: 'reject', nodeId: 'approve', reason: ''},
    {type: 'retry', nodeId: 'failed', acknowledgeDuplicateRisk: false},
    {type: 'retry', nodeId: 'unknown', acknowledgeDuplicateRisk: true},
  ];
  for (const action of actions) assert.equal((await controller.resume(action)).ok, true);
  assert.equal((await controller.cancel()).ok, true);
  const mutations = client.calls.filter(call => call.method === 'run.action').map(call => call.params as Record<string, unknown>);
  assert.equal(mutations.length, 5);
  for (const mutation of mutations) {assert.match(String(mutation.actionId), /^action-/); assert.equal(mutation.runId, 'run-1'); assert.equal(mutation.expectedStateVersion, 7)}
  assert.deepEqual(mutations.map(({actionId: _, ...rest}) => rest), [
    {runId: 'run-1', type: 'approve', expectedStateVersion: 7, nodeId: 'approve'},
    {runId: 'run-1', type: 'reject', expectedStateVersion: 7, nodeId: 'approve', reason: ''},
    {runId: 'run-1', type: 'retry', expectedStateVersion: 7, nodeId: 'failed', expectedAttempt: 2, acknowledgeDuplicateRisk: false},
    {runId: 'run-1', type: 'retry', expectedStateVersion: 7, nodeId: 'unknown', expectedAttempt: 3, acknowledgeDuplicateRisk: true},
    {runId: 'run-1', type: 'cancel', expectedStateVersion: 7},
  ]);
  assert.equal(client.calls.filter(call => call.method === 'run.get').length, 6);
  await controller.close();
});

test('pending mutation lock suppresses duplicate submissions', async () => {
  const client = new FakeClient(); const mutation = deferred<RunActionResponse>(); client.mutations = [mutation.promise]; client.statusViews = [view('waiting'), view('running')];
  const controller = new LiveConsoleController(client, 'run-1', {mode: 'run', onView() {}}); await controller.start();
  const first = controller.resume({type: 'approve', nodeId: 'approve'});
  const duplicate = await controller.resume({type: 'approve', nodeId: 'approve'});
  assert.equal(duplicate.accepted, false);
  assert.equal(client.calls.filter(call => call.method === 'run.action').length, 1);
  mutation.resolve({apiVersion: 'fishyume.application/v1', actionId: 'fixture', runId: 'run-1', type: 'approve', stateVersion: 8, phase: 'running'});
  assert.equal((await first).ok, true);
  await controller.close();
});

test('watch polling stops at terminal state and pure observation close does not detach', async () => {
  const terminal = new FakeClient(); terminal.statusViews = [view('completed')];
  const terminalController = new LiveConsoleController(terminal, 'run-1', {mode: 'watch', pollIntervalMs: 5, onView() {}}); await terminalController.start();
  await new Promise(resolve => setTimeout(resolve, 20));
  assert.equal(terminal.calls.filter(call => call.method === 'run.get').length, 1);
  await terminalController.close();

  const active = new FakeClient(); active.statusViews = [view('running')];
  const activeController = new LiveConsoleController(active, 'run-1', {mode: 'watch', pollIntervalMs: 20, onView() {}}); await activeController.start(); await activeController.close();
  await new Promise(resolve => setTimeout(resolve, 30));
  assert.equal(active.calls.filter(call => call.method === 'run.get').length, 1);
  assert.equal(active.calls.filter(call => call.method === 'run.detach').length, 0);
});

test('watch and run mode detach only disconnect observation after mutations settle', async () => {
  const watchClient = new FakeClient(); watchClient.statusViews = [view('waiting'), view('running')];
  const watchController = new LiveConsoleController(watchClient, 'run-1', {mode: 'watch', onView() {}}); await watchController.start();
  assert.equal((await watchController.resume({type: 'approve', nodeId: 'approve'})).ok, true);
  await watchController.close();
  assert.equal(watchClient.calls.filter(call => call.method === 'run.detach').length, 0);

  const runClient = new FakeClient(); const runController = new LiveConsoleController(runClient, 'run-1', {mode: 'run', onView() {}}); await runController.start();
  assert.ok(runClient.listener); await runController.close(); assert.equal(runClient.listener, undefined);
  assert.equal(runClient.calls.filter(call => call.method === 'run.detach').length, 0);
});

test('terminal views do not detach even when watch previously acquired controller ownership', async () => {
  const watchClient = new FakeClient(); watchClient.statusViews = [view('waiting'), view('completed')];
  const controller = new LiveConsoleController(watchClient, 'run-1', {mode: 'watch', onView() {}}); await controller.start();
  assert.equal((await controller.resume({type: 'approve', nodeId: 'approve'})).ok, true);
  await controller.close();
  assert.equal(watchClient.calls.filter(call => call.method === 'run.detach').length, 0);

  const runClient = new FakeClient(); runClient.statusViews = [view('completed')];
  const runController = new LiveConsoleController(runClient, 'run-1', {mode: 'run', onView() {}}); await runController.start(); await runController.close();
  assert.equal(runClient.calls.filter(call => call.method === 'run.detach').length, 0);
});
