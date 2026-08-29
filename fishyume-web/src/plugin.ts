/**
 * dsh-fishyume host plane: register the Fishyume Web gateway and static client
 * routes on the shared DSH web server, so the Fishyume Team/Run console is
 * served from inside DSH instead of a standalone loopback sidecar.
 *
 * Mirrors the standalone `fishyume-web` sidecar (server.ts) but hosts the same
 * gateway + client under DSH's `webServer` service. The plugin is inert in
 * headless profiles that do not mount a web server.
 */
import type { IncomingMessage, ServerResponse } from 'node:http'
import { randomBytes } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { EngineBridge, type EngineClient } from '../../wf/src/bridge/engine.js'
import { createGatewayHandler, type EngineGateway } from './gateway.js'
import { FishyumeRemote } from './remote.js'
import { FISHYUME_TYPERT_MANIFEST } from './remote-contract.js'
import { securityHeaders } from './security.js'

/** Structural slice of the DSH web server service (kind/path route registration). */
interface WebRouteHost {
  register(route: {
    kind: 'exact' | 'prefix'
    path: string
    handler: (req: IncomingMessage, res: ServerResponse) => void | Promise<void>
  }): () => void
}

/** Focus target the embedded console should open (team/handoff/run). */
export type WebTarget =
  | { kind: 'team'; teamId: string }
  | { kind: 'handoff'; teamId: string; handoffId: string }
  | { kind: 'run'; runId: string }

function parseTarget(value: unknown): WebTarget | undefined {
  if (!value || typeof value !== 'object') return undefined
  const target = value as Record<string, unknown>
  if (target.kind === 'team' && typeof target.teamId === 'string' && target.teamId.length > 0 && target.teamId.length <= 128) return { kind: 'team', teamId: target.teamId }
  if (target.kind === 'handoff' && typeof target.teamId === 'string' && typeof target.handoffId === 'string' && target.teamId.length > 0 && target.handoffId.length > 0 && target.teamId.length <= 128 && target.handoffId.length <= 128) return { kind: 'handoff', teamId: target.teamId, handoffId: target.handoffId }
  if (target.kind === 'run' && typeof target.runId === 'string' && target.runId.length > 0 && target.runId.length <= 128) return { kind: 'run', runId: target.runId }
  return undefined
}

/** URL fragment the iframe uses to focus on a target. */
export function targetFragment(target?: WebTarget): string {
  if (!target) return ''
  const values = new URLSearchParams({ targetKind: target.kind })
  if (target.kind === 'team') values.set('teamId', target.teamId)
  if (target.kind === 'handoff') { values.set('teamId', target.teamId); values.set('handoffId', target.handoffId) }
  if (target.kind === 'run') values.set('runId', target.runId)
  return `&${values.toString()}`
}

function writeJSON(res: ServerResponse, status: number, value: unknown): void {
  res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8' })
  res.end(JSON.stringify(value))
}

function readBody(req: IncomingMessage, maxBytes: number): Promise<string> {
  return new Promise((resolve, reject) => {
    const chunks: Buffer[] = []
    let size = 0
    req.on('data', (chunk) => {
      size += Buffer.byteLength(chunk)
      if (size > maxBytes) { reject(new Error('body too large')); req.destroy(); return }
      chunks.push(Buffer.from(chunk))
    })
    req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')))
    req.on('error', reject)
  })
}

export const name = 'dsh-fishyume'
// No required services: the web server is discovered lazily so headless
// profiles keep the plugin loaded without a route surface.
// Typert is required for the native DSH client contract. webServer remains
// lazy so the same plugin can still load in headless profiles.
export const inject = ['typert']

/** Minimal structural slice of the cordis Context the host plane uses. Kept
 *  local so the plugin typechecks without @deepseek-ai devDependencies; the
 *  real types are provided as peerDependencies at install time. */
export interface PluginContext {
  get(name: string): unknown
  effect(setup: () => void | (() => void), label?: string): void
  on(event: string, listener: (name: string) => void): void
}

interface TypertHost {
  register(manifest: unknown): () => void
}

export interface Config {
  /** Disable the embedded console entirely (default true). */
  enabled?: boolean
  /** Static client directory, relative to this package (default dist/public). */
  publicDir?: string
}

export function apply(ctx: PluginContext, config: Config): void {
  if (config.enabled === false) return

  const token = randomBytes(32).toString('base64url')
  // Focus target the panel should open; bumped by the /api/focus route.
  let focus: { target?: WebTarget; revision: number } = { target: undefined, revision: 0 }
  // Lazy engine: the control plane is launched only when the first RPC arrives,
  // so mounting the plugin has no boot-time side effect and stays unit-testable.
  let engine: EngineClient | undefined
  const engineGateway: EngineGateway = {
    async call<T>(method: string, params?: unknown): Promise<T> {
      engine ??= new EngineBridge(process.env.FISHYUME_ENGINE_PATH)
      return engine.call<T>(method, params)
    },
  }
  // P1 TODO: host/origin must be DSH's own (127.0.0.1:<port>), not the
  // standalone sidecar's. Capture lazily from the first request until the web
  // server service exposes its canonical origin.
  let host = ''
  let origin = ''
  const gateway = createGatewayHandler(engineGateway, () => ({ host, origin, token }), undefined, '/plugins/dsh-fishyume/api/rpc')

  // Native DSH clients use this Typert Remote. The gateway remains available
  // for the standalone client and as a practical fallback on older profiles.
  let remote: FishyumeRemote | undefined
  let remoteManifestDispose: (() => void) | undefined
  const registerRemote = (): void => {
    if (remote !== undefined) return
    const typert = ctx.get('typert') as TypertHost | undefined
    if (typert === undefined) return
    remote = new FishyumeRemote(ctx, engineGateway)
    remoteManifestDispose = typert.register(FISHYUME_TYPERT_MANIFEST)
  }

  let registered = false
  const registerWebSurface = (): void => {
    if (registered) return
    const webServer = ctx.get('webServer') as WebRouteHost | undefined
    if (webServer === undefined) return
    registered = true

    // The DSH web server is the canonical origin for the embedded console.
    // Capture it from the first request because the public service shape does
    // not expose a stable origin across DSH releases.
    const captureIdentity = (req: IncomingMessage): void => {
      if (host !== '') return
      const requestHost = req.headers?.host
      if (!requestHost) return
      host = requestHost
      origin = req.headers?.origin ?? `http://${requestHost}`
    }

    // Public client token (same-origin; the gateway still requires the Bearer
    // token on /api/rpc, so this route only helps the panel build its iframe
    // src and never exposes business state by itself).
    ctx.effect(() => webServer.register({
      kind: 'exact',
      path: '/plugins/dsh-fishyume/token',
      handler: (_req, res) => {
        res.writeHead(200, { 'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store' })
        res.end(JSON.stringify({ token }))
      },
    }), 'dsh-fishyume: token route')

    // Focus target (team/handoff/run): GET returns the current target, POST
    // sets it and bumps the revision so the panel can refocus its iframe.
    ctx.effect(() => webServer.register({
      kind: 'exact',
      path: '/plugins/dsh-fishyume/api/focus',
      handler: async (req, res) => {
        if (req.method !== 'GET' && req.method !== 'POST') { res.writeHead(405); res.end(); return }
        if (req.method === 'GET') { writeJSON(res, 200, focus); return }
        let body: string
        try { body = await readBody(req, 8 * 1024) } catch { res.writeHead(400); res.end(); return }
        let parsed: { target?: unknown }
        try { parsed = JSON.parse(body) as { target?: unknown } } catch { res.writeHead(400); res.end(); return }
        if (parsed.target === undefined) { writeJSON(res, 200, focus); return }
        const target = parseTarget(parsed.target)
        if (target === undefined) { writeJSON(res, 400, { error: { code: 'invalid_target', message: 'invalid Web focus target' } }); return }
        focus = { target, revision: focus.revision + 1 }
        writeJSON(res, 200, focus)
      },
    }), 'dsh-fishyume: focus route')

    // JSON-RPC gateway (POST /api/rpc) — the Fishyume Application/Team API.
    ctx.effect(() => webServer.register({
      kind: 'exact',
      path: '/plugins/dsh-fishyume/api/rpc',
      handler: async (req, res) => { captureIdentity(req); await gateway(req, res) },
    }), 'dsh-fishyume: rpc route')

    // Static client files (index.html / app.js / styles.css).
    const publicDir = join(dirname(fileURLToPath(import.meta.url)), '..', config.publicDir ?? 'dist/public')
    ctx.effect(() => webServer.register({
      kind: 'prefix',
      path: '/plugins/dsh-fishyume/',
      handler: async (req, res) => {
        captureIdentity(req)
        for (const [name, value] of Object.entries(securityHeaders)) {
          if (name === 'Content-Security-Policy' || name === 'X-Frame-Options') continue
          res.setHeader(name, value)
        }
        // This page is intentionally loaded by the DSH shell's same-origin
        // iframe; the standalone sidecar keeps the stricter frame-deny policy.
        res.setHeader('Content-Security-Policy', securityHeaders['Content-Security-Policy'].replace("frame-ancestors 'none'", "frame-ancestors 'self'"))
        res.setHeader('X-Frame-Options', 'SAMEORIGIN')
        const path = (new URL(req.url ?? '/', 'http://x').pathname).replace(/^\/plugins\/dsh-fishyume\//, '') || 'index.html'
        if (path.includes('..') || path.includes('\\')) { res.writeHead(404); res.end(); return }
        try {
          const body = await readFile(join(publicDir, path))
          const type = path.endsWith('.js') ? 'text/javascript; charset=utf-8' : path.endsWith('.css') ? 'text/css; charset=utf-8' : 'text/html; charset=utf-8'
          res.writeHead(200, { 'Content-Type': type, 'Cache-Control': 'no-store' })
          res.end(body)
        } catch {
          res.writeHead(404); res.end()
        }
      },
    }), 'dsh-fishyume: static route')
  }

  registerWebSurface()
  registerRemote()
  ctx.on('internal/service', (service) => {
    if (service === 'webServer') registerWebSurface()
    if (service === 'typert') registerRemote()
  })
  ctx.effect(() => () => {
    remoteManifestDispose?.()
    remoteManifestDispose = undefined
    remote = undefined
  }, 'dsh-fishyume: remote manifest')
}
