import {randomUUID} from 'node:crypto';
import {EngineRpcError, type EngineClient} from './engine.js';
import type {AttemptSnapshot, Conclusion, NodePhase, NodeResult, NodeSnapshot, Reason, RunPhase, RunStatusView, WorkflowSnapshot} from './types.js';

export const applicationApiVersion = 'fishyume.application/v1' as const;
export type JsonScalar = string | number | boolean;
export type ApplicationMethod = 'system.capabilities' | 'workflow.validate' | 'workflow.explain' | 'run.start' | 'run.list' | 'run.get' | 'run.events' | 'run.action' | 'run.result';
export type ApplicationErrorCode = 'invalid_argument' | 'invalid_workflow' | 'not_found' | 'conflict' | 'capability_unavailable' | 'not_ready' | 'protocol_mismatch' | 'internal';
export interface ApplicationError {code: ApplicationErrorCode; message: string; data?: Record<string, unknown>}

export interface WorkflowSource {filename: string; content: string}
export interface WorkflowInput {source?: WorkflowSource; document?: Record<string, unknown>}
export interface SystemCapabilitiesRequest {project?: string}
export interface SystemCapabilitiesResponse {apiVersion: typeof applicationApiVersion; workflowSchemaVersion: string; workflowSchema: Record<string, unknown>; nodeTypes: string[]; actionTypes: ActionType[]; drivers: Array<{driver: string; targets: string[]; ready: boolean; diagnostic?: string; maxConcurrentAgents: number; supportsConcurrentCancel: boolean}>; limits: Record<string, number>; errorCodes: ApplicationErrorCode[]; minimalExample: Record<string, unknown>}
export interface WorkflowValidateRequest {project?: string; workflow: WorkflowInput; inputs?: Record<string, JsonScalar>; driver?: string; target?: string}
export interface ValidationIssue {kind: string; path: string; code: string; message: string}
export interface WorkflowValidateResponse {apiVersion: typeof applicationApiVersion; workflowSchemaVersion: string; valid: boolean; issues: ValidationIssue[]; capabilityGaps: ValidationIssue[]; warnings: string[]}
export interface WorkflowExplainResponse {apiVersion: typeof applicationApiVersion; workflowSchemaVersion: string; name: string; topologicalOrder: string[]; parallelLayers: string[][]; nodes: Array<{id: string; type: string; dependsOn: string[]; parallelLayer: number; approvalPrompt?: string; condition?: Record<string, unknown>; contextSources: string[]; agent?: {driver: string; target: string}}>; capabilityGaps: ValidationIssue[]; warnings: string[]}
export interface RunStartRequest {project: string; workflow: WorkflowInput; inputs?: Record<string, JsonScalar>; driver?: string; target?: string; clientRequestId: string}
export interface RunStartResponse {apiVersion: typeof applicationApiVersion; runId: string; stateVersion: number; attach: string}
export interface RunListRequest {filter?: {project?: string; phase?: string; conclusion?: string}; cursor?: string; limit?: number}
export interface RunSummary {runId: string; workflowName: string; project: string; driver: string; target: string; phase: RunPhase; conclusion?: Conclusion; stateVersion: number; createdAt: string; updatedAt: string}
export interface RunListResponse {apiVersion: typeof applicationApiVersion; items: RunSummary[]; nextCursor?: string}
export interface ApplicationResult {summary?: string; artifacts: string[]; warnings: string[]; checks: string[]; questions: Array<{id: string; prompt: string; choices: string[]; required: boolean}>; decision?: string; reason?: string; usage?: Record<string, number>}
export interface ApplicationNodeView {nodeId: string; type: 'agent' | 'approval'; phase: NodePhase; conclusion?: Conclusion; reason?: Reason; diagnostic?: string; currentAttempt?: number; attempt?: {number: number; phase: string; conclusion?: string; reason?: string; driver: string; target: string; contextHash?: string; startedAt: string; updatedAt: string; completedAt?: string}; result?: ApplicationResult}
export interface ApplicationRunView extends RunSummary {summary?: string; cancelRequested: boolean; effectiveConcurrency: number; topologicalOrder: string[]; nodes: ApplicationNodeView[]; deprecationWarnings: string[]}
export interface RunGetRequest {runId: string}
export interface RunGetResponse {apiVersion: typeof applicationApiVersion; run: ApplicationRunView}
export interface RunEventsRequest {runId: string; afterSequence?: number; limit?: number; waitMs?: number}
export interface ApplicationEvent {runId: string; sequence: number; type: string; phase: RunPhase; conclusion?: Conclusion; reason?: Reason; nodeId?: string; nodePhase?: NodePhase; message?: string; timestamp: string}
export interface RunEventsResponse {apiVersion: typeof applicationApiVersion; runId: string; events: ApplicationEvent[]; nextAfterSequence: number; more: boolean}
export type ActionType = 'approve' | 'reject' | 'answer' | 'retry' | 'cancel';
export interface RunActionRequest {actionId: string; runId: string; type: ActionType; expectedStateVersion: number; nodeId?: string; expectedAttempt?: number; reason?: string; answers?: Record<string, JsonScalar>; acknowledgeDuplicateRisk?: boolean}
export interface RunActionResponse {apiVersion: typeof applicationApiVersion; actionId: string; runId: string; type: ActionType; stateVersion: number; phase: RunPhase; conclusion?: Conclusion}
export interface RunResultRequest {runId: string}
export interface RunResultResponse {apiVersion: typeof applicationApiVersion; runId: string; conclusion: Conclusion; summary?: string; results: Array<{nodeId: string; conclusion?: Conclusion; result?: ApplicationResult}>; completedAt: string}

export interface ApplicationResponses {
  'system.capabilities': SystemCapabilitiesResponse; 'workflow.validate': WorkflowValidateResponse; 'workflow.explain': WorkflowExplainResponse;
  'run.start': RunStartResponse; 'run.list': RunListResponse; 'run.get': RunGetResponse; 'run.events': RunEventsResponse; 'run.action': RunActionResponse; 'run.result': RunResultResponse;
}

export class ApplicationCallError extends Error {
  constructor(readonly applicationError: ApplicationError) {super(applicationError.message); this.name = 'ApplicationCallError'}
}

export async function callApplication<M extends ApplicationMethod>(client: EngineClient, method: M, request: unknown): Promise<ApplicationResponses[M]> {
  try {return await client.call<ApplicationResponses[M]>(method, request)}
  catch (error) {
    if (error instanceof EngineRpcError && isApplicationError(error.data)) throw new ApplicationCallError(error.data);
    throw error;
  }
}

function isApplicationError(value: unknown): value is ApplicationError {
  if (!value || typeof value !== 'object') return false;
  const candidate = value as Partial<ApplicationError>;
  return typeof candidate.code === 'string' && typeof candidate.message === 'string';
}

export function newActionId(): string {return `action-${randomUUID()}`}

export function applicationRunToStatus(response: RunGetResponse): RunStatusView {
  const source = response.run;
  const nodes = Object.fromEntries(source.nodes.map(node => [node.nodeId, {id: node.nodeId, type: node.type, phase: node.phase, conclusion: node.conclusion, reason: node.reason, diagnostic: node.diagnostic, currentAttempt: node.currentAttempt}]));
  const run: WorkflowSnapshot = {
    protocolVersion: 2, stateVersion: source.stateVersion, id: source.runId, workflowName: source.workflowName, project: source.project,
    resolvedDriver: source.driver, resolvedTarget: source.target, phase: source.phase, conclusion: source.conclusion, summary: source.summary,
    effectiveConcurrency: source.effectiveConcurrency, topologicalOrder: source.topologicalOrder, nodes, cancelRequested: source.cancelRequested,
    deprecationWarnings: source.deprecationWarnings, stateDir: '', createdAt: source.createdAt, updatedAt: source.updatedAt,
  };
  const snapshots: NodeSnapshot[] = source.nodes.map(node => ({
    protocolVersion: 2, runId: source.runId, id: node.nodeId, type: node.type, phase: node.phase, conclusion: node.conclusion, reason: node.reason,
    diagnostic: node.diagnostic, currentAttempt: node.currentAttempt, result: node.result ? applicationResultToNodeResult(node.result) : undefined,
    createdAt: source.createdAt, updatedAt: source.updatedAt,
  }));
  const attempts: AttemptSnapshot[] = source.nodes.flatMap(node => node.attempt ? [{
    protocolVersion: 2 as const, runId: source.runId, nodeId: node.nodeId, number: node.attempt.number, phase: node.attempt.phase as NodePhase,
    conclusion: node.attempt.conclusion as Conclusion | undefined, reason: node.attempt.reason as Reason | undefined, resolvedDriver: node.attempt.driver, resolvedTarget: node.attempt.target,
    contextHash: node.attempt.contextHash, startedAt: node.attempt.startedAt, updatedAt: node.attempt.updatedAt, completedAt: node.attempt.completedAt,
  }] : []);
  return {
    protocolVersion: 2, legacy: false, run, nodes: snapshots,
    activeAttempts: attempts.filter(attempt => attempt.phase === 'running'), activeAttempt: attempts.find(attempt => attempt.phase === 'running'),
    waitingApprovals: Object.values(nodes).filter(node => node.type === 'approval' && node.phase === 'waiting'),
    diagnostics: source.nodes.filter(node => node.diagnostic).map(node => ({nodeId: node.nodeId, reason: node.reason, message: node.diagnostic})),
  };
}

function applicationResultToNodeResult(result: ApplicationResult): NodeResult {
  return {summary: result.summary, artifacts: result.artifacts, warnings: result.warnings, checks: result.checks, questions: result.questions, decision: result.decision as 'approved' | 'rejected' | undefined, reason: result.reason, usage: result.usage};
}
