export const protocolVersion = 2 as const;

export type JsonRpcId = string | number;
export interface RpcRequest<T = unknown> {jsonrpc: '2.0'; protocolVersion: 2; id: JsonRpcId; method: string; params?: T}
export interface RpcError {code: number; message: string; data?: unknown}
export interface RpcResponse<T = unknown> {jsonrpc: '2.0'; protocolVersion: 2; id: JsonRpcId | null; result?: T; error?: RpcError}
export interface RpcNotification<T = unknown> {jsonrpc: '2.0'; protocolVersion: 2; method: 'run.event' | 'engine.log'; params: T}

export interface RunStartParams {project: string; driver?: 'codex'; target?: 'local'; backend?: string; tool?: 'codex' | 'claude' | 'opencode'; runtime?: 'local' | 'wsl' | 'ssh'; task: string}
export interface WorkflowStartParams {project: string; driver?: 'codex'; target?: 'local'; backend?: string; filename: string; content: string; inputs?: Record<string, JsonScalar>}
export interface RunStartResult {protocolVersion: 2; runId: string}
export type JsonScalar = string | number | boolean;

export type RunPhase = 'created' | 'running' | 'waiting' | 'paused' | 'cancelling' | 'completed';
export type NodePhase = 'pending' | 'ready' | 'running' | 'waiting' | 'completed' | 'skipped';
export type Conclusion = 'succeeded' | 'failed' | 'cancelled' | 'rejected' | 'indeterminate';
export type Reason = 'approval_required' | 'agent_waiting_input' | 'completion_missing' | 'invalid_result' | 'cancel_failed' | 'condition_false' | 'upstream_failed' | 'failure_policy' | 'workflow_cancelled' | 'controller_detached' | 'user_requested';

export interface NodeSummary {id: string; type: 'agent' | 'approval'; phase: NodePhase; conclusion?: Conclusion; reason?: Reason; diagnostic?: string; currentAttempt?: number}
export interface WorkflowSnapshot {
  protocolVersion: 2; stateSchemaVersion?: number; stateVersion?: number; id: string; workflowName: string; project: string;
  resolvedDriver?: string; resolvedTarget?: string; deprecationWarnings?: string[]; backend?: string;
  phase: RunPhase; conclusion?: Conclusion; reason?: Reason; summary?: string; effectiveConcurrency?: number;
  inputs?: Record<string, JsonScalar>; topologicalOrder: string[]; nodes: Record<string, NodeSummary>;
  activeNodeId?: string; cancelRequested: boolean; stateDir: string; createdAt: string; updatedAt: string;
}
export type RunSnapshot = WorkflowSnapshot;

export interface InputQuestion {id: string; prompt: string; choices?: string[]; required: boolean}
export interface NodeResult {summary?: string; artifacts?: string[]; warnings?: string[]; checks?: string[]; questions?: InputQuestion[]; usage?: {inputTokensEstimated?: number; outputTokensEstimated?: number}; decision?: 'approved' | 'rejected'; reason?: string}
export interface NodeSnapshot extends NodeSummary {protocolVersion: 2; stateSchemaVersion?: number; runId: string; result?: NodeResult; createdAt: string; updatedAt: string}
export interface ExecutionHandle {driver?: string; target?: string; backend?: string; schemaVersion: number; id: string; data?: Record<string, unknown>}
export interface AttemptSnapshot {
  protocolVersion: 2; stateSchemaVersion?: number; runId: string; nodeId: string; number: number; phase: NodePhase;
  conclusion?: Conclusion; reason?: Reason; resolvedDriver?: string; resolvedTarget?: string; backend?: string;
  launchState?: 'prepared' | 'dispatching' | 'handle_persisted' | 'finished_without_handle' | 'session_persisted' | 'finished_without_session';
  execution?: ExecutionHandle; resultConsumed?: boolean;
  contextCompilerVersion?: string; contextManifest?: {compilerVersion: string; components: Array<{name: string; source: string; version: string}>}; contextHash?: string; promptHash?: string;
  startedAt: string; updatedAt: string; completedAt?: string;
}
export interface LegacySnapshot {protocolVersion: 1; id: string; status: string; nodeStatus: string; project: string; summary?: string; stateDir: string; createdAt: string; updatedAt: string}
export interface NodeDiagnostic {nodeId: string; reason?: Reason; message?: string}
export interface RunStatusView {
  protocolVersion: 2; legacy: boolean; run?: WorkflowSnapshot; legacyRun?: LegacySnapshot; nodes?: NodeSnapshot[];
  activeAttempt?: AttemptSnapshot; activeNodes?: NodeSummary[]; activeAttempts?: AttemptSnapshot[];
  waitingApprovals?: NodeSummary[]; diagnostics?: NodeDiagnostic[];
}

export interface RunEvent {protocolVersion: 2; runId: string; sequence: number; type: string; phase: RunPhase; conclusion?: Conclusion; reason?: Reason; nodeId?: string; nodePhase?: NodePhase; message?: string; timestamp: string}

export interface ResumeAction {type: 'approve' | 'reject' | 'answer' | 'retry'; nodeId: string; expectedAttempt?: number; reason?: string; answers?: Record<string, string | number | boolean | null>; acknowledgeDuplicateRisk?: boolean}
export interface EngineHello {engineVersion: string; protocolVersion: 2; supportedMethods: string[]; supportedDrivers: string[]; supportedBackends?: string[]; backendReady: boolean; backendDiagnostic: string; projectChecked: boolean; projectReady: boolean; projectDiagnostic?: string}
