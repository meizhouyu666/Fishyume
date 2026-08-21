import process from 'node:process';
import {Command, Option} from 'clipanion';
import {ApplicationCallError, applicationRunToStatus, callApplication, type RunGetResponse} from '../bridge/application.js';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import type {RunStatusView} from '../bridge/types.js';
import {runLiveConsole} from '../tui/live-console.js';
import {exitCodeForSnapshot, type TextWriter, writeStatus} from '../tui/text-reporter.js';
import {shouldUseTUI} from './run.js';

export async function showStatus(client: EngineClient, runId: string, json: boolean, output: TextWriter): Promise<number> {
  try {
    let response: RunGetResponse | undefined;
    let view: RunStatusView;
    try {
      response = await callApplication(client, 'run.get', {runId}) as RunGetResponse;
      view = applicationRunToStatus(response);
    } catch (error) {
      if (!(error instanceof ApplicationCallError) || error.applicationError.code !== 'capability_unavailable') throw error;
      view = await client.call<RunStatusView>('run.status', {runId});
    }
    if (json) output.write(`${JSON.stringify(response ?? view)}\n`); else writeStatus(view, output);
    if (view.legacy) return view.legacyRun?.status === 'succeeded' ? 0 : 4;
    return view.run ? exitCodeForSnapshot(view.run) : 6;
  } catch (error) {output.write(`fail ${error instanceof Error ? error.message : String(error)}\n`); return 6}
  finally {await client.close()}
}

export function statusWatchError(json: boolean, isTTY: boolean | undefined, environment: NodeJS.ProcessEnv = process.env): string | undefined {
  if (json) return '--watch cannot be combined with --json; use plain status --json for one machine-readable object';
  if (!shouldUseTUI(isTTY, environment)) return '--watch requires an interactive TTY outside CI; use plain fishyume status <run-id>';
  return undefined;
}

export async function watchStatus(client: EngineClient, runId: string, output: TextWriter): Promise<number> {
  try {
    const view = await runLiveConsole(client, {runId, mode: 'watch'});
    return view.run ? exitCodeForSnapshot(view.run) : 6;
  } catch (error) {output.write(`fail ${error instanceof Error ? error.message : String(error)}\n`); return 6}
  finally {await client.close()}
}

export class StatusCommand extends Command {
  static paths = [['status']];
  static usage = Command.Usage({description: 'Read a durable Run snapshot or watch it in the human TUI.'});
  runId = Option.String({required: true, name: 'run-id'});
  json = Option.Boolean('--json', false, {description: 'Print one run.get Application response JSON object'});
  watch = Option.Boolean('--watch', false, {description: 'Attach the interactive TUI until the Run stops'});
  async execute(): Promise<number> {
    if (this.watch) {
      const error = statusWatchError(this.json, process.stdout.isTTY);
      if (error) {this.context.stderr.write(`${error}\n`); return 6}
      return watchStatus(new EngineBridge(), this.runId, this.context.stdout);
    }
    return showStatus(new EngineBridge(), this.runId, this.json, this.context.stdout);
  }
}
