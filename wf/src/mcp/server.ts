import {McpServer} from '@modelcontextprotocol/sdk/server/mcp.js';
import {StdioServerTransport} from '@modelcontextprotocol/sdk/server/stdio.js';
import type {Transport} from '@modelcontextprotocol/sdk/shared/transport.js';
import type {Readable} from 'node:stream';
import process from 'node:process';
import {z} from 'zod';
import {ApplicationCallError, callApplication, callHostMemory, type ApplicationMethod} from '../bridge/application.js';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';

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

const tools: Array<{name: ApplicationMethod; description: string; inputSchema: Record<string, z.ZodTypeAny>}> = [
  {name: 'system.capabilities', description: 'First Host call: discover the bounded versioned authoringGuide, fishyume/v2 Workflow schema, limits, actions, current Driver/target readiness, and immutable routing catalog summary.', inputSchema: {project: z.string().optional()}},
  {name: 'routing.catalog', description: 'Inspect the trusted immutable model capability catalog, exact catalog hash, routing contract limits, and coarse cost/latency/quality classes. This does not select a model or report live Provider availability.', inputSchema: {}},
  {name: 'workflow.validate', description: 'Validate the exact fishyume/v2 start intent without running it. Pass the same workflow, inputs, driver, target, and explicit contextBindings to explain and start; static issues and Driver capability gaps are separate.', inputSchema: workflowRequest},
  {name: 'workflow.explain', description: 'Preflight the exact start intent without mutation: DAG layers, explicit Context Policy dependencies/instructions/Memory bindings, resolved Driver/target, gaps, and warnings. Pass this same payload to run.start plus clientRequestId.', inputSchema: workflowRequest},
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
];

export function createMCPServer(client: EngineClient): McpServer {
  const server = new McpServer({name: 'fishyume', version: '0.2.1-alpha.1'});
  for (const tool of tools) {
    server.registerTool(tool.name, {description: tool.description, inputSchema: tool.inputSchema}, async params => {
      try {
        const response = tool.name === 'memory.create' || tool.name === 'memory.supersede' || tool.name === 'memory.delete'
          ? await callHostMemory(client, tool.name, params)
          : await callApplication(client, tool.name, params);
        const structuredContent = response as unknown as Record<string, unknown>;
        return {content: [{type: 'text' as const, text: JSON.stringify(response)}], structuredContent};
      } catch (error) {
        const applicationError = error instanceof ApplicationCallError ? error.applicationError : {code: 'internal', message: error instanceof Error ? error.message : String(error)};
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
