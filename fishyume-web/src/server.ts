import {randomBytes} from 'node:crypto';
import {existsSync} from 'node:fs';
import {readFile} from 'node:fs/promises';
import {createServer, type IncomingMessage, type ServerResponse} from 'node:http';
import {spawn} from 'node:child_process';
import {dirname, join} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {EngineBridge, type EngineClient} from '../../wf/src/bridge/engine.js';
import {createGatewayHandler, type GatewayLimits} from './gateway.js';
import {isLoopbackAddress, securityHeaders} from './security.js';
import type {WebTarget} from '../../wf/src/mcp/web.js';

const loopbackHost = '127.0.0.1';
const publicFiles = new Map<string, readonly [string, string]>([
  ['/', ['index.html', 'text/html; charset=utf-8']],
  ['/app.js', ['app.js', 'text/javascript; charset=utf-8']],
  ['/app.js.map', ['app.js.map', 'application/json; charset=utf-8']],
  ['/styles.css', ['styles.css', 'text/css; charset=utf-8']],
] as const);

export interface SidecarTarget {target?: WebTarget; revision: number}
export interface SidecarOptions {engine?: EngineClient; gatewayLimits?: GatewayLimits; openBrowser?: boolean; publicDir?: string; target?: WebTarget}
export interface SidecarHandle {origin: string; launchURL: string; token: string; close(): Promise<void>}

export async function startSidecar(options: SidecarOptions = {}): Promise<SidecarHandle> {
  const engine = options.engine ?? new EngineBridge(resolveWebEnginePath());
  await engine.hello();
  const token = randomBytes(32).toString('base64url');
  const publicDir = options.publicDir ?? join(dirname(fileURLToPath(import.meta.url)), 'public');
  let focus: SidecarTarget = {target: options.target, revision: options.target ? 1 : 0};
  let host = '';
  let origin = '';
  const gateway = createGatewayHandler(engine, () => ({host, origin, token}), options.gatewayLimits);
  const server = createServer((request, response) => {void routeRequest(request, response, publicDir, host, origin, token, gateway, () => focus, next => {focus = next})});
  await new Promise<void>((resolveListen, reject) => {
    server.once('error', reject);
    server.listen({host: loopbackHost, port: 0, exclusive: true}, resolveListen);
  });
  const address = server.address();
  if (!address || typeof address === 'string' || address.address !== loopbackHost) {
    server.close();
    await engine.close();
    throw new Error('Fishyume Web failed to bind canonical IPv4 loopback');
  }
  host = `${loopbackHost}:${address.port}`;
  origin = `http://${host}`;
  const launchURL = `${origin}/#token=${encodeURIComponent(token)}${targetFragment(options.target)}`;
  if (options.openBrowser !== false) openURL(launchURL);
  let closePromise: Promise<void> | undefined;
  return {
    origin, launchURL, token,
    close() {
      closePromise ??= (async () => {
        await new Promise<void>((resolveClose, reject) => server.close(error => error ? reject(error) : resolveClose()));
        await engine.close();
      })();
      return closePromise;
    },
  };
}

export function resolveWebEnginePath(env: NodeJS.ProcessEnv = process.env, moduleDir = dirname(fileURLToPath(import.meta.url))): string {
  if (env.FISHYUME_ENGINE_PATH) return env.FISHYUME_ENGINE_PATH;
  const binary = process.platform === 'win32' ? 'fishyume-engine.exe' : 'fishyume-engine';
  const packageName = process.platform === 'win32' && process.arch === 'x64'
    ? 'fishyume-engine-win32-x64'
    : process.platform === 'linux' && process.arch === 'x64'
      ? 'fishyume-engine-linux-x64'
      : undefined;
  if (packageName) {
    const installed = join(moduleDir, '..', '..', packageName, 'bin', binary);
    if (existsSync(installed)) return installed;
  }
  const development = join(moduleDir, '..', '..', 'wf-engine', process.platform === 'win32' ? 'wf-engine.exe' : 'wf-engine');
  if (existsSync(development)) return development;
  if (env.WF_ENGINE_PATH) return env.WF_ENGINE_PATH;
  return binary;
}

async function routeRequest(request: IncomingMessage, response: ServerResponse, publicDir: string, expectedHost: string, expectedOrigin: string, token: string, gateway: ReturnType<typeof createGatewayHandler>, readFocus: () => SidecarTarget, writeFocus: (value: SidecarTarget) => void): Promise<void> {
  for (const [name, value] of Object.entries(securityHeaders)) response.setHeader(name, value);
  if (!isLoopbackAddress(request.socket.remoteAddress) || request.headers.host !== expectedHost) {
    response.writeHead(403, {'Content-Type': 'text/plain; charset=utf-8'});
    response.end('Forbidden');
    return;
  }
  if (request.url === '/api/focus') {await focusRequest(request, response, expectedHost, expectedOrigin, token, readFocus, writeFocus); return}
  if (request.url === '/api/rpc' || request.method !== 'GET') {await gateway(request, response); return}
  const asset = publicFiles.get(request.url ?? '');
  if (!asset) {response.writeHead(404, {'Content-Type': 'text/plain; charset=utf-8'}); response.end('Not found'); return}
  try {
    const body = await readFile(join(publicDir, asset[0]));
    response.writeHead(200, {'Content-Type': asset[1], 'Content-Length': String(body.length)});
    response.end(body);
  } catch {
    response.writeHead(404, {'Content-Type': 'text/plain; charset=utf-8'});
    response.end('Not found');
  }
}

async function focusRequest(request: IncomingMessage, response: ServerResponse, expectedHost: string, expectedOrigin: string, token: string, readFocus: () => SidecarTarget, writeFocus: (value: SidecarTarget) => void): Promise<void> {
  if (request.method !== 'GET' && request.method !== 'POST') {response.writeHead(405); response.end(); return}
  if (request.headers.host !== expectedHost || request.headers.origin !== expectedOrigin || request.headers.authorization !== `Bearer ${token}`) {response.writeHead(403); response.end('Forbidden'); return}
  if (request.method === 'GET') {writeJSON(response, 200, readFocus()); return}
  try {
    const body = await readBody(request, 8 * 1024);
    const parsed = JSON.parse(body) as {target?: unknown};
    if (parsed.target === undefined) {writeJSON(response, 200, readFocus()); return}
    const target = parseTarget(parsed.target);
    if (!target) {writeJSON(response, 400, {error: {code: 'invalid_target', message: 'invalid Web focus target'}}); return}
    const current = readFocus(); writeFocus({target, revision: current.revision + 1}); writeJSON(response, 200, readFocus());
  } catch {writeJSON(response, 400, {error: {code: 'invalid_request', message: 'invalid Web focus request'}})}
}

function parseTarget(value: unknown): WebTarget | undefined {
  if (!value || typeof value !== 'object') return undefined;
  const target = value as Record<string, unknown>;
  if (target.kind === 'team' && typeof target.teamId === 'string' && target.teamId.length > 0 && target.teamId.length <= 128) return {kind: 'team', teamId: target.teamId};
  if (target.kind === 'handoff' && typeof target.teamId === 'string' && typeof target.handoffId === 'string' && target.teamId.length > 0 && target.handoffId.length > 0 && target.teamId.length <= 128 && target.handoffId.length <= 128) return {kind: 'handoff', teamId: target.teamId, handoffId: target.handoffId};
  if (target.kind === 'run' && typeof target.runId === 'string' && target.runId.length > 0 && target.runId.length <= 128) return {kind: 'run', runId: target.runId};
  return undefined;
}

function readBody(request: IncomingMessage, maximumBytes: number): Promise<string> {
  return new Promise((resolve, reject) => {const chunks: Buffer[] = []; let size = 0; request.on('data', chunk => {size += Buffer.byteLength(chunk); if (size > maximumBytes) {reject(new Error('body too large')); request.destroy(); return} chunks.push(Buffer.from(chunk))}); request.on('end', () => resolve(Buffer.concat(chunks).toString('utf8'))); request.on('error', reject)});
}

function writeJSON(response: ServerResponse, status: number, value: unknown): void {const body = Buffer.from(JSON.stringify(value)); response.writeHead(status, {'Content-Type': 'application/json; charset=utf-8', 'Content-Length': String(body.length)}); response.end(body)}

function targetFragment(target?: WebTarget): string {
  if (!target) return '';
  const values = new URLSearchParams({targetKind: target.kind});
  if (target.kind === 'team') values.set('teamId', target.teamId);
  if (target.kind === 'handoff') {values.set('teamId', target.teamId); values.set('handoffId', target.handoffId)}
  if (target.kind === 'run') values.set('runId', target.runId);
  return `&${values.toString()}`;
}

function openURL(url: string): void {
  const child = process.platform === 'win32'
    ? spawn('cmd.exe', ['/d', '/s', '/c', 'start', '', url], {stdio: 'ignore', windowsHide: true})
    : process.platform === 'darwin'
      ? spawn('open', [url], {stdio: 'ignore'})
      : spawn('xdg-open', [url], {stdio: 'ignore'});
  child.on('error', () => undefined);
  child.unref();
}

async function main(): Promise<void> {
  if (process.argv.includes('--help')) {
    process.stdout.write('Usage: fishyume-web [--no-open]\n\nStart an authenticated loopback Web client for the local Fishyume Control Plane.\n');
    return;
  }
  if (process.argv.includes('--version')) {process.stdout.write('0.2.1-alpha.1\n'); return}
  const argumentsList = process.argv.slice(2);
  const target = parseTargetArguments(argumentsList);
  const unknown = argumentsList.filter((argument, index, list) => !['--no-open', '--target-kind', '--team-id', '--handoff-id', '--run-id'].includes(argument) && !(index > 0 && ['--target-kind', '--team-id', '--handoff-id', '--run-id'].includes(list[index - 1] ?? '')));
  if (unknown.length) throw new Error(`unknown option ${unknown[0]}`);
  const sidecar = await startSidecar({openBrowser: !argumentsList.includes('--no-open'), target});
  process.stdout.write(`Fishyume Web: ${sidecar.launchURL}\n`);
  process.stdout.write('Press Ctrl+C to stop the local sidecar.\n');
  const stop = async () => {await sidecar.close(); process.exitCode = 0};
  process.once('SIGINT', () => {void stop()});
  process.once('SIGTERM', () => {void stop()});
}

function parseTargetArguments(argumentsList: string[]): WebTarget | undefined {
  const value = (name: string): string | undefined => {const index = argumentsList.indexOf(name); return index >= 0 ? argumentsList[index + 1] : undefined};
  const kind = value('--target-kind');
  if (!kind && argumentsList.some(argument => ['--team-id', '--handoff-id', '--run-id'].includes(argument))) throw new Error('invalid Web target arguments');
  if (kind === 'team' && value('--team-id')) return {kind, teamId: value('--team-id')!};
  if (kind === 'handoff' && value('--team-id') && value('--handoff-id')) return {kind, teamId: value('--team-id')!, handoffId: value('--handoff-id')!};
  if (kind === 'run' && value('--run-id')) return {kind, runId: value('--run-id')!};
  if (kind) throw new Error('invalid Web target arguments');
  return undefined;
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  main().catch(error => {process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`); process.exitCode = 1});
}
