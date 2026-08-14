import {spawnSync} from 'node:child_process';
import {existsSync} from 'node:fs';
import {delimiter, join} from 'node:path';

export interface CommandResult {
  status: number | null;
  stdout: string;
  stderr: string;
  error?: string;
}

export type CodexRunner = (args: string[]) => CommandResult;

interface CodexInvocation {command: string; prefix: string[]}

function resolveCodexInvocation(environment: NodeJS.ProcessEnv = process.env): CodexInvocation {
  const override = environment.FISHYUME_CODEX_PATH?.trim();
  if (override && existsSync(override) && !/\.(?:cmd|ps1)$/i.test(override)) return {command: override, prefix: []};
  if (process.platform !== 'win32') return {command: 'codex', prefix: []};

  const pathValue = environment.PATH ?? environment.Path ?? '';
  for (const directory of pathValue.split(delimiter).filter(Boolean)) {
    const executable = join(directory, 'codex.exe');
    if (existsSync(executable)) return {command: executable, prefix: []};
    const javascript = join(directory, 'node_modules', '@openai', 'codex', 'bin', 'codex.js');
    if (existsSync(javascript)) return {command: process.execPath, prefix: [javascript]};
  }
  return {command: 'codex', prefix: []};
}

export const runCodex: CodexRunner = args => {
  const invocation = resolveCodexInvocation();
  const result = spawnSync(invocation.command, [...invocation.prefix, ...args], {
    encoding: 'utf8',
    windowsHide: true,
    timeout: 30_000,
  });
  return {
    status: result.status,
    stdout: result.stdout ?? '',
    stderr: result.stderr ?? '',
    ...(result.error ? {error: result.error.message} : {}),
  };
};

export function commandDiagnostic(result: CommandResult): string {
  if (result.error) return result.error.replace(/\s+/g, ' ').slice(0, 240);
  const line = (result.stderr || result.stdout).split(/\r?\n/).map(value => value.trim()).find(Boolean);
  return (line || `exit ${result.status ?? 'unknown'}`).slice(0, 240);
}

export function isMissingMcpServer(result: CommandResult): boolean {
  return result.status !== 0 && /no mcp server named|not found/i.test(`${result.stderr}\n${result.stdout}`);
}

export function isFishyumeMcpConfiguration(text: string): boolean {
  try {
    const value = JSON.parse(text) as Record<string, unknown>;
    const transport = value.transport && typeof value.transport === 'object' ? value.transport as Record<string, unknown> : value;
    const command = typeof transport.command === 'string' ? transport.command : undefined;
    const args = Array.isArray(transport.args) ? transport.args : [];
    const enabled = value.enabled;
    return enabled !== false && Boolean(command && /(?:^|[\\/])fishyume(?:\.cmd|\.exe)?$/i.test(command)) && args.length === 1 && args[0] === 'mcp';
  } catch {
    return false;
  }
}
