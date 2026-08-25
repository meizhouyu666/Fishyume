# Fishyume M7.0 Feasibility and Boundary Spike

> Status: M7.0 feasibility complete; later app-server gate supersedes resume deferral
>
> Date: 2026-08-24
>
> Related plan: [Fishyume M7 development plan](fishyume-m7-session-native-web-team-console-plan.md)

## Scope

M7.0 checks the assumptions required before implementing the public Team
aggregate:

- two explicit trusted model targets can run one-shot, read-only exploration;
- the Codex process surface exposes enough identity and cancellation evidence;
- resume semantics are not assumed before the installed Harness proves them;
- Team execution can be separated from Workflow Run/Node/Attempt identity.

This document records evidence only. It does not freeze or extend
`fishyume.team/v1`.

## Findings

### Trusted catalog

The frozen M6 catalog contains exactly two model IDs:

```text
codex/local/gpt-5.6
codex/local/gpt-5.6-luna
```

The pinned catalog hash is:

```text
7b993c75a20b1a783a1ab9aaeae1235c6514f408f33bc8bdc32de9e85be4a1da
```

Their trusted coarse cost units remain the M6 mapping: `gpt-5.6` is high cost
(`100`) and `gpt-5.6-luna` is low cost (`1`). M7 must reuse this mapping and
persist the catalog hash with each Team turn; it must not create a second price
table.

### Existing execution surface

The current Workflow `agent.AgentDriver.Start` accepts an `AttemptEnvelope`
whose identity is `RunID / NodeID / Attempt`. The current Codex process backend
also derives its artifact directory from:

```text
runs/<run-id>/nodes/<node-id>/attempts/<attempt>
```

This confirms the plan's required boundary: M7 needs a domain-neutral execution
identity and artifact-location primitive plus a separate one-shot
`ExplorationDriver`. Team code must not call the Workflow Driver with a fake
Attempt envelope or synthetic Run.

The first M7.0 code increment now records this boundary in the pure internal
`internal/explorationdriver` package. Its `StartRequest` accepts only
`TeamID/ParticipantID/TurnID`, normalized workspace/target/model identity, a
bounded prompt supplied in memory, and the mandatory `read-only` sandbox. It
has no import path to Workflow Agent, Run, Node, Attempt, Context, or Result
contracts. Contract tests cover unsafe sandbox values, incomplete identity,
handle/diagnostic bounds, cancellation capability claims, and prompt
non-serialization.

The next extraction increment adds `internal/execution.ArtifactLocation`, a
domain-neutral segmented path helper with traversal checks. The existing Codex
Workflow adapter now uses it to produce the unchanged
`runs/<run>/nodes/<node>/attempts/<n>` layout. Team execution can later use the
same primitive with a `teams` namespace without importing Run path logic.

The existing process layer already supports explicit model selection by passing
`--model <model>` to `codex exec`, and the existing test
`TestCodexExecArgsPropagateSelectedModel` verifies that argument propagation.

### Sandbox and recovery evidence

The existing Codex integration tests use the `read-only` sandbox and cover
parallel execution, process observation, restart recovery, and confirmed
cancellation with a fake Codex executable. The following targeted checks
passed from `wf-engine`:

```text
go test ./internal/routing ./internal/driver/codexprocess ./internal/integration \
  -run "TestBuiltinCatalogV1IsValidAndHashPinned|TestCodexExecArgsPropagateSelectedModel|TestParallelWorkflowMatchesAcrossBackends" \
  -count=1
```

The fake execution evidence is sufficient to continue designing bounded
recovery tests. It is not evidence that a real Provider enforces the same
profile.

### Codex CLI capability surface

Installed CLI version:

```text
codex-cli 0.148.0
```

The installed CLI exposes:

- one-shot `exec --ephemeral --json`;
- explicit `--model`;
- explicit `--sandbox` with `read-only`;
- `--output-schema` and `--output-last-message`;
- `exec resume` by session ID or `--last`;
- `exec fork` by session ID.

Help output proves that the exec command surface exists. It did not prove that
resume worked non-interactively, preserved the requested model/sandbox, or
survived Control Plane restart. Those questions were later answered separately
for the app-server v2 surface by the M7.3 capability gate.

### Live two-model probe

Two isolated probes were launched against a temporary directory with:

```text
codex exec --ephemeral --json --color never --sandbox read-only
  --skip-git-repo-check --cd <temporary-project> --model <model-id> <bounded prompt>
```

Both probes produced terminal contributions. The observed final payloads were
bounded JSON objects with the requested model identity, `writeAttempted: false`,
and `sandbox: read-only`. `gpt-5.6` emitted one preliminary metadata warning
(`model metadata not found; fallback metadata`), but still honored the explicit
model argument and completed the probe. No repository file changed.

The temporary project's README SHA-256 remained
`7D159464AFF7270B7D4E4633B3430B118C2A11FCECB99F454A1F6EB48AB41A6A` and its
Git status remained empty after both probes. The temporary project was removed
after evidence collection.

The earlier high-demand result is superseded by this successful bounded retry;
it remains useful historical evidence that the live gate must use bounded waits.

### Resume and cancellation classification

The installed CLI exposes `exec resume`, but its resume command does not accept
the M7-required `--sandbox`, `--cd`, or `--color` controls. A resume request
therefore cannot prove that Fishyume can reassert the original workspace,
sandbox, and output policy. The **exec resume path** is therefore unsupported
for the M7 Session contract. M7 must not enable `session` mode on that path or
emulate continuity by injecting a reconstructed transcript.

At M7.0, confirmed cancellation semantics were covered only by the existing
Codex process contract tests and integration tests using a fake executable. The
later app-server gate added live Provider-specific evidence: interrupting the
exact active turn produced an `interrupted` terminal notification.

## Gate status

| Gate | Status | Evidence |
| --- | --- | --- |
| Two explicit model IDs exist | pass | Frozen M6 catalog and hash |
| Model selection reaches Codex process | pass (fake) | Existing process argument test |
| Read-only process path is exercised | pass (fake) | Existing integration coverage |
| Real `gpt-5.6` one-shot contribution | pass | Terminal structured contribution; read-only probe |
| Real `gpt-5.6-luna` one-shot contribution | pass | Terminal structured contribution; read-only probe |
| `exec resume` continuity | unsupported | Exec resume cannot accept required policy controls |
| app-server v2 continuity | pass (later gate) | Same thread resumed across app-server restart with explicit policy |
| Confirmed cancellation contract | pass (fake) | Existing Codex process/integration contract tests |
| Provider-specific cancellation confirmation | pass (later gate) | Exact app-server turn completed as `interrupted` |
| Domain-neutral artifact extraction | pass | `internal/execution.ArtifactLocation` and Codex adapter regression |

## Decision

M7.0's one-shot feasibility gate is complete: two distinct trusted model IDs
produced terminal contributions under the required read-only profile, and the
workspace remained unchanged. The independent `ExplorationDriver` contract and
domain-neutral artifact-location extraction are implemented and tested.

The M7.0 decision remains correct for `codex exec resume`, which cannot reassert
the required policy controls. It has since been superseded for M7.3 by the
[Codex app-server v2 capability gate](fishyume-m7.3-capability-gate.md). That
later gate demonstrated cross-process policy-preserving resume, directed
follow-up, identity rejection, and confirmed interruption. M7.3 implementation
is approved against app-server v2; M7.4 remains gated on the completed internal
Session Driver.
