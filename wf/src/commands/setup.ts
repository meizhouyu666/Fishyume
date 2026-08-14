import {Command, Option} from 'clipanion';
import {commandDiagnostic, isFishyumeMcpConfiguration, isMissingMcpServer, runCodex, type CodexRunner} from './codex-cli.js';

interface Writer {write(text: string): unknown}

export const codexSetupCommand = 'codex mcp add fishyume -- fishyume mcp';

export async function setupCodex(output: Writer, options: {printOnly?: boolean; force?: boolean; runner?: CodexRunner} = {}): Promise<number> {
  if (options.printOnly) {
    output.write(`${codexSetupCommand}\n`);
    return 0;
  }
  const runner = options.runner ?? runCodex;
  const version = runner(['--version']);
  if (version.status !== 0) {
    output.write(`fail codex CLI unavailable: ${commandDiagnostic(version)}\nRun: npm install -g @openai/codex\n`);
    return 1;
  }

  const existing = runner(['mcp', 'get', 'fishyume', '--json']);
  if (existing.status === 0 && isFishyumeMcpConfiguration(existing.stdout)) {
    output.write('ok codex-mcp Fishyume is already configured\n');
    return 0;
  }
  if (existing.status === 0 && !options.force) {
    output.write('fail codex-mcp the name fishyume is configured for a different command\nRun: fishyume setup codex --force\n');
    return 1;
  }
  if (existing.status !== 0 && !isMissingMcpServer(existing)) {
    output.write(`fail codex-mcp could not inspect existing configuration: ${commandDiagnostic(existing)}\nRun: codex mcp list\n`);
    return 1;
  }
  if (existing.status === 0) {
    const removed = runner(['mcp', 'remove', 'fishyume']);
    if (removed.status !== 0) {
      output.write(`fail codex-mcp could not replace existing configuration: ${commandDiagnostic(removed)}\nRun: codex mcp remove fishyume\n`);
      return 1;
    }
  }

  const added = runner(['mcp', 'add', 'fishyume', '--', 'fishyume', 'mcp']);
  if (added.status !== 0) {
    output.write(`fail codex-mcp setup failed: ${commandDiagnostic(added)}\nRun: ${codexSetupCommand}\n`);
    return 1;
  }
  const verified = runner(['mcp', 'get', 'fishyume', '--json']);
  if (verified.status !== 0 || !isFishyumeMcpConfiguration(verified.stdout)) {
    output.write('fail codex-mcp Codex did not retain the expected Fishyume stdio command\nRun: fishyume setup codex --force\n');
    return 1;
  }
  output.write('ok codex-mcp Fishyume is configured\nNext: restart Codex, then ask it to call Fishyume system.capabilities\n');
  return 0;
}

export class SetupCodexCommand extends Command {
  static paths = [['setup', 'codex']];
  static usage = Command.Usage({description: 'Connect Fishyume to Codex as a local stdio MCP server.'});
  printOnly = Option.Boolean('--print', false, {description: 'Print the official Codex command without changing configuration'});
  force = Option.Boolean('--force', false, {description: 'Replace a conflicting Codex MCP entry named fishyume'});

  async execute(): Promise<number> {
    return setupCodex(this.context.stdout, {printOnly: this.printOnly, force: this.force});
  }
}
