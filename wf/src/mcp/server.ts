import {McpServer} from '@modelcontextprotocol/sdk/server/mcp.js';
import {StdioServerTransport} from '@modelcontextprotocol/sdk/server/stdio.js';
import type {Transport} from '@modelcontextprotocol/sdk/shared/transport.js';
import type {Readable} from 'node:stream';
import process from 'node:process';
import {z} from 'zod';
import {ApplicationCallError, callApplication, type ApplicationMethod} from '../bridge/application.js';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';

const scalar = z.union([z.string(), z.number(), z.boolean()]);
const workflow = z.object({
  source: z.object({filename: z.string(), content: z.string()}).optional(),
  document: z.record(z.string(), z.unknown()).optional(),
});
const workflowRequest = {
  project: z.string().optional(), workflow, inputs: z.record(z.string(), scalar).optional(), driver: z.string().optional(), target: z.string().optional(),
};

const tools: Array<{name: ApplicationMethod; description: string; inputSchema: Record<string, z.ZodTypeAny>}> = [
  {name: 'system.capabilities', description: 'Inspect Application and Workflow schemas, bounds, actions, and current Driver/target readiness before planning calls.', inputSchema: {project: z.string().optional()}},
  {name: 'workflow.validate', description: 'Validate a Workflow without running it; static issues and current Driver capability gaps are returned separately.', inputSchema: workflowRequest},
  {name: 'workflow.explain', description: 'Resolve a Workflow without running it, including DAG order, parallel layers, context sources, Driver/target choices, and warnings.', inputSchema: workflowRequest},
  {name: 'run.start', description: 'Create or replay one durable Workflow Run. Reuse the same clientRequestId only with the same request payload.', inputSchema: {project: z.string(), workflow, inputs: z.record(z.string(), scalar).optional(), driver: z.string().optional(), target: z.string().optional(), clientRequestId: z.string()}},
  {name: 'run.list', description: 'List durable Runs in stable order with optional filters and bounded cursor pagination.', inputSchema: {filter: z.object({project: z.string().optional(), phase: z.string().optional(), conclusion: z.string().optional()}).optional(), cursor: z.string().optional(), limit: z.number().int().optional()}},
  {name: 'run.get', description: 'Read the current durable Run, Node, Attempt, result, and action-precondition state.', inputSchema: {runId: z.string()}},
  {name: 'run.events', description: 'Read durable events after a sequence using bounded pagination and an optional bounded wait.', inputSchema: {runId: z.string(), afterSequence: z.number().int().nonnegative().optional(), limit: z.number().int().optional(), waitMs: z.number().int().nonnegative().optional()}},
  {name: 'run.action', description: 'Submit an idempotent approve, reject, answer, retry, or cancel action against an observed stateVersion.', inputSchema: {actionId: z.string(), runId: z.string(), type: z.enum(['approve', 'reject', 'answer', 'retry', 'cancel']), expectedStateVersion: z.number().int().positive(), nodeId: z.string().optional(), expectedAttempt: z.number().int().positive().optional(), reason: z.string().optional(), answers: z.record(z.string(), scalar).optional(), acknowledgeDuplicateRisk: z.boolean().optional()}},
  {name: 'run.result', description: 'Read the bounded structured result of a terminal Run; a non-terminal Run returns not_ready.', inputSchema: {runId: z.string()}},
];

export function createMCPServer(client: EngineClient): McpServer {
  const server = new McpServer({name: 'fishyume', version: '0.2.1-alpha.1'});
  for (const tool of tools) {
    server.registerTool(tool.name, {description: tool.description, inputSchema: tool.inputSchema}, async params => {
      try {
        const response = await callApplication(client, tool.name, params);
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
