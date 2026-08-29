import { createRequire } from 'node:module'
import type { EngineGateway } from './gateway.js'

interface TypertBase {
  new (ctx: unknown, name: string): { }
}

interface TypertModule {
  TypertRemoteService?: TypertBase
}

const optionalTypert: TypertModule = (() => {
  const candidates = [
    createRequire(import.meta.url),
    // DSH loads linked plugins from a profile directory. Resolve peer
    // dependencies from that runtime cwd when the package's real path has no
    // local node_modules directory.
    createRequire(`${process.cwd()}/package.json`),
  ]
  for (const require of candidates) {
    try {
      return require('@deepseek-ai/dsh-typert-protocol') as TypertModule
    } catch {
      // Try the next resolution root.
    }
  }
  return {}
})()

/**
 * Keep the package runnable from the source checkout as well as from a DSH
 * profile.  The bundled plugin is resolved from the checkout, so its optional
 * peer may not be visible to createRequire even though DSH itself has it.  A
 * minimal Cordis-compatible fallback still needs to register the service;
 * otherwise Typert can accept the descriptor but cannot resolve its receiver.
 */
const TypertRemoteService = optionalTypert.TypertRemoteService ?? class {
  readonly ctx: { provide?: (name: string, value: unknown) => unknown }
  readonly name: string
  readonly typertRemote: { service: unknown; serviceKey: string; namespace: string }

  constructor(ctx: unknown, name: string) {
    this.ctx = ctx as { provide?: (name: string, value: unknown) => unknown }
    this.name = name
    this.typertRemote = Object.freeze({ service: this, serviceKey: name, namespace: name })
    this.ctx.provide?.(name, this)
  }
}

/** Host-side Typert Remote that forwards the existing Fishyume RPC contract. */
export class FishyumeRemote extends TypertRemoteService {
  constructor(ctx: unknown, private readonly engine: EngineGateway) {
    super(ctx, 'fishyume')
  }

  async call(method: string, params?: unknown): Promise<unknown> {
    return this.engine.call(method, params)
  }
}
