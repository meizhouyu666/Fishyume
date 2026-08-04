import {Command, Option} from 'clipanion';
import {EngineBridge, EngineRpcError, type EngineClient} from '../bridge/engine.js';
import type {ResumeAction, RunEvent, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {exitCodeForSnapshot, TextReporter, type TextWriter, writeStatus} from '../tui/text-reporter.js';

const stops = new Set(['waiting', 'paused', 'completed']);

export async function resumeRun(client: EngineClient, runId: string, action: ResumeAction | undefined, output: TextWriter): Promise<number> {
  const reporter = new TextReporter(output); let settle!: () => void; const stopped = new Promise<void>(resolve => {settle = resolve});
  const unsubscribe = client.onRunEvent((event: RunEvent) => {if (event.runId !== runId) return; reporter.event(event); if (stops.has(event.phase)) settle()});
  try {
    const snapshot = await client.call<WorkflowSnapshot>('run.resume', {runId, ...(action ? {action} : {})});
    if (stops.has(snapshot.phase)) settle(); await stopped;
    const view = await client.call<RunStatusView>('run.status', {runId}); writeStatus(view, output);
    return view.run ? exitCodeForSnapshot(view.run) : 6;
  } catch (error) {output.write(`fail ${error instanceof Error ? error.message : String(error)}\n`); return error instanceof EngineRpcError && error.code === -32009 ? 7 : 6}
  finally {unsubscribe(); await client.close()}
}

export class ResumeCommand extends Command {
  static paths = [['resume']]; runId = Option.String({required: true}); approve = Option.String('--approve'); reject = Option.String('--reject'); retry = Option.String('--retry'); reason = Option.String('--reason'); acknowledge = Option.Boolean('--acknowledge-duplicate-risk', false);
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
