import {timingSafeEqual} from 'node:crypto';
import type {IncomingMessage} from 'node:http';

export const maxRequestBytes = 64 * 1024;
export const maxResponseBytes = 2 * 1024 * 1024;
export const maxConcurrentRequests = 8;
export const requestTimeoutMs = 15_000;
export const probeRequestTimeoutMs = 150_000;

export const allowedMethods = new Set([
  'team.list', 'team.get', 'team.messages', 'team.action',
  'team.handoff.list', 'team.handoff.get',
  'run.list', 'run.get', 'run.action',
  'driver.list', 'driver.models.discover', 'driver.models.probe',
  'routing.config.get', 'routing.config.update', 'routing.availability', 'routing.catalog.effective',
]);

export interface RpcEnvelope {method: string; params: Record<string, unknown>}

export function isLoopbackAddress(address: string | undefined): boolean {
  return address === '127.0.0.1' || address === '::1' || address === '::ffff:127.0.0.1';
}

export function authorizeRequest(request: IncomingMessage, expectedHost: string, expectedOrigin: string, token: string): string | undefined {
  if (!isLoopbackAddress(request.socket.remoteAddress)) return 'loopback peer required';
  if (request.headers.host !== expectedHost) return 'invalid Host';
  if (request.headers.origin !== expectedOrigin) return 'invalid Origin';
  const prefix = 'Bearer ';
  const authorization = request.headers.authorization;
  if (!authorization?.startsWith(prefix)) return 'bearer token required';
  const supplied = Buffer.from(authorization.slice(prefix.length), 'utf8');
  const expected = Buffer.from(token, 'utf8');
  if (supplied.length !== expected.length || !timingSafeEqual(supplied, expected)) return 'invalid bearer token';
  return undefined;
}

export function decodeRpcEnvelope(data: Buffer): RpcEnvelope {
  if (data.length === 0 || data.length > maxRequestBytes) throw new Error('request body is empty or exceeds 64 KiB');
  let parsed: unknown;
  try {parsed = JSON.parse(data.toString('utf8'))} catch {throw new Error('request body must be valid JSON')}
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('request body must be an object');
  const record = parsed as Record<string, unknown>;
  if (Object.keys(record).some(key => key !== 'method' && key !== 'params')) throw new Error('request body contains an unknown field');
  if (typeof record.method !== 'string' || !allowedMethods.has(record.method)) throw new Error('method is not exposed by Fishyume Web');
  if (!record.params || typeof record.params !== 'object' || Array.isArray(record.params)) throw new Error('params must be an object');
  return {method: record.method, params: record.params as Record<string, unknown>};
}

export const securityHeaders = {
  'Cache-Control': 'no-store',
  'Content-Security-Policy': "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'",
  'Cross-Origin-Opener-Policy': 'same-origin',
  'Cross-Origin-Resource-Policy': 'same-origin',
  'Referrer-Policy': 'no-referrer',
  'X-Content-Type-Options': 'nosniff',
  'X-Frame-Options': 'DENY',
} as const;
