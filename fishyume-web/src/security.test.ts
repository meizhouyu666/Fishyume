import assert from 'node:assert/strict';
import {EventEmitter} from 'node:events';
import test from 'node:test';
import type {IncomingMessage} from 'node:http';
import {allowedMethods, authorizeRequest, decodeRpcEnvelope, isLoopbackAddress, maxRequestBytes, securityHeaders} from './security.js';

function request(headers: Record<string, string>, address = '127.0.0.1'): IncomingMessage {
  const value = new EventEmitter() as IncomingMessage;
  Object.assign(value, {headers, socket: {remoteAddress: address}});
  return value;
}

test('loopback peer validation rejects routable and ambiguous addresses', () => {
  assert.equal(isLoopbackAddress('127.0.0.1'), true);
  assert.equal(isLoopbackAddress('::ffff:127.0.0.1'), true);
  assert.equal(isLoopbackAddress('0.0.0.0'), false);
  assert.equal(isLoopbackAddress('192.168.1.10'), false);
  assert.equal(isLoopbackAddress(undefined), false);
});

test('API authorization requires exact peer, Host, Origin, and bearer token', () => {
  const headers = {host: '127.0.0.1:4312', origin: 'http://127.0.0.1:4312', authorization: 'Bearer secret'};
  assert.equal(authorizeRequest(request(headers), headers.host, headers.origin, 'secret'), undefined);
  assert.match(authorizeRequest(request({...headers, host: 'localhost:4312'}), headers.host, headers.origin, 'secret') ?? '', /Host/);
  assert.match(authorizeRequest(request({...headers, origin: 'http://evil.invalid'}), headers.host, headers.origin, 'secret') ?? '', /Origin/);
  assert.match(authorizeRequest(request({...headers, authorization: 'Bearer wrong'}), headers.host, headers.origin, 'secret') ?? '', /token/);
  assert.match(authorizeRequest(request(headers, '10.0.0.2'), headers.host, headers.origin, 'secret') ?? '', /loopback/);
});

test('RPC envelope is strict, bounded, and method allowlisted', () => {
  const valid = decodeRpcEnvelope(Buffer.from(JSON.stringify({method: 'team.list', params: {schemaVersion: 'fishyume.team/v1'}})));
  assert.equal(valid.method, 'team.list');
  assert.equal(allowedMethods.has('run.action'), true);
  assert.equal(allowedMethods.has('run.events'), true);
  assert.equal(allowedMethods.has('routing.config.update'), true);
  assert.equal(allowedMethods.has('driver.models.probe'), true);
  assert.equal(allowedMethods.has('team.routes.refresh'), true);
  assert.equal(allowedMethods.has('team.events'), true);
  assert.equal(allowedMethods.has('team.routes.refresh'), true);
  assert.equal(allowedMethods.has('team.template.list'), true);
  assert.equal(allowedMethods.has('team.template.upsert'), true);
  assert.equal(allowedMethods.has('run.start'), false);
  assert.equal(allowedMethods.has('team.handoff.create'), false);
  assert.equal(allowedMethods.has('team.handoff.bindRun'), false);
  assert.throws(() => decodeRpcEnvelope(Buffer.from('{')), /valid JSON/);
  assert.throws(() => decodeRpcEnvelope(Buffer.from(JSON.stringify({method: 'run.start', params: {}}))), /not exposed/);
  assert.throws(() => decodeRpcEnvelope(Buffer.from(JSON.stringify({method: 'team.list', params: {}, extra: true}))), /unknown field/);
  assert.throws(() => decodeRpcEnvelope(Buffer.alloc(maxRequestBytes + 1, 0x61)), /64 KiB/);
});

test('static security policy forbids remote code, framing, caching, and referrers', () => {
  assert.match(securityHeaders['Content-Security-Policy'], /default-src 'self'/);
  assert.match(securityHeaders['Content-Security-Policy'], /frame-ancestors 'none'/);
  assert.equal(securityHeaders['Cache-Control'], 'no-store');
  assert.equal(securityHeaders['Referrer-Policy'], 'no-referrer');
  assert.equal('Access-Control-Allow-Origin' in securityHeaders, false);
});
