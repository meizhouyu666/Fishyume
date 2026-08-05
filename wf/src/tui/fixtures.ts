import type {AttemptSnapshot, NodeSnapshot, NodeSummary, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import type {ConsoleInteractionState} from './interaction.js';

const createdAt = '2026-08-06T08:00:00.000Z';
const updatedAt = '2026-08-06T08:02:18.000Z';

export interface CanonicalVisualFixture {
  id: 'concurrent' | 'approval' | 'retryable' | 'indeterminate' | 'cancelling' | 'terminal';
  title: string;
  view: RunStatusView;
  selectedNodeId: string;
  interaction?: ConsoleInteractionState;
}

function run(id: string, workflowName: string, phase: WorkflowSnapshot['phase'], nodes: Record<string, NodeSummary>, extra: Partial<WorkflowSnapshot> = {}): WorkflowSnapshot {
  return {
    protocolVersion: 2, id, workflowName, project: 'E:/团队/very-long-fishyume-project-path', backend: 'direct', phase,
    effectiveConcurrency: 3, topologicalOrder: Object.keys(nodes), nodes, cancelRequested: false,
    stateDir: `E:/团队/.fishyume/runs/${id}/state-with-a-long-reviewable-path`, createdAt, updatedAt, ...extra,
  };
}

function attempt(runId: string, nodeId: string, number: number, backend: string, id: string, extra: Partial<AttemptSnapshot> = {}): AttemptSnapshot {
  return {
    protocolVersion: 2, runId, nodeId, number, phase: 'running', backend, launchState: 'handle_persisted',
    execution: {backend, schemaVersion: 1, id}, promptHash: `${nodeId}:${number}`, startedAt: createdAt, updatedAt, ...extra,
  };
}

function snapshot(runId: string, node: NodeSummary, extra: Partial<NodeSnapshot> = {}): NodeSnapshot {
  return {protocolVersion: 2, runId, ...node, createdAt, updatedAt, ...extra};
}

const concurrentNodes: Record<string, NodeSummary> = {
  plan: {id: 'plan', type: 'agent', phase: 'completed', conclusion: 'succeeded', currentAttempt: 1},
  '实现-operator-console': {id: '实现-operator-console', type: 'agent', phase: 'running', currentAttempt: 2},
  'windows-pty-check': {id: 'windows-pty-check', type: 'agent', phase: 'running', currentAttempt: 1},
  review: {id: 'review', type: 'approval', phase: 'waiting', reason: 'approval_required', diagnostic: 'Approve the release evidence.'},
  publish: {id: 'publish', type: 'agent', phase: 'pending'},
};
const concurrentRun = run('run-concurrent-a91f', 'release-workflow / 并行验证', 'running', concurrentNodes);

const approvalNodes: Record<string, NodeSummary> = {
  plan: {id: 'plan', type: 'agent', phase: 'completed', conclusion: 'succeeded', currentAttempt: 1},
  'security-review': {id: 'security-review', type: 'approval', phase: 'waiting', reason: 'approval_required', diagnostic: 'Approve production deployment after reviewing duplicate-risk controls.'},
  deploy: {id: 'deploy', type: 'agent', phase: 'pending'},
};
const approvalRun = run('run-approval-b72c', 'production-release', 'waiting', approvalNodes, {reason: 'approval_required'});

const retryNodes: Record<string, NodeSummary> = {
  build: {id: 'build', type: 'agent', phase: 'completed', conclusion: 'succeeded', currentAttempt: 1},
  'integration-tests': {id: 'integration-tests', type: 'agent', phase: 'completed', conclusion: 'failed', reason: 'invalid_result', currentAttempt: 2, diagnostic: 'Expected junit.xml was not produced.'},
  package: {id: 'package', type: 'agent', phase: 'pending'},
};
const retryRun = run('run-retry-c83d', 'release-validation', 'waiting', retryNodes, {summary: 'Integration verification needs operator retry.'});

const indeterminateNodes: Record<string, NodeSummary> = {
  prepare: {id: 'prepare', type: 'agent', phase: 'completed', conclusion: 'succeeded', currentAttempt: 1},
  'publish-artifact': {id: 'publish-artifact', type: 'agent', phase: 'completed', conclusion: 'indeterminate', reason: 'completion_missing', currentAttempt: 3, diagnostic: 'Backend completion was not observed; external publish may already have happened.'},
  notify: {id: 'notify', type: 'agent', phase: 'pending'},
};
const indeterminateRun = run('run-unknown-d94e', 'artifact-release', 'waiting', indeterminateNodes, {summary: 'Operator acknowledgement is required before retry.'});

const cancellingNodes: Record<string, NodeSummary> = {
  prepare: {id: 'prepare', type: 'agent', phase: 'completed', conclusion: 'succeeded', currentAttempt: 1},
  'stop-local-worker': {id: 'stop-local-worker', type: 'agent', phase: 'running', currentAttempt: 2, diagnostic: 'Cancellation requested; execution confirmation pending.'},
  'stop-remote-worker': {id: 'stop-remote-worker', type: 'agent', phase: 'running', currentAttempt: 1, diagnostic: 'Remote session still reports active.'},
  finalize: {id: 'finalize', type: 'agent', phase: 'pending'},
};
const cancellingRun = run('run-stopping-e15f', 'cancel-workflow', 'cancelling', cancellingNodes, {cancelRequested: true, reason: 'user_requested', summary: 'Waiting for active execution confirmation.'});

const terminalNodes: Record<string, NodeSummary> = {
  plan: {id: 'plan', type: 'agent', phase: 'completed', conclusion: 'succeeded', currentAttempt: 1},
  verify: {id: 'verify', type: 'agent', phase: 'completed', conclusion: 'failed', currentAttempt: 2, diagnostic: 'Two checks failed.'},
  cleanup: {id: 'cleanup', type: 'agent', phase: 'completed', conclusion: 'cancelled', currentAttempt: 1},
  approval: {id: 'approval', type: 'approval', phase: 'completed', conclusion: 'rejected', reason: 'user_requested'},
};
const terminalRun = run('run-terminal-f260', 'release-workflow', 'completed', terminalNodes, {conclusion: 'failed', summary: 'Release stopped: verification failed and approval was rejected.'});

const indeterminateInteraction: ConsoleInteractionState = {
  selectedIndex: 1,
  selectedNodeId: 'publish-artifact',
  detailExpanded: true,
  helpVisible: false,
  mode: 'retry-risk-confirm',
  rejectReason: '',
  actionTarget: {nodeId: 'publish-artifact', kind: 'retry', duplicateRisk: true},
};

export const canonicalVisualFixtures: readonly CanonicalVisualFixture[] = [
  {
    id: 'concurrent', title: 'Concurrent execution', view: {protocolVersion: 2, legacy: false, run: concurrentRun,
      activeAttempts: [
        attempt(concurrentRun.id, '实现-operator-console', 2, 'direct', 'pid:4821'),
        attempt(concurrentRun.id, 'windows-pty-check', 1, 'ccpanes', 'session:pty-77'),
      ], waitingApprovals: [concurrentNodes.review!]}, selectedNodeId: '实现-operator-console',
  },
  {
    id: 'approval', title: 'Waiting approval', view: {protocolVersion: 2, legacy: false, run: approvalRun,
      nodes: [snapshot(approvalRun.id, approvalNodes['security-review']!, {result: {summary: 'Deployment awaits a human decision.'}})],
      waitingApprovals: [approvalNodes['security-review']!], diagnostics: [{nodeId: 'security-review', reason: 'approval_required', message: approvalNodes['security-review']!.diagnostic}]}, selectedNodeId: 'security-review',
  },
  {
    id: 'retryable', title: 'Failed and retryable', view: {protocolVersion: 2, legacy: false, run: retryRun,
      nodes: [snapshot(retryRun.id, retryNodes['integration-tests']!, {result: {summary: 'Test runner exited before publishing results.', warnings: ['Retry will create attempt 3.'], checks: ['typecheck passed', 'integration result missing'], artifacts: ['E:/团队/logs/integration-attempt-2.txt']}})],
      diagnostics: [{nodeId: 'integration-tests', reason: 'invalid_result', message: 'Expected junit.xml was not produced.'}]}, selectedNodeId: 'integration-tests',
  },
  {
    id: 'indeterminate', title: 'Indeterminate duplicate-risk confirmation', view: {protocolVersion: 2, legacy: false, run: indeterminateRun,
      nodes: [snapshot(indeterminateRun.id, indeterminateNodes['publish-artifact']!, {result: {summary: 'External side effect cannot be confirmed.'}})],
      diagnostics: [{nodeId: 'publish-artifact', reason: 'completion_missing', message: indeterminateNodes['publish-artifact']!.diagnostic}]}, selectedNodeId: 'publish-artifact', interaction: indeterminateInteraction,
  },
  {
    id: 'cancelling', title: 'Cancellation in progress', view: {protocolVersion: 2, legacy: false, run: cancellingRun,
      activeAttempts: [
        attempt(cancellingRun.id, 'stop-local-worker', 2, 'direct', 'pid:5100'),
        attempt(cancellingRun.id, 'stop-remote-worker', 1, 'ccpanes', 'session:remote-9', {launchState: 'session_persisted'}),
      ], diagnostics: [
        {nodeId: 'stop-local-worker', reason: 'cancel_failed', message: 'Cancellation requested; execution confirmation pending.'},
        {nodeId: 'stop-remote-worker', reason: 'cancel_failed', message: 'Remote session still reports active.'},
      ]}, selectedNodeId: 'stop-remote-worker',
  },
  {
    id: 'terminal', title: 'Terminal summary', view: {protocolVersion: 2, legacy: false, run: terminalRun,
      nodes: Object.values(terminalNodes).map(node => snapshot(terminalRun.id, node, node.id === 'verify' ? {result: {summary: 'Verification failed.', warnings: ['Do not publish this build.'], checks: ['typecheck passed', 'unit tests failed']}} : {})),
      diagnostics: [{nodeId: 'verify', reason: 'invalid_result', message: 'Two checks failed.'}]}, selectedNodeId: 'verify',
  },
] as const;

export function canonicalFixture(id: CanonicalVisualFixture['id']): CanonicalVisualFixture {
  const fixture = canonicalVisualFixtures.find(item => item.id === id);
  if (!fixture) throw new Error(`unknown canonical fixture ${id}`);
  return fixture;
}
