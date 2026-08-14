#!/usr/bin/env node
import {spawn, spawnSync} from 'node:child_process';
import {mkdtemp, readFile, rm, stat} from 'node:fs/promises';
import {existsSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const repoRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const wfRoot = join(repoRoot, 'wf');
const prompt = [
  'You are a deterministic Fishyume MCP acceptance harness.',
  'Use ONLY the fishyume MCP server tools. Do not use shell, filesystem, browser, or any other tools.',
  'Execute this exact sequence and wait for each response:',
  '(1) system.capabilities with project set to the current repository path;',
  '(2) workflow.validate for a workflow named real-host-approval with one approval node named approve and prompt "Approve host smoke?";',
  '(3) workflow.explain for the same workflow;',
  '(4) run.start for the same workflow, project current repository path, clientRequestId "real-host-mcp-smoke-1";',
  '(5) run.events for the returned runId until the approve node is waiting;',
  '(6) run.action approve with actionId "real-host-mcp-approve-1", the observed stateVersion, runId, and nodeId approve;',
  '(7) run.result until terminal.',
  'Do not invent tool results.',
  'At the end respond with exactly one line: HOST_MCP_SMOKE succeeded run=<runId> tools=system.capabilities,workflow.validate,workflow.explain,run.start,run.events,run.action,run.result.',
  'If any tool fails, respond with exactly one line beginning HOST_MCP_SMOKE failed and include only the tool name and error code.',
].join(' ');

function codexInvocation(args = []) {
  if (process.platform !== 'win32') return {command: 'codex', args};
  const npmRoot = process.env.APPDATA ? join(process.env.APPDATA, 'npm', 'node_modules') : undefined;
  const codexJs = npmRoot ? join(npmRoot, '@openai', 'codex', 'bin', 'codex.js') : '';
  if (!codexJs || !existsSync(codexJs)) throw new Error('codex-cli installation was not found');
  return {command: process.execPath, args: [codexJs, ...args]};
}

function runVersion() {
  const invocation = codexInvocation(['--version']);
  const result = spawnSync(invocation.command, invocation.args, {encoding: 'utf8', windowsHide: true});
  if (result.status !== 0) throw new Error('codex-cli is unavailable');
  return result.stdout.trim().split(/\r?\n/)[0] || 'unknown';
}

function tomlLiteral(value) {
  return `'${value.replaceAll("'", "''")}'`;
}

function collectHostEvents(stdout) {
  const tools = [];
  const errors = [];
  let conclusion;
  let runId;
  for (const line of stdout.split(/\r?\n/)) {
    if (!line.trim()) continue;
    let event;
    try {event = JSON.parse(line)} catch {continue}
    if (event.type === 'error' && typeof event.message === 'string') errors.push(event.message);
    if (event.type === 'item.completed' && event.item?.type === 'error' && typeof event.item.message === 'string') errors.push(event.item.message);
    const item = event.item;
    if (item?.type === 'mcp_tool_call') {
      const name = item.name || item.tool_name || item.tool;
      if (typeof name === 'string' && !tools.includes(name)) tools.push(name);
    }
    const text = item?.type === 'message' && Array.isArray(item.content)
      ? item.content.filter(part => part?.type === 'output_text').map(part => part.text || '').join(' ')
      : '';
    const combined = `${text} ${item?.text || ''}`;
    const success = combined.match(/HOST_MCP_SMOKE succeeded run=([^\s]+)/);
    if (success) {conclusion = 'succeeded'; runId = success[1]}
    if (combined.includes('HOST_MCP_SMOKE failed')) conclusion = 'failed';
  }
  return {tools, conclusion, runId, errors};
}

function redact(text) {
  return text
    .replace(/sk-[A-Za-z0-9_-]+/g, '[redacted-key]')
    .replace(/Bearer\s+[^\s]+/gi, 'Bearer [redacted]');
}

async function stopControlPlane(stateDir) {
  let pid;
  try {pid = JSON.parse(await readFile(join(stateDir, 'control-plane.json'), 'utf8')).pid} catch {return}
  if (!Number.isInteger(pid) || pid < 1) return;
  try {process.kill(pid, 'SIGTERM')} catch (error) {
    if (error?.code === 'ESRCH') return;
    throw error;
  }
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    try {process.kill(pid, 0)} catch (error) {
      if (error?.code === 'ESRCH') return;
      throw error;
    }
    await new Promise(resolveWait => setTimeout(resolveWait, 25));
  }
  throw new Error(`temporary Control Plane ${pid} did not exit`);
}

async function assertRemoved(path) {
  try {
    await stat(path);
    throw new Error(`temporary Host MCP directory remains: ${path}`);
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error;
  }
}

async function main() {
  if (process.env.FISHYUME_LIVE_CODEX !== '1') {
    throw new Error('real Codex Host MCP smoke is opt-in; set FISHYUME_LIVE_CODEX=1 after authenticating codex-cli');
  }
  const codexVersion = runVersion();
  const temporary = await mkdtemp(join(tmpdir(), 'fishyume-codex-host-mcp-'));
  const stateDir = join(temporary, 'state');
  const enginePath = join(temporary, process.platform === 'win32' ? 'wf-engine.exe' : 'wf-engine');
  const environment = {...process.env, FISHYUME_ENGINE_PATH: enginePath, FISHYUME_STATE_DIR: stateDir, WF_STATE_DIR: stateDir};
  let child;
  let stdout = '';
  let stderr = '';
  let primaryError;
  try {
    const build = spawnSync('go', ['build', '-o', enginePath, './cmd/wf-engine'], {cwd: join(repoRoot, 'wf-engine'), encoding: 'utf8', windowsHide: true});
    if (build.status !== 0) throw new Error(`engine build failed: ${redact(build.stderr || '')}`.trim());
    const mcpCommand = `mcp_servers.fishyume.command=${tomlLiteral(process.platform === 'win32' ? 'node.exe' : 'node')}`;
    const mcpArgs = `mcp_servers.fishyume.args=[${tomlLiteral(join(wfRoot, 'dist', 'cli.js'))},${tomlLiteral('mcp')}]`;
    const mcpEngine = `mcp_servers.fishyume.env.FISHYUME_ENGINE_PATH=${tomlLiteral(enginePath)}`;
    const mcpState = `mcp_servers.fishyume.env.FISHYUME_STATE_DIR=${tomlLiteral(stateDir)}`;
    const mcpWorkflowState = `mcp_servers.fishyume.env.WF_STATE_DIR=${tomlLiteral(stateDir)}`;
    const invocation = codexInvocation([
      'exec', '--ephemeral', '--ignore-user-config', '--json', '--color', 'never', '--sandbox', 'read-only', '--cd', repoRoot,
      '-c', mcpCommand, '-c', mcpArgs, '-c', mcpEngine, '-c', mcpState, '-c', mcpWorkflowState, prompt,
    ]);
    child = spawn(invocation.command, invocation.args, {cwd: repoRoot, env: environment, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true});
    child.stdout.on('data', chunk => {stdout += chunk.toString('utf8')});
    child.stderr.on('data', chunk => {stderr += chunk.toString('utf8')});
    const exit = await new Promise(resolveExit => {
      const timer = setTimeout(() => {child.kill(); resolveExit({code: null, signal: 'timeout'})}, 180000);
      child.once('exit', (code, signal) => {clearTimeout(timer); resolveExit({code, signal})});
      child.once('error', error => {clearTimeout(timer); resolveExit({code: null, signal: error.message})});
    });
    const observed = collectHostEvents(stdout);
    const expectedTools = ['system.capabilities', 'workflow.validate', 'workflow.explain', 'run.start', 'run.events', 'run.action', 'run.result'];
    const sequenceOk = expectedTools.every((name, index) => observed.tools[index] === name);
    if (exit.code !== 0 || observed.conclusion !== 'succeeded' || !sequenceOk) {
      const reason = exit.signal === 'timeout' ? 'timeout' : observed.conclusion === 'failed' ? 'host-agent-reported-failure' : `exit-${exit.code ?? exit.signal ?? 'unknown'}`;
      const hostError = observed.errors.at(-1) ? `; host=${redact(observed.errors.at(-1))}` : '';
      throw new Error(`real Host MCP smoke ${reason}; tools=${observed.tools.join(',') || 'none'}${hostError}; stderr=${redact(stderr).slice(-500)}`);
    }
    console.log(JSON.stringify({ok: true, codexVersion, runId: observed.runId, tools: observed.tools, sandbox: 'read-only', temporaryDirectoryRemoved: true}));
  } catch (error) {
    primaryError = error;
  } finally {
    if (child && child.exitCode === null) child.kill();
    const cleanupErrors = [];
    try {await stopControlPlane(stateDir)} catch (error) {cleanupErrors.push(error)}
    try {await rm(temporary, {recursive: true, force: true, maxRetries: 5, retryDelay: 100}); await assertRemoved(temporary)} catch (error) {cleanupErrors.push(error)}
    if (primaryError && cleanupErrors.length) throw new AggregateError([primaryError, ...cleanupErrors], 'Host MCP smoke and cleanup failed');
    if (primaryError) throw primaryError;
    if (cleanupErrors.length) throw new AggregateError(cleanupErrors, 'Host MCP smoke cleanup failed');
  }
}

main().catch(error => {
  console.error(`Codex Host MCP smoke failed: ${redact(error.message)}`);
  process.exitCode = 1;
});
