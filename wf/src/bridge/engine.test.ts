import assert from 'node:assert/strict';
import {mkdtemp, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import test from 'node:test';
import {EngineBridge} from './engine.js';

async function fixture(version = 1): Promise<string> {
  const directory = await mkdtemp(join(tmpdir(), 'wf-engine-bridge-'));
  const path = join(directory, 'fixture.mjs');
  await writeFile(path, `
import readline from 'node:readline';
const lines = readline.createInterface({input: process.stdin});
lines.on('line', line => {
  const request = JSON.parse(line);
  if (request.method === 'engine.hello') {
    process.stdout.write(JSON.stringify({jsonrpc:'2.0',protocolVersion:${version},id:request.id,result:{engineVersion:'fixture',protocolVersion:${version},supportedMethods:[],supportedBackends:['ccpanes'],backendReady:true,backendDiagnostic:'ready',projectChecked:false,projectReady:false}})+'\\n');
  } else if (request.method === 'run.start') {
    process.stdout.write(JSON.stringify({jsonrpc:'2.0',protocolVersion:1,method:'run.event',params:{protocolVersion:1,runId:'run-1',sequence:1,type:'run.running',status:'running',nodeStatus:'running',timestamp:new Date().toISOString()}})+'\\n');
    process.stdout.write(JSON.stringify({jsonrpc:'2.0',protocolVersion:1,id:request.id,result:{protocolVersion:1,runId:'run-1'}})+'\\n');
  }
});
`);
  return path;
}

test('correlates responses and routes run.event notifications', async () => {
  const bridge = new EngineBridge(process.execPath, [await fixture()]);
  const events: string[] = [];
  bridge.onRunEvent(event => events.push(event.status));
  const hello = await bridge.hello();
  assert.equal(hello.engineVersion, 'fixture');
  const started = await bridge.call<{runId: string}>('run.start', {});
  assert.equal(started.runId, 'run-1');
  assert.deepEqual(events, ['running']);
  await bridge.close();
});

test('rejects an incompatible protocol version', async () => {
  const bridge = new EngineBridge(process.execPath, [await fixture(99)]);
  await assert.rejects(bridge.hello(), /incompatible protocol version/);
  await bridge.close();
});
