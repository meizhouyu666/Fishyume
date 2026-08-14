# M4 Host Agent Smoke

The automated acceptance test `wf/src/integration/mcp-host-agent.test.ts` exercises the
Agent-facing path end to end without Provider credentials:

```text
MCP Client (Host Agent) -> MCP adapter -> local Control Plane -> Application Service
  -> Codex Driver -> repository fake Agent
```

It verifies `system.capabilities`, `workflow.validate`, `workflow.explain`, idempotent
`run.start`, `run.events`, Approval, `needs_input`/`answer`, and terminal `run.result`.
The fake Agent is only a deterministic protocol fixture; it does not claim that a real
Codex Provider login has passed.

Run it from the repository root:

```powershell
npm --prefix wf run test:mcp-host
```

For the real Host Agent/Provider gate, keep the same request sequence and replace the
fake Agent with a locally authenticated `codex exec --ephemeral --json` installation.
That gate is manual and must record the Codex version, project path, Driver readiness,
the exact Workflow, event/result transcript, and whether the Control Plane was reused
or started fresh. It must never be required by public CI or silently fall back to an
interactive TUI/MCP approval.

## Real Codex Driver Smoke (2026-08-13)

The local native Codex executable was verified with `codex-cli 0.147.0` using
`--ephemeral --sandbox read-only --json --output-schema`. The complete Engine path was
then exercised with a temporary `wf-engine` binary, `FISHYUME_CODEX_PATH` pointing to
that native executable, and the TypeScript `EngineBridge` calling the Application API.
The one-node Workflow returned `conclusion=succeeded` with summary
`codex-engine-live-smoke`; no TUI, dangerous bypass flag, or manual MCP allow was used.

This is an opt-in local acceptance result, not a public CI guarantee: it depends on a
locally installed and authenticated Codex CLI. The deterministic fake-Agent MCP smoke
remains the reproducible CI-safe gate.

Run the repeatable real Driver gate explicitly:

```powershell
$env:FISHYUME_LIVE_CODEX = '1'
npm --prefix wf run smoke:codex-live
```

`FISHYUME_LIVE_PROJECT` may override the read-only workspace. The script always forces
the Fishyume Driver sandbox to `read-only`, uses a temporary state directory, and shuts
down its temporary Control Plane. Without `FISHYUME_LIVE_CODEX=1` it fails before any
Provider call.

## Concurrent Host/TUI acceptance

The deterministic acceptance test `wf/src/integration/mcp-tui-concurrency.test.ts`
connects an MCP SDK Host client and the real `LiveConsoleController` through two
independent Engine connections to one temporary Control Plane and Run. Both clients
submit an Approval from the same observed `stateVersion`; exactly one succeeds and the
other receives `conflict`. The TUI then converges on the Host-observed state, detaches,
and closes its connection while the fake Codex Agent remains active. The Host confirms
that the Run is still running and not cancellation-requested, explicitly cancels it,
and verifies monotonic events and a single Agent Attempt.

Run this Provider-independent gate from the repository root:

```powershell
npm --prefix wf run test:mcp-tui
```

This exercises the production MCP adapter, Application API, Local Control Plane,
EngineBridge, and TUI controller/action binding. It does not render a terminal or make
a Provider call, so PTY appearance and real Host-model tool selection remain separate
manual concerns.
