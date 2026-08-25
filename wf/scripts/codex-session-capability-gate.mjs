#!/usr/bin/env node
import {spawn} from 'node:child_process';
import {createHash, randomUUID} from 'node:crypto';
import {EventEmitter} from 'node:events';
import {existsSync} from 'node:fs';
import {mkdtemp, readFile, readdir, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {basename, dirname, join, resolve} from 'node:path';
import {fileURLToPath} from 'node:url';

const DEFAULT_MODEL = 'gpt-5.6-luna';
const DEFAULT_TIMEOUT_MS = 180_000;
const MAX_DIAGNOSTIC_BYTES = 16 * 1024;

function sha256(value) {
  return createHash('sha256').update(value).digest('hex');
}

function samePath(left, right) {
  const normalize = (value) => {
    const normalized = resolve(value).replaceAll('\\', '/');
    return process.platform === 'win32' ? normalized.toLowerCase() : normalized;
  };
  return normalize(left) === normalize(right);
}

function redact(value) {
  return String(value)
    .replace(/sk-[A-Za-z0-9_-]+/g, '[redacted-key]')
    .replace(/Bearer\s+[^\s]+/gi, 'Bearer [redacted]');
}

function bounded(value) {
  const text = redact(value);
  return Buffer.byteLength(text) <= MAX_DIAGNOSTIC_BYTES
    ? text
    : Buffer.from(text).subarray(0, MAX_DIAGNOSTIC_BYTES).toString('utf8');
}

function codexInvocation(args) {
  if (process.env.FISHYUME_CODEX_APP_SERVER) {
    return {command: process.env.FISHYUME_CODEX_APP_SERVER, args};
  }
  if (process.platform !== 'win32') return {command: 'codex', args};
  const codexJs = process.env.APPDATA
    ? join(process.env.APPDATA, 'npm', 'node_modules', '@openai', 'codex', 'bin', 'codex.js')
    : '';
  if (!codexJs || !existsSync(codexJs)) throw new Error('codex-cli installation was not found');
  return {command: process.execPath, args: [codexJs, ...args]};
}

function waitForExit(child, timeoutMs) {
  return new Promise((resolveExit) => {
    if (child.exitCode !== null || child.signalCode !== null) {
      resolveExit({code: child.exitCode, signal: child.signalCode});
      return;
    }
    const timer = setTimeout(() => resolveExit(undefined), timeoutMs);
    child.once('exit', (code, signal) => {
      clearTimeout(timer);
      resolveExit({code, signal});
    });
  });
}

export class AppServerClient {
  constructor({command, args, cwd, timeoutMs = DEFAULT_TIMEOUT_MS}) {
    this.command = command;
    this.args = args;
    this.cwd = cwd;
    this.timeoutMs = timeoutMs;
    this.nextRequestId = 1;
    this.pending = new Map();
    this.notifications = [];
    this.events = new EventEmitter();
    this.stderr = '';
    this.stdoutBuffer = '';
  }

  async start() {
    this.child = spawn(this.command, this.args, {
      cwd: this.cwd,
      stdio: ['pipe', 'pipe', 'pipe'],
      windowsHide: true,
      shell: false,
    });
    this.child.stdout.on('data', (chunk) => this.#readStdout(chunk));
    this.child.stderr.on('data', (chunk) => {
      this.stderr = bounded(this.stderr + chunk.toString('utf8'));
    });
    this.child.once('error', (error) => this.#failPending(error));
    this.child.once('exit', (code, signal) => {
      this.#failPending(new Error(`app-server exited before completing pending requests (${signal || code})`));
    });
    await this.request('initialize', {
      clientInfo: {name: 'fishyume-m7.3-gate', title: 'Fishyume M7.3 capability gate', version: '1'},
      capabilities: {experimentalApi: true, requestAttestation: false},
    });
    this.notify('initialized');
    return this;
  }

  #readStdout(chunk) {
    this.stdoutBuffer += chunk.toString('utf8');
    for (;;) {
      const newline = this.stdoutBuffer.indexOf('\n');
      if (newline < 0) return;
      const line = this.stdoutBuffer.slice(0, newline).trim();
      this.stdoutBuffer = this.stdoutBuffer.slice(newline + 1);
      if (!line) continue;
      let message;
      try {
        message = JSON.parse(line);
      } catch (error) {
        this.#failPending(new Error(`app-server emitted invalid JSON: ${bounded(error.message)}`));
        continue;
      }
      if (Object.hasOwn(message, 'id') && !Object.hasOwn(message, 'method')) {
        const pending = this.pending.get(String(message.id));
        if (!pending) continue;
        this.pending.delete(String(message.id));
        clearTimeout(pending.timer);
        if (message.error) pending.reject(new Error(message.error.message || JSON.stringify(message.error)));
        else pending.resolve(message.result);
        continue;
      }
      if (message.method && !Object.hasOwn(message, 'id')) {
        this.notifications.push(message);
        this.events.emit('notification', message);
        continue;
      }
      if (message.method && Object.hasOwn(message, 'id')) {
        this.child.stdin.write(`${JSON.stringify({id: message.id, error: {code: -32601, message: 'Fishyume gate does not accept server requests'}})}\n`);
      }
    }
  }

  #failPending(error) {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    this.pending.clear();
  }

  request(method, params, timeoutMs = this.timeoutMs) {
    if (!this.child || this.child.exitCode !== null) throw new Error('app-server is not running');
    const id = this.nextRequestId++;
    return new Promise((resolveRequest, rejectRequest) => {
      const timer = setTimeout(() => {
        this.pending.delete(String(id));
        rejectRequest(new Error(`${method} timed out after ${timeoutMs}ms`));
      }, timeoutMs);
      this.pending.set(String(id), {resolve: resolveRequest, reject: rejectRequest, timer});
      this.child.stdin.write(`${JSON.stringify({id, method, params})}\n`);
    });
  }

  notify(method, params) {
    this.child.stdin.write(`${JSON.stringify(params === undefined ? {method} : {method, params})}\n`);
  }

  waitForNotification(method, predicate = () => true, timeoutMs = this.timeoutMs) {
    const existing = this.notifications.find((message) => message.method === method && predicate(message.params));
    if (existing) return Promise.resolve(existing.params);
    return new Promise((resolveNotification, rejectNotification) => {
      const onNotification = (message) => {
        if (message.method !== method || !predicate(message.params)) return;
        cleanup();
        resolveNotification(message.params);
      };
      const timer = setTimeout(() => {
        cleanup();
        rejectNotification(new Error(`${method} notification timed out after ${timeoutMs}ms`));
      }, timeoutMs);
      const cleanup = () => {
        clearTimeout(timer);
        this.events.off('notification', onNotification);
      };
      this.events.on('notification', onNotification);
    });
  }

  agentMessages(threadId, turnId) {
    return this.notifications
      .filter((message) => message.method === 'item/completed')
      .filter((message) => message.params?.threadId === threadId && message.params?.turnId === turnId)
      .map((message) => message.params.item)
      .filter((item) => item?.type === 'agentMessage')
      .map((item) => item.text);
  }

  async stop() {
    if (!this.child || this.child.exitCode !== null || this.child.signalCode !== null) return;
    this.child.stdin.end();
    let exit = await waitForExit(this.child, 5_000);
    if (!exit) {
      this.child.kill();
      exit = await waitForExit(this.child, 5_000);
    }
    if (!exit) throw new Error('app-server did not stop after stdin closed and termination was requested');
  }
}

function assertThreadPolicy(response, expected) {
  if (!response?.thread?.id) throw new Error('app-server did not return a thread identity');
  if (!samePath(response.cwd, expected.workspace) || !samePath(response.thread.cwd, expected.workspace)) {
    throw new Error(`thread workspace mismatch: response=${response.cwd} thread=${response.thread.cwd}`);
  }
  if (response.model !== expected.model) throw new Error(`thread model mismatch: ${response.model}`);
  if (response.sandbox?.type !== 'readOnly') throw new Error(`thread sandbox mismatch: ${JSON.stringify(response.sandbox)}`);
  if (response.approvalPolicy !== 'never') throw new Error(`thread approval policy mismatch: ${JSON.stringify(response.approvalPolicy)}`);
}

async function startTurn(client, {threadId, prompt, workspace, model}) {
  const response = await client.request('turn/start', {
    threadId,
    input: [{type: 'text', text: prompt, text_elements: []}],
    cwd: workspace,
    model,
    approvalPolicy: 'never',
    sandboxPolicy: {type: 'readOnly', networkAccess: false},
  });
  const turnId = response?.turn?.id;
  if (!turnId || response.turn.status !== 'inProgress') {
    throw new Error(`turn/start returned an invalid turn: ${JSON.stringify(response?.turn)}`);
  }
  return turnId;
}

async function completeTurn(client, threadId, turnId) {
  return client.waitForNotification(
    'turn/completed',
    (params) => params?.threadId === threadId && params?.turn?.id === turnId,
  );
}

async function expectRejected(operation, label) {
  try {
    await operation();
  } catch (error) {
    return bounded(error.message);
  }
  throw new Error(`${label} unexpectedly succeeded`);
}

export async function runCapabilityGate({
  model = DEFAULT_MODEL,
  timeoutMs = DEFAULT_TIMEOUT_MS,
  workspace: suppliedWorkspace,
  invocation = codexInvocation(['app-server', '--listen', 'stdio://']),
} = {}) {
  const ownedWorkspace = !suppliedWorkspace;
  const workspace = suppliedWorkspace
    ? resolve(suppliedWorkspace)
    : await mkdtemp(join(tmpdir(), 'fishyume-m7.3-session-'));
  const appServerCwd = invocation.cwd ? resolve(invocation.cwd) : process.cwd();
  const sentinelPath = join(workspace, 'sentinel.txt');
  const deniedWritePath = join(workspace, 'fishyume-read-only-denied.txt');
  const marker = `FISHYUME-CONTINUITY-${randomUUID()}`;
  const initialSentinel = 'Fishyume M7.3 session capability gate sentinel\n';
  let firstClient;
  let resumedClient;
  try {
    if (!suppliedWorkspace) await writeFile(sentinelPath, initialSentinel, 'utf8');
    const sentinelBefore = sha256(await readFile(sentinelPath));
    const entriesBefore = (await readdir(workspace)).sort();
    firstClient = await new AppServerClient({...invocation, cwd: appServerCwd, timeoutMs}).start();
    const started = await firstClient.request('thread/start', {
      cwd: workspace,
      model,
      sandbox: 'read-only',
      approvalPolicy: 'never',
      ephemeral: false,
      developerInstructions: 'This is a bounded Fishyume capability probe. Do not spawn subagents or access the network.',
    });
    assertThreadPolicy(started, {workspace, model});
    if (started.thread.ephemeral) throw new Error('capability gate requires a persisted, resumable thread');
    const threadId = started.thread.id;
    const sessionId = started.thread.sessionId;

    const initialTurnId = await startTurn(firstClient, {
      threadId,
      workspace,
      model,
      prompt: [
        `Remember this exact marker for the next turn: ${marker}`,
        `Use the shell exactly once to try to create ${JSON.stringify(basename(deniedWritePath))} in the current working directory.`,
        'The write must be attempted so the read-only sandbox is exercised. Do not modify any other file.',
        'Then state the marker and whether the write was denied.',
      ].join(' '),
    });
    const initialTerminal = await completeTurn(firstClient, threadId, initialTurnId);
    if (initialTerminal.turn.status !== 'completed') {
      throw new Error(`initial turn did not complete: ${initialTerminal.turn.status}`);
    }
    const writeAttempt = firstClient.notifications.some((message) => {
      const item = message.params?.item;
      return message.method === 'item/completed'
        && message.params?.turnId === initialTurnId
        && item?.type === 'commandExecution'
        && String(item.command).includes(basename(deniedWritePath));
    });
    if (!writeAttempt) throw new Error('initial turn did not exercise the requested read-only write denial');
    if (existsSync(deniedWritePath)) throw new Error('read-only session created the denied probe file');
    if (sha256(await readFile(sentinelPath)) !== sentinelBefore) throw new Error('read-only session changed the sentinel file');

    await firstClient.stop();
    firstClient = undefined;

    resumedClient = await new AppServerClient({...invocation, cwd: appServerCwd, timeoutMs}).start();
    const wrongThreadDiagnostic = await expectRejected(
      () => resumedClient.request('thread/resume', {
        threadId: randomUUID(), cwd: workspace, model, sandbox: 'read-only', approvalPolicy: 'never',
      }),
      'unknown thread resume',
    );
    const resumed = await resumedClient.request('thread/resume', {
      threadId, cwd: workspace, model, sandbox: 'read-only', approvalPolicy: 'never',
    });
    assertThreadPolicy(resumed, {workspace, model});
    if (resumed.thread.id !== threadId || resumed.thread.sessionId !== sessionId) {
      throw new Error('resume returned a different thread or session identity');
    }
    const recoveredTurn = resumed.thread.turns.find((turn) => turn.id === initialTurnId);
    if (!recoveredTurn || recoveredTurn.status !== 'completed') {
      throw new Error('resume did not recover the completed initial turn');
    }

    const followUpTurnId = await startTurn(resumedClient, {
      threadId,
      workspace,
      model,
      prompt: 'Return only the exact marker I asked you to remember in the previous turn. Do not use tools.',
    });
    const followUpTerminal = await completeTurn(resumedClient, threadId, followUpTurnId);
    if (followUpTerminal.turn.status !== 'completed') {
      throw new Error(`follow-up turn did not complete: ${followUpTerminal.turn.status}`);
    }
    const followUpOutput = resumedClient.agentMessages(threadId, followUpTurnId).at(-1)?.trim();
    if (followUpOutput !== marker) {
      throw new Error(`follow-up did not preserve hidden conversation continuity: ${bounded(followUpOutput || '<empty>')}`);
    }
    const staleTurnDiagnostic = await expectRejected(
      () => resumedClient.request('turn/interrupt', {threadId, turnId: initialTurnId}),
      'stale turn interrupt',
    );

    const cancelTurnId = await startTurn(resumedClient, {
      threadId,
      workspace,
      model,
      prompt: 'Use the shell now to run one command that waits for 60 seconds, then reply WAIT_FINISHED. Do not perform any other action.',
    });
    await resumedClient.waitForNotification(
      'item/started',
      (params) => params?.threadId === threadId && params?.turnId === cancelTurnId && params?.item?.type === 'commandExecution',
      Math.min(timeoutMs, 90_000),
    );
    await resumedClient.request('turn/interrupt', {threadId, turnId: cancelTurnId});
    const cancelTerminal = await completeTurn(resumedClient, threadId, cancelTurnId);
    if (cancelTerminal.turn.status !== 'interrupted') {
      throw new Error(`cancelled turn terminal status is not interrupted: ${cancelTerminal.turn.status}`);
    }
    if (existsSync(deniedWritePath) || sha256(await readFile(sentinelPath)) !== sentinelBefore) {
      throw new Error('workspace integrity changed after resume or cancellation');
    }
    const entriesAfter = (await readdir(workspace)).sort();
    if (JSON.stringify(entriesAfter) !== JSON.stringify(entriesBefore)) {
      throw new Error(`workspace entries changed: before=${entriesBefore.join(',')} after=${entriesAfter.join(',')}`);
    }

    return {
      ok: true,
      gate: 'm7.3-codex-app-server-v2',
      model,
      workspacePolicy: 'read-only',
      approvalPolicy: 'never',
      threadId,
      sessionId,
      initialTurnId,
      followUpTurnId,
      cancelTurnId,
      capabilities: {
        supportsResume: true,
        supportsPark: true,
        supportsRecovery: true,
        supportsDirectedInput: true,
        supportsConfirmedCancel: true,
      },
      evidence: {
        serverRestarted: true,
        sameThreadIdentity: true,
        sameSessionIdentity: true,
        previousTurnRecovered: true,
        hiddenHistoryRebuiltByFishyume: false,
        continuityMarkerMatched: true,
        readOnlyWriteDenied: true,
        sentinelSha256: sentinelBefore,
        wrongThreadRejected: true,
        staleTurnRejected: true,
        cancelledTurnStatus: 'interrupted',
        wrongThreadDiagnostic,
        staleTurnDiagnostic,
      },
    };
  } finally {
    await firstClient?.stop().catch(() => {});
    await resumedClient?.stop().catch(() => {});
    if (ownedWorkspace) {
      await rm(workspace, {recursive: true, force: true, maxRetries: 5, retryDelay: 200});
    }
  }
}

export function parseArgs(args) {
  const options = {};
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === '--model') options.model = args[++index];
    else if (arg === '--timeout-ms') options.timeoutMs = Number(args[++index]);
    else if (arg === '--help' || arg === '-h') return {help: true};
    else throw new Error(`unknown argument: ${arg}`);
  }
  if (options.model !== undefined && !options.model.trim()) throw new Error('--model requires a value');
  if (options.timeoutMs !== undefined && (!Number.isSafeInteger(options.timeoutMs) || options.timeoutMs < 1_000)) {
    throw new Error('--timeout-ms must be an integer of at least 1000');
  }
  return options;
}

export async function main(args = process.argv.slice(2)) {
  const options = parseArgs(args);
  if (options.help) {
    console.log('Usage: node wf/scripts/codex-session-capability-gate.mjs [--model ID] [--timeout-ms N]');
    console.log('Requires FISHYUME_LIVE_CODEX=1 and a locally authenticated codex-cli app-server.');
    return;
  }
  if (process.env.FISHYUME_LIVE_CODEX !== '1') {
    throw new Error('M7.3 Codex Session gate is opt-in; set FISHYUME_LIVE_CODEX=1');
  }
  console.log(JSON.stringify(await runCapabilityGate(options)));
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(`M7.3 Codex Session gate failed: ${bounded(error.message)}`);
    process.exitCode = 1;
  });
}
