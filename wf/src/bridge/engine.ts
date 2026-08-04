import {spawn, type ChildProcessWithoutNullStreams} from 'node:child_process';
import {existsSync} from 'node:fs';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import type {EngineHello, RpcNotification, RpcRequest, RpcResponse, RunEvent} from './types.js';
import {protocolVersion} from './types.js';

const maxMessageBytes = 1024 * 1024;
const gracefulCloseTimeoutMs = 1500;
const forcedCloseTimeoutMs = 5000;

export type EventListener = (event: RunEvent) => void;
export type DiagnosticListener = (message: string) => void;

export interface EngineClient {
  hello(project?: string): Promise<EngineHello>;
  call<T>(method: string, params?: unknown): Promise<T>;
  onRunEvent(listener: EventListener): () => void;
  onDiagnostic(listener: DiagnosticListener): () => void;
  close(): Promise<void>;
}

export class EngineRpcError extends Error {
  constructor(message: string, readonly code: number, readonly data?: unknown) { super(message); this.name = 'EngineRpcError'; }
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

export class EngineBridge implements EngineClient {
  readonly child: ChildProcessWithoutNullStreams;
  #nextId = 1;
  #stdoutBuffer = '';
  #stderrBuffer = '';
  #pending = new Map<number, {resolve(value: unknown): void; reject(reason: Error): void}>();
  #eventListeners = new Set<EventListener>();
  #diagnosticListeners = new Set<DiagnosticListener>();
  #processClosed = false;
  #closedPromise: Promise<void>;
  #resolveClosed!: () => void;
  #closePromise?: Promise<void>;

  constructor(enginePath = resolveEnginePath(), engineArgs: string[] = []) {
    this.#closedPromise = new Promise(resolve => {this.#resolveClosed = resolve});
    this.child = spawn(enginePath, engineArgs, {stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true});
    this.child.stdout.setEncoding('utf8');
    this.child.stderr.setEncoding('utf8');
    this.child.stdout.on('data', chunk => this.#consumeStdout(String(chunk)));
    this.child.stderr.on('data', chunk => this.#consumeStderr(String(chunk)));
    this.child.on('error', error => {
	  const diagnostic = (error as NodeJS.ErrnoException).code === 'ENOENT'
	    ? new Error(`Fishyume Engine not found at ${enginePath}. Install the matching platform package or set FISHYUME_ENGINE_PATH.`)
	    : error;
	  this.#rejectAll(diagnostic);
      if (this.child.pid === undefined) this.#markClosed();
    });
    this.child.on('exit', (code, signal) => this.#rejectAll(new Error(`fishyume engine exited (${code ?? signal ?? 'unknown'})`)));
    this.child.on('close', () => this.#markClosed());
  }

  onRunEvent(listener: EventListener): () => void {
    this.#eventListeners.add(listener);
    return () => this.#eventListeners.delete(listener);
  }

  onDiagnostic(listener: DiagnosticListener): () => void {
    this.#diagnosticListeners.add(listener);
    return () => this.#diagnosticListeners.delete(listener);
  }

  async hello(project?: string): Promise<EngineHello> {
    const hello = await this.call<EngineHello>('engine.hello', project ? {project} : undefined);
    if (hello.protocolVersion !== protocolVersion) {
      throw new Error(`incompatible protocol version ${hello.protocolVersion}`);
    }
    return hello;
  }

  call<T>(method: string, params?: unknown): Promise<T> {
    const id = this.#nextId++;
    const request: RpcRequest = {jsonrpc: '2.0', protocolVersion, id, method, ...(params === undefined ? {} : {params})};
    const line = `${JSON.stringify(request)}\n`;
    if (Buffer.byteLength(line) > maxMessageBytes) return Promise.reject(new Error('request exceeds maximum protocol message size'));
    return new Promise<T>((resolve, reject) => {
      this.#pending.set(id, {resolve: value => resolve(value as T), reject});
      this.child.stdin.write(line, error => {
        if (error) {
          this.#pending.delete(id);
          reject(error);
        }
      });
    });
  }

  async close(): Promise<void> {
    this.#closePromise ??= this.#closeProcess();
    return this.#closePromise;
  }

  #consumeStdout(chunk: string): void {
    this.#stdoutBuffer += chunk;
    if (Buffer.byteLength(this.#stdoutBuffer) > maxMessageBytes * 2) {
      this.#rejectAll(new Error('engine protocol buffer exceeded limit'));
      this.child.kill();
      return;
    }
    for (;;) {
      const newline = this.#stdoutBuffer.indexOf('\n');
      if (newline < 0) return;
      const line = this.#stdoutBuffer.slice(0, newline);
      this.#stdoutBuffer = this.#stdoutBuffer.slice(newline + 1);
      if (line) this.#handleLine(line);
    }
  }

  #handleLine(line: string): void {
    if (Buffer.byteLength(line) > maxMessageBytes) throw new Error('engine response exceeds maximum protocol message size');
    let message: RpcResponse | RpcNotification;
    try { message = JSON.parse(line) as RpcResponse | RpcNotification; }
    catch { this.#rejectAll(new Error('engine emitted malformed JSON')); return; }
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

  async #closeProcess(): Promise<void> {
    if (this.#processClosed) return;
    if (!this.child.stdin.destroyed) this.child.stdin.end();
    if (await this.#waitForClose(gracefulCloseTimeoutMs)) return;
    const killSent = this.child.kill('SIGKILL');
    if (!killSent && !this.#processClosed) throw new Error('fishyume engine did not accept termination request');
    if (!await this.#waitForClose(forcedCloseTimeoutMs)) {
      throw new Error(`fishyume engine child ${this.child.pid ?? 'unknown'} did not exit after termination`);
    }
  }

  async #waitForClose(timeoutMs: number): Promise<boolean> {
    if (this.#processClosed) return true;
    let timer: NodeJS.Timeout | undefined;
    try {
      return await Promise.race([
        this.#closedPromise.then(() => true),
        new Promise<boolean>(resolve => {timer = setTimeout(() => resolve(false), timeoutMs)}),
      ]);
    } finally {
      if (timer) clearTimeout(timer);
    }
  }

  #markClosed(): void {
    if (this.#processClosed) return;
    this.#processClosed = true;
    this.#resolveClosed();
  }
}
