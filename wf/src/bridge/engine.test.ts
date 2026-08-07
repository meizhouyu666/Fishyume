import assert from 'node:assert/strict';
import {mkdtemp, readFile, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import test from 'node:test';
import {EngineBridge, resolveEnginePath} from './engine.js';

async function fixture(version = 2): Promise<string> {
  const directory = await mkdtemp(join(tmpdir(), 'wf-engine-bridge-')); const path = join(directory, 'fixture.mjs');
  await writeFile(path, `
import readline from 'node:readline';
const lines = readline.createInterface({input: process.stdin});
lines.on('line', line => {
  const request = JSON.parse(line);
  if (request.method === 'engine.hello') {
    process.stdout.write(JSON.stringify({jsonrpc:'2.0',protocolVersion:${version},id:request.id,result:{engineVersion:'fixture',protocolVersion:${version},supportedMethods:[],supportedDrivers:['codex'],backendReady:true,backendDiagnostic:request.params?.driver ?? 'ready',projectChecked:false,projectReady:false}})+'\\n');
  } else if (request.method === 'run.start') {
    process.stdout.write(JSON.stringify({jsonrpc:'2.0',protocolVersion:2,method:'run.event',params:{protocolVersion:2,runId:'run-1',sequence:1,type:'node.running',phase:'running',nodeId:'agent-1',nodePhase:'running',timestamp:new Date().toISOString()}})+'\\n');
    process.stdout.write(JSON.stringify({jsonrpc:'2.0',protocolVersion:2,id:request.id,result:{protocolVersion:2,runId:'run-1'}})+'\\n');
  }
});
`);
  return path;
}

function assertExited(bridge: EngineBridge): void {
	const child = bridge.child;
	assert.ok(child);
	assert.ok(child.exitCode !== null || child.signalCode !== null, `child ${child.pid ?? 'unknown'} is still running`);
}

test('correlates responses and routes v2 run.event notifications', async () => {
  const bridge = new EngineBridge(process.execPath, [await fixture()]);
  try {
    const events: string[] = [];
    bridge.onRunEvent(event => events.push(event.phase));
    const hello = await bridge.hello();
    assert.equal(hello.engineVersion, 'fixture');
    const started = await bridge.call<{runId: string}>('run.start', {});
    assert.equal(started.runId, 'run-1');
    assert.deepEqual(events, ['running']);
  } finally {
    await bridge.close();
  }
  assertExited(bridge);
});

test('hello forwards the selected Driver', async () => {
  const bridge = new EngineBridge(process.execPath, [await fixture()]);
  try {
    const hello = await bridge.hello('project', 'codex');
    assert.equal(hello.backendDiagnostic, 'codex');
  } finally {
    await bridge.close();
  }
  assertExited(bridge);
});

test('rejects an incompatible protocol version and closes on the error path', async () => {
  const bridge = new EngineBridge(process.execPath, [await fixture(99)]);
  try {
    await assert.rejects(bridge.hello(), /incompatible protocol version/);
  } finally {
    await bridge.close();
  }
  assertExited(bridge);
});

test('close waits for a forced child exit', {timeout: 10_000}, async () => {
  const path = await fixture();
  const source = await readFile(path, 'utf8');
  await writeFile(path, `${source}\nsetInterval(() => {}, 1000);\n`);
  const bridge = new EngineBridge(process.execPath, [path]);
  try {
    await bridge.hello();
  } finally {
    await bridge.close();
  }
  assertExited(bridge);
});

test('Fishyume engine path takes precedence over the legacy WF alias', () => {
  assert.equal(resolveEnginePath({FISHYUME_ENGINE_PATH: 'fishyume-engine', WF_ENGINE_PATH: 'wf-engine'}), 'fishyume-engine');
  assert.match(resolveEnginePath({WF_ENGINE_PATH: 'legacy-wf-engine'}), /wf-engine(?:\.exe)?$/);
});

test('missing Engine diagnostics are actionable', async () => {
  const bridge = new EngineBridge(join(tmpdir(), 'missing-fishyume-engine'));
  try {
    await assert.rejects(bridge.hello(), /set FISHYUME_ENGINE_PATH/);
  } finally {
    await bridge.close();
  }
});
