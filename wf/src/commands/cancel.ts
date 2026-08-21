import {Command, Option} from 'clipanion';
import {applicationRunToStatus, callApplication, newActionId, type RunGetResponse} from '../bridge/application.js';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import type {RunEvent} from '../bridge/types.js';
import {exitCodeForSnapshot, TextReporter, type TextWriter, writeStatus} from '../tui/text-reporter.js';

export async function cancelRun(client: EngineClient, runId: string, output: TextWriter): Promise<number> {
  const reporter = new TextReporter(output); const unsubscribe = client.onRunEvent((event: RunEvent) => {if (event.runId === runId) reporter.event(event)});
  try {
    const before = applicationRunToStatus(await callApplication(client, 'run.get', {runId}) as RunGetResponse);
    if (!before.run) throw new Error(`run ${runId} is missing`);
    await callApplication(client, 'run.action', {actionId: newActionId(), runId, type: 'cancel', expectedStateVersion: before.run.stateVersion ?? 0});
    const view = applicationRunToStatus(await callApplication(client, 'run.get', {runId}) as RunGetResponse); writeStatus(view, output); return view.run ? exitCodeForSnapshot(view.run) : 6;
  } catch (error) {
    output.write(`fail ${error instanceof Error ? error.message : String(error)}\n`);
    try {const view = applicationRunToStatus(await callApplication(client, 'run.get', {runId}) as RunGetResponse); writeStatus(view, output); return view.run ? exitCodeForSnapshot(view.run) : 6} catch {return 7}
  } finally {unsubscribe(); await client.close()}
}

export class CancelCommand extends Command {
  static paths = [['cancel']];
  static usage = Command.Usage({description: 'Request cancellation of every active Attempt in a Run.'});
  runId = Option.String({required: true, name: 'run-id'});
  async execute(): Promise<number> {return cancelRun(new EngineBridge(), this.runId, this.context.stdout)}
}
