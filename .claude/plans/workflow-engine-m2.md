# Workflow Engine M2.1 Implementation Plan

> Plan status: APPROVED FOR IMPLEMENTATION
> Architecture source: `docs/workflow-engine-m2-architecture.md` v0.1
> Implementation owner: delegated existing Codex Worker
> Leader responsibility: scope control, review, verification, and acceptance

## 1. Objective

Implement M2.1 as an explicit, recoverable multi-node DAG workflow engine while preserving all M1 capabilities. A user can run a versioned YAML/JSON Workflow containing Agent and Approval nodes, inspect it from another process, approve/reject/retry it, resume it after the controller exits or crashes, and cancel the Workflow without knowing the active CC-Panes session.

M2.1 understands a DAG but deliberately limits `maxConcurrency` to `1`. Do not implement parallel Agent execution.

## 2. Non-negotiable constraints

- Go remains the only orchestrator, Workflow parser/validator, scheduler, state owner, and CC-Panes caller.
- TypeScript remains CLI/TUI and JSON-RPC client only.
- Preserve `wf doctor` and the M1 ad-hoc `wf run --project <path> "<task>"` experience.
- Continue using `cc-panes-ctl --json`; do not add an MCP SDK or undocumented REST integration.
- TaskBinding terminal state remains authoritative for Agent success/failure.
- Do not infer success from terminal text, session idle, or process exit alone.
- Do not add a daemon, SQLite, automatic retry, fallback, Planner Agent, dynamic nodes, CommandNode, SummarizerNode, parallel execution, or `agent-team-workflow`.
- Do not implement arbitrary expression evaluation, `text/template`, JavaScript, or Shell conditions.
- Never persist provider credentials, `CC_PANES_API_TOKEN`, complete environment maps, or full session histories.
- Do not modify this plan or the architecture document during implementation. Report conflicts instead.
- Do not commit, push, create a PR, or modify unrelated repositories.

## 3. Compatibility and migration

1. Upgrade Engine/CLI RPC to protocol version 2 because the snapshot model changes from a flat status to phase/conclusion/reason.
2. Keep the M1 ad-hoc command by normalizing it into an in-memory `wf/v1` Workflow with one Agent node.
3. Keep `wf doctor` behavior and diagnostics.
4. Detect legacy M1 `run.json` snapshots and support read-only `wf status`; do not claim M2 resume support for M1 Runs.
5. Existing M1 tests must continue passing unless they explicitly assert protocol-1-only shapes; update those tests to assert the intended M2-compatible behavior rather than deleting coverage.

## 4. Phase 1 — Workflow domain, parser, and validation

Create a cohesive Go package such as `wf-engine/internal/workflow` containing:

- versioned Workflow document types;
- input declarations and normalized scalar values;
- Agent and Approval node definitions;
- structured conditions (`compare`, `all`, `any`, `not`);
- deterministic DAG validation/topological ordering;
- restricted path-template parsing/rendering;
- normalization to canonical JSON for persistence.

### YAML/JSON parsing

- Accept `.yaml`, `.yml`, and `.json` documents with `apiVersion: wf/v1`.
- Use one small, established Go YAML dependency; `gopkg.in/yaml.v3` is acceptable.
- Reject unknown `apiVersion`, unknown node type, unknown fields where practical, duplicate YAML keys, invalid IDs, missing dependencies, cycles, missing required inputs, unsupported scalar types, and `maxConcurrency != 1`.
- JSON and equivalent YAML must normalize to the same canonical Workflow structure.

### Template restrictions

- Implement only `{{ simple.path }}` references.
- Permit `inputs.<name>` and public result fields of ancestor nodes.
- Reject functions, pipes, loops, condition syntax, dynamic indexing, Backend metadata, session output, and non-ancestor node references.
- Missing values are errors; never silently render an empty string.
- Artifact arrays render deterministically as JSON, not Go debug formatting.

### Verification

Add table-driven tests for valid YAML/JSON equivalence, duplicate keys, cycles, missing dependencies, stable ordering, invalid conditions, invalid references, missing inputs, unsupported concurrency, and deterministic rendering.

## 5. Phase 2 — Lifecycle model and durable store

Replace the flat M1 runtime model with persisted M2 domain types while retaining a legacy decoder for status display.

### Runtime model

Implement:

- Run phases: `created`, `running`, `waiting`, `paused`, `cancelling`, `completed`.
- Node phases: `pending`, `ready`, `running`, `waiting`, `completed`, `skipped`.
- Conclusions: `succeeded`, `failed`, `cancelled`, `rejected`, `indeterminate`.
- Machine-readable reasons including `approval_required`, `agent_waiting_input`, `completion_missing`, `invalid_result`, `condition_false`, `upstream_failed`, `workflow_cancelled`, `controller_detached`, and `user_requested`.
- Attempt records with monotonic attempt number, Backend session, TaskBinding/launch metadata, timestamps, prompt hash, and result.

Do not assign a conclusion to active/waiting objects. Validate phase/conclusion combinations before persistence.

### Store layout

Extend `internal/store` to support arbitrary node IDs and attempts:

```text
runs/<run-id>/
  run.json
  workflow.json
  events.jsonl
  nodes/<node-id>/node.json
  nodes/<node-id>/attempts/<n>/attempt.json
  nodes/<node-id>/attempts/<n>/result.json
  nodes/<node-id>/attempts/<n>/output.log
```

Requirements:

- Validate run/node IDs before building paths; prevent traversal.
- Use temp-file plus atomic replacement for every mutable snapshot.
- Keep events append-only with monotonic sequence numbers.
- Never overwrite an older Attempt when retrying.
- Persist the normalized Workflow before scheduling the first node.
- Add read APIs required by `status`, `resume`, and `cancel`.
- Maintain strict file permissions consistent with M1.

### Verification

Use temporary `WF_STATE_DIR` tests for round trips, arbitrary safe node IDs, path rejection, attempt preservation, atomic snapshots, legacy M1 snapshot detection, and corrupted-state diagnostics.

## 6. Phase 3 — Cross-process control lease

Implement a per-Run lease, preferably in a focused package or inside the store package with a small interface.

Requirements:

- Acquire using atomic exclusive file creation.
- Record random owner ID, PID, command, created time, heartbeat, and expiry.
- Heartbeat while `run`, `resume`, or `cancel` controls the Run.
- Refuse a second live owner with an actionable diagnostic.
- Allow safe takeover only after expiry.
- Release only when owner ID matches.
- A stale owner must not cause Agent relaunch; resume must reconcile first.
- `wf status` remains read-only and does not acquire the write lease.

Add deterministic tests using an injected clock/short lease interval. Cover competing owners, heartbeat, stale takeover, wrong-owner release, and crash-like abandonment.

## 7. Phase 4 — Scheduler, conditions, approvals, and context

Refactor `internal/run` into a scheduler/service that operates on persisted Workflow/Node/Attempt state.

### Scheduler

- Recompute readiness from persisted dependencies; do not rely only on an in-memory cursor.
- Use deterministic topological ordering and launch at most one Agent.
- Evaluate a node condition only after all dependencies have a stable result.
- False condition -> `skipped` with `condition_false`.
- Explicit Agent failure -> Run `completed/failed`; unstarted downstream nodes -> `skipped/upstream_failed`.
- All eligible work finished -> aggregate the truthful Run conclusion.
- Approval rejection with an eligible rejected branch continues; rejection without a runnable branch -> Run `completed/rejected`.

### Approval nodes

- Reaching Approval sets Node and Run to `waiting/approval_required` and persists before notifying CLI.
- `approve`/`reject` action must name the current waiting Approval node.
- Persist decision and optional reason as structured result.
- Repeating the exact decision is idempotent; conflicting decisions return an error.

### Context contract

- Validate non-empty summary and structured arrays before downstream use.
- Define documented byte/count limits as constants and test boundaries.
- Do not silently truncate context.
- Render only explicitly referenced inputs/results.
- Inject node `requiredSkills` into the engine-owned prompt contract.
- Persist a SHA-256 of the final rendered prompt in Attempt state, not the full rendered prompt.
- Continue persisting the normalized TaskBinding result and recent output.

## 8. Phase 5 — CC-Panes reconciliation and attempts

Extend the Backend abstraction only as needed to distinguish observations required for recovery. Do not leak CC-Panes-specific types into Workflow state.

### Required behavior

- Launch returns enough opaque metadata to persist and later reconstruct/reconcile a Session.
- Reconcile an existing Attempt without launching a new one.
- TaskBinding terminal result wins over session observation.
- `waitingInput` -> `waiting/agent_waiting_input`.
- `idle` without terminal Binding -> bounded result reconciliation, then `waiting/completion_missing`.
- `exited`, lost, or not-found session without terminal Binding -> `completed/indeterminate`.
- A completed Binding missing required summary/shape -> `waiting/invalid_result`.
- Transport failure that does not prove Agent outcome must not automatically become a definitive Agent failure.

### Retry

- No automatic retry.
- Explicit retry creates Attempt N+1 and preserves Attempt N.
- Retry is allowed only from documented human-resolvable states.
- Retrying an indeterminate Attempt requires an explicit acknowledgement flag from the CLI after warning about duplicate side effects.

### Fake backend coverage

Cover completed, failed, waiting input, idle then delayed Binding, persistent idle without Binding, exited without Binding, malformed Binding, lost session, resume of active session, and retry without overwriting history.

## 9. Phase 6 — Workflow cancellation

Preserve M1's truthful cancel ordering and apply it at Workflow scope.

1. Acquire the Run lease.
2. Persist `cancelRequested=true` and Run phase `cancelling` before Backend cancellation.
3. Cancel only the current Run's active Attempt.
4. Record active Node `completed/cancelled` only after Backend success.
5. Mark never-started nodes `skipped/workflow_cancelled`.
6. Complete Run as `cancelled/user_requested`.
7. On Backend cancellation failure, preserve the cancel request and a retryable state; do not emit a false terminal cancelled event.
8. Concurrent/repeated cancel commands must cause at most one successful kill and remain idempotent.

Add race-focused tests equivalent to and extending the M1 cancellation tests.

## 10. Phase 7 — JSON-RPC v2

Update the protocol and TypeScript mirror types.

Methods:

```text
engine.hello
run.start
run.startWorkflow
run.status
run.resume
run.cancel
```

- Keep `run.start` for the single-node ad-hoc path.
- `run.startWorkflow` accepts Workflow file content or a normalized document plus resolved inputs; Go performs authoritative parsing/validation.
- `run.status` is read-only and returns the current snapshot, nodes, and active Attempt summary.
- `run.resume` accepts either no action or exactly one of approve/reject/retry.
- `run.cancel` implements Workflow cancellation.
- Notifications remain ordered and include Run phase/conclusion/reason plus relevant Node identity.
- stdout remains protocol-only under concurrent scheduler, heartbeat, and Backend activity.

Test malformed actions, protocol mismatch, event ordering, status on legacy Runs, and duplicate resume/cancel calls.

## 11. Phase 8 — TypeScript CLI and TUI

Add Clipanion commands/options:

```text
wf run --workflow <file> --project <path> [--input key=value] [--inputs file.json]
wf status <run-id> [--json]
wf resume <run-id>
wf resume <run-id> --approve <node-id>
wf resume <run-id> --reject <node-id> [--reason text]
wf resume <run-id> --retry <node-id> [--acknowledge-duplicate-risk]
wf cancel <run-id>
```

Requirements:

- Preserve existing ad-hoc `wf run` and `wf doctor` commands.
- `status` shows Workflow-level phase/conclusion/reason first, then compact per-node state and current Attempt.
- Users never need a CC-Panes session ID for normal control.
- First Ctrl+C remains detach, not cancel; persist `paused/controller_detached` and release the lease.
- `cancel` prints `cancelling` until Backend confirms terminal cancellation.
- Non-TTY output remains stable and testable.
- `--json` emits one machine-readable object without Ink rendering.
- Exit codes distinguish success, failed/rejected/cancelled, waiting/paused, indeterminate, validation error, and control conflict in a documented table.

Avoid a large TUI redesign. Extend the existing compact view only enough to show Workflow and current Node.

## 12. Phase 9 — Documentation and integration verification

Update `README.md` with:

- M2.1 capabilities and non-goals;
- one YAML Workflow example;
- ad-hoc versus `--workflow` run examples;
- status/resume/approve/reject/retry/cancel examples;
- detach versus cancel;
- recovery expectations without a daemon;
- state directory layout and security notes;
- legacy M1 Run limitation;
- known CC-Panes worker approval requirement for TaskBinding updates.

Add fake-backend integration tests proving:

1. YAML parse -> normalized persisted Workflow.
2. Agent plan node succeeds.
3. Approval blocks and the first Engine process exits.
4. A new Engine approves and resumes the Run.
5. Implementation Agent succeeds and context contains only explicit structured references.
6. A conditionally skipped node is persisted correctly.
7. Final Run succeeds.

Add separate integrations for rejection, crash/reconcile without duplicate launch, persistent idle/missing completion, explicit retry, cancel failure/retry, and competing controllers.

## 13. Required verification

Run and report exact results:

```powershell
cd wf-engine
go test ./...
go test -race ./...
go vet ./...
go build ./cmd/wf-engine

cd ..\wf
npm run typecheck
npm test
npm run build
```

If `go test -race` cannot run in the installed Windows Go environment, report the exact reason and compensate with focused concurrency tests; do not silently omit it.

Run `git diff --check` and verify that generated binaries, `.ccpanes/`, state directories, and node dependencies remain ignored.

A live CC-Panes smoke test may only target the already registered `my-agent` project. Use a minimal Workflow with Agent -> Approval -> Agent, and do not modify unrelated projects. If worker-side TaskBinding update approval blocks the smoke, report it as an environment prerequisite rather than weakening result semantics.

## 14. Acceptance checklist

- [ ] M1 doctor and ad-hoc run remain functional.
- [ ] Go authoritatively parses and validates YAML/JSON Workflows.
- [ ] M2.1 never runs more than one Agent at a time.
- [ ] Lifecycle phase is separated from terminal conclusion.
- [ ] Approval, rejection, waiting, cancel, failure, and indeterminate are not conflated.
- [ ] `wf cancel` operates at Workflow scope and is truthful/retryable.
- [ ] `wf resume` recovers an existing Attempt before considering a launch.
- [ ] Explicit retry preserves all prior Attempts.
- [ ] Run lease prevents concurrent controllers.
- [ ] Conditions and templates cannot execute arbitrary code.
- [ ] Downstream context contains only explicit structured references.
- [ ] Legacy M1 Runs are status-readable but not falsely resumable.
- [ ] Durable snapshots/events survive controller exit and crash tests.
- [ ] Go tests/vet/build and TypeScript tests/typecheck/build pass.
- [ ] README accurately documents behavior and limitations.

## 15. Worker reporting requirements

At completion report:

1. Files changed, grouped by implementation phase.
2. Exact verification commands and results, including race-test status.
3. Any architecture/implementation mismatch or intentional internal file-layout adjustment.
4. Evidence for recovery without duplicate launch, cancel truthfulness, and controller lease exclusion.
5. Whether a live CC-Panes Agent -> Approval -> Agent smoke test was completed.
6. Remaining limitations or follow-up candidates for M2.2.

Before reporting completion, update the assigned CC-Panes TaskBinding and report to the Leader. Leave the working tree uncommitted and do not push.
