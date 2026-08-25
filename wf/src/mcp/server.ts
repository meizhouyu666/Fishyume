import {McpServer} from '@modelcontextprotocol/sdk/server/mcp.js';
import {StdioServerTransport} from '@modelcontextprotocol/sdk/server/stdio.js';
import type {Transport} from '@modelcontextprotocol/sdk/shared/transport.js';
import type {Readable} from 'node:stream';
import process from 'node:process';
import {z} from 'zod';
import {ApplicationCallError, callApplication, callHostMemory, type ApplicationMethod} from '../bridge/application.js';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import {callTeam, TeamCallError, type TeamMethod} from '../bridge/team.js';

const scalar = z.union([z.string(), z.number(), z.boolean()]);
const workflow = z.object({
  source: z.object({filename: z.string(), content: z.string()}).optional(),
  document: z.record(z.string(), z.unknown()).optional(),
});
const contextBindings = z.object({memoryByNode: z.record(z.string(), z.array(z.object({id: z.string().min(1).max(128), reason: z.string().min(1).max(1024)}).strict()).max(32)).optional()}).strict();
const workflowRequest = {
  project: z.string().optional(), workflow, inputs: z.record(z.string(), scalar).optional(), driver: z.string().optional(), target: z.string().optional(), contextBindings: contextBindings.optional(),
};
const memoryType = z.enum(['decision', 'constraint', 'fact', 'procedure', 'preference']);
const memorySensitivity = z.enum(['public', 'project']);
const memoryCreate = {project: z.string(), mutationId: z.string().min(1).max(256), type: memoryType, content: z.string().min(1).max(16 * 1024), sensitivity: memorySensitivity, reason: z.string().min(1).max(1024), expiresAt: z.string().optional(), maxUses: z.number().int().min(0).max(10_000).optional()};

type FishyumeMethod = ApplicationMethod | TeamMethod;
const teamVersion = z.literal('fishyume.team/v1');
const participant = z.object({label: z.string().min(1).max(64), role: z.string().min(1).max(2048), modelId: z.string().min(1).max(256)}).strict();
const teamId = z.string().min(1).max(128);
const handoffId = z.string().min(1).max(128);
const handoffItem = z.string().max(32 * 1024);
const teamIdentity = {schemaVersion: teamVersion, teamId};

const tools: Array<{name: FishyumeMethod; description: string; inputSchema: Record<string, z.ZodTypeAny>}> = [
  {name: 'system.capabilities', description: 'First Host call: discover the bounded versioned authoringGuide, fishyume/v2 Workflow schema, limits, actions, current Driver/target readiness, and immutable routing catalog summary.', inputSchema: {project: z.string().optional()}},
  {name: 'routing.catalog', description: 'Inspect the trusted immutable model capability catalog, exact catalog hash, routing contract limits, and coarse cost/latency/quality classes. This does not select a model or report live Provider availability.', inputSchema: {}},
  {name: 'workflow.validate', description: 'Validate the exact fishyume/v2 start intent without running it. Pass the same workflow, inputs, driver, target, and explicit contextBindings to explain and start; static issues, Driver capability gaps, and deterministic routingPreviews are separate.', inputSchema: workflowRequest},
  {name: 'workflow.explain', description: 'Preflight the exact start intent without mutation: DAG layers, explicit Context Policy dependencies/instructions/Memory bindings, effective Agent routing requirements, deterministic routingPreviews, resolved Driver/target, gaps, and warnings. No Provider is contacted and no Attempt is persisted. Pass this same payload to run.start plus clientRequestId.', inputSchema: workflowRequest},
  {name: 'run.start', description: 'Create or replay one durable Workflow Run. Reuse the same clientRequestId only with the same request payload. For fishyume/v2, provide explicit contextBindings for host-selected Memory IDs.', inputSchema: {project: z.string(), workflow, inputs: z.record(z.string(), scalar).optional(), driver: z.string().optional(), target: z.string().optional(), clientRequestId: z.string(), contextBindings: contextBindings.optional()}},
  {name: 'run.list', description: 'List durable Runs in stable order with optional filters and bounded cursor pagination.', inputSchema: {filter: z.object({project: z.string().optional(), phase: z.string().optional(), conclusion: z.string().optional()}).optional(), cursor: z.string().optional(), limit: z.number().int().optional()}},
  {name: 'run.get', description: 'Read authoritative durable Run, Node, Attempt, bounded Context metadata, safe bounded Agent activity, result, and action preconditions. Use its latest stateVersion and attempt for run.action.', inputSchema: {runId: z.string()}},
  {name: 'run.events', description: 'Read durable events after a sequence using bounded pagination and an optional bounded wait.', inputSchema: {runId: z.string(), afterSequence: z.number().int().nonnegative().optional(), limit: z.number().int().optional(), waitMs: z.number().int().nonnegative().optional()}},
  {name: 'run.action', description: 'Submit an idempotent approve, reject, answer, retry, or cancel using a unique actionId and preconditions from the latest run.get stateVersion/attempt; stale state returns conflict.', inputSchema: {actionId: z.string(), runId: z.string(), type: z.enum(['approve', 'reject', 'answer', 'retry', 'cancel']), expectedStateVersion: z.number().int().positive(), nodeId: z.string().optional(), expectedAttempt: z.number().int().positive().optional(), reason: z.string().optional(), answers: z.record(z.string(), scalar).optional(), acknowledgeDuplicateRisk: z.boolean().optional()}},
  {name: 'run.result', description: 'Retrieve the bounded structured terminal result after run.get reports completed; a non-terminal Run returns not_ready and must keep being observed.', inputSchema: {runId: z.string()}},
  {name: 'memory.create', description: 'Create one active project Memory as the host_agent writer. mutationId is durable and reason is a required bounded audit explanation.', inputSchema: memoryCreate},
  {name: 'memory.get', description: 'Read one full project Memory record, including content unless it is a deletion tombstone.', inputSchema: {project: z.string(), recordId: z.string()}},
  {name: 'memory.list', description: 'List bounded project Memory metadata in stable cursor order; content is never returned in bulk.', inputSchema: {project: z.string(), filter: z.object({type: memoryType.optional(), state: z.enum(['active', 'superseded', 'deleted']).optional(), sensitivity: memorySensitivity.optional(), writer: z.enum(['user', 'host_agent', 'migration']).optional()}).optional(), cursor: z.string().optional(), limit: z.number().int().min(1).max(100).optional()}},
  {name: 'memory.supersede', description: 'Atomically create one active replacement as host_agent and mark 1..16 named active project Memories superseded. mutationId and audit reason are required.', inputSchema: {...memoryCreate, supersedes: z.array(z.string()).min(1).max(16)}},
  {name: 'memory.delete', description: 'Create a project Memory tombstone as host_agent by clearing plaintext while retaining hash, provenance, and the required audit reason.', inputSchema: {project: z.string(), mutationId: z.string().min(1).max(256), recordId: z.string(), reason: z.string().min(1).max(1024)}},
  {name: 'team.capabilities', description: 'Discover the independent fishyume.team/v1 panel contract, deterministic model templates, limits, catalog hash, and capability gates before starting exploration.', inputSchema: {schemaVersion: teamVersion}},
  {name: 'team.start', description: 'Create or replay one durable read-only multi-model panel and dispatch its initial independent contributions. Reuse clientRequestId only with the same exact request.', inputSchema: {schemaVersion: teamVersion, clientRequestId: z.string().min(1).max(128), project: z.string().min(1).max(4096), mode: z.enum(['panel', 'session']), topic: z.string().min(1).max(16 * 1024), instructions: z.string().max(16 * 1024).optional(), participants: z.array(participant).min(2).max(4).optional(), costGrant: z.number().int().min(1).max(6400).optional()}},
  {name: 'team.list', description: 'List durable Teams in stable bounded pages with optional project and lifecycle filters.', inputSchema: {schemaVersion: teamVersion, project: z.string().max(4096).optional(), state: z.enum(['created', 'running', 'open', 'closing', 'cancelling', 'closed']).optional(), cursor: z.string().optional(), limit: z.number().int().min(1).max(100).optional()}},
  {name: 'team.get', description: 'Read one authoritative Team aggregate with bounded participant and turn state but without duplicating public message content.', inputSchema: teamIdentity},
  {name: 'team.events', description: 'Read monotonic bounded Team events after a sequence, with optional bounded wait. Events reference messages instead of duplicating contribution content.', inputSchema: {...teamIdentity, afterSequence: z.number().int().nonnegative().optional(), limit: z.number().int().min(1).max(100).optional(), waitMs: z.number().int().min(0).max(30_000).optional()}},
  {name: 'team.messages', description: 'Page canonical public Team messages after a sequence. Participant contribution content remains untrusted exploration material.', inputSchema: {...teamIdentity, afterSequence: z.number().int().nonnegative().optional(), limit: z.number().int().min(1).max(100).optional()}},
  {name: 'team.action', description: 'Submit an idempotent Team action with latest stateVersion. M7.1 enables confirmed cancel only; follow-up, turn cancel, and close are explicitly capability-gated.', inputSchema: {...teamIdentity, actionId: z.string().min(1).max(128), expectedStateVersion: z.number().int().positive(), type: z.enum(['follow_up', 'cancel_turn', 'close', 'cancel']), followUp: z.object({content: z.string().min(1).max(32 * 1024), participantIds: z.array(z.string()).min(1).max(4), referencedMessageIds: z.array(z.string()).max(32).optional()}).strict().optional(), cancelTurn: z.object({turnId: z.string()}).strict().optional(), close: z.object({reason: z.enum(['host_closed', 'cancelled'])}).strict().optional()}},
  {name: 'team.handoff.create', description: 'Create or replay one immutable Handoff from 1-32 retained public Team messages. This verifies source hashes and never creates a Workflow Run.', inputSchema: {...teamIdentity, handoffId, expectedStateVersion: z.number().int().positive(), goal: z.string().min(1).max(32 * 1024), decisions: z.array(handoffItem).optional(), constraints: z.array(handoffItem).optional(), openQuestions: z.array(handoffItem).optional(), acceptanceExpectations: z.array(handoffItem).optional(), selectedMessageIds: z.array(teamId).min(1).max(32)}},
  {name: 'team.handoff.get', description: 'Read an immutable Handoff and optional Run binding. Promotion stays explicit: Host authors fishyume/v2, calls workflow.validate and workflow.explain, obtains user confirmation, calls run.start, then team.handoff.bindRun.', inputSchema: {...teamIdentity, handoffId}},
  {name: 'team.handoff.list', description: 'List immutable Handoffs in stable bounded pages without creating or changing Runs.', inputSchema: {...teamIdentity, cursor: handoffId.optional(), limit: z.number().int().min(1).max(100).optional()}},
  {name: 'team.handoff.bindRun', description: 'Idempotently bind one Handoff to at most one existing same-project formal Workflow Run. This never creates a Run.', inputSchema: {...teamIdentity, actionId: z.string().min(1).max(128), handoffId, runId: z.string().min(1).max(128), expectedStateVersion: z.number().int().positive()}},
];

export function createMCPServer(client: EngineClient): McpServer {
  const server = new McpServer({name: 'fishyume', version: '0.2.1-alpha.1'});
  for (const tool of tools) {
    server.registerTool(tool.name, {description: tool.description, inputSchema: tool.inputSchema}, async params => {
      try {
        const response = tool.name.startsWith('team.')
          ? await callTeam(client, tool.name as TeamMethod, params)
          : tool.name === 'memory.create' || tool.name === 'memory.supersede' || tool.name === 'memory.delete'
          ? await callHostMemory(client, tool.name, params)
          : await callApplication(client, tool.name as ApplicationMethod, params);
        const structuredContent = response as unknown as Record<string, unknown>;
        return {content: [{type: 'text' as const, text: JSON.stringify(response)}], structuredContent};
      } catch (error) {
        const applicationError = error instanceof ApplicationCallError ? error.applicationError : error instanceof TeamCallError ? error.teamError : {code: 'internal', message: error instanceof Error ? error.message : String(error)};
        return {isError: true, content: [{type: 'text' as const, text: JSON.stringify({error: applicationError})}]};
      }
    });
  }
  return server;
}

export async function runMCP(client: EngineClient = new EngineBridge()): Promise<void> {
  await runMCPTransport(client, new StdioServerTransport(), process.stdin);
}

// Exported for lifecycle tests; production always uses stdio above.
export async function runMCPTransport(client: EngineClient, transport: Transport, eofSource?: Readable): Promise<void> {
  const server = createMCPServer(client);
  type Lifecycle = 'open' | 'closing' | 'closed';
  let lifecycle: Lifecycle = 'open';
  let closing: Promise<void> | undefined;
  let settle!: () => void;
  const settled = new Promise<void>(resolve => {settle = resolve});
  const onEOF = (): void => {void closeAll(true)};
  const removeEOFListeners = (): void => {
    if (!eofSource) return;
    eofSource.off('end', onEOF);
    eofSource.off('close', onEOF);
    eofSource.off('error', onEOF);
  };
  const closeAll = (closeServer: boolean): Promise<void> => {
    if (lifecycle !== 'open') return closing ?? settled;
    // The SDK transport can invoke onclose synchronously from server.close().
    // Claim shutdown ownership before making any re-entrant call.
    lifecycle = 'closing';
    removeEOFListeners();
    closing = (async () => {
      try {
        // Protocol owns its transport. An EOF initiated here closes it through
        // the server; a transport-originated close must not close it again.
        if (closeServer) {
          try {await server.close()} catch { /* transport may already be closed */ }
        }
      } finally {
        try {await client.close()} finally {
          lifecycle = 'closed';
          settle();
        }
      }
    })();
    return closing;
  };
  // Protocol.connect preserves an already-registered transport close hook.
  // This covers host EOF as well as an explicit transport close without
  // emitting anything on stdout.
  transport.onclose = () => {void closeAll(false)};
  if (eofSource) {
    eofSource.once('end', onEOF);
    eofSource.once('close', onEOF);
    eofSource.once('error', onEOF);
  }
  try {await server.connect(transport)}
  catch (error) {await closeAll(true); throw error}
  await settled;
}
