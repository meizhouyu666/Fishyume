import process from 'node:process';
import {Command, Option} from 'clipanion';
import {EngineBridge, type EngineClient} from '../bridge/engine.js';
import {callApplication, type RunListResponse} from '../bridge/application.js';
import {runDashboard, dashboardText} from '../tui/dashboard.js';
import {runLiveConsole} from '../tui/live-console.js';
import {shouldUseTUI} from './run.js';

export async function showDashboard(client: EngineClient, interactive: boolean, output: {write(text: string): unknown}, limit = 50): Promise<number> {
  try {
    if (!interactive) {
      const response = await callApplication(client, 'run.list', {limit}) as RunListResponse;
      output.write(dashboardText(response.items, process.stdout.columns ?? 120));
      return 0;
    }
    const selection = await runDashboard(client, limit);
    if (selection.kind === 'exit') return 0;
    await runLiveConsole(client, {runId: selection.runId, mode: 'watch'});
    return 0;
  } catch (error) {
    output.write(`Fishyume Dashboard could not open: ${error instanceof Error ? error.message : String(error)}\nRun: fishyume doctor\n`);
    return 1;
  } finally {await client.close()}
}

export class DashboardCommand extends Command {
  static paths = [['dashboard']];
  static usage = Command.Usage({description: 'Open the Fishyume Operator Dashboard and attach to a durable Run.'});
  limit = Option.String('--limit', '50', {description: 'Maximum durable Runs to show (1-100)'});
  async execute(): Promise<number> {
    const limit = Number(this.limit);
    if (!Number.isInteger(limit) || limit < 1 || limit > 100) {this.context.stderr.write('--limit must be an integer from 1 to 100\n'); return 6}
    return showDashboard(new EngineBridge(), shouldUseTUI(process.stdout.isTTY), this.context.stdout, limit);
  }
}
