import assert from 'node:assert/strict';
import {mkdtemp, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import test from 'node:test';
import type {RunGetResponse} from '../bridge/application.js';
import type {EngineClient, EventListener} from '../bridge/engine.js';
import type {Conclusion, EngineHello, RunEvent, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {cancelRun} from './cancel.js';
import {runDoctor} from './doctor.js';
import {resumeRun} from './resume.js';
import {parseInputValues, resolveRunSelection, runWorkflow, shouldUseTUI} from './run.js';
import {showStatus, statusWatchError} from './status.js';

class FakeClient implements EngineClient {
  listener?: EventListener; closed = false; helloDriver?: string; calls: Array<{method: string; params?: unknown}> = [];
  constructor(private readonly conclusion: Conclusion = 'succeeded', private readonly ready = true, private readonly phase: WorkflowSnapshot['phase'] = 'completed') {}
  async hello(project?: string, driver?: string): Promise<EngineHello> {this.helloDriver = driver; return {engineVersion: 'fixture', protocolVersion: 2, supportedMethods: [], supportedDrivers: ['codex'], backendReady: this.ready, backendDiagnostic: this.ready ? 'ready' : 'not ready', projectChecked: Boolean(project), projectReady: this.ready, projectDiagnostic: this.ready ? 'registered' : 'missing'}}
  async call<T>(method: string, params?: unknown): Promise<T> {
    this.calls.push({method, params});
    if (method === 'run.start') {queueMicrotask(() => this.listener?.(this.event())); return {apiVersion: 'fishyume.application/v1', runId: 'run-1', stateVersion: 1, attach: 'fishyume attach run-1'} as T}
    if (method === 'run.get') return this.applicationView() as T;
    if (method === 'run.action') {queueMicrotask(() => this.listener?.(this.event())); return {apiVersion: 'fishyume.application/v1', actionId: 'fixture', runId: 'run-1', type: 'cancel', stateVersion: 2, phase: this.phase, ...(this.phase === 'completed' ? {conclusion: this.conclusion} : {})} as T}
    throw new Error(`unexpected method ${method}`);
  }
  onRunEvent(listener: EventListener): () => void {this.listener = listener; return () => {this.listener = undefined}}
  onDiagnostic(): () => void {return () => undefined}
  async close(): Promise<void> {this.closed = true}
  private snapshot(): WorkflowSnapshot {return {protocolVersion: 2, id: 'run-1', workflowName: 'ad-hoc', phase: this.phase, ...(this.phase === 'completed' ? {conclusion: this.conclusion} : {}), project: 'p', resolvedDriver: 'codex', resolvedTarget: 'local', summary: 'done', topologicalOrder: ['agent-1'], nodes: {'agent-1': {id: 'agent-1', type: 'agent', phase: this.phase === 'completed' ? 'completed' : 'waiting', ...(this.phase === 'completed' ? {conclusion: this.conclusion} : {})}}, cancelRequested: false, stateDir: 'state', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:01Z'} as WorkflowSnapshot}
  private view(): RunStatusView {return {protocolVersion: 2, legacy: false, run: this.snapshot(), nodes: []}}
  private applicationView(): RunGetResponse {
    const snapshot = this.snapshot();
    return {apiVersion: 'fishyume.application/v1', run: {runId: snapshot.id, workflowName: snapshot.workflowName, project: snapshot.project, driver: snapshot.resolvedDriver ?? 'codex', target: snapshot.resolvedTarget ?? 'local', phase: snapshot.phase, conclusion: snapshot.conclusion, stateVersion: snapshot.stateVersion ?? 1, createdAt: snapshot.createdAt, updatedAt: snapshot.updatedAt, summary: snapshot.summary, cancelRequested: snapshot.cancelRequested, effectiveConcurrency: snapshot.effectiveConcurrency ?? 1, topologicalOrder: snapshot.topologicalOrder, nodes: Object.values(snapshot.nodes).map(node => ({nodeId: node.id, type: node.type, phase: node.phase, conclusion: node.conclusion, reason: node.reason, currentAttempt: node.currentAttempt})), deprecationWarnings: []}};
  }
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
  const start = client.calls.find(call => call.method === 'run.start');
  assert.equal((start?.params as {driver?: string}).driver, 'codex');
  assert.equal((start?.params as {target?: string}).target, 'local');
  assert.match((start?.params as {clientRequestId: string}).clientRequestId, /^cli-start-/);
  assert.deepEqual((start?.params as {workflow: {document: unknown}}).workflow.document, {apiVersion: 'fishyume/v2', name: 'ad-hoc', defaults: {agent: {driver: 'codex', target: 'local'}}, execution: {maxConcurrency: 1}, nodes: {'agent-1': {type: 'agent', task: 't'}}});
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
    const start = client.calls.find(call => call.method === 'run.start');
    assert.deepEqual((start?.params as {workflow: unknown}).workflow, {source: {filename: 'workflow.yaml', content}});
    assert.equal('driver' in (start?.params as object), false);
    assert.equal('target' in (start?.params as object), false);
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

test('status json emits one run.get Application response', async () => {let output = ''; assert.equal(await showStatus(new FakeClient(), 'run-1', true, {write(text) {output += text}}), 0); assert.equal(output.trim().split('\n').length, 1); assert.equal(JSON.parse(output).run.runId, 'run-1'); assert.equal(JSON.parse(output).apiVersion, 'fishyume.application/v1')})

test('resume and cancel helpers use run.get and run.action only', async () => {
  const resumeClient = new FakeClient();
  assert.equal(await resumeRun(resumeClient, 'run-1', {type: 'approve', nodeId: 'agent-1'}, {write() {}}), 0);
  assert.deepEqual(resumeClient.calls.map(call => call.method), ['run.get', 'run.action', 'run.get']);
  const cancelClient = new FakeClient('cancelled');
  assert.equal(await cancelRun(cancelClient, 'run-1', {write() {}}), 3);
  assert.deepEqual(cancelClient.calls.map(call => call.method), ['run.get', 'run.action', 'run.get']);
});

test('status watch protects JSON, non-TTY, and CI contracts', () => {
  assert.match(statusWatchError(true, true, {} as NodeJS.ProcessEnv) ?? '', /cannot be combined with --json/);
  assert.match(statusWatchError(false, false, {} as NodeJS.ProcessEnv) ?? '', /requires an interactive TTY/);
  assert.match(statusWatchError(false, true, {CI: '1'} as NodeJS.ProcessEnv) ?? '', /requires an interactive TTY/);
  assert.equal(statusWatchError(false, true, {NO_COLOR: '1'} as NodeJS.ProcessEnv), undefined);
});

test('input values accept scalars and reject structured values', () => {assert.deepEqual(parseInputValues(['goal=ship', 'count=2', 'dry=true'], {base: 'x'}), {base: 'x', goal: 'ship', count: 2, dry: true}); assert.throws(() => parseInputValues(['bad=[1,2]']), /JSON scalar/)})

test('TTY selection preserves text mode for automation and supports monochrome TUI', () => {assert.equal(shouldUseTUI(false, {}), false); assert.equal(shouldUseTUI(true, {CI: '1'}), false); assert.equal(shouldUseTUI(true, {NO_COLOR: '1'}), true)})
