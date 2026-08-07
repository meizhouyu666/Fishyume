import assert from 'node:assert/strict';
import test from 'node:test';
import type {EngineClient, EventListener} from '../bridge/engine.js';
import type {EngineHello, ResumeAction, RunEvent, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {LiveConsoleController} from './live-console.js';

const createdAt = '2026-08-06T00:00:00Z';
function view(phase: WorkflowSnapshot['phase'], updatedAt = createdAt): RunStatusView {
  return {protocolVersion: 2, legacy: false, run: {protocolVersion: 2, stateVersion: 7, id: 'run-1', workflowName: 'fixture', project: 'p', backend: 'direct', phase, ...(phase === 'completed' ? {conclusion: 'succeeded' as const} : {}), topologicalOrder: ['node'], nodes: {node: {id: 'node', type: 'agent', phase: phase === 'completed' ? 'completed' : 'running', ...(phase === 'completed' ? {conclusion: 'succeeded' as const} : {})}}, cancelRequested: false, stateDir: 'state', createdAt, updatedAt}};
}

class FakeClient implements EngineClient {
  calls: Array<{method: string; params?: unknown}> = [];
  listener?: EventListener;
  closed = false;
  statusViews: Array<RunStatusView | Promise<RunStatusView>> = [view('running')];
  mutations: Array<Promise<WorkflowSnapshot> | WorkflowSnapshot> = [];
  async hello(): Promise<EngineHello> {throw new Error('not used')}
  async call<T>(method: string, params?: unknown): Promise<T> {
    this.calls.push({method, params});
    if (method === 'run.status') {
      const next = this.statusViews.shift() ?? view('running');
      return await next as T;
    }
    if (method === 'run.resume' || method === 'run.cancel') {
      const next = this.mutations.shift() ?? view('running').run!;
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

test('resume and cancel use exact existing RPC parameters and refresh afterward', async () => {
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
  assert.deepEqual(client.calls.filter(call => call.method === 'run.resume').map(call => call.params), actions.map(action => ({runId: 'run-1', expectedStateVersion: 7, action})));
  assert.deepEqual(client.calls.find(call => call.method === 'run.cancel')?.params, {runId: 'run-1', expectedStateVersion: 7});
  assert.equal(client.calls.filter(call => call.method === 'run.status').length, 6);
  await controller.close();
});

test('pending mutation lock suppresses duplicate submissions', async () => {
  const client = new FakeClient(); const mutation = deferred<WorkflowSnapshot>(); client.mutations = [mutation.promise]; client.statusViews = [view('waiting'), view('running')];
  const controller = new LiveConsoleController(client, 'run-1', {mode: 'run', onView() {}}); await controller.start();
  const first = controller.resume({type: 'approve', nodeId: 'approve'});
  const duplicate = await controller.resume({type: 'approve', nodeId: 'approve'});
  assert.equal(duplicate.accepted, false);
  assert.equal(client.calls.filter(call => call.method === 'run.resume').length, 1);
  mutation.resolve(view('running').run!);
  assert.equal((await first).ok, true);
  await controller.close();
});

test('watch polling stops at terminal state and pure observation close does not detach', async () => {
  const terminal = new FakeClient(); terminal.statusViews = [view('completed')];
  const terminalController = new LiveConsoleController(terminal, 'run-1', {mode: 'watch', pollIntervalMs: 5, onView() {}}); await terminalController.start();
  await new Promise(resolve => setTimeout(resolve, 20));
  assert.equal(terminal.calls.filter(call => call.method === 'run.status').length, 1);
  await terminalController.close();

  const active = new FakeClient(); active.statusViews = [view('running')];
  const activeController = new LiveConsoleController(active, 'run-1', {mode: 'watch', pollIntervalMs: 20, onView() {}}); await activeController.start(); await activeController.close();
  await new Promise(resolve => setTimeout(resolve, 30));
  assert.equal(active.calls.filter(call => call.method === 'run.status').length, 1);
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
