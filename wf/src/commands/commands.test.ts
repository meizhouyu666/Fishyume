import assert from 'node:assert/strict';
import test from 'node:test';
import type {EngineClient, EventListener} from '../bridge/engine.js';
import type {EngineHello, RunEvent, RunSnapshot, RunStatus} from '../bridge/types.js';
import {runDoctor} from './doctor.js';
import {runWorkflow} from './run.js';

class FakeClient implements EngineClient {
  listener?: EventListener;
  closed = false;
  constructor(private readonly finalStatus: RunStatus = 'succeeded', private readonly ready = true) {}
  async hello(project?: string): Promise<EngineHello> {
    return {engineVersion: 'fixture', protocolVersion: 1, supportedMethods: [], supportedBackends: ['ccpanes'],
      backendReady: this.ready, backendDiagnostic: this.ready ? 'ready' : 'not ready',
      projectChecked: Boolean(project), projectReady: this.ready, projectDiagnostic: this.ready ? 'registered' : 'missing'};
  }
  async call<T>(method: string): Promise<T> {
    if (method === 'run.start') {
      queueMicrotask(() => this.listener?.(this.event()));
      return {protocolVersion: 1, runId: 'run-1'} as T;
    }
    if (method === 'run.get') return this.snapshot() as T;
    throw new Error(`unexpected method ${method}`);
  }
  onRunEvent(listener: EventListener): () => void { this.listener = listener; return () => { this.listener = undefined; }; }
  onDiagnostic(): () => void { return () => undefined; }
  async close(): Promise<void> { this.closed = true; }
  private snapshot(): RunSnapshot {
    return {protocolVersion: 1, id: 'run-1', status: this.finalStatus, nodeStatus: this.finalStatus,
      project: 'p', tool: 'codex', runtime: 'local', backend: 'ccpanes', summary: 'done', stateDir: 'state',
      createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:01Z'};
  }
  private event(): RunEvent {
    const snapshot = this.snapshot();
    return {protocolVersion: 1, runId: snapshot.id, sequence: 4, type: 'run.terminal', status: snapshot.status,
      nodeStatus: snapshot.nodeStatus, message: snapshot.summary, timestamp: snapshot.updatedAt};
  }
}

test('doctor returns non-zero for a failed required check', async () => {
  let output = '';
  assert.equal(await runDoctor(new FakeClient('succeeded', false), 'p', {write(text) { output += text; }}), 1);
  assert.match(output, /fail backend/);
});

test('run command helper returns status-derived exit codes', async () => {
  for (const [status, expected] of [['succeeded', 0], ['failed', 1], ['blocked', 1], ['indeterminate', 1]] as const) {
    const client = new FakeClient(status);
    let output = '';
    const code = await runWorkflow(client, {project: 'p', tool: 'codex', runtime: 'local', task: 't', useTUI: false}, {write(text) { output += text; }});
    assert.equal(code, expected, status);
    assert.equal(client.closed, true);
    assert.match(output, new RegExp(`status=${status}`));
  }
});
