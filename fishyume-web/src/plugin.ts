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
import { EngineBridge } from '../../wf/src/bridge/engine.js'
import { createGatewayHandler } from './gateway.js'
import { securityHeaders } from './security.js'

/** Structural slice of the DSH web server service (kind/path route registration). */
interface WebRouteHost {
  register(route: {
    kind: 'exact' | 'prefix'
    path: string
    handler: (req: IncomingMessage, res: ServerResponse) => void | Promise<void>
  }): () => void
}

export const name = 'dsh-fishyume'
// No required services: the web server is discovered lazily so headless
// profiles keep the plugin loaded without a route surface.
export const inject: string[] = []

/** Minimal structural slice of the cordis Context the host plane uses. Kept
 *  local so the plugin typechecks without @deepseek-ai devDependencies; the
 *  real types are provided as peerDependencies at install time. */
export interface PluginContext {
  get<T = unknown>(name: string): T | undefined
  effect(setup: () => void | (() => void), label?: string): void
  on(event: string, listener: (name: string) => void): void
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
  const engine = new EngineBridge(process.env.FISHYUME_ENGINE_PATH)
  // P1 TODO: host/origin must be DSH's own (127.0.0.1:<port>), not the
  // standalone sidecar's. Capture lazily from the first request until the web
  // server service exposes its canonical origin.
  let host = ''
  let origin = ''
  const gateway = createGatewayHandler(engine, () => ({ host, origin, token }))

  let registered = false
  const registerWebSurface = (): void => {
    if (registered) return
    const webServer = ctx.get('webServer') as WebRouteHost | undefined
    if (webServer === undefined) return
    registered = true

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

    // JSON-RPC gateway (POST /api/rpc) — the Fishyume Application/Team API.
    ctx.effect(() => webServer.register({
      kind: 'exact',
      path: '/plugins/dsh-fishyume/api/rpc',
      handler: (req, res) => { void gateway(req, res) },
    }), 'dsh-fishyume: rpc route')

    // Static client files (index.html / app.js / styles.css).
    const publicDir = join(dirname(fileURLToPath(import.meta.url)), '..', config.publicDir ?? 'dist/public')
    ctx.effect(() => webServer.register({
      kind: 'prefix',
      path: '/plugins/dsh-fishyume/',
      handler: async (req, res) => {
        for (const [name, value] of Object.entries(securityHeaders)) res.setHeader(name, value)
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
  ctx.on('internal/service', (service) => {
    if (service === 'webServer') registerWebSurface()
  })
}
