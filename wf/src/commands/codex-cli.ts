import {spawnSync} from 'node:child_process';
import {existsSync, realpathSync} from 'node:fs';
import {delimiter, join, resolve} from 'node:path';

export interface CommandResult {
  status: number | null;
  stdout: string;
  stderr: string;
  error?: string;
}

export type CodexRunner = (args: string[]) => CommandResult;
export interface McpInvocation {command: string; args: string[]}

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

export function currentFishyumeMcpInvocation(entrypoint = process.argv[1]): McpInvocation {
  return {command: process.execPath, args: [resolveEntrypoint(entrypoint), 'mcp']};
}

function resolveEntrypoint(entrypoint: string | undefined): string {
  if (!entrypoint) throw new Error('Fishyume CLI entrypoint is unavailable');
  return realpathSync(resolve(entrypoint));
}

function samePath(left: string, right: string): boolean {
  const normalizedLeft = left.replaceAll('\\', '/');
  const normalizedRight = right.replaceAll('\\', '/');
  return process.platform === 'win32' ? normalizedLeft.toLowerCase() === normalizedRight.toLowerCase() : normalizedLeft === normalizedRight;
}

export function isFishyumeMcpConfiguration(text: string, expected: McpInvocation = currentFishyumeMcpInvocation()): boolean {
  try {
    const value = JSON.parse(text) as Record<string, unknown>;
    const transport = value.transport && typeof value.transport === 'object' ? value.transport as Record<string, unknown> : value;
    const command = typeof transport.command === 'string' ? transport.command : undefined;
    const args = Array.isArray(transport.args) ? transport.args : [];
    const enabled = value.enabled;
    return enabled !== false && Boolean(command && samePath(command, expected.command)) && args.length === expected.args.length && args.every((value, index) => typeof value === 'string' && (index === 0 ? samePath(value, expected.args[index]) : value === expected.args[index]));
  } catch {
    return false;
  }
}
