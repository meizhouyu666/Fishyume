# Fishyume M7.0 Feasibility and Boundary Spike

> Status: engineering spike recorded; live Provider gate pending
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

Help output proves that the command surface exists. It does not prove that
resume works non-interactively, preserves the requested model/sandbox, or
survives Control Plane restart. Those remain live-driver acceptance questions
for M7.0/M7.3.

### Live two-model probe

Two isolated probes were launched against a temporary directory with:

```text
codex exec --ephemeral --json --color never --sandbox read-only
  --skip-git-repo-check --cd <temporary-project> --model <model-id> <bounded prompt>
```

Neither probe produced a terminal contribution. Both entered the Codex
service's temporary high-demand reconnect path and were stopped after the
bounded wait. No repository was used as the probe workspace and no tracked
file changed. This result is classified as `unavailable/inconclusive`, not as
model or sandbox failure.

## Gate status

| Gate | Status | Evidence |
| --- | --- | --- |
| Two explicit model IDs exist | pass | Frozen M6 catalog and hash |
| Model selection reaches Codex process | pass (fake) | Existing process argument test |
| Read-only process path is exercised | pass (fake) | Existing integration coverage |
| Real `gpt-5.6` one-shot contribution | pending | Provider high-demand reconnect |
| Real `gpt-5.6-luna` one-shot contribution | pending | Provider high-demand reconnect |
| Non-interactive resume continuity | pending | CLI help is insufficient |
| Domain-neutral artifact extraction | design required | Current paths are Run-bound |

## Decision

M7.0 is not yet approved as a public-contract gate. The implementation may
prepare test fixtures and the internal extraction design, but must not ship
`fishyume.team/v1` or begin M7.1 public behavior until a bounded live probe
captures two distinct terminal contributions and records the effective model,
sandbox, cancellation, and workspace-integrity evidence.

If the next live probe remains unavailable, the evidence must be recorded as an
environment block and the Team implementation should proceed only behind fake
Exploration Drivers. Fishyume must not claim multi-model live support from
catalog metadata alone.
