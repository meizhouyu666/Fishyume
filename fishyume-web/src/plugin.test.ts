import { test } from 'node:test'
import assert from 'node:assert'
import { apply, name, inject, type PluginContext } from './plugin.js'

interface FakeResponse {
  status: number
  headers: Record<string, string>
  body: string
}
function fakeResponse(): FakeResponse & { writeHead(s: number, h?: Record<string, string>): void; setHeader(k: string, v: string): void; end(b?: string | Buffer): void } {
  const state: FakeResponse = { status: 0, headers: {}, body: '' }
  return {
    ...state,
    writeHead(s, h) { state.status = s; if (h) Object.assign(state.headers, h) },
    setHeader(k, v) { state.headers[k] = v },
    end(b) { if (typeof b === 'string') state.body += b },
    get status() { return state.status },
    get headers() { return state.headers },
    get body() { return state.body },
  }
}

interface Route { kind: string; path: string; handler: (req: unknown, res: unknown) => void | Promise<void> }
function fakeWebServer(): { routes: Route[]; register(route: Route): () => void } {
  const routes: Route[] = []
  return { routes, register: (route) => { routes.push(route); return () => {} } }
}

function fakeContext(webServer: ReturnType<typeof fakeWebServer>): PluginContext {
  return {
    get: (n) => (n === 'webServer' ? webServer : undefined),
    // cordis runs the setup synchronously; mimic that so routes register.
    effect: (setup) => { setup() },
    on: () => {},
  }
}

test('plugin exports its identity and registers token/rpc/static routes', () => {
  assert.equal(name, 'dsh-fishyume')
  assert.deepEqual(inject, ['typert'])
  const server = fakeWebServer()
  apply(fakeContext(server), {})
  const paths = server.routes.map((r) => `${r.kind}:${r.path}`)
  assert.ok(paths.includes('exact:/plugins/dsh-fishyume/token'), `token route missing: ${paths.join(', ')}`)
  assert.ok(paths.includes('exact:/plugins/dsh-fishyume/api/focus'), `focus route missing: ${paths.join(', ')}`)
  assert.ok(paths.includes('exact:/plugins/dsh-fishyume/api/rpc'), `rpc route missing: ${paths.join(', ')}`)
  assert.ok(paths.includes('prefix:/plugins/dsh-fishyume/'), `static route missing: ${paths.join(', ')}`)
})

test('plugin registers the Typert host manifest when DSH provides typert', () => {
  const server = fakeWebServer()
  const manifests: unknown[] = []
  const ctx = fakeContext(server)
  const originalGet = ctx.get
  ctx.get = (name) => name === 'typert' ? { register: (manifest: unknown) => { manifests.push(manifest); return () => {} } } : originalGet(name)
  apply(ctx, {})
  assert.equal(manifests.length, 1)
  assert.equal((manifests[0] as { package?: string }).package, 'dsh-fishyume')
  assert.equal((manifests[0] as { face?: string }).face, 'host')
})

test('token route returns a JSON bearer token without touching the engine', async () => {
  const server = fakeWebServer()
  apply(fakeContext(server), {})
  const route = server.routes.find((r) => r.path === '/plugins/dsh-fishyume/token')
  assert.ok(route)
  const res = fakeResponse()
  await route!.handler({}, res)
  assert.equal(res.status, 200)
  const parsed = JSON.parse(res.body) as { token?: string }
  assert.ok(typeof parsed.token === 'string' && parsed.token.length > 0, 'token should be a non-empty string')
})

test('static route responds 200 or 404 without throwing (missing public dir is safe)', async () => {
  const server = fakeWebServer()
  apply(fakeContext(server), {})
  const route = server.routes.find((r) => r.path === '/plugins/dsh-fishyume/')
  assert.ok(route)
  const res = fakeResponse()
  await route!.handler({ url: '/plugins/dsh-fishyume/index.html' }, res)
  assert.ok(res.status === 200 || res.status === 404, `static route status = ${res.status}`)
  assert.equal(res.headers['X-Frame-Options'], 'SAMEORIGIN')
  assert.match(res.headers['Content-Security-Policy'] ?? '', /frame-ancestors 'self'/)
})

test('prefixed RPC route reaches gateway authorization with the DSH origin', async () => {
  const server = fakeWebServer()
  apply(fakeContext(server), {})
  const route = server.routes.find((r) => r.path === '/plugins/dsh-fishyume/api/rpc')
  assert.ok(route)
  const res = fakeResponse()
  const req = {
    method: 'POST',
    url: '/plugins/dsh-fishyume/api/rpc',
    headers: {host: '127.0.0.1:3080', origin: 'http://127.0.0.1:3080'},
    socket: {remoteAddress: '127.0.0.1'},
    on(event: string, cb: (...args: unknown[]) => void) {
      if (event === 'end') cb()
      return undefined
    },
  }
  await route!.handler(req, res)
  assert.equal(res.status, 403)
})

test('disabled config skips route registration', () => {
  const server = fakeWebServer()
  apply(fakeContext(server), { enabled: false })
  assert.equal(server.routes.length, 0)
})

test('focus route GET returns initial state and POST bumps revision', async () => {
  const server = fakeWebServer()
  apply(fakeContext(server), {})
  const route = server.routes.find((r) => r.path === '/plugins/dsh-fishyume/api/focus')
  assert.ok(route)

  const getRes = fakeResponse()
  await route!.handler({ method: 'GET' }, getRes)
  const initial = JSON.parse(getRes.body) as { revision?: number; target?: unknown }
  assert.equal(initial.revision, 0)
  assert.equal(initial.target, undefined)

  const postRes = fakeResponse()
  const postReq = {
    method: 'POST',
    on(event: string, cb: (...args: unknown[]) => void) {
      if (event === 'data') cb(Buffer.from(JSON.stringify({ target: { kind: 'team', teamId: 'team-1' } })))
      if (event === 'end') cb()
      return undefined
    },
    destroy: () => {},
  }
  await route!.handler(postReq, postRes)
  const updated = JSON.parse(postRes.body) as { revision?: number; target?: { kind?: string; teamId?: string } }
  assert.equal(updated.revision, 1)
  assert.equal(updated.target?.kind, 'team')
  assert.equal(updated.target?.teamId, 'team-1')
})

test('focus route POST rejects invalid target', async () => {
  const server = fakeWebServer()
  apply(fakeContext(server), {})
  const route = server.routes.find((r) => r.path === '/plugins/dsh-fishyume/api/focus')
  assert.ok(route)
  const res = fakeResponse()
  const req = {
    method: 'POST',
    on(event: string, cb: (...args: unknown[]) => void) {
      if (event === 'data') cb(Buffer.from(JSON.stringify({ target: { kind: 'team' } })))
      if (event === 'end') cb()
      return undefined
    },
    destroy: () => {},
  }
  await route!.handler(req, res)
  assert.equal(res.status, 400)
})
