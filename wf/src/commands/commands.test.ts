import assert from 'node:assert/strict';
import {mkdtemp, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import test from 'node:test';
import type {EngineClient, EventListener} from '../bridge/engine.js';
import type {Conclusion, EngineHello, RunEvent, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {runDoctor} from './doctor.js';
import {parseInputValues, resolveRunSelection, runWorkflow, shouldUseTUI} from './run.js';
import {showStatus, statusWatchError} from './status.js';

class FakeClient implements EngineClient {
  listener?: EventListener; closed = false; helloDriver?: string; calls: Array<{method: string; params?: unknown}> = [];
  constructor(private readonly conclusion: Conclusion = 'succeeded', private readonly ready = true, private readonly phase: WorkflowSnapshot['phase'] = 'completed') {}
  async hello(project?: string, driver?: string): Promise<EngineHello> {this.helloDriver = driver; return {engineVersion: 'fixture', protocolVersion: 2, supportedMethods: [], supportedDrivers: ['codex'], backendReady: this.ready, backendDiagnostic: this.ready ? 'ready' : 'not ready', projectChecked: Boolean(project), projectReady: this.ready, projectDiagnostic: this.ready ? 'registered' : 'missing'}}
  async call<T>(method: string, params?: unknown): Promise<T> {
    this.calls.push({method, params});
    if (method === 'run.start' || method === 'run.startWorkflow') {queueMicrotask(() => this.listener?.(this.event())); return {protocolVersion: 2, runId: 'run-1'} as T}
    if (method === 'run.status') return this.view() as T;
    throw new Error(`unexpected method ${method}`);
  }
  onRunEvent(listener: EventListener): () => void {this.listener = listener; return () => {this.listener = undefined}}
  onDiagnostic(): () => void {return () => undefined}
  async close(): Promise<void> {this.closed = true}
  private snapshot(): WorkflowSnapshot {return {protocolVersion: 2, id: 'run-1', workflowName: 'ad-hoc', phase: this.phase, ...(this.phase === 'completed' ? {conclusion: this.conclusion} : {}), project: 'p', resolvedDriver: 'codex', resolvedTarget: 'local', summary: 'done', topologicalOrder: ['agent-1'], nodes: {'agent-1': {id: 'agent-1', type: 'agent', phase: this.phase === 'completed' ? 'completed' : 'waiting', ...(this.phase === 'completed' ? {conclusion: this.conclusion} : {})}}, cancelRequested: false, stateDir: 'state', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:01Z'} as WorkflowSnapshot}
  private view(): RunStatusView {return {protocolVersion: 2, legacy: false, run: this.snapshot(), nodes: []}}
  private event(): RunEvent {const snapshot = this.snapshot(); return {protocolVersion: 2, runId: snapshot.id, sequence: 4, type: 'run.completed', phase: snapshot.phase, conclusion: snapshot.conclusion, message: snapshot.summary, timestamp: snapshot.updatedAt}}
}

test('doctor returns non-zero for a failed required check', async () => {let output = ''; assert.equal(await runDoctor(new FakeClient('succeeded', false), 'p', 'codex', {write(text) {output += text}}), 1); assert.match(output, /fail driver/)})

test('run helper returns lifecycle-derived exit codes', async () => {
  for (const [conclusion, expected] of [['succeeded', 0], ['failed', 1], ['rejected', 2], ['cancelled', 3], ['indeterminate', 5]] as const) {
    const client = new FakeClient(conclusion); let output = '';
    const code = await runWorkflow(client, {project: 'p', driver: 'codex', target: 'local', task: 't', useTUI: false}, {write(text) {output += text}});
    assert.equal(code, expected, conclusion); assert.equal(client.closed, true); assert.match(output, new RegExp(`conclusion=${conclusion}`));
  }
  assert.equal(await runWorkflow(new FakeClient('succeeded', true, 'waiting'), {project: 'p', driver: 'codex', target: 'local', task: 't', useTUI: false}, {write() {}}), 4);
});

test('run helper forwards an explicit Driver to Doctor and run creation', async () => {
  const client = new FakeClient();
  assert.equal(await runWorkflow(client, {project: 'p', driver: 'codex', target: 'local', task: 't', useTUI: false}, {write() {}}), 0);
  assert.equal(client.helloDriver, 'codex');
  assert.deepEqual(client.calls.find(call => call.method === 'run.start'), {method: 'run.start', params: {project: 'p', driver: 'codex', target: 'local', backend: undefined, tool: undefined, runtime: undefined, task: 't'}});
});

test('workflow Driver selection is left to the Engine when CLI has no override', async () => {
  const directory = await mkdtemp(join(tmpdir(), 'fishyume-workflow-'));
  const path = join(directory, 'workflow.yaml');
  const content = 'apiVersion: fishyume/v1\nname: fixture\ndefaults: {agent: {driver: codex, target: local}}\nexecution: {maxConcurrency: 1}\nnodes: {approve: {type: approval, prompt: approve}}\n';
  await writeFile(path, content);
  const client = new FakeClient('succeeded', false);
  try {
    assert.equal(await runWorkflow(client, {project: 'p', workflow: path, useTUI: false}, {write() {}}), 0);
    assert.equal(client.helloDriver, undefined);
    assert.deepEqual(client.calls.find(call => call.method === 'run.startWorkflow'), {method: 'run.startWorkflow', params: {project: 'p', driver: undefined, target: undefined, backend: undefined, filename: 'workflow.yaml', content, inputs: undefined}});
  } finally {
    await rm(directory, {recursive: true, force: true});
  }
});

test('command defaults apply only to ad-hoc runs and do not override workflow selection', () => {
  assert.deepEqual(resolveRunSelection(undefined, undefined, undefined, false), {driver: 'codex', target: 'local'});
  assert.deepEqual(resolveRunSelection('workflow.yaml', undefined, undefined, false), {driver: undefined, target: undefined});
  assert.deepEqual(resolveRunSelection('workflow.yaml', 'codex', 'local', false), {driver: 'codex', target: 'local'});
  assert.deepEqual(resolveRunSelection(undefined, undefined, undefined, true), {driver: undefined, target: undefined});
});

test('status json emits one machine-readable object', async () => {let output = ''; assert.equal(await showStatus(new FakeClient(), 'run-1', true, {write(text) {output += text}}), 0); assert.equal(output.trim().split('\n').length, 1); assert.equal(JSON.parse(output).run.id, 'run-1')})

test('status watch protects JSON, non-TTY, and CI contracts', () => {
  assert.match(statusWatchError(true, true, {} as NodeJS.ProcessEnv) ?? '', /cannot be combined with --json/);
  assert.match(statusWatchError(false, false, {} as NodeJS.ProcessEnv) ?? '', /requires an interactive TTY/);
  assert.match(statusWatchError(false, true, {CI: '1'} as NodeJS.ProcessEnv) ?? '', /requires an interactive TTY/);
  assert.equal(statusWatchError(false, true, {NO_COLOR: '1'} as NodeJS.ProcessEnv), undefined);
});

test('input values accept scalars and reject structured values', () => {assert.deepEqual(parseInputValues(['goal=ship', 'count=2', 'dry=true'], {base: 'x'}), {base: 'x', goal: 'ship', count: 2, dry: true}); assert.throws(() => parseInputValues(['bad=[1,2]']), /JSON scalar/)})

test('TTY selection preserves text mode for automation and supports monochrome TUI', () => {assert.equal(shouldUseTUI(false, {}), false); assert.equal(shouldUseTUI(true, {CI: '1'}), false); assert.equal(shouldUseTUI(true, {NO_COLOR: '1'}), true)})
