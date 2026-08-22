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
const dashboardHandoff = process.argv.includes('--dashboard-handoff');
const autoDetach = process.argv.includes('--auto-detach');
if (dashboardHandoff && !ptyHandoff) throw new Error('--dashboard-handoff requires --pty-handoff');
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
  '(5) run.events for the returned runId until the approve node is waiting, then call run.get and retain its waiting stateVersion and latest event sequence;',
  '(6) call run.events again after that latest sequence with waitMs 30000 so the attached human TUI can approve;',
  '(7) after the event wait returns, call run.action approve using actionId "real-host-mcp-pty-stale-1" and the RETAINED run.get waiting stateVersion, not a newer stateVersion;',
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
    '[mcp_servers.fishyume.tools."run.get"]',
    'approval_mode = "approve"',
    '[mcp_servers.fishyume.tools."run.action"]',
    'approval_mode = "approve"',
    '[mcp_servers.fishyume.tools."run.result"]',
    'approval_mode = "approve"',
  ].filter(line => line !== undefined).join('\n') + '\n';
  await writeFile(join(codexHome, 'config.toml'), config, 'utf8');
  return codexHome;
}

function isRecord(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function toolName(item) {
  const name = item?.name || item?.tool_name || item?.tool?.name || item?.tool;
  return typeof name === 'string' ? name : undefined;
}

function toolPayload(item) {
  const result = item?.result;
  if (!isRecord(result)) return undefined;
  const structured = result.structured_content ?? result.structuredContent;
  if (isRecord(structured)) return structured;
  if (!Array.isArray(result.content)) return undefined;
  for (const part of result.content) {
    if (!isRecord(part) || typeof part.text !== 'string') continue;
    try {
      const parsed = JSON.parse(part.text);
      if (isRecord(parsed)) return parsed;
    } catch {}
  }
  return undefined;
}

function collectHostEvents(stdout) {
  const tools = [];
  const calls = [];
  const errors = [];
  const messages = [];
  const eventKinds = [];
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
      const name = toolName(item);
      if (name && !tools.includes(name)) tools.push(name);
      if (name && event.type === 'item.completed') {
        calls.push({name, arguments: isRecord(item.arguments) ? item.arguments : {}, payload: toolPayload(item), status: item.status, error: item.error});
      }
    }
    const text = (item?.type === 'message' || item?.type === 'agent_message') && Array.isArray(item.content)
      ? item.content.filter(part => part?.type === 'output_text').map(part => part.text || '').join(' ')
      : '';
    const combined = `${text} ${item?.text || ''}`.trim();
    const success = combined.match(/^HOST_MCP_(?:SMOKE|PTY) succeeded run=([^\s]+)(?: tools=[^\r\n]+| conflict=host)?\.?$/);
    if (success) {conclusion = 'succeeded'; runId = success[1]}
    if (/^HOST_MCP_(?:SMOKE|PTY) failed\b/.test(combined)) conclusion = 'failed';
  }
  return {tools, calls, conclusion, runId, errors, messages, eventKinds};
}

function hasCompletedAgentMarker(stdout, marker) {
  for (const line of stdout.split(/\r?\n/)) {
    if (!line.trim()) continue;
    let event;
    try {event = JSON.parse(line)} catch {continue}
    if (event.type === 'item.completed' && event.item?.type === 'agent_message' && typeof event.item.text === 'string' && marker.test(event.item.text.trim())) return true;
  }
  return false;
}

function requireEvidence(condition, message) {
  if (!condition) throw new Error(`Host MCP evidence invalid: ${message}`);
}

function validateToolOrder(calls, requiredNames) {
  let previous = -1;
  for (const name of requiredNames) {
    const next = calls.findIndex((call, index) => index > previous && call.name === name);
    requireEvidence(next >= 0, `missing ordered completed tool ${name}`);
    previous = next;
  }
}

function validateHostEvidence(observed, requirePty) {
  const calls = observed.calls;
  validateToolOrder(calls, ['system.capabilities', 'workflow.validate', 'workflow.explain', 'run.start', 'run.events', 'run.action', 'run.result']);
  const startIndex = calls.findIndex(call => call.name === 'run.start' && typeof call.payload?.runId === 'string' && Number.isInteger(call.payload?.stateVersion));
  requireEvidence(startIndex >= 0, 'run.start completed payload is missing runId/stateVersion');
  const start = calls[startIndex];
  const runId = start.payload.runId;
  requireEvidence(observed.runId === runId, 'Host final runId does not match run.start');

  const resultIndex = calls.findIndex((call, index) => index > startIndex && call.name === 'run.result' && call.arguments.runId === runId && call.payload?.runId === runId);
  requireEvidence(resultIndex >= 0, 'run.result completed payload does not match run.start');
  const result = calls[resultIndex];
  const terminalConclusions = new Set(['succeeded', 'failed', 'rejected', 'cancelled', 'indeterminate']);
  requireEvidence(result.status === 'completed' && terminalConclusions.has(result.payload?.conclusion) && typeof result.payload?.completedAt === 'string', 'run.result is not a terminal completed result');

  if (!requirePty) {
    const appliedAction = calls.find((call, index) => index > startIndex && index < resultIndex && call.name === 'run.action' && call.arguments.runId === runId && call.payload?.runId === runId && call.status === 'completed');
    requireEvidence(Boolean(appliedAction), 'standard run.action did not complete for the started Run');
    return {runId, resultConclusion: result.payload.conclusion};
  }

  const actionIndex = calls.findIndex((call, index) => index > startIndex && index < resultIndex && call.name === 'run.action' && call.arguments.actionId === 'real-host-mcp-pty-stale-1');
  requireEvidence(actionIndex >= 0, 'stale run.action call is missing');
  const action = calls[actionIndex];
  const waitingGetIndex = calls.findIndex((call, index) => index > startIndex && index < actionIndex && call.name === 'run.get' && call.arguments.runId === runId && call.payload?.run?.runId === runId && call.payload?.run?.phase === 'waiting');
  requireEvidence(waitingGetIndex >= 0, 'waiting run.get evidence is missing');
  const waitingGet = calls[waitingGetIndex];
  const retainedStateVersion = waitingGet.payload.run.stateVersion;
  requireEvidence(Number.isInteger(retainedStateVersion) && action.arguments.expectedStateVersion === retainedStateVersion, 'run.action did not retain the waiting stateVersion');
  requireEvidence(action.arguments.runId === runId && action.arguments.nodeId === 'approve' && action.arguments.type === 'approve', 'run.action target does not match the attached Approval');
  requireEvidence(action.status === 'failed' && action.payload?.error?.code === 'conflict', 'run.action completed payload is not exact conflict');
  requireEvidence(action.payload.error.data?.expectedStateVersion === retainedStateVersion, 'conflict payload expectedStateVersion does not match the retained value');
  requireEvidence(Number.isInteger(action.payload.error.data?.currentStateVersion) && action.payload.error.data.currentStateVersion > retainedStateVersion, 'conflict payload does not prove a newer state');
  const waitingEvent = calls.slice(startIndex + 1, waitingGetIndex + 1).some(call => call.name === 'run.events' && call.arguments.runId === runId && call.payload?.runId === runId && Array.isArray(call.payload?.events) && call.payload.events.some(event => event?.runId === runId && event?.nodeId === 'approve' && event?.nodePhase === 'waiting'));
  requireEvidence(waitingEvent, 'run.events did not observe the Approval waiting');
  const handoffWait = calls.slice(waitingGetIndex + 1, actionIndex).some(call => call.name === 'run.events' && call.arguments.runId === runId && call.arguments.waitMs === 30000 && call.payload?.runId === runId && Array.isArray(call.payload?.events));
  requireEvidence(handoffWait, 'the bounded post-handoff run.events call is missing');
  return {runId, retainedStateVersion, currentStateVersion: action.payload.error.data.currentStateVersion, conflictCode: 'conflict', resultConclusion: result.payload.conclusion};
}

function redact(text) {
  return text
    .replace(/sk-[A-Za-z0-9_-]+/g, '[redacted-key]')
    .replace(/Bearer\s+[^\s]+/gi, 'Bearer [redacted]');
}

function childExit(child) {
  if (!child || (child.exitCode === null && child.signalCode === null)) return undefined;
  return {code: child.exitCode, signal: child.signalCode};
}

function waitForChildExit(child, timeoutMs) {
  const exited = childExit(child);
  if (exited) return Promise.resolve(exited);
  return new Promise(resolveExit => {
    const cleanup = () => {
      clearTimeout(timer);
      child.off('exit', onExit);
      child.off('error', onError);
    };
    const onExit = (code, signal) => {cleanup(); resolveExit({code, signal})};
    const onError = error => {cleanup(); resolveExit({code: null, signal: error.message})};
    const timer = setTimeout(() => {cleanup(); resolveExit(undefined)}, timeoutMs);
    child.once('exit', onExit);
    child.once('error', onError);
  });
}

async function terminateAndWait(child, label) {
  const exited = childExit(child);
  if (exited) return exited;
  if (!child?.pid) throw new Error(`${label} has no process id`);
  if (process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T'], {stdio: 'ignore', windowsHide: true});
  } else {
    try {process.kill(-child.pid, 'SIGTERM')} catch (error) {
      if (error?.code !== 'ESRCH') throw error;
      try {child.kill('SIGTERM')} catch (fallbackError) {if (fallbackError?.code !== 'ESRCH') throw fallbackError}
    }
  }
  const graceful = await waitForChildExit(child, 2000);
  if (graceful) return graceful;
  if (process.platform === 'win32') {
    spawnSync('taskkill', ['/PID', String(child.pid), '/T', '/F'], {stdio: 'ignore', windowsHide: true});
  } else {
    try {process.kill(-child.pid, 'SIGKILL')} catch (error) {
      if (error?.code !== 'ESRCH') throw error;
      try {child.kill('SIGKILL')} catch (fallbackError) {if (fallbackError?.code !== 'ESRCH') throw fallbackError}
    }
  }
  const forced = await waitForChildExit(child, 3000);
  if (!forced) throw new Error(`${label} process tree did not exit`);
  return forced;
}

async function waitOrTerminate(child, timeoutMs, label) {
  const exit = await waitForChildExit(child, timeoutMs);
  if (exit) return exit;
  await terminateAndWait(child, label);
  return {code: null, signal: 'timeout'};
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
  let hostMarkerResolve;
  const hostMarker = new Promise(resolveMarker => {hostMarkerResolve = resolveMarker});
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
    child = spawn(invocation.command, invocation.args, {cwd: repoRoot, env: environment, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true, detached: process.platform !== 'win32'});
    // An explicit EOF prevents Codex from waiting for an unintended stdin prompt on Windows.
    child.stdin.end();
    child.stdout.on('data', chunk => {
      stdout += chunk.toString('utf8');
      const completedMarker = ptyHandoff
        ? /^(?:HOST_MCP_PTY succeeded run=[^\s]+ conflict=host\.?|HOST_MCP_PTY failed\b.*)$/
        : /^(?:HOST_MCP_SMOKE succeeded run=[^\s]+ tools=system\.capabilities,workflow\.validate,workflow\.explain,run\.start,run\.events,run\.action,run\.result\.?|HOST_MCP_SMOKE failed\b.*)$/;
      if (hostMarkerResolve && hasCompletedAgentMarker(stdout, completedMarker)) {
        const resolveMarker = hostMarkerResolve;
        hostMarkerResolve = undefined;
        resolveMarker();
      }
      if (!ptyHandoff || attachChild) return;
      const runId = stdout.match(/run-[a-z0-9]+/gi)?.at(-1);
      if (!runId) return;
      console.log(`HOST_MCP_PTY ${dashboardHandoff ? 'opening dashboard' : 'attaching'} run=${runId}`);
      attachChild = spawn(process.execPath, [join(wfRoot, 'dist', 'cli.js'), ...(dashboardHandoff ? [] : ['attach', runId])], {
        cwd: repoRoot, env: environment, stdio: 'inherit', windowsHide: true, detached: process.platform !== 'win32',
      });
    });
    child.stderr.on('data', chunk => {stderr += chunk.toString('utf8')});
    const outcome = await Promise.race([
      waitOrTerminate(child, 180000, 'Codex Host').then(exit => ({exit, completedByMarker: false})),
      hostMarker.then(async () => {
        await new Promise(resolveFlush => setTimeout(resolveFlush, 250));
        return {exit: await terminateAndWait(child, 'Codex Host completion'), completedByMarker: true};
      }),
    ]);
    const {exit, completedByMarker} = outcome;
    const observed = collectHostEvents(stdout);
    let evidence;
    let evidenceError;
    try {evidence = validateHostEvidence(observed, ptyHandoff)} catch (error) {evidenceError = error}
    if ((!completedByMarker && exit.code !== 0) || observed.conclusion !== 'succeeded' || evidenceError) {
      const reason = exit.signal === 'timeout' ? 'timeout' : observed.conclusion === 'failed' ? 'host-agent-reported-failure' : `exit-${exit.code ?? exit.signal ?? 'unknown'}`;
      const hostError = observed.errors.at(-1) ? `; host=${redact(observed.errors.at(-1))}` : '';
      const agentMessage = observed.messages.at(-1) ? `; agent=${redact(observed.messages.at(-1)).slice(-500)}` : '';
      const contract = evidenceError ? `; contract=${redact(evidenceError.message)}` : '';
      throw new Error(`real Host MCP smoke ${reason}; tools=${observed.tools.join(',') || 'none'}${hostError}${agentMessage}${contract}; events=${observed.eventKinds.join(',')}; stderr=${redact(stderr).slice(-500)}`);
    }
    if (ptyHandoff) {
      if (!attachChild) throw new Error('real Host MCP PTY smoke did not discover a Run to attach');
      console.log(`HOST_MCP_PTY host complete run=${evidence.runId}; ${autoDetach ? 'detaching' : 'press q to detach'}`);
      let attachExit;
      if (autoDetach) {
        await new Promise(resolveDetach => setTimeout(resolveDetach, 250));
        attachExit = await terminateAndWait(attachChild, 'Fishyume attach');
      } else {
        attachExit = await waitOrTerminate(attachChild, 60000, 'Fishyume attach');
        if (attachExit.code !== 0) throw new Error(`real Host MCP PTY attach failed (${attachExit.signal ?? attachExit.code ?? 'unknown'})`);
      }
    }
    successSummary = {ok: true, codexVersion, runId: evidence.runId, tools: observed.tools, sandbox: 'read-only', resultConclusion: evidence.resultConclusion, ...(ptyHandoff ? {ptyHandoff: true, dashboardHandoff, staleActionConflict: true, conflictCode: evidence.conflictCode, retainedStateVersion: evidence.retainedStateVersion, currentStateVersion: evidence.currentStateVersion} : {})};
  } catch (error) {
    primaryError = error;
  } finally {
    const cleanupErrors = [];
    try {if (attachChild) await terminateAndWait(attachChild, 'Fishyume attach')} catch (error) {cleanupErrors.push(error)}
    try {if (child) await terminateAndWait(child, 'Codex Host')} catch (error) {cleanupErrors.push(error)}
    try {await stopControlPlane(stateDir)} catch (error) {cleanupErrors.push(error)}
    try {await rm(temporary, {recursive: true, force: true, maxRetries: 5, retryDelay: 100}); await assertRemoved(temporary)} catch (error) {cleanupErrors.push(error)}
    if (primaryError && cleanupErrors.length) throw new AggregateError([primaryError, ...cleanupErrors], 'Host MCP smoke and cleanup failed');
    if (primaryError) throw primaryError;
    if (cleanupErrors.length) throw new AggregateError(cleanupErrors, 'Host MCP smoke cleanup failed');
  }
  console.log(JSON.stringify({...successSummary, temporaryDirectoryRemoved: true}));
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch(error => {
    console.error(`Codex Host MCP smoke failed: ${redact(error.message)}`);
    process.exitCode = 1;
  });
}

export {collectHostEvents, hasCompletedAgentMarker, terminateAndWait, validateHostEvidence};
