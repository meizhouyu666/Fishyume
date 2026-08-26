import {spawn, type ChildProcess} from 'node:child_process';
import {request as httpRequest} from 'node:http';
import {request as httpsRequest} from 'node:https';
import {randomUUID} from 'node:crypto';

export type WebTarget =
  | {kind: 'team'; teamId: string}
  | {kind: 'handoff'; teamId: string; handoffId: string}
  | {kind: 'run'; runId: string};

export interface WebOpenResponse {status: 'opened' | 'focused' | 'unavailable'; target: WebTarget; reason?: string}
export interface WebOpenManager {open(target: WebTarget): Promise<WebOpenResponse>; close(): Promise<void>}

export interface WebProcessLike {
  readonly exitCode: number | null;
  readonly killed: boolean;
  once(event: 'error' | 'exit', listener: (...args: unknown[]) => void): this;
  kill(signal?: NodeJS.Signals): boolean;
}

export interface WebLauncher {
  spawn(command: string, args: string[]): WebProcessLike;
  command: string;
}

export interface WebOpenManagerOptions {
  launcher?: WebLauncher;
  fetchFocus?: (origin: string, token: string, target?: WebTarget) => Promise<boolean>;
  waitMs?: number;
}

interface SidecarIdentity {origin: string; token: string; process: WebProcessLike}

const defaultWaitMs = 5_000;

export function createWebOpenManager(options: WebOpenManagerOptions = {}): WebOpenManager {
  const launcher = options.launcher ?? defaultLauncher();
  const fetchFocus = options.fetchFocus ?? postFocus;
  const waitMs = options.waitMs ?? defaultWaitMs;
  let identity: SidecarIdentity | undefined;
  let starting: Promise<SidecarIdentity> | undefined;

  const open = async (target: WebTarget): Promise<WebOpenResponse> => {
    if (identity && isAlive(identity.process)) {
      try {
        if (await fetchFocus(identity.origin, identity.token, target)) return {status: 'focused', target};
      } catch { /* recover by starting a fresh sidecar below */ }
      forget();
    }
    try {
      identity = await (starting ??= start(target));
      starting = undefined;
      return {status: 'opened', target};
    } catch (error) {
      starting = undefined;
      return {status: 'unavailable', target, reason: error instanceof Error ? error.message : String(error)};
    }
  };

  const start = (target: WebTarget): Promise<SidecarIdentity> => new Promise((resolve, reject) => {
    const child = launcher.spawn(launcher.command, targetArgs(target));
    let output = '';
    let settled = false;
    const timer = setTimeout(() => finish(new Error('Fishyume Web sidecar did not publish a launch URL')), waitMs);
    const finish = (error?: Error, value?: SidecarIdentity): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      if (error) {child.kill('SIGTERM'); reject(error)} else if (value) resolve(value);
    };
    const onData = (chunk: unknown): void => {
      output += String(chunk);
      const match = output.match(/Fishyume Web:\s+(http:\/\/127\.0\.0\.1:\d+\/#token=([^\s]+))/);
      if (!match) return;
      const launchURL = match[1]!;
      const parsed = new URL(launchURL);
      finish(undefined, {origin: parsed.origin, token: parsed.hash.slice(1).split('&').find(value => value.startsWith('token='))?.slice(6) ?? '', process: child});
    };
    const stdout = (child as ChildProcess).stdout;
    const stderr = (child as ChildProcess).stderr;
    stdout?.on('data', onData);
    stderr?.on('data', onData);
    child.once('error', (...args: unknown[]) => finish(new Error(String(args[0] ?? 'failed to start Web sidecar'))));
    child.once('exit', (...args: unknown[]) => {if (!settled) finish(new Error(`Web sidecar exited (${String(args[0] ?? 'unknown')})`))});
  });

  const forget = (): void => {
    identity = undefined;
  };
  const close = async (): Promise<void> => {
    const child = identity?.process;
    identity = undefined;
    if (child && isAlive(child)) child.kill('SIGTERM');
    if (starting) {try {const pending = await starting; pending.process.kill('SIGTERM')} catch { /* already failed */ } finally {starting = undefined}}
  };
  return {open, close};
}

function targetArgs(target: WebTarget): string[] {
  return ['--target-kind', target.kind, ...(target.kind === 'team' ? ['--team-id', target.teamId] : target.kind === 'handoff' ? ['--team-id', target.teamId, '--handoff-id', target.handoffId] : ['--run-id', target.runId])];
}

function isAlive(process: WebProcessLike): boolean {return process.exitCode === null && !process.killed}

function defaultLauncher(): WebLauncher {
  return {command: process.env.FISHYUME_WEB_COMMAND ?? 'fishyume-web', spawn: (command, args) => spawn(command, args, {stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true})};
}

async function postFocus(origin: string, token: string, target?: WebTarget): Promise<boolean> {
  const url = new URL('/api/focus', origin);
  return new Promise(resolve => {
    const transport = url.protocol === 'https:' ? httpsRequest : httpRequest;
    const body = target ? JSON.stringify({target}) : '';
    const request = transport(url, {method: target ? 'POST' : 'GET', headers: {Host: url.host, Origin: origin, Authorization: `Bearer ${decodeURIComponent(token)}`, ...(target ? {'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body)} : {})}}, response => {
      response.resume();
      response.once('end', () => resolve(response.statusCode === 200));
    });
    request.once('error', () => resolve(false));
    if (body) request.end(body); else request.end();
  });
}

export function webActionId(): string {return `web-${randomUUID()}`}
