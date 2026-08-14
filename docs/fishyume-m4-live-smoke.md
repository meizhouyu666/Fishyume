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

## Real parallel Driver Smoke (2026-08-14)

The same local native `codex-cli 0.147.0` passed the parallel Driver gate with
`FISHYUME_LIVE_CODEX=1` and the Fishyume sandbox forced to `read-only`. Run
`run-56bf2b78e36770c0eced51c1` completed two independent Agent Nodes in parallel,
waited for an Approval, submitted the Approval through the Application API, and
completed the dependent final Agent. The structured result contained `planA`, `planB`,
`approve`, and `finalize`, with final summary `codex-live-parallel-final`. The run used
a temporary state directory and was cleaned up; no Provider credential, prompt, or
local path was recorded here.

Repeat the gate with:

```powershell
$env:FISHYUME_LIVE_CODEX = '1'
npm --prefix wf run smoke:codex-live:parallel
```

Run the repeatable real Driver gate explicitly:

```powershell
$env:FISHYUME_LIVE_CODEX = '1'
npm --prefix wf run smoke:codex-live
```

`FISHYUME_LIVE_PROJECT` may override the read-only workspace. The script always forces
the Fishyume Driver sandbox to `read-only`, uses a temporary state directory, and shuts
down its temporary Control Plane. Without `FISHYUME_LIVE_CODEX=1` it fails before any
Provider call.

For the real parallel Driver gate, use the same opt-in after authenticating `codex-cli`:

```powershell
$env:FISHYUME_LIVE_CODEX = '1'
npm --prefix wf run smoke:codex-live:parallel
```

This runs two independent real Codex Agent Nodes at `maxConcurrency: 2`, waits for a
durable Approval, approves it through the Application API, then runs a dependent final
Agent. Every Node must be present in the structured result, and the final summary must
match the acceptance contract. The workflow is read-only and the cleanup path cancels
any non-terminal Run before shutting down the temporary Control Plane.

## Real Codex Host Agent through MCP

The opt-in Host Agent gate launches a real `codex exec --ephemeral --json` process and
configures exactly one MCP server: the local `wf mcp` stdio server. It creates a
temporary `CODEX_HOME`, copies only the locally authenticated `auth.json`, and writes
only the selected model provider plus Fishyume MCP settings. The read-only Codex
sandbox remains enforced; no user MCP servers or interactive approval prompts enter
the test. The Host Agent must call this sequence in order:

```text
system.capabilities -> workflow.validate -> workflow.explain -> run.start
-> run.events -> run.action -> run.result
```

The workflow contains only an Approval node, so the nested Fishyume Run does not make
an additional Provider call. The script records only the Codex version, Run ID, tool
sequence, sandbox mode, and cleanup status; prompts, credentials, and full JSONL are
never written to the repository.

Run it only after authenticating the local Codex CLI. The `--prefix` path is relative
to the current PowerShell directory, so use an explicit repository path when running
from outside the checkout:

```powershell
$repo = 'E:\meizhouyu\agentstudy\my-agent'
$env:FISHYUME_LIVE_CODEX = '1'
npm --prefix (Join-Path $repo 'wf') run smoke:codex-host-mcp
```

This gate is intentionally excluded from public CI. If Codex authentication or the
network is unavailable, the command reports a redacted failure and the deterministic
MCP Host Agent test remains the required CI gate. The smoke config sets Fishyume MCP
tool approval to `approve` inside the temporary home, which is the non-interactive
equivalent of explicitly allowing this selected local server; it does not change the
product's normal interactive approval policy or weaken the sandbox.

## Concurrent Host/TUI acceptance

### Installed setup plus zero-argument Dashboard record (2026-08-14)

The M4.5 product gate installed Fishyume globally from the repository-root Preview installer, registered the canonical absolute Node/installed-CLI MCP transport, and explicitly approved the nine Fishyume tools as part of the user-invoked setup action. A real `codex-cli 0.147.0` process using that installed user configuration called only `system.capabilities` and completed without an interactive MCP allow.

A separate isolated Host run then created `run-6a8bf4c7ca480b68e0f7f47f`. Instead of receiving a copied Run ID, the human PTY launched zero-argument Fishyume, saw the single waiting Run in the Dashboard, pressed `Enter` to attach, and pressed `a` to approve. The TUI reached `SUCCEEDED`; the Host received exact stale-action `conflict` from retained `stateVersion: 3` to current `stateVersion: 5`, then read terminal success. Final evidence included `dashboardHandoff: true`, `interactiveApproval: false`, and temporary cleanup. CC-Panes supplied only the Windows PTY.

Repeat the installed-MCP and Dashboard gates explicitly:

```powershell
$env:FISHYUME_LIVE_CODEX = '1'
npm --prefix wf run smoke:codex-installed-mcp
npm --prefix wf run smoke:codex-host-dashboard
```

### Real Host plus rendered PTY record (2026-08-14)

`codex-cli 0.147.0` started Run `run-b45156ced1fce99208dd2228` through the real
Fishyume MCP server and left its Approval waiting. The same Run was attached in a
CC-Panes-backed Windows PTY at 120 columns with `fishyume attach`. The rendered Calm
Operator Console showed the Approval and accepted `a`; the Run became `SUCCEEDED`.
The Host retained waiting `stateVersion: 3` from `run.get`, then submitted it after
the TUI action. The completed MCP payload reported exact `error.code: "conflict"`
with `currentStateVersion: 5`; `run.result` returned the same Run with terminal
`conclusion: "succeeded"`. The acceptance runner detached the terminal observer, and
the temporary Control Plane and Codex home were removed. CC-Panes supplied only the terminal PTY; it was not a
Fishyume backend, state owner, or product dependency.

Repeat the explicit local gate from a real terminal with:

```powershell
$env:FISHYUME_LIVE_CODEX = '1'
npm --prefix wf run smoke:codex-host-pty
```

When the Approval is rendered, press `a`; after the terminal result and host-complete
marker appear, press `q`. The final JSON must report `ptyHandoff: true`,
`conflictCode: "conflict"`, matching retained/current versions,
`resultConclusion: "succeeded"`, `sandbox: "read-only"`, and temporary cleanup.
For an operator-driven PTY gate that should close itself after the Host reaches the
terminal result, use `npm --prefix wf run smoke:codex-host-pty:auto`; approval still
comes from the rendered terminal, while detach is deterministic.

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

## Control Plane crash/restart acceptance

The deterministic acceptance test `wf/src/integration/control-plane-restart.test.ts`
starts an Application Run with a durable Approval action and an active fake Codex
Attempt, force-terminates the temporary Control Plane, reconnects through a fresh
EngineBridge, and verifies that the action receipt replays, the active Attempt remains
Attempt 1, and cancellation through the replacement service reaches a terminal Run.

Run it from the repository root:

```powershell
npm --prefix wf run test:restart
```

The test uses a temporary state directory and fake Agent, never a Provider or Docker.
