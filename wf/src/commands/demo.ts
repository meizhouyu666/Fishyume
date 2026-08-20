import {Command, Option} from 'clipanion';
import {renderRunText} from '../tui/presentation.js';
import {actionableNodes, initialConsoleInteractionState} from '../tui/interaction.js';
import type {AttemptSnapshot, NodeSnapshot, NodeSummary, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';

function demoView(): RunStatusView {
  const createdAt = '2026-08-20T10:00:00.000Z';
  const updatedAt = '2026-08-20T10:02:18.000Z';
  const nodes: Record<string, NodeSummary> = {
    plan: {id: 'plan', type: 'agent', phase: 'completed', conclusion: 'succeeded', currentAttempt: 1, dependsOn: [], parallelLayer: 0},
    implement: {id: 'implement', type: 'agent', phase: 'running', currentAttempt: 1, dependsOn: ['plan'], parallelLayer: 1},
    verify: {id: 'verify', type: 'agent', phase: 'running', currentAttempt: 1, dependsOn: ['plan'], parallelLayer: 1},
    approve: {id: 'approve', type: 'approval', phase: 'waiting', reason: 'approval_required', diagnostic: '请检查并行节点结果后批准下一阶段。', dependsOn: ['implement', 'verify'], parallelLayer: 2},
    publish: {id: 'publish', type: 'agent', phase: 'pending', dependsOn: ['approve'], parallelLayer: 3},
  };
  const run: WorkflowSnapshot = {
    protocolVersion: 2, id: 'demo-topology-001', workflowName: 'topology-demo / 并行审批', project: process.cwd(),
    resolvedDriver: 'codex', resolvedTarget: 'local', backend: 'direct', phase: 'waiting', reason: 'approval_required',
    effectiveConcurrency: 2, topologicalOrder: Object.keys(nodes), parallelLayers: [['plan'], ['implement', 'verify'], ['approve'], ['publish']], nodes,
    cancelRequested: false, stateDir: `${process.cwd()}\\.fishyume\\demo`, createdAt, updatedAt,
  };
  const attempt = (nodeId: string): AttemptSnapshot => ({protocolVersion: 2, runId: run.id, nodeId, number: 1, phase: 'running', backend: 'direct', resolvedDriver: 'codex', resolvedTarget: 'local', launchState: 'handle_persisted', execution: {driver: 'codex', target: 'local', schemaVersion: 1, id: `demo-${nodeId}`}, startedAt: createdAt, updatedAt, activity: {schemaVersion: 'fishyume.attempt-activity/v1', summary: `${nodeId} 正在执行`, items: [{kind: 'turn', status: 'running', message: 'Node Agent 正在处理任务'}], truncated: false}});
  const snapshots: NodeSnapshot[] = Object.values(nodes).map(node => ({protocolVersion: 2, runId: run.id, ...node, createdAt, updatedAt}));
  return {protocolVersion: 2, legacy: false, run, nodes: snapshots, activeAttempts: [attempt('implement'), attempt('verify')], waitingApprovals: [nodes.approve!]};
}

export function demoText(width: number, symbolMode: 'unicode' | 'ascii' = 'unicode'): string {
  const view = demoView();
  const interaction = {...initialConsoleInteractionState, selectedIndex: 3, selectedNodeId: 'approve'};
  return renderRunText(view, width, 138_000, {
    selectedNodeId: 'approve', detailExpanded: true, symbolMode, interactive: true,
    action: {interaction, actionable: actionableNodes(view), pending: false},
  }) + '\n';
}

export class DemoCommand extends Command {
  static paths = [['demo']];
  static usage = Command.Usage({description: 'Preview the Chinese topology console without starting an Engine or calling a model.'});
  width = Option.String('--width', '120', {description: 'Terminal width to preview (80, 120, or 160)'});
  ascii = Option.Boolean('--ascii', false, {description: 'Use ASCII connectors instead of Unicode'});

  async execute(): Promise<number> {
    const width = Number(this.width);
    if (![80, 120, 160].includes(width)) {this.context.stderr.write('--width must be 80, 120, or 160\n'); return 6}
    this.context.stdout.write(demoText(width, this.ascii ? 'ascii' : 'unicode'));
    return 0;
  }
}
