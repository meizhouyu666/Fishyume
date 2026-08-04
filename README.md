# Fishyume — Workflow Engine M2.1.1 Alpha

Fishyume (`fishyume`, with `wf` retained as a compatibility alias) is a local-first, recoverable DAG workflow runner for CC-Panes. A temporary TypeScript CLI process talks to a headless Go engine over NDJSON JSON-RPC 2.0. Go is the only workflow parser, validator, scheduler, state owner, and CC-Panes caller; TypeScript only parses commands, bridges the protocol, and renders terminal output.

M2.1 supports versioned YAML/JSON workflows with Agent and Approval nodes, explicit dependencies, restricted conditions and templates, durable Run/Node/Attempt state, cross-process resume/cancel, and a per-Run controller lease. It deliberately runs at most one Agent at a time (`maxConcurrency: 1`). M1 `doctor` and ad-hoc single-Agent runs remain available.

## Prerequisites

- Go 1.26 or newer
- Node.js 24 or newer and npm
- A release CC-Panes instance with its orchestrator and daemon ready
- A project already registered in CC-Panes
- A dedicated non-interactive CC-Panes launch profile for Fishyume-managed workers; create it explicitly in CC-Panes and copy its profile ID
- Launched workers must be allowed to update the TaskBinding included in their engine-owned completion contract

The engine only wraps `cc-panes-ctl --json`. It does not use an MCP SDK, undocumented REST calls, or TypeScript-side CC-Panes access.

## Build and verify

```powershell
cd wf
npm install
npm run build

cd wf-engine
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/wf-engine

cd ..\wf
npm install
npm run typecheck
npm test
npm run build
```

For local CLI use:

```powershell
$env:FISHYUME_ENGINE_PATH = "E:\path\to\my-agent\wf-engine\wf-engine.exe"
$env:FISHYUME_CCPANES_PROFILE_ID = "<exact-cc-panes-profile-id>"
fishyume doctor --project "E:\registered-project"
```

Fishyume passes that exact profile ID to the CC-Panes `launch_task` control-plane call. The profile should be non-interactive and scoped to Fishyume-managed workers. Repository code never creates, binds, or silently enables a global unrestricted profile. `FISHYUME_CCPANES_PROFILE_ID` takes precedence over the compatibility alias `WF_CCPANES_PROFILE_ID`; an invalid preferred value is reported rather than falling back.

## Installation matrix

| Host | CLI package | Engine package |
|---|---|---|
| Windows x64 | `fishyume` | `fishyume-engine-win32-x64` |
| Linux x64 / WSL | `fishyume` | `fishyume-engine-linux-x64` |

Unsupported hosts can still install the CLI, but `fishyume doctor` reports the missing Engine. Set `FISHYUME_ENGINE_PATH` to an approved local binary when needed. The legacy `WF_ENGINE_PATH` and `WF_STATE_DIR` names remain supported, but Fishyume variables win when both are set.

## Workflow format

```yaml
apiVersion: fishyume/v1
name: implement-with-approval

inputs:
  goal:
    required: true

defaults:
  tool: codex
  runtime: local

execution:
  maxConcurrency: 1

nodes:
  plan:
    type: agent
    task: |
      Plan this change: {{ inputs.goal }}

  approve:
    type: approval
    dependsOn: [plan]
    prompt: |
      Approve this plan? {{ nodes.plan.result.summary }}

  implement:
    type: agent
    dependsOn: [approve]
    when:
      node: approve
      field: result.decision
      equals: approved
    task: |
      Implement the approved plan: {{ nodes.plan.result.summary }}
    requiredSkills: []
```

Inputs are JSON scalars only. Templates accept only simple paths such as `{{ inputs.goal }}` and `{{ nodes.plan.result.summary }}`. Conditions are structured `node`/`field`/`equals`, `all`, `any`, or `not` objects. There is no shell, JavaScript, Go template, function, loop, dynamic indexing, or arbitrary expression execution.

## Commands

```powershell
fishyume --help
fishyume --version

# Existing ad-hoc single-Agent path
fishyume run --project "E:\registered-project" "Implement the requested change"

# Versioned workflow
fishyume run --workflow .\workflow.yaml --project "E:\registered-project" --input goal="Implement M2"
fishyume run --workflow .\workflow.yaml --project "E:\registered-project" --inputs .\inputs.json

fishyume status <run-id>
fishyume status <run-id> --json
fishyume resume <run-id>
fishyume resume <run-id> --approve approve
fishyume resume <run-id> --reject approve --reason "Revise the plan"
fishyume resume <run-id> --retry implement
fishyume resume <run-id> --retry implement --acknowledge-duplicate-risk
fishyume cancel <run-id>
```

`status` is read-only and does not acquire the controller lease. `resume` always reconciles an existing Attempt and its TaskBinding/session before considering a new launch. Explicit retry creates Attempt N+1 and preserves all prior Attempts. Retrying an indeterminate Attempt requires `--acknowledge-duplicate-risk` because its side effects may already exist.

## Detach, recovery, and cancel

The first Ctrl+C detaches. The Run becomes `paused/controller_detached`, the temporary engine exits, and an active CC-Panes Agent session is left running. No later node is scheduled until `fishyume resume` takes control again.

There is no daemon. Each `run`, `resume`, or `cancel` command obtains a durable lease, heartbeats it, and releases it on exit. A second live controller is rejected; an expired lease can be taken over after a crash. Taking over never implies relaunching an Agent—the persisted Attempt is reconciled first.

An `idle` state observed during Agent startup is provisional. Fishyume performs an approximately 10-second bounded, cancellation-aware reconciliation of both the TaskBinding and CC-Panes session using repeated observations. A terminal TaskBinding wins; an active startup continues waiting; only sustained idle with a non-terminal Binding becomes `waiting/completion_missing`.

Cancel is Workflow-scoped and users do not need a session ID. A separate `fishyume cancel` process writes an atomic, durable request that the current lease owner observes without deleting or stealing its live lease. The owner persists `cancelRequested=true` before asking the Backend to stop the active Attempt, so a Wait result racing with kill cannot publish a failed terminal state. It records `cancelled` only after Backend confirmation. A failed kill leaves `waiting/cancel_failed` and can be retried truthfully; an unresolved request can be adopted after owner crash and lease expiry. Pending nodes become `skipped/workflow_cancelled` only after confirmed cancellation.

## Lifecycle and exit codes

Run phase (`created`, `running`, `waiting`, `paused`, `cancelling`, `completed`) is separate from terminal conclusion (`succeeded`, `failed`, `rejected`, `cancelled`, `indeterminate`). Waiting and paused Runs do not claim a terminal conclusion.

| Code | Meaning |
|---:|---|
| 0 | succeeded |
| 1 | failed |
| 2 | rejected |
| 3 | cancelled |
| 4 | waiting, paused, or cancelling |
| 5 | indeterminate |
| 6 | validation, usage, or protocol error |
| 7 | controller conflict / resume-control failure |

## Durable state

The default root is `%LOCALAPPDATA%\fishyume` on Windows, `$XDG_STATE_HOME/fishyume` or `~/.local/state/fishyume` on Linux, and `~/Library/Application Support/fishyume` on macOS. Existing `wf` roots are read-only fallback locations for status and are never rewritten or deleted.

```text
runs/<run-id>/
  run.json
  workflow.json
  events.jsonl
  control.lock
  cancel.request.json   # present only while a cross-process request is unresolved
  cancel.response.json  # last resolved response; replaced/removed by the next request
  nodes/<node-id>/
    node.json
    attempts/<n>/
      attempt.json
      result.json
      output.log
```

Mutable snapshots and cancel coordination records use temporary files and atomic replacement; events are append-only with monotonic sequence numbers. `workflow.json` is the immutable normalized copy. Attempt state stores a SHA-256 prompt hash, durable launch-dispatch state, opaque Backend session metadata, TaskBinding identity, normalized result, and recent output—not the complete rendered prompt or session history. Resolved request files are removed; the ID-scoped response is harmless after a crash and is cleared before a retry request.

Provider credentials, `CC_PANES_API_TOKEN`, complete environment maps, and full terminal histories must not be persisted. New M2 snapshots carry `stateSchemaVersion` independently of RPC protocol version; missing values in old snapshots are treated as version 1. `FISHYUME_STATE_DIR` changes the state root; protect that directory because workflow inputs and structured summaries may contain project information.

Legacy M1 `run.json` snapshots remain visible through `fishyume status` (and the `wf` alias) but are read-only and cannot be resumed as M2 DAGs.

## Environment variables

- `FISHYUME_ENGINE_PATH`: explicit Go engine binary (preferred)
- `FISHYUME_STATE_DIR`: durable state root override (preferred)
- `WF_ENGINE_PATH`: legacy explicit Go engine binary
- `WF_CCPANES_CTL`: explicit `cc-panes-ctl` binary
- `FISHYUME_CCPANES_PROFILE_ID`: exact dedicated CC-Panes launch profile ID (preferred)
- `WF_CCPANES_PROFILE_ID`: legacy launch profile ID alias
- `WF_STATE_DIR`: legacy durable state root override

The npm package contains no `postinstall` download hook. Platform Engine packages are optional, version-pinned artifacts; offline archive builds use `wf/scripts/build-engine-artifacts.ps1` and emit SHA-256 checksums. Alpha release preparation does not publish, update automatically, or contact a remote service.

## M2.1 limits

M2.1 does not provide parallel Agents, `maxConcurrency > 1`, automatic retry, model fallback, Planner Agents, dynamic nodes, command/summarizer nodes, SQLite, a daemon, arbitrary expressions, or `agent-team-workflow`. Parallel ready nodes and aggregate concurrent cancellation are M2.2 work.
