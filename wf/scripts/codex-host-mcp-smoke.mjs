#!/usr/bin/env node
import {spawn, spawnSync} from 'node:child_process';
import {copyFile, mkdir, mkdtemp, readFile, rm, stat, writeFile} from 'node:fs/promises';
import {existsSync} from 'node:fs';
import {homedir, tmpdir} from 'node:os';
import {join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const repoRoot = resolve(fileURLToPath(new URL('../..', import.meta.url)));
const wfRoot = join(repoRoot, 'wf');
const ptyHandoff = process.argv.includes('--pty-handoff');
const autoDetach = process.argv.includes('--auto-detach');
const standardPrompt = [
  'You are a deterministic Fishyume MCP acceptance harness.',
  'Use ONLY the fishyume MCP server tools. Do not use shell, filesystem, browser, or any other tools.',
  'Execute this exact sequence and wait for each response:',
  '(1) system.capabilities with project set to the current repository path;',
  '(2) workflow.validate for a workflow named real-host-approval with one approval node named approve and prompt "Approve host smoke?";',
  '(3) workflow.explain for the same workflow;',
  '(4) run.start for the same workflow, project current repository path, clientRequestId "real-host-mcp-smoke-1";',
  '(5) run.events for the returned runId until the approve node is waiting; use the stateVersion from the latest response, not an earlier response;',
  '(6) run.action approve with actionId "real-host-mcp-approve-1", the latest observed stateVersion, runId, and nodeId approve; if it returns conflict, call run.events once more and retry with that response stateVersion;',
  '(7) run.result until terminal.',
  'Do not invent tool results.',
  'At the end respond with exactly one line: HOST_MCP_SMOKE succeeded run=<runId> tools=system.capabilities,workflow.validate,workflow.explain,run.start,run.events,run.action,run.result.',
  'If any tool fails, respond with exactly one line beginning HOST_MCP_SMOKE failed and include only the tool name and error code.',
].join(' ');
const ptyPrompt = [
  'You are a deterministic Fishyume MCP plus human TUI acceptance harness.',
  'Use ONLY the fishyume MCP server tools. Do not use shell, filesystem, browser, or any other tools.',
  'Execute this exact sequence and wait for each response:',
  '(1) system.capabilities with project set to the current repository path;',
  '(2) workflow.validate for a workflow named real-host-pty with one approval node named approve and prompt "Approve PTY handoff?";',
  '(3) workflow.explain for the same workflow;',
  '(4) run.start for the same workflow, project current repository path, clientRequestId "real-host-mcp-pty-1";',
  '(5) run.events for the returned runId until the approve node is waiting; retain this waiting response stateVersion and latest sequence;',
  '(6) call run.events again after that latest sequence with waitMs 30000 so the attached human TUI can approve;',
  '(7) after the event wait returns, call run.action approve using actionId "real-host-mcp-pty-stale-1" and the RETAINED waiting stateVersion, not a newer stateVersion;',
  '(8) the run.action MUST fail with conflict because the TUI already acted;',
  '(9) after the conflict, call run.events from the newest observed sequence with waitMs 30000 until a terminal run event is observed, then call run.result;',
  '(10) if run.result still returns not_ready, call run.events again with the newest sequence and waitMs 30000, then retry run.result, for at most five result attempts.',
  'Do not invent tool results. A successful run.action means the TUI did not act and is a test failure.',
  'At the end respond with exactly one line: HOST_MCP_PTY succeeded run=<runId> conflict=host.',
  'If any requirement fails, respond with exactly one line beginning HOST_MCP_PTY failed and include only the tool name and error code.',
].join(' ');
const prompt = ptyHandoff ? ptyPrompt : standardPrompt;

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
  return JSON.stringify(value);
}

async function readProviderOverrides() {
  const codexHome = process.env.CODEX_HOME || join(homedir(), '.codex');
  let config;
  try {config = await readFile(join(codexHome, 'config.toml'), 'utf8')} catch {return {codexHome}}
  const provider = process.env.FISHYUME_CODEX_MODEL_PROVIDER || config.match(/^model_provider\s*=\s*["']([^"']+)["']/m)?.[1];
  if (!provider) return {codexHome};
  const escaped = provider.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const section = config.match(new RegExp(`\\[model_providers\\.${escaped}\\]([\\s\\S]*?)(?=\\n\\[[^\\n]+\\]|$)`))?.[1] || '';
  const baseUrl = process.env.FISHYUME_CODEX_BASE_URL || section.match(/^base_url\s*=\s*["']([^"']+)["']/m)?.[1];
  const wireApi = process.env.FISHYUME_CODEX_WIRE_API || section.match(/^wire_api\s*=\s*["']([^"']+)["']/m)?.[1];
  return {codexHome, provider, baseUrl, wireApi};
}

async function createTemporaryCodexHome(temporary, providerConfig, enginePath, stateDir) {
  const codexHome = join(temporary, 'codex-home');
  await mkdir(codexHome, {recursive: true});
  const authPath = join(providerConfig.codexHome, 'auth.json');
  await copyFile(authPath, join(codexHome, 'auth.json'));
  if (!providerConfig.provider || !providerConfig.baseUrl) {
    throw new Error('local Codex config must define model_provider and its base_url');
  }
  const mcpCommand = process.execPath;
  const mcpCli = join(wfRoot, 'dist', 'cli.js');
  const config = [
    `model_provider = ${tomlLiteral(providerConfig.provider)}`,
    '',
    `[model_providers.${providerConfig.provider}]`,
    `name = ${tomlLiteral(providerConfig.provider)}`,
    providerConfig.wireApi ? `wire_api = ${tomlLiteral(providerConfig.wireApi)}` : undefined,
    `base_url = ${tomlLiteral(providerConfig.baseUrl)}`,
    '',
    '[mcp_servers.fishyume]',
    'type = "stdio"',
    'required = true',
    'default_tools_approval_mode = "approve"',
    `command = ${tomlLiteral(mcpCommand)}`,
    `args = [${tomlLiteral(mcpCli)}, "mcp"]`,
    'startup_timeout_sec = 120',
    '',
    '[mcp_servers.fishyume.env]',
    `FISHYUME_ENGINE_PATH = ${tomlLiteral(enginePath)}`,
    `FISHYUME_STATE_DIR = ${tomlLiteral(stateDir)}`,
    `WF_STATE_DIR = ${tomlLiteral(stateDir)}`,
    '',
    '[mcp_servers.fishyume.tools."system.capabilities"]',
    'approval_mode = "approve"',
    '[mcp_servers.fishyume.tools."workflow.validate"]',
    'approval_mode = "approve"',
    '[mcp_servers.fishyume.tools."workflow.explain"]',
    'approval_mode = "approve"',
    '[mcp_servers.fishyume.tools."run.start"]',
    'approval_mode = "approve"',
    '[mcp_servers.fishyume.tools."run.events"]',
    'approval_mode = "approve"',
    '[mcp_servers.fishyume.tools."run.action"]',
    'approval_mode = "approve"',
    '[mcp_servers.fishyume.tools."run.result"]',
    'approval_mode = "approve"',
  ].filter(line => line !== undefined).join('\n') + '\n';
  await writeFile(join(codexHome, 'config.toml'), config, 'utf8');
  return codexHome;
}

function collectHostEvents(stdout) {
  const tools = [];
  const errors = [];
  const messages = [];
  const eventKinds = [];
  let staleActionConflict = false;
  let conclusion;
  let runId;
  for (const line of stdout.split(/\r?\n/)) {
    if (!line.trim()) continue;
    let event;
    try {event = JSON.parse(line)} catch {continue}
    const kind = `${event.type || 'unknown'}${event.item?.type ? `/${event.item.type}` : ''}`;
    if (!eventKinds.includes(kind)) eventKinds.push(kind);
    if (event.type === 'error' && typeof event.message === 'string') errors.push(event.message);
    if (event.type === 'item.completed' && event.item?.type === 'error' && typeof event.item.message === 'string') errors.push(event.item.message);
    const item = event.item;
    if (item?.type === 'agent_message' && typeof item.text === 'string') messages.push(item.text);
    if (item?.type === 'mcp_tool_call' || item?.type === 'function_call' || item?.type === 'custom_tool_call') {
      const name = item.name || item.tool_name || item.tool?.name || item.tool;
      if (typeof name === 'string' && !tools.includes(name)) tools.push(name);
      if (name === 'run.action' && /conflict/i.test(JSON.stringify(item))) staleActionConflict = true;
    }
    const text = (item?.type === 'message' || item?.type === 'agent_message') && Array.isArray(item.content)
      ? item.content.filter(part => part?.type === 'output_text').map(part => part.text || '').join(' ')
      : '';
    const combined = `${text} ${item?.text || ''}`;
    const success = combined.match(/HOST_MCP_(?:SMOKE|PTY) succeeded run=([^\s]+)/);
    if (success) {conclusion = 'succeeded'; runId = success[1]}
    if (combined.includes('HOST_MCP_SMOKE failed') || combined.includes('HOST_MCP_PTY failed')) conclusion = 'failed';
  }
  return {tools, conclusion, runId, errors, messages, eventKinds, staleActionConflict};
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
  let attachChild;
  let stdout = '';
  let stderr = '';
  let primaryError;
  let successSummary;
  try {
    const build = spawnSync('go', ['build', '-o', enginePath, './cmd/wf-engine'], {cwd: join(repoRoot, 'wf-engine'), encoding: 'utf8', windowsHide: true});
    if (build.status !== 0) throw new Error(`engine build failed: ${redact(build.stderr || '')}`.trim());
    const providerConfig = await readProviderOverrides();
    const temporaryCodexHome = await createTemporaryCodexHome(temporary, providerConfig, enginePath, stateDir);
    environment.CODEX_HOME = temporaryCodexHome;
    const invocation = codexInvocation([
      'exec', '--ephemeral', '--json', '--color', 'never', '--sandbox', 'read-only', '--cd', repoRoot, prompt,
    ]);
    child = spawn(invocation.command, invocation.args, {cwd: repoRoot, env: environment, stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true});
    child.stdout.on('data', chunk => {
      stdout += chunk.toString('utf8');
      if (!ptyHandoff || attachChild) return;
      const runId = stdout.match(/run-[a-z0-9]+/gi)?.at(-1);
      if (!runId) return;
      console.log(`HOST_MCP_PTY attaching run=${runId}`);
      attachChild = spawn(process.execPath, [join(wfRoot, 'dist', 'cli.js'), 'attach', runId], {
        cwd: repoRoot, env: environment, stdio: 'inherit', windowsHide: true,
      });
    });
    child.stderr.on('data', chunk => {stderr += chunk.toString('utf8')});
    const exit = await new Promise(resolveExit => {
      const timer = setTimeout(() => {child.kill(); resolveExit({code: null, signal: 'timeout'})}, 180000);
      child.once('exit', (code, signal) => {clearTimeout(timer); resolveExit({code, signal})});
      child.once('error', error => {clearTimeout(timer); resolveExit({code: null, signal: error.message})});
    });
    const observed = collectHostEvents(stdout);
    const expectedTools = ['system.capabilities', 'workflow.validate', 'workflow.explain', 'run.start', 'run.events', 'run.action', 'run.result'];
    let previousToolIndex = -1;
    const sequenceOk = expectedTools.every(name => {
      const toolIndex = observed.tools.indexOf(name, previousToolIndex + 1);
      if (toolIndex < 0) return false;
      previousToolIndex = toolIndex;
      return true;
    });
    if (exit.code !== 0 || observed.conclusion !== 'succeeded' || !sequenceOk || (ptyHandoff && !observed.staleActionConflict)) {
      const reason = exit.signal === 'timeout' ? 'timeout' : observed.conclusion === 'failed' ? 'host-agent-reported-failure' : `exit-${exit.code ?? exit.signal ?? 'unknown'}`;
      const hostError = observed.errors.at(-1) ? `; host=${redact(observed.errors.at(-1))}` : '';
      const agentMessage = observed.messages.at(-1) ? `; agent=${redact(observed.messages.at(-1)).slice(-500)}` : '';
      const conflict = ptyHandoff ? `; staleActionConflict=${observed.staleActionConflict}` : '';
      throw new Error(`real Host MCP smoke ${reason}; tools=${observed.tools.join(',') || 'none'}${hostError}${agentMessage}${conflict}; events=${observed.eventKinds.join(',')}; stderr=${redact(stderr).slice(-500)}`);
    }
    if (ptyHandoff) {
      if (!attachChild) throw new Error('real Host MCP PTY smoke did not discover a Run to attach');
      console.log(`HOST_MCP_PTY host complete run=${observed.runId}; ${autoDetach ? 'detaching' : 'press q to detach'}`);
      if (autoDetach && attachChild.exitCode === null) {
        await new Promise(resolveDetach => setTimeout(resolveDetach, 250));
        attachChild.kill();
      }
      const attachExit = await new Promise(resolveExit => {
        if (attachChild.exitCode !== null) return resolveExit({code: attachChild.exitCode, signal: null});
        const timer = setTimeout(() => {attachChild.kill(); resolveExit({code: null, signal: 'timeout'})}, 60000);
        attachChild.once('exit', (code, signal) => {clearTimeout(timer); resolveExit({code, signal})});
        attachChild.once('error', error => {clearTimeout(timer); resolveExit({code: null, signal: error.message})});
      });
      if (attachExit.code !== 0 && !(autoDetach && attachExit.signal === 'SIGTERM')) throw new Error(`real Host MCP PTY attach failed (${attachExit.signal ?? attachExit.code ?? 'unknown'})`);
    }
    successSummary = {ok: true, codexVersion, runId: observed.runId, tools: observed.tools, sandbox: 'read-only', ...(ptyHandoff ? {ptyHandoff: true, staleActionConflict: true} : {})};
  } catch (error) {
    primaryError = error;
  } finally {
    if (child && child.exitCode === null) child.kill();
    if (attachChild && attachChild.exitCode === null) attachChild.kill();
    const cleanupErrors = [];
    try {await stopControlPlane(stateDir)} catch (error) {cleanupErrors.push(error)}
    try {await rm(temporary, {recursive: true, force: true, maxRetries: 5, retryDelay: 100}); await assertRemoved(temporary)} catch (error) {cleanupErrors.push(error)}
    if (primaryError && cleanupErrors.length) throw new AggregateError([primaryError, ...cleanupErrors], 'Host MCP smoke and cleanup failed');
    if (primaryError) throw primaryError;
    if (cleanupErrors.length) throw new AggregateError(cleanupErrors, 'Host MCP smoke cleanup failed');
  }
  console.log(JSON.stringify({...successSummary, temporaryDirectoryRemoved: true}));
}

main().catch(error => {
  console.error(`Codex Host MCP smoke failed: ${redact(error.message)}`);
  process.exitCode = 1;
});
