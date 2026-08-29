import type {IncomingMessage, ServerResponse} from 'node:http';
import {authorizeRequest, decodeRpcEnvelope, maxConcurrentRequests, maxRequestBytes, maxResponseBytes, probeRequestTimeoutMs, requestTimeoutMs, securityHeaders} from './security.js';

export interface EngineGateway {call<T>(method: string, params?: unknown): Promise<T>}
export interface GatewayIdentity {host: string; origin: string; token: string}
export interface GatewayLimits {maxConcurrentRequests: number; maxRequestBytes: number; maxResponseBytes: number; requestTimeoutMs: number; probeRequestTimeoutMs?: number}

const defaultLimits: GatewayLimits = {maxConcurrentRequests, maxRequestBytes, maxResponseBytes, requestTimeoutMs, probeRequestTimeoutMs};

export function createGatewayHandler(engine: EngineGateway, identity: () => GatewayIdentity, limits: GatewayLimits = defaultLimits, routePath = '/api/rpc') {
  let active = 0;
  return async (request: IncomingMessage, response: ServerResponse): Promise<void> => {
    for (const [name, value] of Object.entries(securityHeaders)) response.setHeader(name, value);
    if (request.method !== 'POST' || request.url !== routePath) {writeJSON(response, 404, {error: {code: 'not_found', message: 'route not found'}}); return}
    const authorizationError = authorizeRequest(request, identity().host, identity().origin, identity().token);
    if (authorizationError) {writeJSON(response, 403, {error: {code: 'forbidden', message: authorizationError}}); return}
    if (active >= limits.maxConcurrentRequests) {writeJSON(response, 429, {error: {code: 'busy', message: 'too many concurrent requests'}}, limits.maxResponseBytes); return}
    active++;
    try {
      const body = await readBody(request, limits.maxRequestBytes);
      const envelope = decodeRpcEnvelope(body);
      const timeout = envelope.method === 'driver.models.probe' ? limits.probeRequestTimeoutMs ?? probeRequestTimeoutMs : limits.requestTimeoutMs;
      const result = await withTimeout(engine.call(envelope.method, envelope.params), timeout);
      writeJSON(response, 200, {result}, limits.maxResponseBytes);
    } catch (error) {
      const value = error as {applicationError?: unknown; teamError?: unknown; data?: unknown};
      const data = value.data && typeof value.data === 'object' && 'code' in value.data ? value.data : undefined;
      const known = value.applicationError ?? value.teamError ?? data;
      writeJSON(response, known ? 409 : 400, {error: known ?? {code: 'invalid_request', message: error instanceof Error ? error.message : String(error)}}, limits.maxResponseBytes);
    } finally {active--}
  };
}

function readBody(request: IncomingMessage, maximumBytes: number): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = [];
    let size = 0;
    let exceeded = false;
    request.on('data', (chunk: Buffer) => {
      if (exceeded) return;
      size += chunk.length;
      if (size > maximumBytes) {exceeded = true; chunks.length = 0; return}
      chunks.push(chunk);
    });
    request.on('end', () => exceeded ? reject(new Error('request body exceeds limit')) : resolve(Buffer.concat(chunks)));
    request.on('error', reject);
  });
}

function withTimeout<T>(promise: Promise<T>, milliseconds: number): Promise<T> {
  let timer: NodeJS.Timeout | undefined;
  return Promise.race([
    promise,
    new Promise<never>((_, reject) => {timer = setTimeout(() => reject(new Error('Engine response timed out')), milliseconds)}),
  ]).finally(() => {if (timer) clearTimeout(timer)});
}

function writeJSON(response: ServerResponse, status: number, value: unknown, maximumBytes = maxResponseBytes): void {
  const data = Buffer.from(JSON.stringify(value));
  if (data.length > maximumBytes) {
    response.writeHead(502, {'Content-Type': 'application/json; charset=utf-8'});
    response.end(JSON.stringify({error: {code: 'response_too_large', message: 'Engine response exceeds 2 MiB'}}));
    return;
  }
  response.writeHead(status, {'Content-Type': 'application/json; charset=utf-8', 'Content-Length': String(data.length)});
  response.end(data);
}
