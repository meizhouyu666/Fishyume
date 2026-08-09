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
  {name: 'system.capabilities', description: 'Return Fishyume Application API schemas, limits, actions, and available Driver targets.', inputSchema: {project: z.string().optional()}},
  {name: 'workflow.validate', description: 'Validate one Workflow deterministically and report static issues separately from Driver capability gaps.', inputSchema: workflowRequest},
  {name: 'workflow.explain', description: 'Explain the normalized DAG, parallel layers, context sources, and resolved Driver targets without running it.', inputSchema: workflowRequest},
  {name: 'run.start', description: 'Start one validated Workflow idempotently using a caller-owned clientRequestId.', inputSchema: {project: z.string(), workflow, inputs: z.record(z.string(), scalar).optional(), driver: z.string().optional(), target: z.string().optional(), clientRequestId: z.string()}},
  {name: 'run.list', description: 'List Runs in stable order with bounded cursor pagination.', inputSchema: {filter: z.object({project: z.string().optional(), phase: z.string().optional(), conclusion: z.string().optional()}).optional(), cursor: z.string().optional(), limit: z.number().int().optional()}},
  {name: 'run.get', description: 'Get the current durable Run, Node, Attempt, and action precondition state.', inputSchema: {runId: z.string()}},
  {name: 'run.events', description: 'Read durable Run events after a sequence with bounded pagination and short polling only.', inputSchema: {runId: z.string(), afterSequence: z.number().int().nonnegative().optional(), limit: z.number().int().optional(), waitMs: z.number().int().nonnegative().optional()}},
  {name: 'run.action', description: 'Submit an idempotent approve, reject, answer, retry, or cancel action bound to observed state.', inputSchema: {actionId: z.string(), runId: z.string(), type: z.enum(['approve', 'reject', 'answer', 'retry', 'cancel']), expectedStateVersion: z.number().int().positive(), nodeId: z.string().optional(), expectedAttempt: z.number().int().positive().optional(), reason: z.string().optional(), answers: z.record(z.string(), scalar).optional(), acknowledgeDuplicateRisk: z.boolean().optional()}},
  {name: 'run.result', description: 'Return the bounded structured terminal result, or a stable not_ready error.', inputSchema: {runId: z.string()}},
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
  let closing: Promise<void> | undefined;
  let settle!: () => void;
  const settled = new Promise<void>(resolve => {settle = resolve});
  const onEOF = (): void => {void closeAll()};
  const removeEOFListeners = (): void => {
    if (!eofSource) return;
    eofSource.off('end', onEOF);
    eofSource.off('close', onEOF);
    eofSource.off('error', onEOF);
  };
  const closeAll = async (): Promise<void> => {
    if (closing) return closing;
    closing = (async () => {
      try {await server.close()} catch { /* transport may already be closed */ }
      try {await transport.close()} catch { /* best effort during EOF */ }
      try {await client.close()} finally {removeEOFListeners(); settle()}
    })();
    return closing;
  };
  // Protocol.connect preserves an already-registered transport close hook.
  // This covers host EOF as well as an explicit transport close without
  // emitting anything on stdout.
  transport.onclose = () => {void closeAll()};
  if (eofSource) {
    eofSource.once('end', onEOF);
    eofSource.once('close', onEOF);
    eofSource.once('error', onEOF);
  }
  try {await server.connect(transport)}
  catch (error) {await closeAll(); throw error}
  await settled;
}
