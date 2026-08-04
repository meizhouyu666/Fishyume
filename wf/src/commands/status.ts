import {Command, Option} from 'clipanion';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import type {RunStatusView} from '../bridge/types.js';
import {exitCodeForSnapshot, type TextWriter, writeStatus} from '../tui/text-reporter.js';

export async function showStatus(client: EngineClient, runId: string, json: boolean, output: TextWriter): Promise<number> {
  try {
    const view = await client.call<RunStatusView>('run.status', {runId});
    if (json) output.write(`${JSON.stringify(view)}\n`); else writeStatus(view, output);
    if (view.legacy) return view.legacyRun?.status === 'succeeded' ? 0 : 4;
    return view.run ? exitCodeForSnapshot(view.run) : 6;
  } catch (error) {output.write(`fail ${error instanceof Error ? error.message : String(error)}\n`); return 6}
  finally {await client.close()}
}

export class StatusCommand extends Command {
  static paths = [['status']]; runId = Option.String({required: true}); json = Option.Boolean('--json', false);
  async execute(): Promise<number> {return showStatus(new EngineBridge(), this.runId, this.json, this.context.stdout)}
}
