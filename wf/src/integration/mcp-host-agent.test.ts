import assert from 'node:assert/strict';
import {spawnSync} from 'node:child_process';
import {mkdtemp, readFile, rm, stat} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import test from 'node:test';
import {Client} from '@modelcontextprotocol/sdk/client/index.js';
import {InMemoryTransport} from '@modelcontextprotocol/sdk/inMemory.js';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import type {ApplicationRunView, MemoryGetResponse, MemoryMutationResponse, RoutingCatalogResponse, RunActionResponse, RunEventsResponse, RunGetResponse, RunResultResponse, RunStartResponse, SystemCapabilitiesResponse, WorkflowExplainResponse, WorkflowValidateResponse} from '../bridge/application.js';
import {createMCPServer} from '../mcp/server.js';

const workflow = {
  apiVersion: 'fishyume/v2',
  name: 'mcp-host-agent-smoke',
  defaults: {agent: {driver: 'codex', target: 'local'}},
  execution: {maxConcurrency: 1},
  nodes: {
    plan: {type: 'agent', task: 'scenario:terminal-succeeded\nCreate a plan.', context: {projectInstructions: ['README.md'], dependencies: []}},
    approve: {type: 'approval', dependsOn: ['plan'], prompt: 'Approve the plan?'},
    implement: {type: 'agent', dependsOn: ['approve'], task: 'scenario:needs-input-then-succeeded\nImplement the approved plan.', context: {projectInstructions: ['README.md'], dependencies: ['plan']}},
  },
};

async function withTimeout<T>(promise: Promise<T>, label: string, timeoutMs = 15_000): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  try {
    return await Promise.race([
      promise,
      new Promise<T>((_, reject) => {timer = setTimeout(() => reject(new Error(`${label} timeout`)), timeoutMs)}),
    ]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}

function bridgeWithEnvironment(enginePath: string, environment: Record<string, string>, bridges: EngineBridge[]): EngineBridge {
  const previous = new Map<string, string | undefined>();
  for (const [key, value] of Object.entries(environment)) {
    previous.set(key, process.env[key]);
    process.env[key] = value;
  }
  try {
    const bridge = new EngineBridge(enginePath);
    bridges.push(bridge);
    return bridge;
  } finally {
    for (const [key, value] of previous) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  }
}

function textContent(value: unknown): string {
  const content = (value as {content?: Array<{type?: string; text?: string}>}).content ?? [];
  const text = content.find(item => item.type === 'text')?.text;
  assert.ok(text, `MCP response did not contain text content: ${JSON.stringify(value)}`);
  return text;
}

async function callTool<T>(client: Client, name: string, args: Record<string, unknown>): Promise<T> {
  const response = await client.callTool({name, arguments: args});
  assert.notEqual(response.isError, true, `${name} returned an MCP error: ${textContent(response)}`);
  return JSON.parse(textContent(response)) as T;
}

async function waitForRun(client: Client, runId: string, accept: (run: ApplicationRunView) => boolean, label: string): Promise<ApplicationRunView> {
  let afterSequence = 0;
  let latest: ApplicationRunView | undefined;
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    const events = await callTool<RunEventsResponse>(client, 'run.events', {runId, afterSequence, limit: 50, waitMs: 250});
    afterSequence = events.nextAfterSequence;
    const view = await callTool<RunGetResponse>(client, 'run.get', {runId});
    latest = view.run;
    if (accept(view.run)) return view.run;
  }
  throw new Error(`${label} timeout: ${JSON.stringify(latest)}`);
}

async function stopTemporaryControlPlane(stateDir: string): Promise<void> {
  let pid: number | undefined;
  try {pid = (JSON.parse(await readFile(join(stateDir, 'control-plane.json'), 'utf8')) as {pid?: number}).pid} catch {return}
  if (!pid) return;
  try {process.kill(pid, 'SIGTERM')} catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ESRCH') return;
    throw error;
  }
  const deadline = Date.now() + 5_000;
  while (Date.now() < deadline) {
    try {process.kill(pid, 0)} catch (error) {
      if ((error as NodeJS.ErrnoException).code === 'ESRCH') return;
      throw error;
    }
    await new Promise(resolve => setTimeout(resolve, 25));
  }
  throw new Error(`temporary Control Plane ${pid} did not exit`);
}

test('MCP Host Agent completes capabilities, authoring, approval, answer, events, and result', {timeout: 90_000}, async () => {
  const testDir = dirname(fileURLToPath(import.meta.url));
  const projectRoot = join(testDir, '..', '..', '..');
  const engineRoot = join(projectRoot, 'wf-engine');
  const temporary = await mkdtemp(join(tmpdir(), 'fishyume-mcp-host-'));
  const enginePath = join(temporary, process.platform === 'win32' ? 'wf-engine.exe' : 'wf-engine');
  const codexPath = join(temporary, process.platform === 'win32' ? 'fake-codex.exe' : 'fake-codex');
  const stateDir = join(temporary, 'state');
  const bridges: EngineBridge[] = [];
  const environment = {FISHYUME_CODEX_PATH: codexPath, FISHYUME_STATE_DIR: stateDir, WF_STATE_DIR: stateDir};
  const engineBuild = spawnSync('go', ['build', '-o', enginePath, './cmd/wf-engine'], {cwd: engineRoot, encoding: 'utf8'});
  assert.equal(engineBuild.status, 0, engineBuild.stderr);
  const codexBuild = spawnSync('go', ['build', '-o', codexPath, './internal/driver/codexprocess/testdata/fake-agent'], {cwd: engineRoot, encoding: 'utf8'});
  assert.equal(codexBuild.status, 0, codexBuild.stderr);

  const bridge = bridgeWithEnvironment(enginePath, environment, bridges);
  const server = createMCPServer(bridge);
  const host = new Client({name: 'fishyume-acceptance-host', version: '1.0.0'});
  const [hostTransport, serverTransport] = InMemoryTransport.createLinkedPair();
  try {
    await Promise.all([server.connect(serverTransport), host.connect(hostTransport)]);
    const capabilities = await callTool<SystemCapabilitiesResponse>(host, 'system.capabilities', {});
    assert.equal(capabilities.apiVersion, 'fishyume.application/v1');
    assert.equal(capabilities.workflowSchemaVersion, 'fishyume/v2');
    assert.equal(capabilities.authoringGuide.schemaVersion, 'fishyume.authoring-guide/v1');
    assert.equal(capabilities.authoringGuide.workflowApiVersion, 'fishyume/v2');
    assert.deepEqual(capabilities.authoringGuide.recommendedFlow.filter(method => !method.startsWith('memory.')), ['system.capabilities', 'routing.catalog', 'workflow.validate', 'workflow.explain', 'run.start', 'run.events', 'run.get', 'run.action', 'run.result']);
    assert.ok(capabilities.drivers.some(driver => driver.driver === 'codex' && driver.targets.includes('local')));
    const catalog = await callTool<RoutingCatalogResponse>(host, 'routing.catalog', {});
    assert.equal(catalog.catalogHash, capabilities.routingCatalog.catalogHash);
    assert.equal(catalog.catalog.models.length, capabilities.routingCatalog.modelCount);
    assert.equal(catalog.dynamicAvailability, true);

    const includedMarker = 'M5_6_INCLUDED_MEMORY_CONTENT_MUST_NOT_LEAK';
    const omittedMarker = 'M5_6_OMITTED_MEMORY_CONTENT_MUST_NOT_LEAK';
    let includedMemory = await callTool<MemoryMutationResponse>(host, 'memory.create', {project: projectRoot, mutationId: 'm5-6-included-memory-0', type: 'constraint', content: includedMarker, sensitivity: 'project', reason: 'M5.6 selected Memory acceptance'});
    const omittedMemory = await callTool<MemoryMutationResponse>(host, 'memory.create', {project: projectRoot, mutationId: 'm5-6-omitted-memory', type: 'fact', content: omittedMarker + 'x'.repeat(16 * 1024 - omittedMarker.length), sensitivity: 'project', reason: 'M5.6 budget omission acceptance'});
    // Optional components are ordered by stable record ID. Select a small record
    // before the full-budget record so inclusion and budget omission are deterministic.
    for (let candidate = 1; includedMemory.recordId > omittedMemory.recordId && candidate <= 64; candidate++) {
      includedMemory = await callTool<MemoryMutationResponse>(host, 'memory.create', {project: projectRoot, mutationId: `m5-6-included-memory-${candidate}`, type: 'constraint', content: includedMarker, sensitivity: 'project', reason: 'M5.6 selected Memory acceptance'});
    }
    assert.ok(includedMemory.recordId < omittedMemory.recordId, 'could not establish deterministic optional Memory order');
    const contextBindings = {memoryByNode: {plan: [
      {id: includedMemory.recordId, reason: 'Small selected constraint required by this node'},
      {id: omittedMemory.recordId, reason: 'Oversized optional record must remain omitted'},
    ]}};
    const exactIntent = {project: projectRoot, workflow: {document: workflow}, inputs: {}, driver: 'codex', target: 'local', contextBindings};
    const validated = await callTool<WorkflowValidateResponse>(host, 'workflow.validate', exactIntent);
    assert.equal(validated.valid, true);
    assert.deepEqual(validated.issues, []);
    assert.deepEqual(validated.routingRequirements.map(view => view.nodeId), ['plan', 'implement']);
    assert.equal(validated.routingRequirements[0]?.requirement.complexity, 'standard');
    assert.equal(validated.routingRequirements[0]?.requirement.allowModelFallback, false);
    const explained = await callTool<WorkflowExplainResponse>(host, 'workflow.explain', exactIntent);
    assert.deepEqual(explained.topologicalOrder, ['plan', 'approve', 'implement']);
    assert.deepEqual(explained.parallelLayers, [['plan'], ['approve'], ['implement']]);
    const explainedImplement = explained.nodes.find(node => node.id === 'implement');
    assert.deepEqual(explainedImplement?.contextSources, ['plan']);
    assert.deepEqual(explainedImplement?.projectInstructions, ['README.md']);
    assert.equal(explainedImplement?.contextPolicyVersion, 'context-policy/v1');
    const explainedPlan = explained.nodes.find(node => node.id === 'plan');
    assert.deepEqual(explainedPlan?.memoryBindings, contextBindings.memoryByNode.plan);
    assert.equal(explainedPlan?.routing?.maxContextBytes, 131072);
    assert.equal(explained.nodes.find(node => node.id === 'approve')?.routing, undefined);

    const startRequest = {...exactIntent, clientRequestId: 'mcp-host-smoke-1'};
    const started = await callTool<RunStartResponse>(host, 'run.start', startRequest);
    const replayed = await callTool<RunStartResponse>(host, 'run.start', startRequest);
    assert.deepEqual(replayed, started, 'same clientRequestId must replay the committed start response');
    const approval = await waitForRun(host, started.runId, run => run.phase === 'waiting' && run.nodes.some(node => node.nodeId === 'approve' && node.phase === 'waiting'), 'approval');
    const approvalNode = approval.nodes.find(node => node.nodeId === 'approve');
    assert.ok(approvalNode);
    const approved = await callTool<RunActionResponse>(host, 'run.action', {actionId: 'mcp-host-approve-1', runId: started.runId, type: 'approve', expectedStateVersion: approval.stateVersion, nodeId: 'approve'});
    assert.equal(approved.type, 'approve');

    const stale = await host.callTool({name: 'run.action', arguments: {actionId: 'mcp-host-stale-1', runId: started.runId, type: 'approve', expectedStateVersion: approval.stateVersion, nodeId: 'approve'}});
    assert.equal(stale.isError, true);
    assert.equal((JSON.parse(textContent(stale)) as {error?: {code?: string}}).error?.code, 'conflict');

    const needsInput = await waitForRun(host, started.runId, run => run.nodes.some(node => node.nodeId === 'implement' && node.reason === 'agent_waiting_input'), 'needs_input');
    const implement = needsInput.nodes.find(node => node.nodeId === 'implement');
    assert.ok(implement?.currentAttempt);
    assert.equal(implement.result?.questions[0]?.id, 'approval');
    await callTool<RunActionResponse>(host, 'run.action', {actionId: 'mcp-host-answer-1', runId: started.runId, type: 'answer', expectedStateVersion: needsInput.stateVersion, nodeId: 'implement', expectedAttempt: implement.currentAttempt, answers: {approval: 'yes'}});

    const result = await withTimeout((async () => {
      while (true) {
        try {return await callTool<RunResultResponse>(host, 'run.result', {runId: started.runId})}
        catch (error) {if (!(error instanceof Error) || !error.message.includes('not ready')) throw error}
        await new Promise(resolve => setTimeout(resolve, 30));
      }
    })(), 'run.result');
    assert.equal(result.conclusion, 'succeeded');
    assert.equal(result.results.length, 3);
    const firstPage = await callTool<RunEventsResponse>(host, 'run.events', {runId: started.runId, afterSequence: 0, limit: 1});
    assert.equal(firstPage.events.length, 1);
    const secondPage = await callTool<RunEventsResponse>(host, 'run.events', {runId: started.runId, afterSequence: firstPage.nextAfterSequence, limit: 1});
    assert.ok(secondPage.events[0]!.sequence > firstPage.events[0]!.sequence);
    const events = await callTool<RunEventsResponse>(host, 'run.events', {runId: started.runId, limit: 100});
    assert.ok(events.events.some(event => event.type === 'run.created'));
    assert.ok(events.events.some(event => event.type === 'node.approval_required'));
    assert.ok(events.events.some(event => event.reason === 'agent_waiting_input' || event.type.includes('waiting_input')));
    assert.ok(events.events.every((event, index) => index === 0 || event.sequence > events.events[index - 1]!.sequence));
    const terminal = await callTool<RunGetResponse>(host, 'run.get', {runId: started.runId});
    assert.equal(started.attach, `fishyume attach ${started.runId}`);
    const terminalPlan = terminal.run.nodes.find(node => node.nodeId === 'plan');
    assert.deepEqual(terminalPlan?.attempt?.context?.memoryUsage?.recordIds, [includedMemory.recordId]);
    assert.equal(terminalPlan?.attempt?.context?.memoryUsage?.committed, true);
    const includedAfter = await callTool<MemoryGetResponse>(host, 'memory.get', {project: projectRoot, recordId: includedMemory.recordId});
    const omittedAfter = await callTool<MemoryGetResponse>(host, 'memory.get', {project: projectRoot, recordId: omittedMemory.recordId});
    assert.equal(includedAfter.record.useCount, 1, 'included Memory must be consumed exactly once');
    assert.equal(omittedAfter.record.useCount, 0, 'budget-omitted Memory must not be consumed');
    const publicEvidence = JSON.stringify({capabilities, catalog, validated, explained, started, approval, needsInput, result, events, terminal});
    assert.equal(publicEvidence.includes(includedMarker), false, 'included Memory content leaked through public metadata');
    assert.equal(publicEvidence.includes(omittedMarker), false, 'omitted Memory content leaked through public metadata');
  } finally {
    await host.close().catch(() => undefined);
    await server.close().catch(() => undefined);
    for (const client of bridges.reverse()) await client.close().catch(() => undefined);
    await stopTemporaryControlPlane(stateDir).catch(() => undefined);
    await rm(temporary, {recursive: true, force: true, maxRetries: 3, retryDelay: 100});
    await assert.rejects(stat(temporary), (error: NodeJS.ErrnoException) => error.code === 'ENOENT');
  }
});
