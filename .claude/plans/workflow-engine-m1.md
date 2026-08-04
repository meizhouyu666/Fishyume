# Workflow Engine M1 Implementation Plan

> Plan status: APPROVED FOR IMPLEMENTATION  
> Architecture source: `docs/workflow-engine-architecture.md` v0.2  
> Implementation owner: delegated Codex Worker  
> Leader responsibility: scope control, monitoring, review, and acceptance

## 1. Objective

Implement the first vertical slice of `wf`: a TypeScript CLI starts a headless Go engine over NDJSON JSON-RPC, and the Go engine runs one Codex/Claude session through the CC-Panes `cc-panes-ctl --json` control-plane CLI. A TaskBinding terminal state is the authoritative completion result.

The milestone is successful when `wf doctor` diagnoses the local environment and `wf run --project <registered-project> --tool codex "<task>"` launches one agent, displays progress, persists a run record, and reports a truthful terminal state.

## 2. Constraints

- Go is the only workflow orchestrator and state owner.
- TypeScript must not call CC-Panes directly.
- Use Node.js 24+ and npm. Do not introduce Bun.
- Use Ink 5, React 18, and clipanion for the CLI.
- Use `cc-panes-ctl --json`; do not implement an MCP client or depend directly on undocumented REST routes.
- Use only a fixed single `AgentNode` in M1.
- Do not implement multi-node DAGs, Planner Agent, resume, automatic retry, model fallback, SQLite, or precise Token enforcement.
- Do not enable or integrate agent-team-workflow.
- Do not automatically commit, push, create a PR, or modify unrelated repositories.
- Do not modify this plan or the approved architecture document. If either conflicts with implementation reality, stop and report the conflict.
- Never persist or log `CC_PANES_API_TOKEN` or provider credentials.

## 3. Target Repository Layout

```text
my-agent/
├── README.md
├── .gitignore
├── docs/
│   └── workflow-engine-architecture.md
├── wf-engine/
│   ├── go.mod
│   ├── cmd/wf-engine/main.go
│   └── internal/
│       ├── backend/
│       │   ├── backend.go
│       │   └── ccpanes/
│       │       ├── backend.go
│       │       ├── ctl.go
│       │       ├── discovery_windows.go
│       │       └── types.go
│       ├── rpc/
│       │   ├── protocol.go
│       │   ├── server.go
│       │   └── types.go
│       ├── run/
│       │   ├── model.go
│       │   ├── service.go
│       │   └── events.go
│       └── store/
│           ├── paths.go
│           └── json_store.go
└── wf/
    ├── package.json
    ├── tsconfig.json
    └── src/
        ├── cli.tsx
        ├── commands/
        │   ├── doctor.tsx
        │   └── run.tsx
        ├── bridge/
        │   ├── engine.ts
        │   └── types.ts
        └── tui/
            ├── run-app.tsx
            └── text-reporter.ts
```

Exact filenames may be adjusted when a simpler cohesive layout is justified, but the package boundaries and responsibility split must remain unchanged.

## 4. Phase 1 — Scaffold and Contracts

### Go

1. Create `wf-engine/go.mod` using an appropriate module path that does not claim an external domain the project does not own.
2. Keep the Go implementation standard-library-only in M1 unless a dependency is clearly necessary.
3. Define normalized domain types:
   - RunStatus
   - NodeStatus
   - RunSnapshot
   - RunEvent
   - LaunchSpec
   - Session
   - BackendResult
4. Define the Backend interface from the approved architecture.

### TypeScript

1. Create an ESM package requiring Node 24+.
2. Add compatible versions of TypeScript, React 18, Ink 5, clipanion, and necessary type packages.
3. Define RPC request, response, notification, run snapshot, and run event types matching Go JSON fields.
4. Add scripts for build, typecheck, and test.

### Verification

- `go test ./...` from `wf-engine`.
- `npm run typecheck` from `wf`.
- `npm run build` from `wf`.

## 5. Phase 2 — NDJSON JSON-RPC Engine

Implement JSON-RPC 2.0 over stdin/stdout:

- One UTF-8 JSON object per line.
- stdout contains protocol messages only.
- diagnostics use stderr.
- serialize concurrent writes through one writer/encoder.
- reject malformed JSON, unsupported protocol versions, unknown methods, and oversized messages without crashing the server.

Methods:

```text
engine.hello
run.start
run.get
run.detach
run.cancel
```

Notifications:

```text
run.event
engine.log
```

`engine.hello` must return:

- engine version
- protocol version 1
- supported methods
- supported backends (`ccpanes`)

`run.start` must create a run, return a run ID promptly, and perform execution asynchronously while emitting ordered events.

Add focused tests using in-memory readers/writers for handshake, unknown method, malformed line, ordered notifications, and terminal response behavior.

## 6. Phase 3 — Durable Run Store

Implement cross-platform state root selection:

- `WF_STATE_DIR` override first.
- Windows LocalAppData.
- Linux XDG state path with `~/.local/state` fallback.
- macOS Application Support.

For each run, write:

```text
runs/<run-id>/run.json
runs/<run-id>/events.jsonl
runs/<run-id>/nodes/agent-1/output.log
```

Requirements:

- snapshots use write-to-temp plus rename.
- events are append-only JSON lines.
- event sequence numbers are monotonic.
- timestamps use UTC RFC3339.
- errors include operation and path context.
- secrets and complete environment maps are never persisted.

Add unit tests with `WF_STATE_DIR` pointing to a temporary directory.

## 7. Phase 4 — CC-Panes Backend

### Executable discovery

Discover `cc-panes-ctl` in this order:

1. `WF_CCPANES_CTL`.
2. `PATH`.
3. Windows-only discovery from a running `cc-panes.exe`, deriving `binaries/cc-panes-ctl.exe`.

Return an actionable diagnostic if discovery fails.

### Control-plane wrapper

Implement a wrapper that:

- executes commands without a shell whenever possible.
- always requests JSON output.
- captures stdout and stderr separately.
- reports exit code and command category without leaking prompts or secrets.
- tolerates the MCP call result envelope and extracts structured content safely.

Required operations:

1. `status` / doctor.
2. create TaskBinding.
3. launch task.
4. update TaskBinding with session identifiers.
5. wait for session state changes.
6. query the matching TaskBinding.
7. read recent output.
8. kill a session on explicit cancel.

### State mapping

- TaskBinding `completed` with zero or absent successful exit code → SUCCEEDED.
- TaskBinding `failed` → FAILED.
- session `waitingInput` before a terminal binding → BLOCKED.
- session `idle` or `exited` without terminal binding → INDETERMINATE.
- transport/process errors → FAILED with a diagnostic category.
- detach → PAUSED without killing the session.
- explicit cancel → kill the session and set internal state CANCELLED.

Do not infer success from terminal output text alone.

### Worker prompt contract

Append a clearly separated engine-owned completion section to the user's task. It must tell the worker to update the supplied TaskBinding ID with:

- `status: completed|failed`
- `progress: 100`
- `exitCode`
- concise `completionSummary`
- metadata containing artifacts, warnings, checks, and estimated usage when available

The injected section must state that updating the binding is mandatory and must not ask the worker to modify workflow engine plan files.

### Tests

Use a fake `cc-panes-ctl` executable/script fixture. Do not require a live CC-Panes process for unit tests. Cover successful completion, failed binding, waiting input, idle without binding, malformed JSON, command failure, detach, and cancel.

## 8. Phase 5 — TypeScript CLI and TUI

### Engine bridge

Implement child process startup and protocol management:

- engine path priority: `WF_ENGINE_PATH`, packaged sibling binary, then `wf-engine` from PATH.
- read stdout by lines and decode JSON-RPC.
- forward engine stderr as diagnostic events without treating logs as protocol.
- correlate responses by ID.
- route `run.event` notifications to subscribers.
- reject incompatible protocol versions.
- shut down the child cleanly after terminal completion.

### `wf doctor`

Display checks for:

- engine startup and handshake
- protocol compatibility
- CC-Panes control CLI discovery
- release orchestrator and daemon readiness
- optional project registration when `--project` is supplied

Return non-zero on failed required checks.

### `wf run`

Arguments:

```text
wf run [--project <path>] [--tool codex|claude|opencode] [--runtime local|wsl|ssh] <task>
```

Defaults:

- project: current directory
- backend: ccpanes
- tool: codex
- runtime: local

Behavior:

- show a minimal Ink status view on TTY.
- use a plain line reporter when stdout is not a TTY or `NO_COLOR`/CI behavior makes TUI unsuitable.
- show run ID, node status, elapsed time, summary, and state directory.
- first Ctrl+C sends `run.detach` and exits after confirmation from the engine; it must not kill the CC-Panes session.

Add tests for protocol parsing, event routing, plain reporter output, and command exit codes. Avoid brittle snapshot tests of terminal animation.

## 9. Phase 6 — Documentation and End-to-End Verification

Create `README.md` covering:

- current M1 capabilities and explicit non-goals
- prerequisites
- development build commands
- `WF_ENGINE_PATH`, `WF_CCPANES_CTL`, and `WF_STATE_DIR`
- `wf doctor`
- one-agent `wf run` example
- how detach differs from cancel
- where run data is stored
- known limitation: a registered CC-Panes project and worker-side binding update capability are required

Create `.gitignore` entries for build output, dependencies, coverage, and local state without ignoring architecture or plan files.

Run and report:

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

Then run a fake-backend/fixture integration test proving this sequence:

```text
CLI starts engine
→ hello succeeds
→ run.start returns run ID
→ events progress through dispatching/running
→ fake TaskBinding becomes completed
→ final snapshot is succeeded
→ persisted files exist and parse
```

A live CC-Panes smoke test may only be run after confirming the target project is registered. Do not launch work in unrelated projects.

## 10. Acceptance Checklist

- [ ] Go is the sole state owner and orchestrator.
- [ ] TS contains no CC-Panes backend implementation.
- [ ] Engine stdout remains valid NDJSON protocol under tests.
- [ ] `wf doctor` provides actionable failures.
- [ ] CC-Panes Backend uses `cc-panes-ctl --json`.
- [ ] TaskBinding terminal state is required for success.
- [ ] Idle without terminal binding becomes INDETERMINATE.
- [ ] detach does not kill the Agent session.
- [ ] explicit cancel kills only the run's own session.
- [ ] snapshots and events persist outside the target project by default.
- [ ] credentials are absent from logs and persisted state.
- [ ] Go tests, vet, and build pass.
- [ ] TypeScript typecheck, tests, and build pass.
- [ ] README documents setup, usage, and limitations.

## 11. Worker Reporting Requirements

At completion, report:

1. Files created or modified, grouped by phase.
2. Exact verification commands and results.
3. Any intentional deviation from the suggested file layout.
4. Any blocked item or plan/code mismatch.
5. Whether a live CC-Panes smoke test was run; if not, why not.

Do not commit or push. Leave the working tree ready for Leader inspection.
