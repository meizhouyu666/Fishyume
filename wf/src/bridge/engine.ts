import {spawn, type ChildProcess, type ChildProcessWithoutNullStreams} from 'node:child_process';
import {existsSync} from 'node:fs';
import {readFile} from 'node:fs/promises';
import {homedir} from 'node:os';
import {dirname, join, resolve} from 'node:path';
import {connect, type Socket} from 'node:net';
import {fileURLToPath} from 'node:url';
import type {EngineHello, RpcNotification, RpcRequest, RpcResponse, RunEvent} from './types.js';
import {protocolVersion} from './types.js';

const engineVersion = '0.2.1-alpha.1';
const controlProtocolVersion = 1;
const stateSchemaVersion = 3;
const maxMessageBytes = 1024 * 1024;
const maxHandshakeBytes = 64 * 1024;
const connectTimeoutMs = 1500;
const startupTimeoutMs = 10_000;
const startupPollMs = 25;
const gracefulCloseTimeoutMs = 1500;
const forcedCloseTimeoutMs = 5000;

export type EventListener = (event: RunEvent) => void;
export type DiagnosticListener = (message: string) => void;

export interface EngineClient {
  hello(project?: string, driver?: string): Promise<EngineHello>;
  call<T>(method: string, params?: unknown): Promise<T>;
  onRunEvent(listener: EventListener): () => void;
  onDiagnostic(listener: DiagnosticListener): () => void;
  close(): Promise<void>;
}

interface ControlPlaneOwner {
  protocolVersion: number;
  rpcProtocolVersion: number;
  stateSchema: number;
  engineVersion: string;
  ownerId: string;
  stateDirHash: string;
  stateDir: string;
  endpoint: string;
  transport: 'named-pipe' | 'unix';
  pid: number;
  userIdentity: string;
  createdAt: string;
}

interface HandshakeResponse {
  ok: boolean;
  engineVersion: string;
  error?: string;
  handshake: {protocolVersion: number; stateSchema: number; ownerId: string; stateDirHash: string};
}

class IncompatibleControlPlaneError extends Error {}

export class EngineRpcError extends Error {
  constructor(message: string, readonly code: number, readonly data?: unknown) {super(message); this.name = 'EngineRpcError'}
}

export function resolveEnginePath(env: NodeJS.ProcessEnv = process.env): string {
  if (env.FISHYUME_ENGINE_PATH) return env.FISHYUME_ENGINE_PATH;
  const here = dirname(fileURLToPath(import.meta.url));
  const binary = process.platform === 'win32' ? 'fishyume-engine.exe' : 'fishyume-engine';
  const platformPackage = process.platform === 'win32' && process.arch === 'x64'
    ? join(here, '..', '..', '..', 'fishyume-engine-win32-x64', 'bin', binary)
    : process.platform === 'linux' && process.arch === 'x64'
      ? join(here, '..', '..', '..', 'fishyume-engine-linux-x64', 'bin', binary)
      : undefined;
  if (platformPackage && existsSync(platformPackage)) return platformPackage;
  const development = join(here, '..', '..', '..', 'wf-engine', process.platform === 'win32' ? 'wf-engine.exe' : 'wf-engine');
  if (existsSync(development)) return development;
  if (env.WF_ENGINE_PATH) return env.WF_ENGINE_PATH;
  return binary;
}

export function resolveStateRoot(env: NodeJS.ProcessEnv = process.env): string {
  const override = env.FISHYUME_STATE_DIR || env.WF_STATE_DIR;
  if (override) return resolve(override);
  if (process.platform === 'win32') return join(env.LOCALAPPDATA || join(homedir(), 'AppData', 'Local'), 'fishyume');
  if (process.platform === 'darwin') return join(homedir(), 'Library', 'Application Support', 'fishyume');
  return join(env.XDG_STATE_HOME || join(homedir(), '.local', 'state'), 'fishyume');
}

export class EngineBridge implements EngineClient {
  readonly child?: ChildProcessWithoutNullStreams;
  readonly #enginePath: string;
  readonly #environment: NodeJS.ProcessEnv;
  readonly #stdioMode: boolean;
  readonly #ready: Promise<void>;
  #startupError?: Error;
  #socket?: Socket;
  #nextId = 1;
  #protocolBuffer = '';
  #stderrBuffer = '';
  #pending = new Map<number, {resolve(value: unknown): void; reject(reason: Error): void}>();
  #eventListeners = new Set<EventListener>();
  #diagnosticListeners = new Set<DiagnosticListener>();
  #transportClosed = false;
  #closedPromise: Promise<void>;
  #resolveClosed!: () => void;
  #closePromise?: Promise<void>;

  constructor(enginePath = resolveEnginePath(), engineArgs?: string[]) {
    this.#enginePath = enginePath;
    this.#environment = {...process.env};
    this.#stdioMode = engineArgs !== undefined;
    this.#closedPromise = new Promise(resolveClosed => {this.#resolveClosed = resolveClosed});
    if (this.#stdioMode) {
      const child = spawn(enginePath, engineArgs ?? [], {stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true, env: this.#environment});
      this.child = child;
      this.#wireStdio(child);
      this.#ready = this.#waitForSpawn(child);
    } else {
      this.#ready = this.#connectControlPlane();
    }
  }

  onRunEvent(listener: EventListener): () => void {
    this.#eventListeners.add(listener);
    return () => this.#eventListeners.delete(listener);
  }

  onDiagnostic(listener: DiagnosticListener): () => void {
    this.#diagnosticListeners.add(listener);
    return () => this.#diagnosticListeners.delete(listener);
  }

  async hello(project?: string, driver?: string): Promise<EngineHello> {
    const params = project || driver ? {...(project ? {project} : {}), ...(driver ? {driver} : {})} : undefined;
    const hello = await this.call<EngineHello>('engine.hello', params);
    if (hello.protocolVersion !== protocolVersion) throw new Error(`incompatible protocol version ${hello.protocolVersion}`);
    return hello;
  }

  async call<T>(method: string, params?: unknown): Promise<T> {
    await this.#ready;
    if (this.#transportClosed) throw new Error('Fishyume Engine connection is closed');
    const id = this.#nextId++;
    const request: RpcRequest = {jsonrpc: '2.0', protocolVersion, id, method, ...(params === undefined ? {} : {params})};
    const line = `${JSON.stringify(request)}\n`;
    if (Buffer.byteLength(line) > maxMessageBytes) throw new Error('request exceeds maximum protocol message size');
    return new Promise<T>((resolveCall, reject) => {
      this.#pending.set(id, {resolve: value => resolveCall(value as T), reject});
      const stream = this.#stdioMode ? this.child?.stdin : this.#socket;
      if (!stream) {
        this.#pending.delete(id);
        reject(new Error('Fishyume Engine transport is unavailable'));
        return;
      }
      stream.write(line, error => {
        if (error) {
          this.#pending.delete(id);
          reject(error);
        }
      });
    });
  }

  async close(): Promise<void> {
    this.#closePromise ??= this.#closeTransport();
    return this.#closePromise;
  }

  #wireStdio(child: ChildProcessWithoutNullStreams): void {
    child.stdout.setEncoding('utf8');
    child.stderr.setEncoding('utf8');
    child.stdout.on('data', chunk => this.#consumeProtocol(String(chunk)));
    child.stderr.on('data', chunk => this.#consumeStderr(String(chunk)));
    child.on('error', error => this.#rejectAll(this.#spawnDiagnostic(error)));
    child.on('exit', (code, signal) => this.#rejectAll(new Error(`fishyume engine exited (${code ?? signal ?? 'unknown'})`)));
    child.on('close', () => this.#markClosed());
  }

  #waitForSpawn(child: ChildProcess): Promise<void> {
    return new Promise((resolveSpawn, reject) => {
      if (child.pid !== undefined) {resolveSpawn(); return}
      child.once('spawn', resolveSpawn);
      child.once('error', error => reject(this.#spawnDiagnostic(error)));
    });
  }

  #spawnDiagnostic(error: Error): Error {
    return (error as NodeJS.ErrnoException).code === 'ENOENT'
      ? new Error(`Fishyume Engine not found at ${this.#enginePath}. Install the matching platform package or set FISHYUME_ENGINE_PATH.`)
      : error;
  }

  async #connectControlPlane(): Promise<void> {
    const stateRoot = resolveStateRoot(this.#environment);
    const metadataPath = join(stateRoot, 'control-plane.json');
    let lastError: unknown;
    const existing = await this.#readOwner(metadataPath, stateRoot).catch(error => {lastError = error; return undefined});
    if (existing) {
      try {
        await this.#connectOwner(existing);
        return;
      } catch (error) {
        if (error instanceof IncompatibleControlPlaneError) throw error;
        lastError = error;
      }
    }
    this.#launchControlPlane();
    const deadline = Date.now() + startupTimeoutMs;
    while (Date.now() < deadline) {
      if (this.#startupError) throw this.#startupError;
      const owner = await this.#readOwner(metadataPath, stateRoot).catch(error => {lastError = error; return undefined});
      if (owner) {
        try {
          await this.#connectOwner(owner);
          return;
        } catch (error) {
          if (error instanceof IncompatibleControlPlaneError) throw error;
          lastError = error;
        }
      }
      await new Promise(resolveWait => setTimeout(resolveWait, startupPollMs));
    }
    throw new Error(`Fishyume Control Plane did not become ready: ${lastError instanceof Error ? lastError.message : String(lastError ?? 'metadata unavailable')}`);
  }

  async #readOwner(path: string, expectedStateRoot: string): Promise<ControlPlaneOwner> {
    const owner = JSON.parse(await readFile(path, 'utf8')) as Partial<ControlPlaneOwner>;
    const required = [owner.engineVersion, owner.ownerId, owner.stateDirHash, owner.stateDir, owner.endpoint, owner.transport, owner.userIdentity, owner.createdAt];
    if (owner.protocolVersion !== controlProtocolVersion || owner.rpcProtocolVersion !== protocolVersion || owner.stateSchema !== stateSchemaVersion || owner.pid === undefined || required.some(value => !value)) {
      throw new Error('Control Plane metadata is incomplete or uses an unsupported protocol');
    }
    const expected = process.platform === 'win32' ? resolve(expectedStateRoot).toLowerCase() : resolve(expectedStateRoot);
    const actual = process.platform === 'win32' ? resolve(owner.stateDir!).toLowerCase() : resolve(owner.stateDir!);
    if (actual !== expected) throw new Error(`Control Plane stateDir mismatch: expected ${expectedStateRoot}, received ${owner.stateDir}`);
    return owner as ControlPlaneOwner;
  }

  #launchControlPlane(): void {
    const child = spawn(this.#enginePath, ['serve'], {detached: true, stdio: 'ignore', windowsHide: true, env: this.#environment});
    child.once('error', error => {
      const diagnostic = this.#spawnDiagnostic(error);
      this.#startupError = diagnostic;
      for (const listener of this.#diagnosticListeners) listener(diagnostic.message);
    });
    child.unref();
  }

  async #connectOwner(owner: ControlPlaneOwner): Promise<void> {
    const socket = await this.#openSocket(owner.endpoint);
    try {
      const response = await this.#handshake(socket, owner);
      if (response.engineVersion !== engineVersion) {
        throw new IncompatibleControlPlaneError(`incompatible Control Plane engine version ${response.engineVersion}; CLI requires ${engineVersion}`);
      }
      if (!response.ok || response.handshake.protocolVersion !== owner.protocolVersion || response.handshake.stateSchema !== owner.stateSchema || response.handshake.ownerId !== owner.ownerId || response.handshake.stateDirHash !== owner.stateDirHash) {
        throw new Error(response.error || 'Control Plane handshake identity mismatch');
      }
      this.#socket = socket;
      socket.setEncoding('utf8');
      socket.on('data', chunk => this.#consumeProtocol(String(chunk)));
      socket.on('error', error => this.#rejectAll(error));
      socket.on('close', () => this.#markClosed());
    } catch (error) {
      socket.destroy();
      throw error;
    }
  }

  #openSocket(endpoint: string): Promise<Socket> {
    return new Promise((resolveSocket, reject) => {
      const socket = connect(endpoint);
      const timer = setTimeout(() => {
        socket.destroy();
        reject(new Error(`Control Plane connection timed out after ${connectTimeoutMs}ms`));
      }, connectTimeoutMs);
      socket.once('connect', () => {clearTimeout(timer); resolveSocket(socket)});
      socket.once('error', error => {clearTimeout(timer); reject(error)});
    });
  }

  #handshake(socket: Socket, owner: ControlPlaneOwner): Promise<HandshakeResponse> {
    return new Promise((resolveHandshake, reject) => {
      let buffer = '';
      const timer = setTimeout(() => {
        cleanup();
        reject(new Error('Control Plane handshake timed out'));
      }, connectTimeoutMs);
      const cleanup = (): void => {
        clearTimeout(timer);
        socket.off('data', onData);
        socket.off('error', onError);
      };
      const onError = (error: Error): void => {cleanup(); reject(error)};
      const onData = (chunk: Buffer | string): void => {
        buffer += String(chunk);
        if (Buffer.byteLength(buffer) > maxHandshakeBytes) {cleanup(); reject(new Error('Control Plane handshake exceeded limit')); return}
        const newline = buffer.indexOf('\n');
        if (newline < 0) return;
        cleanup();
        try {resolveHandshake(JSON.parse(buffer.slice(0, newline)) as HandshakeResponse)}
        catch {reject(new Error('Control Plane emitted malformed handshake JSON'))}
      };
      socket.on('data', onData);
      socket.once('error', onError);
      socket.write(`${JSON.stringify({
        protocolVersion: owner.protocolVersion,
        rpcProtocolVersion: owner.rpcProtocolVersion,
        stateSchema: owner.stateSchema,
        engineVersion: owner.engineVersion,
        ownerId: owner.ownerId,
        stateDirHash: owner.stateDirHash,
      })}\n`, error => {if (error) onError(error)});
    });
  }

  #consumeProtocol(chunk: string): void {
    this.#protocolBuffer += chunk;
    if (Buffer.byteLength(this.#protocolBuffer) > maxMessageBytes * 2) {
      this.#rejectAll(new Error('engine protocol buffer exceeded limit'));
      if (this.#stdioMode) this.child?.kill(); else this.#socket?.destroy();
      return;
    }
    for (;;) {
      const newline = this.#protocolBuffer.indexOf('\n');
      if (newline < 0) return;
      const line = this.#protocolBuffer.slice(0, newline);
      this.#protocolBuffer = this.#protocolBuffer.slice(newline + 1);
      if (line) this.#handleLine(line);
    }
  }

  #handleLine(line: string): void {
    if (Buffer.byteLength(line) > maxMessageBytes) {this.#rejectAll(new Error('engine response exceeds maximum protocol message size')); return}
    let message: RpcResponse | RpcNotification;
    try {message = JSON.parse(line) as RpcResponse | RpcNotification}
    catch {this.#rejectAll(new Error('engine emitted malformed JSON')); return}
    if (message.protocolVersion !== protocolVersion) {
      this.#rejectAll(new Error(`engine emitted incompatible protocol version ${message.protocolVersion}`));
      return;
    }
    if ('method' in message) {
      if (message.method === 'run.event') for (const listener of this.#eventListeners) listener(message.params as RunEvent);
      return;
    }
    if (typeof message.id !== 'number') return;
    const pending = this.#pending.get(message.id);
    if (!pending) return;
    this.#pending.delete(message.id);
    if (message.error) pending.reject(new EngineRpcError(message.error.message, message.error.code, message.error.data));
    else pending.resolve(message.result);
  }

  #consumeStderr(chunk: string): void {
    this.#stderrBuffer += chunk;
    for (;;) {
      const newline = this.#stderrBuffer.indexOf('\n');
      if (newline < 0) return;
      const line = this.#stderrBuffer.slice(0, newline).trim();
      this.#stderrBuffer = this.#stderrBuffer.slice(newline + 1);
      if (line) for (const listener of this.#diagnosticListeners) listener(line);
    }
  }

  #rejectAll(error: Error): void {
    for (const pending of this.#pending.values()) pending.reject(error);
    this.#pending.clear();
  }

  async #closeTransport(): Promise<void> {
    try {await this.#ready} catch (error) {
      this.#markClosed();
      if (error instanceof Error) this.#rejectAll(error);
      return;
    }
    if (this.#transportClosed) return;
    if (!this.#stdioMode) {
      const socket = this.#socket;
      if (!socket || socket.destroyed) {this.#markClosed(); return}
      socket.end();
      if (!await this.#waitForClose(gracefulCloseTimeoutMs)) socket.destroy();
      await this.#waitForClose(forcedCloseTimeoutMs);
      return;
    }
    const child = this.child;
    if (!child) return;
    if (!child.stdin.destroyed) child.stdin.end();
    if (await this.#waitForClose(gracefulCloseTimeoutMs)) return;
    const killSent = child.kill('SIGKILL');
    if (!killSent && !this.#transportClosed) throw new Error('fishyume engine did not accept termination request');
    if (!await this.#waitForClose(forcedCloseTimeoutMs)) throw new Error(`fishyume engine child ${child.pid ?? 'unknown'} did not exit after termination`);
  }

  async #waitForClose(timeoutMs: number): Promise<boolean> {
    if (this.#transportClosed) return true;
    let timer: NodeJS.Timeout | undefined;
    try {
      return await Promise.race([
        this.#closedPromise.then(() => true),
        new Promise<boolean>(resolveWait => {timer = setTimeout(() => resolveWait(false), timeoutMs)}),
      ]);
    } finally {if (timer) clearTimeout(timer)}
  }

  #markClosed(): void {
    if (this.#transportClosed) return;
    this.#transportClosed = true;
    this.#resolveClosed();
  }
}
