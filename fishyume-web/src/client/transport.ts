/** Client transport boundary shared by the native DSH workspace and tests. */

export interface RpcTransport {
  call<T>(method: string, params: Record<string, unknown>): Promise<T>
  reset(): void
}

export interface RpcConnection {
  rpc: {
    call(channel: string, endpoint: string, payload: unknown): Promise<unknown>
  }
}

export interface RpcRemote {
  call(method: string, params?: unknown): Promise<unknown>
}

export function createHttpTransport(options: {
  rpcPath: string
  tokenPath: string
  fetcher?: typeof fetch
}): RpcTransport {
  const fetcher = options.fetcher ?? fetch
  let token: string | undefined
  const getToken = async (): Promise<string> => {
    if (token) return token
    const response = await fetcher(options.tokenPath, { cache: 'no-store' })
    const data = await response.json() as { token?: string }
    if (!response.ok || !data.token) throw new Error('Fishyume token unavailable')
    token = data.token
    return token
  }
  return {
    async call<T>(method: string, params: Record<string, unknown>) {
      const response = await fetcher(options.rpcPath, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${await getToken()}` },
        body: JSON.stringify({ method, params }),
      })
      const payload = await response.json() as { result?: T; error?: { message?: string } }
      if (!response.ok || payload.error) throw new Error(payload.error?.message ?? `Request failed (${response.status})`)
      return payload.result as T
    },
    reset() { token = undefined },
  }
}

/**
 * Prefer a DSH RPC channel when the host provides one, then fall back to the
 * authenticated same-origin gateway used by the standalone client. The
 * fallback is intentionally structural so this package does not need to
 * bundle a second copy of the DSH connection runtime.
 */
export function createConnectionTransport(connection: RpcConnection, fallback: RpcTransport, channel = 'dsh-fishyume'): RpcTransport {
  return {
    async call<T>(method: string, params: Record<string, unknown>) {
      try {
        const result = await connection.rpc.call(channel, 'call', { method, params }) as { ok?: boolean; value?: T; error?: { message?: string } }
        if (result?.ok) return result.value as T
        if (result?.error) throw new Error(result.error.message ?? 'Fishyume RPC failed')
      } catch {
        // Older hosts do not expose a Fishyume channel yet; use the gateway.
      }
      return fallback.call<T>(method, params)
    },
    reset() { fallback.reset() },
  }
}

/** Adapt a mounted DSH Typert Remote to the same domain transport interface. */
export function createRemoteTransport(remote: RpcRemote, fallback: RpcTransport): RpcTransport {
  return {
    async call<T>(method: string, params: Record<string, unknown>) {
      const result = await remote.call(method, params) as { ok?: boolean; value?: T; error?: { message?: string } } | T
      if (result && typeof result === 'object' && 'ok' in result) {
        const envelope = result as { ok?: boolean; value?: T; error?: { message?: string } }
        if (envelope.ok === true) return envelope.value as T
        if (envelope.error) throw new Error(envelope.error.message ?? 'Fishyume Remote failed')
        throw new Error('Fishyume Remote returned an invalid response')
      }
      return result as T
    },
    reset() { fallback.reset() },
  }
}
