import {Command, Option} from 'clipanion';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import type {RunEvent, RunStatusView, WorkflowSnapshot} from '../bridge/types.js';
import {exitCodeForSnapshot, TextReporter, type TextWriter, writeStatus} from '../tui/text-reporter.js';

export async function cancelRun(client: EngineClient, runId: string, output: TextWriter): Promise<number> {
  const reporter = new TextReporter(output); const unsubscribe = client.onRunEvent((event: RunEvent) => {if (event.runId === runId) reporter.event(event)});
  try {
    const before = await client.call<RunStatusView>('run.status', {runId});
    const snapshot = await client.call<WorkflowSnapshot>('run.cancel', {runId, expectedStateVersion: before.run?.stateVersion}); const view = await client.call<RunStatusView>('run.status', {runId}); writeStatus(view, output); return exitCodeForSnapshot(view.run ?? snapshot);
  } catch (error) {
    output.write(`fail ${error instanceof Error ? error.message : String(error)}\n`);
    try {const view = await client.call<RunStatusView>('run.status', {runId}); writeStatus(view, output); return view.run ? exitCodeForSnapshot(view.run) : 6} catch {return 7}
  } finally {unsubscribe(); await client.close()}
}

export class CancelCommand extends Command {
  static paths = [['cancel']]; runId = Option.String({required: true});
  async execute(): Promise<number> {return cancelRun(new EngineBridge(), this.runId, this.context.stdout)}
}
