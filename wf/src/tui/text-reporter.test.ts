import assert from 'node:assert/strict';
import test from 'node:test';
import type {RunEvent, RunSnapshot} from '../bridge/types.js';
import {exitCodeForStatus, TextReporter} from './text-reporter.js';

const snapshot: RunSnapshot = {
  protocolVersion: 1, id: 'run-1', status: 'succeeded', nodeStatus: 'succeeded',
  project: 'p', tool: 'codex', runtime: 'local', backend: 'ccpanes', summary: 'done',
  stateDir: 'state', createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:01Z',
};

test('plain reporter includes identifiers, status, elapsed, summary, and state directory', () => {
  let output = '';
  const reporter = new TextReporter({write(text) { output += text; }});
  const event: RunEvent = {protocolVersion: 1, runId: 'run-1', sequence: 2, type: 'run.running', status: 'running', nodeStatus: 'running', message: 'working', timestamp: snapshot.updatedAt};
  reporter.started({...snapshot, status: 'created', nodeStatus: 'created'});
  reporter.event(event);
  reporter.finished(snapshot, 2500);
  assert.match(output, /run run-1/);
  assert.match(output, /status=succeeded/);
  assert.match(output, /elapsed=2s/);
  assert.match(output, /summary=done/);
  assert.match(output, /state=state/);
});

test('terminal status exit codes are truthful', () => {
  assert.equal(exitCodeForStatus('succeeded'), 0);
  assert.equal(exitCodeForStatus('paused'), 0);
  for (const status of ['failed', 'blocked', 'indeterminate', 'cancelled'] as const) {
    assert.equal(exitCodeForStatus(status), 1);
  }
});
