import assert from 'node:assert/strict';
import test from 'node:test';
import {PassThrough} from 'node:stream';
import {Client} from '@modelcontextprotocol/sdk/client/index.js';
import {InMemoryTransport} from '@modelcontextprotocol/sdk/inMemory.js';
import {StdioServerTransport} from '@modelcontextprotocol/sdk/server/stdio.js';
import type {RoutingCatalogResponse, SystemCapabilitiesResponse} from '../bridge/application.js';
import type {EngineClient, EventListener} from '../bridge/engine.js';
import type {EngineHello} from '../bridge/types.js';
import {runMachine} from '../commands/machine.js';
import {createMCPServer, runMCPTransport} from './server.js';

const capabilities: SystemCapabilitiesResponse = {
  apiVersion: 'fishyume.application/v1', workflowSchemaVersion: 'fishyume/v1', workflowSchema: {type: 'object'},
  nodeTypes: ['agent', 'approval'], actionTypes: ['approve', 'reject', 'answer', 'retry', 'cancel'],
  drivers: [{driver: 'codex', targets: ['local'], ready: true, maxConcurrentAgents: 1, supportsConcurrentCancel: true}],
  limits: {maxEventWaitMs: 30000}, errorCodes: ['invalid_argument', 'conflict'], minimalExample: {apiVersion: 'fishyume/v1'},
  authoringGuide: {schemaVersion: 'fishyume.authoring-guide/v1', recommendedFlow: ['system.capabilities', 'routing.catalog', 'workflow.validate', 'workflow.explain', 'run.start', 'run.events', 'run.get', 'run.action', 'run.result'], workflowApiVersion: 'fishyume/v2', rules: ['Use exact intent.']},
  routingCatalog: {schemaVersion: 'fishyume.capability-catalog/v1', policyVersion: 'fishyume.routing-policy/v1', source: 'fishyume.builtin', catalogHash: 'a'.repeat(64), modelCount: 1, inspectMethod: 'routing.catalog'},
};

const routingCatalog: RoutingCatalogResponse = {
  apiVersion: 'fishyume.application/v1', source: 'fishyume.builtin', catalogHash: 'a'.repeat(64), dynamicAvailability: false,
  catalog: {schemaVersion: 'fishyume.capability-catalog/v1', policyVersion: 'fishyume.routing-policy/v1', models: [{id: 'codex/local/model', target: {driver: 'codex', provider: 'local', model: 'model'}, capabilities: ['repo_read'], contextLimitBytes: 131072, maxOutputBytes: 32768, quality: 'balanced', cost: 'low', latency: 'fast', supportsCancellation: true}]},
  limits: {maxCatalogModels: 256, maxCandidates: 32, maxFallbacks: 8, maxRoutingBudgetBytes: 16777216, maxCostUnits: 1000000}, errorCodes: ['routing_invalid_contract'],
};

class FakeApplicationClient implements EngineClient {
  closed = false;
	closeCount = 0;
  async hello(): Promise<EngineHello> {throw new Error('not used')}
  async call<T>(method: string): Promise<T> {
    if (method === 'system.capabilities') return capabilities as T;
    if (method === 'routing.catalog') return routingCatalog as T;
    throw new Error(`unexpected ${method}`);
  }
  onRunEvent(_listener: EventListener): () => void {return () => undefined}
  onDiagnostic(): () => void {return () => undefined}
  async close(): Promise<void> {this.closed = true; this.closeCount++}
}

class RecordingMemoryClient extends FakeApplicationClient {
  calls: Array<{method: string; params?: unknown}> = [];
  override async call<T>(method: string, params?: unknown): Promise<T> {
    this.calls.push({method, params});
    if (method === 'memory.host.create') return {apiVersion: 'fishyume.application/v1', revision: 1, recordId: 'memory-a', affectedIds: [], replayed: false} as T;
    return super.call<T>(method);
  }
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

test('production StdioServerTransport path settles cleanly on stdin EOF', async () => {
  const input = new PassThrough();
  const output = new PassThrough();
  let stdout = '';
  output.setEncoding('utf8').on('data', chunk => {stdout += chunk});
  const engine = new FakeApplicationClient();
  const transport = new StdioServerTransport(input, output);
  const running = runMCPTransport(engine, transport, input);
  input.resume();
  input.end();
  await Promise.race([running, new Promise<never>((_, reject) => setTimeout(() => reject(new Error('production MCP transport did not settle after stdin EOF')), 1000))]);
  assert.equal(stdout, '');
  assert.equal(engine.closeCount, 1);
});

test('MCP and Machine CLI expose identical Application response JSON', async () => {
  const mcpServer = createMCPServer(new FakeApplicationClient());
  const mcpClient = new Client({name: 'fake-host-agent', version: '1.0.0'});
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await Promise.all([mcpServer.connect(serverTransport), mcpClient.connect(clientTransport)]);
  try {
    const listed = await mcpClient.listTools();
    assert.deepEqual(listed.tools.map(tool => tool.name), ['system.capabilities', 'routing.catalog', 'workflow.validate', 'workflow.explain', 'run.start', 'run.list', 'run.get', 'run.events', 'run.action', 'run.result', 'memory.create', 'memory.get', 'memory.list', 'memory.supersede', 'memory.delete']);
    for (const tool of listed.tools) {
      const schema = JSON.stringify(tool.inputSchema);
      for (const legacy of ['"backend"', '"tool"', '"runtime"']) assert.equal(schema.includes(legacy), false, `${tool.name} exposed ${legacy}`);
      assert.ok(tool.description?.length, `${tool.name} is missing an Agent-facing description`);
      for (const legacy of ['Backend', 'CC-Panes', 'TaskBinding', 'Session']) assert.equal(tool.description?.includes(legacy), false, `${tool.name} description exposed ${legacy}`);
    }
    assert.match(listed.tools.find(tool => tool.name === 'run.start')?.description ?? '', /clientRequestId/);
    assert.match(listed.tools.find(tool => tool.name === 'run.action')?.description ?? '', /stateVersion/);
    assert.match(listed.tools.find(tool => tool.name === 'run.events')?.description ?? '', /bounded/);
    assert.match(listed.tools.find(tool => tool.name === 'workflow.validate')?.description ?? '', /same workflow, inputs, driver, target/);
    assert.match(listed.tools.find(tool => tool.name === 'workflow.explain')?.description ?? '', /Context Policy/);
    for (const method of ['system.capabilities', 'routing.catalog'] as const) {
      const machineClient = new FakeApplicationClient(); let machineOutput = '';
      assert.equal(await runMachine(machineClient, method, '{}', {write(text) {machineOutput += text}}), 0);
      const result = await mcpClient.callTool({name: method, arguments: {}});
      assert.deepEqual(result.structuredContent, JSON.parse(machineOutput));
      const content = result.content as Array<{type: string; text?: string}>;
      assert.equal(content[0]?.type, 'text');
      assert.deepEqual(JSON.parse(content[0]?.text ?? ''), JSON.parse(machineOutput));
    }
  } finally {
    await mcpClient.close();
    await mcpServer.close();
  }
});

test('MCP Memory writes use the fixed host_agent RPC facade and require an audit reason', async () => {
  const engine = new RecordingMemoryClient();
  const server = createMCPServer(engine);
  const host = new Client({name: 'memory-host', version: '1.0.0'});
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await Promise.all([server.connect(serverTransport), host.connect(clientTransport)]);
  try {
    const rejected = await host.callTool({name: 'memory.create', arguments: {project: 'p', mutationId: 'm', type: 'fact', content: 'value', sensitivity: 'project'}});
    assert.equal(rejected.isError, true);
    const result = await host.callTool({name: 'memory.create', arguments: {project: 'p', mutationId: 'm', type: 'fact', content: 'value', sensitivity: 'project', reason: 'explicit host audit'}});
    assert.notEqual(result.isError, true);
    assert.equal(engine.calls.at(-1)?.method, 'memory.host.create');
    assert.deepEqual(engine.calls.at(-1)?.params, {project: 'p', mutationId: 'm', type: 'fact', content: 'value', sensitivity: 'project', reason: 'explicit host audit'});
  } finally {
    await host.close();
    await server.close();
  }
});
