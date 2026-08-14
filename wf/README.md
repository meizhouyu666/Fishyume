# Fishyume CLI

Install `fishyume` to get the primary `fishyume` command and compatible `wf` alias. Installation does not download executable code; matching platform Engine packages are optional exact-version dependencies.

Fishyume `0.2.1-alpha.1` implements the M4.0-M4.3 Agent-native Control Plane. A Host Agent calls the same Application API through MCP (`fishyume mcp`) or the one-response Machine CLI (`fishyume machine`). Humans attach the Calm Operator TUI to the same durable Run with `fishyume attach <run-id>` or `fishyume status <run-id> --watch`. The headless `codex` Driver executes Agent Attempts on the `local` target. New Runs do not require or use CC-Panes.

CLI/TUI clients discover or detached-start the user-level service, then connect over a Windows Named Pipe or Linux/macOS Unix Domain Socket. Closing a client does not stop a Run. The Control Plane owns durable state, serializes mutations, and reconciles persisted Attempts before scheduling after restart. Direct `wf-engine` invocation retains stdio JSON-RPC for tests and controlled embedding.

## Start and observe a Run

```powershell
fishyume doctor --driver codex --project "E:\project"
fishyume run --driver codex --target local --project "E:\project" "Implement the requested change"
fishyume attach <run-id>
```

For a Workflow file, use canonical Agent selection:

```yaml
defaults:
  agent:
    driver: codex
    target: local
```

`execution.maxConcurrency` provides bounded parallel Agent execution. The effective capacity is bounded by the Workflow request, Driver capability, and Fishyume safety ceiling. Only structured Results complete Attempts, and cancellation requires Driver confirmation against the persisted process fingerprint.

## Agent interfaces

The MCP server and Machine CLI expose only the current Application API: `system.capabilities`, `workflow.validate`, `workflow.explain`, `run.start`, `run.list`, `run.get`, `run.events`, `run.action`, and `run.result`.

```powershell
fishyume machine system.capabilities --params '{}'
fishyume machine run.get --params '{"runId":"<run-id>"}'
fishyume mcp
```

Call `system.capabilities` before authoring automation. `run.start` is idempotent by caller-owned `clientRequestId`; `run.action` requires a unique `actionId` and the observed `stateVersion`. Event reads and responses are bounded. Machine output is one Application response JSON object; MCP returns the same response types.

## Human console

The TUI presents one Run, compact Workflow rows, one selected-node detail view, and only currently valid actions. `a` approves or answers, `r` rejects, `R` retries, and `c` cancels after confirmation. `d`, `q`, and `Ctrl+C` disconnect observation without pausing or cancelling the Run. Non-TTY/CI output remains line-oriented; `status --watch` requires an interactive terminal, and `status --json` emits one object.

The console supports 80/120/160 columns, CJK display width, TrueColor through monochrome, `NO_COLOR`, and ASCII fallback through `TERM=dumb` or `FISHYUME_ASCII=1`.

## Compatibility window

Deprecated CLI `--backend/--tool/--runtime`, Workflow `defaults.backend/tool/runtime` and node `tool/runtime`, plus `FISHYUME_BACKEND`, remain compatibility inputs and emit warnings. `direct` normalizes to Driver `codex`; the supported current target is `local`. Historical CC-Panes snapshots remain readable for status and receive an explicit diagnostic if their old execution cannot be recovered. Compatibility does not destructively migrate or delete stored state.

Use `--driver/--target` and `defaults.agent.driver/target` for all new automation. See [`../docs/fishyume-m4-migration-guide.md`](../docs/fishyume-m4-migration-guide.md) for exact mappings and conflict behavior.

M4 is technically closed: the product/migration release surface, deterministic and real Codex MCP Host flows, local real-Codex single-node and parallel Driver smokes, rendered Host/PTY stale-action conflict, two-client MCP Host/TUI-controller acceptance, Provider-independent Application crash/restart, independent review, and all public CI jobs passed. A real Provider crash record remains optional supplemental evidence. No package publication or GitHub Release was performed, and public CI never requires Provider credentials. M5 is active only at the Context Engineering & Memory contract/evaluation entry point.
