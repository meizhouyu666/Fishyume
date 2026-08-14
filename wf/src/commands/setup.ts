import {Command, Option} from 'clipanion';
import {commandDiagnostic, currentFishyumeMcpInvocation, isFishyumeMcpConfiguration, isMissingMcpServer, runCodex, type CodexRunner, type McpInvocation} from './codex-cli.js';
import {applyCodexMcpApprovalPolicy} from './codex-config.js';

interface Writer {write(text: string): unknown}

function quoteCommandArgument(value: string): string {
  return `"${value.replaceAll('"', '\\"')}"`;
}

export function codexSetupCommand(invocation: McpInvocation = currentFishyumeMcpInvocation()): string {
  return `codex mcp add fishyume -- ${[invocation.command, ...invocation.args].map(quoteCommandArgument).join(' ')}`;
}

export async function setupCodex(output: Writer, options: {printOnly?: boolean; force?: boolean; runner?: CodexRunner; invocation?: McpInvocation; policyWriter?: () => Promise<void>} = {}): Promise<number> {
  const invocation = options.invocation ?? currentFishyumeMcpInvocation();
  const copyableCommand = codexSetupCommand(invocation);
  if (options.printOnly) {
    output.write(`${copyableCommand}\n`);
    return 0;
  }
  const runner = options.runner ?? runCodex;
  const writePolicy = options.policyWriter ?? (() => applyCodexMcpApprovalPolicy());
  const version = runner(['--version']);
  if (version.status !== 0) {
    output.write(`fail codex CLI unavailable: ${commandDiagnostic(version)}\nRun: npm install -g @openai/codex\n`);
    return 1;
  }

  const existing = runner(['mcp', 'get', 'fishyume', '--json']);
  if (existing.status === 0 && isFishyumeMcpConfiguration(existing.stdout, invocation)) {
    try {await writePolicy()} catch (error) {
      output.write(`fail codex-mcp transport is configured but tool approval policy could not be applied: ${error instanceof Error ? error.message : String(error)}\n`);
      return 1;
    }
    output.write('ok codex-mcp Fishyume is already configured and approved\n');
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

  const added = runner(['mcp', 'add', 'fishyume', '--', invocation.command, ...invocation.args]);
  if (added.status !== 0) {
    output.write(`fail codex-mcp setup failed: ${commandDiagnostic(added)}\nRun: ${copyableCommand}\n`);
    return 1;
  }
  const verified = runner(['mcp', 'get', 'fishyume', '--json']);
  if (verified.status !== 0 || !isFishyumeMcpConfiguration(verified.stdout, invocation)) {
    output.write('fail codex-mcp Codex did not retain the expected Fishyume stdio command\nRun: fishyume setup codex --force\n');
    return 1;
  }
  try {await writePolicy()} catch (error) {
    output.write(`fail codex-mcp transport is configured but tool approval policy could not be applied: ${error instanceof Error ? error.message : String(error)}\n`);
    return 1;
  }
  output.write('ok codex-mcp Fishyume is configured and approved\nNext: restart Codex, then ask it to call Fishyume system.capabilities\n');
  return 0;
}

export class SetupCodexCommand extends Command {
  static paths = [['setup', 'codex']];
  static usage = Command.Usage({description: 'Connect Fishyume to Codex as a local stdio MCP server.'});
  printOnly = Option.Boolean('--print', false, {description: 'Print the low-level Codex transport command without changing configuration or approval policy'});
  force = Option.Boolean('--force', false, {description: 'Replace a conflicting Codex MCP entry named fishyume'});

  async execute(): Promise<number> {
    return setupCodex(this.context.stdout, {printOnly: this.printOnly, force: this.force});
  }
}
