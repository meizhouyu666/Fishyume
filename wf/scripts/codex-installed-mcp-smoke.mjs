#!/usr/bin/env node
import {spawn} from 'node:child_process';
import {existsSync} from 'node:fs';
import {join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import {collectHostEvents, terminateAndWait} from './codex-host-mcp-smoke.mjs';

const repoRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)));

function codexInvocation(args) {
  if (process.platform !== 'win32') return {command: 'codex', args};
  const codexJs = process.env.APPDATA ? join(process.env.APPDATA, 'npm', 'node_modules', '@openai', 'codex', 'bin', 'codex.js') : '';
  if (!codexJs || !existsSync(codexJs)) throw new Error('codex-cli installation was not found');
  return {command: process.execPath, args: [codexJs, ...args]};
}

function redact(text) {
  return text.replace(/sk-[A-Za-z0-9_-]+/g, '[redacted-key]').replace(/Bearer\s+[^\s]+/gi, 'Bearer [redacted]');
}

async function waitForExit(child, timeoutMs) {
  return new Promise(resolveExit => {
    const timer = setTimeout(() => resolveExit(undefined), timeoutMs);
    child.once('exit', (code, signal) => {clearTimeout(timer); resolveExit({code, signal})});
    child.once('error', error => {clearTimeout(timer); resolveExit({code: null, signal: error.message})});
  });
}

async function main() {
  if (process.env.FISHYUME_LIVE_CODEX !== '1') throw new Error('installed MCP smoke is opt-in; set FISHYUME_LIVE_CODEX=1');
  const prompt = [
    'Use only the configured fishyume MCP server.',
    `Call system.capabilities exactly once with project set to ${JSON.stringify(repoRoot)}.`,
    'Do not call shell, filesystem, browser, or any other tool.',
    'Do not read stdin and do not ask for additional input.',
    'After the completed tool response, reply with exactly INSTALLED_MCP_SMOKE succeeded.',
    'If the tool is unavailable, requires interactive approval, or fails, reply with exactly INSTALLED_MCP_SMOKE failed.',
  ].join(' ');
  const invocation = codexInvocation(['exec', '--ephemeral', '--json', '--color', 'never', '--sandbox', 'read-only', '--cd', repoRoot, prompt]);
  const child = spawn(invocation.command, invocation.args, {cwd: repoRoot, stdio: ['inherit', 'pipe', 'pipe'], windowsHide: true, detached: process.platform !== 'win32'});
  let stdout = '';
  let stderr = '';
  child.stdout.on('data', chunk => {stdout += chunk.toString('utf8')});
  child.stderr.on('data', chunk => {stderr += chunk.toString('utf8')});
  let exit = await waitForExit(child, 120_000);
  if (!exit) {
    await terminateAndWait(child, 'installed Codex MCP smoke');
    throw new Error('installed Codex MCP smoke timed out');
  }
  const observed = collectHostEvents(stdout);
  const calls = observed.calls.filter(call => call.name === 'system.capabilities');
  const valid = exit.code === 0 && observed.errors.length === 0 && observed.tools.length === 1 && observed.tools[0] === 'system.capabilities' && calls.length === 1 && calls[0].status === 'completed' && calls[0].payload?.apiVersion === 'fishyume.application/v1';
  if (!valid) {
    const error = observed.errors.at(-1) || stderr || observed.messages.at(-1) || `exit ${exit.code ?? exit.signal ?? 'unknown'}`;
    throw new Error(`installed MCP contract failed; exit=${exit.code ?? exit.signal ?? 'unknown'}; tools=${observed.tools.join(',') || 'none'}; events=${observed.eventKinds.join(',') || 'none'}; diagnostic=${redact(error).slice(-400)}`);
  }
  console.log(JSON.stringify({ok: true, codexMcp: 'fishyume', tools: observed.tools, interactiveApproval: false, sandbox: 'read-only'}));
}

main().catch(error => {
  console.error(`Installed Codex MCP smoke failed: ${redact(error.message)}`);
  process.exitCode = 1;
});
