import {Command, Option} from 'clipanion';
import {ApplicationCallError, applicationRunToStatus, callApplication, newActionId, type RunGetResponse} from '../bridge/application.js';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import type {ResumeAction, RunEvent} from '../bridge/types.js';
import {exitCodeForSnapshot, TextReporter, type TextWriter, writeStatus} from '../tui/text-reporter.js';

const stops = new Set(['waiting', 'paused', 'completed']);

export async function resumeRun(client: EngineClient, runId: string, action: ResumeAction | undefined, output: TextWriter): Promise<number> {
  const reporter = new TextReporter(output); let settle!: () => void; const stopped = new Promise<void>(resolve => {settle = resolve});
  const unsubscribe = client.onRunEvent((event: RunEvent) => {if (event.runId !== runId) return; reporter.event(event); if (stops.has(event.phase)) settle()});
  try {
    const before = applicationRunToStatus(await callApplication(client, 'run.get', {runId}) as RunGetResponse);
    if (!before.run) throw new Error(`run ${runId} is missing`);
    if (!action) {writeStatus(before, output); return exitCodeForSnapshot(before.run)}
    const node = before.run.nodes[action.nodeId];
    const response = await callApplication(client, 'run.action', {actionId: newActionId(), runId, type: action.type, expectedStateVersion: before.run.stateVersion ?? 0, nodeId: action.nodeId, ...((action.type === 'retry' || action.type === 'answer') && node?.currentAttempt !== undefined ? {expectedAttempt: node.currentAttempt} : {}), ...(action.reason === undefined ? {} : {reason: action.reason}), ...(action.answers === undefined ? {} : {answers: action.answers}), ...(action.acknowledgeDuplicateRisk === undefined ? {} : {acknowledgeDuplicateRisk: action.acknowledgeDuplicateRisk})});
    if (stops.has(response.phase)) settle(); await stopped;
    const view = applicationRunToStatus(await callApplication(client, 'run.get', {runId}) as RunGetResponse); writeStatus(view, output);
    return view.run ? exitCodeForSnapshot(view.run) : 6;
  } catch (error) {output.write(`fail ${error instanceof Error ? error.message : String(error)}\n`); return error instanceof ApplicationCallError && error.applicationError.code === 'conflict' ? 7 : 6}
  finally {unsubscribe(); await client.close()}
}

export class ResumeCommand extends Command {
  static paths = [['resume']];
  static usage = Command.Usage({description: 'Continue a waiting Run or submit one approval, rejection, or retry action.'});
  runId = Option.String({required: true, name: 'run-id'});
  approve = Option.String('--approve', {description: 'Approve the specified Approval node'});
  reject = Option.String('--reject', {description: 'Reject the specified Approval node'});
  retry = Option.String('--retry', {description: 'Retry the specified Agent node'});
  reason = Option.String('--reason', {description: 'Reason for --reject'});
  acknowledge = Option.Boolean('--acknowledge-duplicate-risk', false, {description: 'Acknowledge duplicate side-effect risk for retry'});
  async execute(): Promise<number> {
    const selected = [this.approve, this.reject, this.retry].filter(Boolean); if (selected.length > 1) {this.context.stderr.write('choose only one of --approve, --reject, or --retry\n'); return 6}
    if (this.reason && !this.reject) {this.context.stderr.write('--reason requires --reject\n'); return 6}
    if (this.acknowledge && !this.retry) {this.context.stderr.write('--acknowledge-duplicate-risk requires --retry\n'); return 6}
    let action: ResumeAction | undefined;
    if (this.approve) action = {type: 'approve', nodeId: this.approve};
    if (this.reject) action = {type: 'reject', nodeId: this.reject, ...(this.reason ? {reason: this.reason} : {})};
    if (this.retry) action = {type: 'retry', nodeId: this.retry, acknowledgeDuplicateRisk: this.acknowledge};
    return resumeRun(new EngineBridge(), this.runId, action, this.context.stdout);
  }
}
