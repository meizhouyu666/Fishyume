# Fishyume M7.1 One-round Panel Acceptance

> Status: complete
>
> Date: 2026-08-25
>
> Contract: `fishyume.team/v1`

## Delivered Slice

M7.1 adds a lightweight exploration path before formal Workflow execution. A
user or Host Agent can start a two-to-four participant Panel without authoring
a DAG. Each participant is a complete one-shot Codex process with an explicit
model and role, not a small Workflow Node or in-process subagent.

The Panel is independent from Workflow state:

- Team artifacts are persisted under `teams/`;
- no Run, Node, Attempt, Result, M5 Context, or M5 Memory record is created;
- every execution receives the engine-owned `read-only` sandbox policy;
- public contributions and bounded diagnostics survive restart;
- a partial participant failure does not erase a successful contribution.

## Frozen Public Surface

The complete v1 method names and schemas are frozen in
`contracts/fishyume-team-v1.json`:

| Method group | M7.1 availability |
| --- | --- |
| `team.capabilities` | available |
| `team.start/list/get/events/messages` | available |
| `team.action` with `cancel` | available |
| `follow_up`, `cancel_turn`, `close` actions | `capability_unavailable` |
| `team.handoff.create/get/list/bindRun` | `capability_unavailable` until M7.2 |

Team methods are not added to the frozen `fishyume.application/v1` method set.
MCP, Machine CLI, Control Plane IPC, and stdio RPC route the same Team contract
and preserve the separate Team error model.

## Human CLI

```powershell
fishyume team start "Compare two approaches"
fishyume team start --detach "Compare two approaches"
fishyume team list
fishyume team show <team-id>
fishyume team cancel <team-id>
```

`team start` waits and prints contributions by default. `--detach` returns after
durable start, `--json` provides machine output, and Ctrl+C detaches observation
without converting it into cancellation.

## Acceptance Evidence

| Requirement | Evidence |
| --- | --- |
| two distinct trusted models | process-backed Panel test checks `gpt-5.6` and `gpt-5.6-luna` |
| independent public contributions | messages retain participant and turn identity |
| read-only repository | before/after directory digest is identical |
| no Workflow or M5 state | state-root assertions reject `runs/` and `memory/` creation |
| partial failure | one valid contribution remains while its peer has failed state and diagnostic |
| crash recovery | persisted active handles are observed without another external start |
| confirmed cancellation | active handles must return `confirmed` before Team closure |
| cancellation recovery | incomplete durable action intent is resumed after service restart |
| bounded stable reads | events and messages use strict monotonic sequence pagination |
| unsupported mutation safety | gated actions create no receipt, message, or turn |
| transport parity | Go RPC plus TypeScript bridge, MCP, Machine, and CLI surface tests |

Repository closure gates:

```powershell
go test ./wf-engine/... -count=1
go vet ./wf-engine/...
npm --prefix wf run verify
git diff --check
```

Repeated Windows process tests additionally exercise process identity and
confirmed cancellation without treating a reused PID as the original Agent.

## Deferred Work

M7.2 will implement immutable Handoff creation, retrieval, listing, and Run
binding over the already frozen schemas. Multi-turn Session work remains gated
because the current Codex resume command cannot enforce the required workspace
and sandbox policy. Fishyume does not emulate resume with expanding prompts.
