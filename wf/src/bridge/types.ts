export const protocolVersion = 1 as const;

export type JsonRpcId = string | number;

export interface RpcRequest<T = unknown> {
  jsonrpc: '2.0';
  protocolVersion: 1;
  id: JsonRpcId;
  method: string;
  params?: T;
}

export interface RpcError {
  code: number;
  message: string;
  data?: unknown;
}

export interface RpcResponse<T = unknown> {
  jsonrpc: '2.0';
  protocolVersion: 1;
  id: JsonRpcId | null;
  result?: T;
  error?: RpcError;
}

export interface RpcNotification<T = unknown> {
  jsonrpc: '2.0';
  protocolVersion: 1;
  method: 'run.event' | 'engine.log';
  params: T;
}

export interface RunStartParams {
  project: string;
  tool?: 'codex' | 'claude' | 'opencode';
  runtime?: 'local' | 'wsl' | 'ssh';
  task: string;
}

export interface RunStartResult {
  protocolVersion: 1;
  runId: string;
}

export type RunStatus =
  | 'created'
  | 'dispatching'
  | 'running'
  | 'succeeded'
  | 'failed'
  | 'blocked'
  | 'indeterminate'
  | 'paused'
  | 'cancelled';

export type NodeStatus = RunStatus;

export interface RunSnapshot {
  protocolVersion: 1;
  id: string;
  status: RunStatus;
  nodeStatus: NodeStatus;
  project: string;
  tool: string;
  runtime: string;
  backend: string;
  summary?: string;
  stateDir: string;
  createdAt: string;
  updatedAt: string;
}

export interface RunEvent {
  protocolVersion: 1;
  runId: string;
  sequence: number;
  type: string;
  status: RunStatus;
  nodeStatus: NodeStatus;
  message?: string;
  timestamp: string;
}

export interface EngineHello {
  engineVersion: string;
  protocolVersion: 1;
  supportedMethods: string[];
  supportedBackends: string[];
  backendReady: boolean;
  backendDiagnostic: string;
  projectChecked: boolean;
  projectReady: boolean;
  projectDiagnostic?: string;
}
