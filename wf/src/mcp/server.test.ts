import assert from 'node:assert/strict';
import test from 'node:test';
import {PassThrough} from 'node:stream';
import {Client} from '@modelcontextprotocol/sdk/client/index.js';
import {InMemoryTransport} from '@modelcontextprotocol/sdk/inMemory.js';
import {StdioServerTransport} from '@modelcontextprotocol/sdk/server/stdio.js';
import type {RoutingCatalogResponse, SystemCapabilitiesResponse, WorkflowExplainResponse, WorkflowValidateResponse} from '../bridge/application.js';
import type {EngineClient, EventListener} from '../bridge/engine.js';
import type {EngineHello} from '../bridge/types.js';
import {runMachine} from '../commands/machine.js';
import type {TeamCapabilities} from '../bridge/team.js';
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

const teamCapabilities: TeamCapabilities = {
  schemaVersion: 'fishyume.team/v1', supportedModes: ['panel'], features: {panel: true, handoff: true, session: false, followUp: false, cancelTurn: false, close: false, cancel: true},
  limits: {minParticipants: 2, maxParticipants: 4}, participantTemplates: [
    {label: 'architect', role: 'propose', modelId: 'codex/local/gpt-5.6', driver: 'codex', target: 'local'},
    {label: 'reviewer', role: 'challenge', modelId: 'codex/local/gpt-5.6-luna', driver: 'codex', target: 'local'},
  ], catalogHash: 'b'.repeat(64),
};

const handoff = {
  schemaVersion: 'fishyume.team/v1', handoffId: 'handoff-1', teamId: 'team-1', sourceTeamVersion: 4,
  goal: 'Promote the accepted design', decisions: ['Use the smaller design'], selectedMessageIds: ['message-1'],
  sourceMessageHashes: ['c'.repeat(64)], contentHash: 'd'.repeat(64), createdAt: '2026-08-25T00:00:00Z',
};
const handoffBinding = {teamId: 'team-1', handoffId: 'handoff-1', runId: 'run-1', project: 'C:/project', boundAt: '2026-08-25T00:01:00Z'};

const routingPreviewResponse = {
  apiVersion: 'fishyume.application/v1', workflowSchemaVersion: 'fishyume/v2', valid: true, issues: [], capabilityGaps: [], warnings: [], routingRequirements: [],
  routingPreviews: [{nodeId: 'work', driver: 'codex', target: 'local', requirement: {schemaVersion: 'fishyume.routing-requirement/v1', capabilities: ['repo_read'], complexity: 'simple', quality: 'economy', latency: 'fast', maxCostUnits: 1, maxContextBytes: 131072, maxOutputBytes: 32768, allowModelFallback: false}, decision: {schemaVersion: 'fishyume.routing-decision/v1', catalogHash: 'a'.repeat(64), requirement: {schemaVersion: 'fishyume.routing-requirement/v1', capabilities: ['repo_read'], complexity: 'simple', quality: 'economy', latency: 'fast', maxCostUnits: 1, maxContextBytes: 131072, maxOutputBytes: 32768, allowModelFallback: false}, selected: {driver: 'codex', provider: 'local', model: 'model'}, reasonCodes: ['capability_match'], budget: {maxCostUnits: 1, contextBytes: 131072, outputBytes: 32768}, fallbackPolicy: {mode: 'none', maxAttempts: 1, requireNoSideEffect: false, requireApproval: false}}}],
} as WorkflowValidateResponse;
const explainPreviewResponse = {...routingPreviewResponse, name: 'preview', topologicalOrder: ['work'], parallelLayers: [['work']], nodes: []} as WorkflowExplainResponse;

class FakeApplicationClient implements EngineClient {
  closed = false;
	closeCount = 0;
  async hello(): Promise<EngineHello> {throw new Error('not used')}
  async call<T>(method: string): Promise<T> {
    if (method === 'system.capabilities') return capabilities as T;
    if (method === 'routing.catalog') return routingCatalog as T;
    if (method === 'workflow.validate') return routingPreviewResponse as T;
    if (method === 'workflow.explain') return explainPreviewResponse as T;
    if (method === 'team.capabilities') return teamCapabilities as T;
    if (method === 'team.handoff.create') return {schemaVersion: 'fishyume.team/v1', handoff, replayed: false} as T;
    if (method === 'team.handoff.get') return {schemaVersion: 'fishyume.team/v1', handoff, binding: handoffBinding} as T;
    if (method === 'team.handoff.list') return {schemaVersion: 'fishyume.team/v1', items: [handoff]} as T;
    if (method === 'team.handoff.bindRun') return {schemaVersion: 'fishyume.team/v1', binding: handoffBinding, replayed: false} as T;
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

test('M6.7 MCP and Machine CLI preserve routing previews identically', async () => {
  const mcpServer = createMCPServer(new FakeApplicationClient());
  const mcpClient = new Client({name: 'preview-host', version: '1.0.0'});
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await Promise.all([mcpServer.connect(serverTransport), mcpClient.connect(clientTransport)]);
  try {
    const args = {workflow: {document: {apiVersion: 'fishyume/v2'}}};
    for (const method of ['workflow.validate', 'workflow.explain'] as const) {
      const machineClient = new FakeApplicationClient(); let machineOutput = '';
      assert.equal(await runMachine(machineClient, method, JSON.stringify(args), {write(text) {machineOutput += text}}), 0);
      const result = await mcpClient.callTool({name: method, arguments: args});
      assert.deepEqual(result.structuredContent, JSON.parse(machineOutput));
      assert.equal((result.structuredContent as {routingPreviews: unknown[]}).routingPreviews.length, 1);
    }
  } finally {
    await mcpClient.close();
    await mcpServer.close();
  }
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
    assert.deepEqual(listed.tools.map(tool => tool.name), ['system.capabilities', 'routing.catalog', 'workflow.validate', 'workflow.explain', 'run.start', 'run.list', 'run.get', 'run.events', 'run.action', 'run.result', 'memory.create', 'memory.get', 'memory.list', 'memory.supersede', 'memory.delete', 'team.capabilities', 'team.start', 'team.list', 'team.get', 'team.events', 'team.messages', 'team.action', 'team.handoff.create', 'team.handoff.get', 'team.handoff.list', 'team.handoff.bindRun']);
    for (const tool of listed.tools) {
      const schema = JSON.stringify(tool.inputSchema);
      for (const legacy of ['"backend"', '"tool"', '"runtime"']) assert.equal(schema.includes(legacy), false, `${tool.name} exposed ${legacy}`);
      assert.ok(tool.description?.length, `${tool.name} is missing an Agent-facing description`);
      for (const legacy of ['Backend', 'CC-Panes', 'TaskBinding']) assert.equal(tool.description?.includes(legacy), false, `${tool.name} description exposed ${legacy}`);
    }
    assert.match(listed.tools.find(tool => tool.name === 'run.start')?.description ?? '', /clientRequestId/);
    assert.match(listed.tools.find(tool => tool.name === 'run.action')?.description ?? '', /stateVersion/);
    assert.match(listed.tools.find(tool => tool.name === 'run.events')?.description ?? '', /bounded/);
    assert.match(listed.tools.find(tool => tool.name === 'workflow.validate')?.description ?? '', /same workflow, inputs, driver, target/);
    assert.match(listed.tools.find(tool => tool.name === 'workflow.explain')?.description ?? '', /Context Policy/);
    assert.match(listed.tools.find(tool => tool.name === 'team.handoff.get')?.description ?? '', /workflow\.validate.*workflow\.explain.*user confirmation.*run\.start.*team\.handoff\.bindRun/);
    for (const method of ['system.capabilities', 'routing.catalog', 'team.capabilities'] as const) {
      const machineClient = new FakeApplicationClient(); let machineOutput = '';
      const args = method === 'team.capabilities' ? {schemaVersion: 'fishyume.team/v1'} : {};
      assert.equal(await runMachine(machineClient, method, JSON.stringify(args), {write(text) {machineOutput += text}}), 0);
      const result = await mcpClient.callTool({name: method, arguments: args});
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

test('M7.2 MCP and Machine CLI preserve all Handoff responses identically', async () => {
  const mcpServer = createMCPServer(new FakeApplicationClient());
  const mcpClient = new Client({name: 'handoff-host', version: '1.0.0'});
  const [clientTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  await Promise.all([mcpServer.connect(serverTransport), mcpClient.connect(clientTransport)]);
  try {
    const cases = [
      ['team.handoff.create', {schemaVersion: 'fishyume.team/v1', teamId: 'team-1', handoffId: 'handoff-1', expectedStateVersion: 4, goal: handoff.goal, decisions: handoff.decisions, selectedMessageIds: ['message-1']}],
      ['team.handoff.get', {schemaVersion: 'fishyume.team/v1', teamId: 'team-1', handoffId: 'handoff-1'}],
      ['team.handoff.list', {schemaVersion: 'fishyume.team/v1', teamId: 'team-1', limit: 10}],
      ['team.handoff.bindRun', {schemaVersion: 'fishyume.team/v1', actionId: 'bind-1', teamId: 'team-1', handoffId: 'handoff-1', runId: 'run-1', expectedStateVersion: 4}],
    ] as const;
    for (const [method, args] of cases) {
      const machineClient = new FakeApplicationClient(); let machineOutput = '';
      assert.equal(await runMachine(machineClient, method, JSON.stringify(args), {write(text) {machineOutput += text}}), 0);
      const result = await mcpClient.callTool({name: method, arguments: args});
      assert.deepEqual(result.structuredContent, JSON.parse(machineOutput));
    }
    const rejected = await mcpClient.callTool({name: 'team.handoff.get', arguments: {schemaVersion: 'fishyume.team/v1', teamId: 'team-1', handoffId: 'h'.repeat(129)}});
    assert.equal(rejected.isError, true);
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
