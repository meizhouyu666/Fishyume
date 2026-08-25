# Fishyume M7.2 Handoff and Explicit Workflow Promotion Acceptance

> Status: complete
>
> Date: 2026-08-25
>
> Contract: frozen `fishyume.team/v1`

## Delivered Slice

M7.2 connects accepted one-round Panel evidence to formal Workflow execution
without adding an automatic Planner. The four previously frozen methods are
now available:

```text
team.handoff.create
team.handoff.get
team.handoff.list
team.handoff.bindRun
```

A Handoff is an immutable artifact built from selected retained Team messages.
Its semantic SHA-256 hash covers the goal, decisions, constraints, questions,
acceptance expectations, selected message identities, and retained source
hashes. `createdAt` is deliberately excluded, so retry and recovery preserve
the same semantic identity.

## Safety and Persistence

- every selected message must still exist and its retained `contentHash` must
  match the SHA-256 of its canonical public content;
- create writes a durable intent before the immutable Artifact, and bind writes
  a separate durable intent before the binding collection;
- startup recovery resumes incomplete create and bind intents without changing
  `createdAt`, duplicating an event, or starting a Run;
- one Handoff can bind to at most one existing Run in the same normalized
  project; separate Handoffs in one Team bind independently;
- create/get/list/bind do not increment the Team lifecycle `stateVersion`;
- no Handoff field was added to the frozen Run, Attempt, Context, Memory, or
  `fishyume.application/v1` contracts.

State is retained under the Team aggregate:

```text
teams/<team-id>/
  handoffs/<handoff-id>.json
  handoff-bindings.json
  handoff-intents/<digest>.json
  handoff-binding-intents/<digest>.json
```

## Explicit Promotion

Human CLI commands cover create/list/show/bind, with `--json` where applicable.
Create automatically reads the latest Team version and defaults to all retained
participant contributions when `--message` is omitted. MCP and Machine CLI
carry identical request and response JSON.

Promotion remains Host-owned and user-confirmed:

```text
team.handoff.get
  -> Host authors fishyume/v2
  -> workflow.validate
  -> workflow.explain
  -> user confirms
  -> run.start
  -> team.handoff.bindRun
```

There is no automatic Workflow generation, automatic Run creation, or synthetic
Run/Node/Attempt/Context/Memory state in this increment.

## Acceptance Evidence

| Requirement | Evidence |
| --- | --- |
| deterministic semantic hash | timestamp exclusion and decision mutation tests |
| source integrity | missing and altered retained-message tests |
| immutable/idempotent create | exact replay, conflicting request, strict storage tests |
| crash recovery | faulted Artifact and binding writes resume from durable intents |
| no Run on create | service and RPC state-root assertions |
| existing same-project Run only | real Run lookup RPC integration plus not-found/conflict mapping |
| independent bindings | multiple Handoffs bind to different Runs in one Team |
| bounded contract | aggregate request/Artifact byte limits and duplicate ID tests |
| surface parity | Go RPC, TypeScript bridge, human CLI, MCP, and Machine tests |
| unchanged Host golden path | existing M5.6 `fishyume/v2` validate/explain/start tests |

Closure gates:

```powershell
go test ./wf-engine/... -count=1
go vet ./wf-engine/...
npm --prefix wf run verify
git diff --check
```

Multi-turn Session, follow-up, single-turn cancellation, active close, Web or
Desktop clients, and additional Drivers remain outside M7.2.
