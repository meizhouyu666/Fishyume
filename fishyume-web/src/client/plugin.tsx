/**
 * dsh-fishyume client plane: mount an iframe panel into `shell.overlay` that
 * loads the Fishyume console served from the plugin's own host routes.
 */
import { useEffect, useState } from 'react'

export const inject = ['slots']

/** Minimal structural slice of the DSH client context the client plane uses.
 *  Kept local so the plugin typechecks without @deepseek-ai devDependencies. */
interface ClientSlots {
  inject(slot: string, register: () => void): void
  register(options: { name: string; id: string; order?: number; label?: string }, Component: () => JSX.Element): void
}
export interface ClientContext {
  slots: ClientSlots
}

function targetFragment(target: { kind: string; teamId?: string; handoffId?: string; runId?: string } | undefined): string {
  if (!target) return ''
  const values = new URLSearchParams({ targetKind: target.kind })
  if (target.kind === 'team' && target.teamId) values.set('teamId', target.teamId)
  if (target.kind === 'handoff' && target.teamId && target.handoffId) { values.set('teamId', target.teamId); values.set('handoffId', target.handoffId) }
  if (target.kind === 'run' && target.runId) values.set('runId', target.runId)
  return `&${values.toString()}`
}

function FishyumeConsole(): JSX.Element {
  const [token, setToken] = useState<string>('')
  const [fragment, setFragment] = useState<string>('')
  const [focusRevision, setFocusRevision] = useState<number>(-1)

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

  // Poll the focus target; a revision bump refocuses the iframe (web.open).
  useEffect(() => {
    let cancelled = false
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = (): void => {
      void fetch('/plugins/dsh-fishyume/api/focus')
        .then((response) => response.json())
        .then((data: { target?: { kind: string; teamId?: string; handoffId?: string; runId?: string }; revision?: number }) => {
          if (cancelled) return
          const revision = data.revision ?? 0
          if (revision !== focusRevision) {
            setFocusRevision(revision)
            setFragment(targetFragment(data.target))
          }
        })
        .catch(() => { /* keep last successful snapshot */ })
        .finally(() => {
          if (!cancelled) timer = setTimeout(poll, 2000)
        })
    }
    poll()
    return () => { cancelled = true; if (timer !== undefined) clearTimeout(timer) }
  }, [focusRevision])

  if (token === '') {
    return <div style={{ padding: '12px', fontSize: '12px', opacity: 0.6 }}>Fishyume console…</div>
  }
  return (
    <iframe
      src={`/plugins/dsh-fishyume/#token=${encodeURIComponent(token)}${fragment}`}
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
