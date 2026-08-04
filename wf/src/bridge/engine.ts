import {spawn, type ChildProcessWithoutNullStreams} from 'node:child_process';
import {existsSync} from 'node:fs';
import {dirname, join} from 'node:path';
import {fileURLToPath} from 'node:url';
import type {EngineHello, RpcNotification, RpcRequest, RpcResponse, RunEvent} from './types.js';
import {protocolVersion} from './types.js';

const maxMessageBytes = 1024 * 1024;

export type EventListener = (event: RunEvent) => void;
export type DiagnosticListener = (message: string) => void;

export interface EngineClient {
  hello(project?: string): Promise<EngineHello>;
  call<T>(method: string, params?: unknown): Promise<T>;
  onRunEvent(listener: EventListener): () => void;
  onDiagnostic(listener: DiagnosticListener): () => void;
  close(): Promise<void>;
}

export function resolveEnginePath(env: NodeJS.ProcessEnv = process.env): string {
  if (env.WF_ENGINE_PATH) return env.WF_ENGINE_PATH;
  const here = dirname(fileURLToPath(import.meta.url));
  const sibling = join(here, '..', process.platform === 'win32' ? 'wf-engine.exe' : 'wf-engine');
  return existsSync(sibling) ? sibling : 'wf-engine';
}

export class EngineBridge implements EngineClient {
  readonly child: ChildProcessWithoutNullStreams;
  #nextId = 1;
  #stdoutBuffer = '';
  #stderrBuffer = '';
  #pending = new Map<number, {resolve(value: unknown): void; reject(reason: Error): void}>();
  #eventListeners = new Set<EventListener>();
  #diagnosticListeners = new Set<DiagnosticListener>();

  constructor(enginePath = resolveEnginePath(), engineArgs: string[] = []) {
    this.child = spawn(enginePath, engineArgs, {stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true});
    this.child.stdout.setEncoding('utf8');
    this.child.stderr.setEncoding('utf8');
    this.child.stdout.on('data', chunk => this.#consumeStdout(String(chunk)));
    this.child.stderr.on('data', chunk => this.#consumeStderr(String(chunk)));
    this.child.on('error', error => this.#rejectAll(error));
    this.child.on('exit', (code, signal) => this.#rejectAll(new Error(`wf-engine exited (${code ?? signal ?? 'unknown'})`)));
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
    if (this.child.exitCode !== null) return;
    this.child.stdin.end();
    await new Promise<void>(resolve => {
      const timer = setTimeout(() => { this.child.kill(); resolve(); }, 1000);
      this.child.once('exit', () => { clearTimeout(timer); resolve(); });
    });
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
    if (message.error) pending.reject(new Error(message.error.message));
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
}
