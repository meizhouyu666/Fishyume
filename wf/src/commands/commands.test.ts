import assert from 'node:assert/strict';
import test from 'node:test';
import type {EngineClient, EventListener} from '../bridge/engine.js';
import type {Conclusion, EngineHello, RunEvent, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {runDoctor} from './doctor.js';
import {parseInputValues, runWorkflow} from './run.js';
import {showStatus} from './status.js';

class FakeClient implements EngineClient {
  listener?: EventListener; closed = false;
  constructor(private readonly conclusion: Conclusion = 'succeeded', private readonly ready = true, private readonly phase: WorkflowSnapshot['phase'] = 'completed') {}
  async hello(project?: string): Promise<EngineHello> {return {engineVersion: 'fixture', protocolVersion: 2, supportedMethods: [], supportedBackends: ['ccpanes'], backendReady: this.ready, backendDiagnostic: this.ready ? 'ready' : 'not ready', projectChecked: Boolean(project), projectReady: this.ready, projectDiagnostic: this.ready ? 'registered' : 'missing'}}
  async call<T>(method: string): Promise<T> {
    if (method === 'run.start') {queueMicrotask(() => this.listener?.(this.event())); return {protocolVersion: 2, runId: 'run-1'} as T}
    if (method === 'run.status') return this.view() as T;
    throw new Error(`unexpected method ${method}`);
  }
  onRunEvent(listener: EventListener): () => void {this.listener = listener; return () => {this.listener = undefined}}
  onDiagnostic(): () => void {return () => undefined}
  async close(): Promise<void> {this.closed = true}
  private snapshot(): WorkflowSnapshot {return {protocolVersion: 2, id: 'run-1', workflowName: 'ad-hoc', phase: this.phase, ...(this.phase === 'completed' ? {conclusion: this.conclusion} : {}), project: 'p', backend: 'ccpanes', summary: 'done', topologicalOrder: ['agent-1'], nodes: {'agent-1': {id: 'agent-1', type: 'agent', phase: this.phase === 'completed' ? 'completed' : 'waiting', ...(this.phase === 'completed' ? {conclusion: this.conclusion} : {})}}, cancelRequested: false, stateDir: 'state', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:01Z'} as WorkflowSnapshot}
  private view(): RunStatusView {return {protocolVersion: 2, legacy: false, run: this.snapshot(), nodes: []}}
  private event(): RunEvent {const snapshot = this.snapshot(); return {protocolVersion: 2, runId: snapshot.id, sequence: 4, type: 'run.completed', phase: snapshot.phase, conclusion: snapshot.conclusion, message: snapshot.summary, timestamp: snapshot.updatedAt}}
}

test('doctor returns non-zero for a failed required check', async () => {let output = ''; assert.equal(await runDoctor(new FakeClient('succeeded', false), 'p', {write(text) {output += text}}), 1); assert.match(output, /fail backend/)})

test('run helper returns lifecycle-derived exit codes', async () => {
  for (const [conclusion, expected] of [['succeeded', 0], ['failed', 1], ['rejected', 2], ['cancelled', 3], ['indeterminate', 5]] as const) {
    const client = new FakeClient(conclusion); let output = '';
    const code = await runWorkflow(client, {project: 'p', tool: 'codex', runtime: 'local', task: 't', useTUI: false}, {write(text) {output += text}});
    assert.equal(code, expected, conclusion); assert.equal(client.closed, true); assert.match(output, new RegExp(`conclusion=${conclusion}`));
  }
  assert.equal(await runWorkflow(new FakeClient('succeeded', true, 'waiting'), {project: 'p', tool: 'codex', runtime: 'local', task: 't', useTUI: false}, {write() {}}), 4);
});

test('status json emits one machine-readable object', async () => {let output = ''; assert.equal(await showStatus(new FakeClient(), 'run-1', true, {write(text) {output += text}}), 0); assert.equal(output.trim().split('\n').length, 1); assert.equal(JSON.parse(output).run.id, 'run-1')})

test('input values accept scalars and reject structured values', () => {assert.deepEqual(parseInputValues(['goal=ship', 'count=2', 'dry=true'], {base: 'x'}), {base: 'x', goal: 'ship', count: 2, dry: true}); assert.throws(() => parseInputValues(['bad=[1,2]']), /JSON scalar/)})
