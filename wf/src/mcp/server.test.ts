import assert from 'node:assert/strict';
import test from 'node:test';
import {PassThrough} from 'node:stream';
import {Client} from '@modelcontextprotocol/sdk/client/index.js';
import {InMemoryTransport} from '@modelcontextprotocol/sdk/inMemory.js';
import type {SystemCapabilitiesResponse} from '../bridge/application.js';
import type {EngineClient, EventListener} from '../bridge/engine.js';
import type {EngineHello} from '../bridge/types.js';
import {runMachine} from '../commands/machine.js';
import {createMCPServer, runMCPTransport} from './server.js';

const capabilities: SystemCapabilitiesResponse = {
  apiVersion: 'fishyume.application/v1', workflowSchemaVersion: 'fishyume/v1', workflowSchema: {type: 'object'},
  nodeTypes: ['agent', 'approval'], actionTypes: ['approve', 'reject', 'answer', 'retry', 'cancel'],
  drivers: [{driver: 'codex', targets: ['local'], ready: true, maxConcurrentAgents: 1, supportsConcurrentCancel: true}],
  limits: {maxEventWaitMs: 30000}, errorCodes: ['invalid_argument', 'conflict'], minimalExample: {apiVersion: 'fishyume/v1'},
};

class FakeApplicationClient implements EngineClient {
  closed = false;
	closeCount = 0;
  async hello(): Promise<EngineHello> {throw new Error('not used')}
  async call<T>(method: string): Promise<T> {
    if (method !== 'system.capabilities') throw new Error(`unexpected ${method}`);
    return capabilities as T;
  }
  onRunEvent(_listener: EventListener): () => void {return () => undefined}
  onDiagnostic(): () => void {return () => undefined}
  async close(): Promise<void> {this.closed = true; this.closeCount++}
}

test('MCP transport close settles the command and closes the EngineClient exactly once', async () => {
  const engine = new FakeApplicationClient();
  const [hostTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  const running = runMCPTransport(engine, serverTransport);
  await hostTransport.start();
  await hostTransport.close();
  await Promise.race([running, new Promise<void>((_, reject) => setTimeout(() => reject(new Error('MCP command did not settle after host EOF')), 250))]);
  assert.equal(engine.closed, true);
  assert.equal(engine.closeCount, 1);
});

test('MCP stdio input EOF settles without an explicit transport close', async () => {
  const engine = new FakeApplicationClient();
  const input = new PassThrough();
  const [hostTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  const running = runMCPTransport(engine, serverTransport, input);
  await hostTransport.start();
  input.resume();
  input.end();
  await Promise.race([running, new Promise<void>((_, reject) => setTimeout(() => reject(new Error('MCP command did not settle after stdin EOF')), 250))]);
  assert.equal(engine.closeCount, 1);
  assert.equal(input.listenerCount('end'), 0);
  assert.equal(input.listenerCount('close'), 0);
  assert.equal(input.listenerCount('error'), 0);
});

test('MCP and Machine CLI expose identical Application response JSON', async () => {
  const machineClient = new FakeApplicationClient(); let machineOutput = '';
  assert.equal(await runMachine(machineClient, 'system.capabilities', '{}', {write(text) {machineOutput += text}}), 0);

  const mcpServer = createMCPServer(new FakeApplicationClient());
  const mcpClient = new Client({name: 'fake-host-agent', version: '1.0.0'});
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await Promise.all([mcpServer.connect(serverTransport), mcpClient.connect(clientTransport)]);
  try {
    const listed = await mcpClient.listTools();
    assert.deepEqual(listed.tools.map(tool => tool.name), ['system.capabilities', 'workflow.validate', 'workflow.explain', 'run.start', 'run.list', 'run.get', 'run.events', 'run.action', 'run.result']);
    for (const tool of listed.tools) {
      const schema = JSON.stringify(tool.inputSchema);
      for (const legacy of ['"backend"', '"tool"', '"runtime"']) assert.equal(schema.includes(legacy), false, `${tool.name} exposed ${legacy}`);
    }
    const result = await mcpClient.callTool({name: 'system.capabilities', arguments: {}});
    assert.deepEqual(result.structuredContent, JSON.parse(machineOutput));
    const content = result.content as Array<{type: string; text?: string}>;
    assert.equal(content[0]?.type, 'text');
    assert.deepEqual(JSON.parse(content[0]?.text ?? ''), JSON.parse(machineOutput));
  } finally {
    await mcpClient.close();
    await mcpServer.close();
  }
});
