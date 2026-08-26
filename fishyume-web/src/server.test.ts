import assert from 'node:assert/strict';
import {mkdirSync, mkdtempSync, rmSync, writeFileSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import test from 'node:test';
import type {EngineClient, EventListener} from '../../wf/src/bridge/engine.js';
import type {EngineHello} from '../../wf/src/bridge/types.js';
import {resolveWebEnginePath, startSidecar} from './server.js';

class FakeEngine implements EngineClient {
  calls: Array<{method: string; params?: unknown}> = [];
  closed = false;
  responder: (method: string, params?: unknown) => unknown | Promise<unknown> = () => ({schemaVersion: 'fishyume.team/v1', items: []});
  async hello(): Promise<EngineHello> {return {protocolVersion: 2, engineVersion: 'fixture'} as EngineHello}
  async call<T>(method: string, params?: unknown): Promise<T> {this.calls.push({method, params}); return await this.responder(method, params) as T}
  onRunEvent(_listener: EventListener): () => void {return () => undefined}
  onDiagnostic(): () => void {return () => undefined}
  async close(): Promise<void> {this.closed = true}
}

test('sidecar binds ephemeral canonical loopback and protects its RPC gateway', async () => {
  const directory = mkdtempSync(join(tmpdir(), 'fishyume-web-public-'));
  writeFileSync(join(directory, 'index.html'), '<!doctype html><title>fixture</title>');
  const engine = new FakeEngine();
  const sidecar = await startSidecar({engine, openBrowser: false, publicDir: directory});
  try {
    const parsed = new URL(sidecar.launchURL);
    assert.equal(parsed.hostname, '127.0.0.1');
    assert.notEqual(parsed.port, '0');
    assert.equal(parsed.hash, `#token=${sidecar.token}`);
    assert.equal(parsed.search, '');

    const page = await fetch(sidecar.origin, {headers: {Host: parsed.host}});
    assert.equal(page.status, 200);
    assert.equal(page.headers.get('x-frame-options'), 'DENY');

    const unauthorized = await fetch(`${sidecar.origin}/api/rpc`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({method: 'team.list', params: {}})});
    assert.equal(unauthorized.status, 403);

    const authorized = await fetch(`${sidecar.origin}/api/rpc`, {method: 'POST', headers: {'Content-Type': 'application/json', Authorization: `Bearer ${sidecar.token}`, Origin: sidecar.origin}, body: JSON.stringify({method: 'team.list', params: {schemaVersion: 'fishyume.team/v1'}})});
    assert.equal(authorized.status, 200);
    assert.deepEqual(engine.calls, [{method: 'team.list', params: {schemaVersion: 'fishyume.team/v1'}}]);

    const preflight = await fetch(`${sidecar.origin}/api/rpc`, {method: 'OPTIONS', headers: {Origin: 'http://evil.invalid'}});
    assert.equal(preflight.status, 404);
    assert.equal(preflight.headers.get('access-control-allow-origin'), null);
  } finally {
    await sidecar.close();
    rmSync(directory, {recursive: true, force: true});
  }
  assert.equal(engine.closed, true);
});

test('bundled sidecar resolves a sibling platform Engine package', () => {
  const directory = mkdtempSync(join(tmpdir(), 'fishyume-web-engine-path-'));
  try {
    const moduleDirectory = join(directory, 'node_modules', 'fishyume-web', 'dist');
    const packageName = process.platform === 'win32' ? 'fishyume-engine-win32-x64' : 'fishyume-engine-linux-x64';
    const binary = process.platform === 'win32' ? 'fishyume-engine.exe' : 'fishyume-engine';
    const enginePath = join(directory, 'node_modules', packageName, 'bin', binary);
    mkdirSync(moduleDirectory, {recursive: true});
    mkdirSync(join(enginePath, '..'), {recursive: true});
    writeFileSync(enginePath, 'fixture');
    assert.equal(resolveWebEnginePath({}, moduleDirectory), enginePath);
    assert.equal(resolveWebEnginePath({FISHYUME_ENGINE_PATH: 'explicit-engine'}, moduleDirectory), 'explicit-engine');
  } finally {rmSync(directory, {recursive: true, force: true})}
});

test('sidecar focus endpoint updates the browser target with the same bearer contract', async () => {
  const directory = mkdtempSync(join(tmpdir(), 'fishyume-web-focus-'));
  writeFileSync(join(directory, 'index.html'), '<!doctype html><title>fixture</title>');
  const sidecar = await startSidecar({engine: new FakeEngine(), openBrowser: false, publicDir: directory});
  const headers = {Authorization: 'Bearer ' + sidecar.token, Origin: sidecar.origin};
  try {
    const rejected = await fetch(sidecar.origin + '/api/focus', {method: 'POST', headers: {...headers, Origin: 'http://evil.invalid'}, body: JSON.stringify({target: {kind: 'run', runId: 'run-1'}})});
    assert.equal(rejected.status, 403);
    const response = await fetch(sidecar.origin + '/api/focus', {method: 'POST', headers, body: JSON.stringify({target: {kind: 'run', runId: 'run-1'}})});
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), {revision: 1, target: {kind: 'run', runId: 'run-1'}});
    const read = await fetch(sidecar.origin + '/api/focus', {headers});
    assert.deepEqual(await read.json(), {revision: 1, target: {kind: 'run', runId: 'run-1'}});
  } finally {
    await sidecar.close();
    rmSync(directory, {recursive: true, force: true});
  }
});

test('gateway enforces concurrency, body, timeout, and response limits', async () => {
  const directory = mkdtempSync(join(tmpdir(), 'fishyume-web-limits-'));
  writeFileSync(join(directory, 'index.html'), '<!doctype html><title>fixture</title>');
  const engine = new FakeEngine();
  const sidecar = await startSidecar({
    engine, openBrowser: false, publicDir: directory,
    gatewayLimits: {maxConcurrentRequests: 1, maxRequestBytes: 256, maxResponseBytes: 256, requestTimeoutMs: 30},
  });
  const headers = {'Content-Type': 'application/json', Authorization: `Bearer ${sidecar.token}`, Origin: sidecar.origin};
  const request = (body: string) => fetch(`${sidecar.origin}/api/rpc`, {method: 'POST', headers, body});
  try {
    let release!: (value: unknown) => void;
    let entered!: () => void;
    const started = new Promise<void>(resolve => {entered = resolve});
    engine.responder = () => new Promise(resolve => {release = resolve; entered()});
    const first = request(JSON.stringify({method: 'team.list', params: {}}));
    await started;
    const busy = await request(JSON.stringify({method: 'team.list', params: {}}));
    assert.equal(busy.status, 429);
    release({items: []});
    assert.equal((await first).status, 200);

    const oversized = await request(JSON.stringify({method: 'team.list', params: {value: 'x'.repeat(300)}}));
    assert.equal(oversized.status, 400);

    engine.responder = () => ({value: 'x'.repeat(300)});
    const tooLarge = await request(JSON.stringify({method: 'team.list', params: {}}));
    assert.equal(tooLarge.status, 502);

    engine.responder = () => new Promise(() => undefined);
    const timedOut = await request(JSON.stringify({method: 'team.list', params: {}}));
    assert.equal(timedOut.status, 400);
    assert.match(await timedOut.text(), /timed out/);
  } finally {
    await sidecar.close();
    rmSync(directory, {recursive: true, force: true});
  }
});
