import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createConnectionTransport, createHttpTransport, createRemoteTransport, type RpcTransport } from './client/transport.js'

test('HTTP transport authenticates once and decodes JSON-RPC results', async () => {
  const requests: RequestInit[] = []
  const transport = createHttpTransport({
    rpcPath: '/rpc',
    tokenPath: '/token',
    fetcher: (async (input, init) => {
      requests.push(init ?? {})
      if (String(input) === '/token') return new Response(JSON.stringify({ token: 'secret' }), { status: 200 })
      return new Response(JSON.stringify({ result: { ok: true } }), { status: 200 })
    }) as typeof fetch,
  })
  assert.deepEqual(await transport.call('team.list', {}), { ok: true })
  assert.deepEqual(await transport.call('run.list', {}), { ok: true })
  assert.equal(requests.filter((request) => request.method === 'POST').length, 2)
  assert.equal(requests.filter((request) => request.method !== 'POST').length, 1)
})

test('connection transport falls back when the host channel is unavailable', async () => {
  let fallbackCalls = 0
  const fallback: RpcTransport = { async call<T>() { fallbackCalls += 1; return { value: 1 } as T }, reset() {} }
  const transport = createConnectionTransport({ rpc: { async call() { throw new Error('missing channel') } } }, fallback)
  assert.deepEqual(await transport.call('team.list', {}), { value: 1 })
  assert.equal(fallbackCalls, 1)
})

test('remote transport unwraps a successful Typert result', async () => {
  const fallback: RpcTransport = { async call() { throw new Error('fallback should not run') }, reset() {} }
  const transport = createRemoteTransport({ async call(method, params) { return { ok: true, value: { method, params } } } }, fallback)
  assert.deepEqual(await transport.call('team.list', { limit: 1 }), { method: 'team.list', params: { limit: 1 } })
})

test('remote transport preserves a Typert business error', async () => {
  const fallback: RpcTransport = { async call() { throw new Error('fallback should not run') }, reset() {} }
  const transport = createRemoteTransport({ async call() { return { ok: false, error: { message: 'run conflict' } } } }, fallback)
  await assert.rejects(() => transport.call('run.action', {}), /run conflict/)
})
