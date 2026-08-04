# wf — Workflow Engine M1

`wf` is a local-first, single-agent workflow runner. The TypeScript CLI starts a headless Go engine over NDJSON JSON-RPC 2.0. Go owns orchestration, state transitions, persistence, and the CC-Panes backend; TypeScript only handles command parsing, process bridging, and terminal rendering.

## M1 capabilities

- `wf doctor` checks engine startup, protocol compatibility, `cc-panes-ctl` discovery, release orchestrator/daemon readiness, and optional project registration.
- `wf run` launches one Codex, Claude, or OpenCode session in a registered CC-Panes project.
- TaskBinding terminal state is authoritative. Idle terminal output is never treated as success.
- Run snapshots, ordered events, and recent agent output are persisted outside the target project.
- First Ctrl+C detaches and leaves the CC-Panes session running.

M1 does not implement DAGs, planner agents, resume, automatic retry, model fallback, SQLite, precise token enforcement, deployment, commits, or pull requests.

## Prerequisites

- Go 1.26 or newer
- Node.js 24 or newer and npm
- A release CC-Panes instance with its orchestrator and daemon ready
- A project registered in CC-Panes
- The launched worker must be able to update the TaskBinding supplied in its prompt

## Development build

```powershell
cd wf-engine
go test ./...
go vet ./...
go build ./cmd/wf-engine

cd ..\wf
npm install
npm run typecheck
npm test
npm run build
```

For local CLI use, point the TypeScript bridge at the built engine:

```powershell
$env:WF_ENGINE_PATH = "E:\path\to\my-agent\wf-engine\wf-engine.exe"
node .\wf\dist\cli.js doctor --project "E:\path\to\registered-project"
```

## Environment variables

- `WF_ENGINE_PATH`: engine binary override. Resolution order is this override, a packaged sibling binary, then `wf-engine` from `PATH`.
- `WF_CCPANES_CTL`: full path to `cc-panes-ctl` when it cannot be discovered from `PATH` or a running Windows `cc-panes.exe`.
- `WF_STATE_DIR`: run-state root override, useful for tests and custom installations.

Provider credentials and `CC_PANES_API_TOKEN` are never written to run records or diagnostics.

## Commands

```powershell
node .\wf\dist\cli.js doctor
node .\wf\dist\cli.js doctor --project "E:\registered-project"
node .\wf\dist\cli.js run --project "E:\registered-project" --tool codex --runtime local "Implement the requested change"
```

On a TTY, `wf run` displays a compact Ink view. In CI, non-TTY output, or `NO_COLOR` mode it prints stable text lines containing the run ID, status, node status, elapsed time, summary, and state directory.

## Detach and cancel

- Detach is the first Ctrl+C behavior. The engine records `paused`, confirms the detach, and does not kill the CC-Panes session.
- Cancel is the explicit `run.cancel` JSON-RPC operation. It kills only the session recorded for that run and records `cancelled`.

The M1 CLI exposes Ctrl+C detach; integrations can use the engine's explicit cancel method when termination is intended.

## Run data

The default state root is `%LOCALAPPDATA%\wf` on Windows, `$XDG_STATE_HOME/wf` or `~/.local/state/wf` on Linux, and `~/Library/Application Support/wf` on macOS.

```text
runs/<run-id>/run.json
runs/<run-id>/events.jsonl
runs/<run-id>/nodes/agent-1/output.log
```

Snapshots use temporary-file replacement, events are append-only JSON lines with monotonic sequence numbers, and timestamps are UTC RFC3339 values.

## Known limitations

The target project must already be registered in CC-Panes. A run can become `succeeded` only when the launched worker performs the mandatory TaskBinding update with a successful terminal status and a structured completion summary. Waiting input becomes `blocked`; idle or exited sessions without a terminal binding become `indeterminate`.
