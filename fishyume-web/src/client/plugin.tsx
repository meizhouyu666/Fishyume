/**
 * dsh-fishyume client plane: mount an iframe panel into `shell.overlay` that
 * loads the Fishyume console served from the plugin's own host routes.
 */
import type { ClientContext } from '@deepseek-ai/dsh-client-runtime/client'
// Type-only: the frame-level overlay is declared by ui-layout.
import type {} from '@deepseek-ai/dsh-client-ui-layout/client'
import { useEffect, useState } from 'react'

export const inject = ['slots']

function FishyumeConsole(): JSX.Element {
  const [token, setToken] = useState<string>('')

  useEffect(() => {
    let cancelled = false
    void fetch('/plugins/dsh-fishyume/token')
      .then((response) => response.json())
      .then((data: { token?: string }) => {
        if (!cancelled) setToken(data.token ?? '')
      })
      .catch(() => {
        if (!cancelled) setToken('')
      })
    return () => { cancelled = true }
  }, [])

  if (token === '') {
    return <div style={{ padding: '12px', fontSize: '12px', opacity: 0.6 }}>Fishyume console…</div>
  }
  return (
    <iframe
      src={`/plugins/dsh-fishyume/#token=${encodeURIComponent(token)}`}
      style={{ width: '100%', height: '100%', border: 0, background: 'transparent' }}
      title="Fishyume console"
    />
  )
}

export function apply(ctx: ClientContext): void {
  ctx.slots.inject('shell.overlay', () => ctx.slots.register({
    name: 'shell.overlay',
    id: 'dsh-fishyume-console',
    order: 90,
    label: 'Fishyume console',
  }, FishyumeConsole))
}
