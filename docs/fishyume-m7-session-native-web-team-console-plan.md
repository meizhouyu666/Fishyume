# Fishyume M7: Lightweight Team Exploration and Explicit Workflow Promotion

> Status: M7.5 Optional Web Team Client complete on 2026-08-25; the Web
> package remains optional and outside the core package dependency graph.
>
> Date: 2026-08-25
>
> Baseline: M6 is closed. `fishyume.application/v1`, `fishyume/v2`, Run state,
> Attempt, Context/Memory, and routing contracts remain frozen.

## 1. Decision Summary

M7 adds a low-startup-cost exploration mode before formal Workflow execution.
It does not make Workflow lighter and does not replace the deterministic Run
engine.

The product has two explicit commitment levels:

```text
Lightweight exploration
  TeamSession -> parallel proposals -> directed follow-up -> Handoff Artifact
                                                         |
                                                         | explicit promotion
                                                         v
Formal execution
  Host Agent -> fishyume/v2 Workflow -> validate/explain -> run.start -> result
```

The governing decisions are:

1. `TeamSession` is a low-commitment, read-only-by-default exploration space.
2. `AgentSession` is an external Harness session owned by its Driver. It is not
   a Workflow Attempt and does not share the Run state machine.
3. M7 does not use the M5 Context Compiler or Memory-consumption semantics for
   discussion turns. The external Harness owns its conversation context.
4. Fishyume persists only public contributions, participant identity, bounded
   events, execution evidence, and immutable Handoff Artifacts. It does not
   persist hidden reasoning, full prompts, full tool traces, credentials, or
   complete environment maps.
5. M7 does not infer a DAG from a short draft. A Host Agent consumes a Handoff
   Artifact and explicitly authors the formal `fishyume/v2` Workflow.
6. The frozen `fishyume.application/v1` method set and Workflow Run semantics
   are not extended in place. Team behavior uses a separate versioned
   `fishyume.team/v1` contract over the same Control Plane and transports.
7. The optional Web client is last. The first useful product slice is a
   one-round, multi-model Panel available through MCP and CLI.

## 2. Problem and Product Position

Fishyume's deterministic Workflow is intentionally heavy. It is appropriate
when a task has an approved plan, multiple durable stages, side effects,
Approval gates, recovery requirements, or formal acceptance evidence.

It is unnecessarily expensive for exploratory tasks such as:

- ask several models for independent architecture options;
- compare two implementation strategies;
- collect risks from different model/role perspectives;
- challenge a proposal before committing to a development milestone;
- perform one or two directed follow-up rounds before choosing a plan.

Today these tasks require either one Host Agent to simulate all perspectives or
a complete Workflow DAG. M7 fills the gap between a user question and a formal
Workflow without weakening the Workflow contract.

The product remains an Agent control plane. M7 broadens it from only durable
execution to two related responsibilities:

- coordinate bounded external-Agent exploration;
- execute approved work through the existing deterministic Workflow engine.

M7 is not an autonomous multi-agent society. Models do not form unbounded chat
loops, rewrite the active DAG, or decide that a business task is complete.

## 3. Goals and Non-goals

### 3.1 Goals

1. Start a useful multi-model comparison without authoring YAML or a DAG.
2. Run two to four model perspectives in parallel, either explicitly selected
   or resolved from an advertised deterministic default.
3. Default every exploration execution to read-only workspace access.
4. Preserve public proposals and follow-up messages across Control Plane
   restart without duplicate launches.
5. Allow a user or Host Agent to direct bounded follow-up turns.
6. Freeze selected conclusions into an immutable, traceable Handoff Artifact.
7. Let a Host Agent turn that artifact into a formal Workflow using the
   existing `workflow.validate`, `workflow.explain`, and `run.start` path.
8. Expose identical Team semantics through MCP and Machine CLI.
9. Keep a future Web client optional and outside the core npm package.

### 3.2 Non-goals

- no embedded model inference or Tool loop in Fishyume;
- no automatic Planner that turns an underspecified draft into a DAG;
- no M5 Context Envelope, Context manifest, Memory selection, or Memory usage
  receipt for TeamSession turns;
- no reuse of Workflow Run, Node, Attempt, Result, or conclusion semantics for
  discussion state;
- no participant writes to the target repository in the initial M7 scope;
- no autonomous participant-to-participant debate loop;
- no multi-user remote collaboration or public network listener;
- no generic Shell, HTTP, container, or ETL Workflow nodes;
- no third-party hot-loaded Driver SDK or plugin marketplace in this milestone;
- no claim of multi-provider support until a second built-in Driver has its own
  contract and acceptance evidence;
- no Web UI before the MCP/CLI Team flow is useful on its own.

## 4. Domain Model and Ownership

### 4.1 TeamSession

A `TeamSession` is the product-level exploration aggregate.

It owns:

- topic and bounded instructions;
- normalized local project identity;
- mode (`panel` initially, `session` after resumable support);
- participant declarations and public identities;
- aggregate state version and lifecycle;
- public messages and contributions;
- participant-turn references and execution status;
- Team cost grant and cumulative coarse catalog cost;
- Handoff Artifact references;
- event sequence, mutation receipts, and recovery metadata.

It does not own a Workflow, DAG, Node, Attempt, Context manifest, routing usage
receipt, or formal Result.

Lifecycle:

```text
created -> running -> open -> closing -> closed
              |         \-> cancelling -> closed
              +----------------------------> closed  (panel settled)
              \-> cancelling -> closed
```

- `panel` mode automatically moves to `closed` after every initial participant
  turn reaches a terminal participant-turn state.
- `session` mode moves to `open` after the initial round and closes only through
  an explicit action.
- A TeamSession has no `succeeded` or `failed` conclusion. Closing means only
  that exploration ended.
- Every closed TeamSession records exactly one bounded close reason:
  `panel_settled`, `host_closed`, or `cancelled`. The reason describes why no
  more turns will be accepted; it does not summarize the quality of the work.
- Individual participant turns may be `responded`, `failed`, `indeterminate`,
  or `cancelled`; partial panels remain inspectable and exportable.

### 4.2 Participant

A participant is an engine-issued identity within one TeamSession:

```text
participantId
label
role
modelId
resolved driver / target / model
capabilities
state
current AgentSession reference, when available
```

For explicit selection, the client supplies a bounded label, role, and trusted
catalog `modelId`. If selection is omitted, Fishyume resolves the advertised
deterministic default. In both cases it persists the exact participant identity
and target before launch.

For comparison panels, at least two distinct model IDs are required. The first
implementation can use the two trusted Codex models already present in the M6
catalog. This is multi-model but not yet multi-provider.

There is no client-provided `from` identity. Host messages are written as
`host`; model contributions can only be committed by the participant execution
controller. This prevents participant impersonation without inventing a broad
mailbox authorization system.

Every participant turn records `TeamTurnUsageV1`: the selected target, trusted
catalog hash, coarse catalog cost units, cumulative Team cost units, and any
Driver-reported token estimates. It is Team accounting, not a Workflow
`RoutingUsageV1`, Provider invoice, or token-price estimate. Cost is reserved
before launch, and a turn that would exceed the Team grant is rejected.
Reservation uses the frozen M6 `CostUnitsForTarget` mapping from the persisted
catalog; M7 does not introduce a second model-price table.

Participant-turn lifecycle is explicit and independently recoverable:

```text
prepared -> dispatching -> active -> responded
                               |-> failed
                               |-> indeterminate
                               \-> cancelling -> cancelled
```

`prepared` is durable before external launch. `dispatching` means launch intent
exists but a durable Harness handle is not yet confirmed. Recovery reconciles
that intent before deciding whether launch may be retried. Once `active`, a
turn is never relaunched under the same identity. Cancellation is not terminal
until the Driver confirms termination or reconciliation proves it. A cancel
requested in `prepared` or `dispatching` also enters `cancelling`; failed launch
or unprovable cancellation may terminate as `failed` or `indeterminate` rather
than remaining active forever.

### 4.3 AgentSession

An `AgentSession` is a Driver-owned external Harness conversation identity.

```text
teamId / participantId / sessionGeneration
driver / target / model
handle schema version and opaque handle
capability flags
last reconciled turn
parked / active / lost / closed state
```

It is separate from a Workflow Attempt because the two have different
commitments:

| Concern | AgentSession | Workflow Attempt |
| --- | --- | --- |
| Purpose | exploration and follow-up | execute a formal Node |
| Workspace | read-only by default | Workflow/Driver policy |
| Context | external Harness conversation | M5 deterministic Context |
| Completion | public contribution | structured Result evidence |
| Routing | explicit participant model | persisted M6 routing decision |
| Side effects | prohibited initially | tracked and reconciled |
| Retry | another turn/session generation | formal retry/fallback rules |

M7 does not silently map one to the other.

### 4.4 Message and Contribution

The first implementation does not expose a generic `mailbox.send/list` API.
Messages exist only inside a TeamSession, which keeps identity, authorization,
quota, and lifecycle rules narrow.

`TeamMessageV1` contains:

```text
id / teamId / sequence / kind / actor / recipients / turnId
content / referencedMessageIds / createdAt / contentHash
```

`kind` is `host_message` or `participant_contribution`. Host content is bounded
Markdown. Participant content is the canonical encoded `ContributionV1`; the
actor and kind determine the payload schema without heuristic parsing.

Actor is either `host` or an engine-known participant. A participant message is
called a contribution when it terminates a participant turn. Its terminal
status describes whether a response was captured; it is not formal task success.

Captured participant output must decode as a bounded `ContributionV1` before
the execution controller can commit its `TeamMessageV1`:

```text
status: completed | partial | unable
contentMarkdown
warnings[]
openQuestions[]
usageEstimates, when reported by the Driver
```

Validation proves only schema, bounds, and actor/turn ownership. The Markdown,
warnings, and open questions remain untrusted discussion material; they are not
Workflow Results, approvals, or executable instructions. Invalid or oversized
output terminates the turn as `failed` and is retained only as bounded execution
evidence, never as a public message. Driver-reported estimates are copied into
`TeamTurnUsageV1` after validation; they never replace trusted catalog cost.
Any valid `ContributionV1`, including `partial` or `unable`, terminates the
outer participant turn as `responded`; the inner status is the participant's
public self-assessment, not an engine lifecycle state.

Messages are public Team artifacts. Hidden chain-of-thought and complete Harness
history are outside the contract and must not be requested or persisted.

### 4.5 Handoff Artifact

`HandoffArtifactV1` is an immutable bridge from exploration to formal planning:

```text
schemaVersion
handoffId
teamId
sourceTeamVersion
goal
decisions[]
constraints[]
openQuestions[]
acceptanceExpectations[]
selectedMessageIds[]
sourceMessageHashes[]
contentHash
createdAt
```

The Host or user supplies the selected content. Fishyume validates references,
canonicalizes the artifact, computes the hash, and stores it. Fishyume does not
summarize the discussion or generate missing decisions.

Creation snapshots exactly `sourceTeamVersion` and does not close or otherwise
mutate the Team lifecycle. Later messages cannot alter an existing Handoff; a
Host that wants newer evidence creates another Handoff. Creation is allowed
while a Team is open only when `expectedStateVersion` still matches and every
selected message is already committed.

After a Host Agent starts a formal Workflow with the frozen `run.start` method,
it may bind the resulting `runId` to the Handoff Artifact through an idempotent
Team mutation. The Team domain owns that link; the frozen Run snapshot does not
gain a new required field. One Handoff binds to at most one Run: rebinding the
same Run is idempotent, while a different Run returns `conflict`. `RunLookup`
must confirm that the Run exists and targets the same normalized project. The
binding records provenance asserted by the Host; Fishyume does not claim it can
prove that every Workflow instruction was derived from the Handoff.

### 4.6 Workflow Run

Workflow Run behavior remains unchanged:

- the Host Agent authors `fishyume/v2`;
- `workflow.validate` and `workflow.explain` remain authoritative preflight;
- the user confirms the formal plan when appropriate;
- `run.start` is still the only public boundary that creates a Workflow Run;
- the M5 Context Compiler and Memory consumption begin only inside formal
  Attempts;
- `run.result` remains the structured completion evidence.

### 4.7 Component boundary

```text
MCP / Machine CLI / human CLI
              |
              +-------------------------------+
              |                               |
              v                               v
      fishyume.team/v1              fishyume.application/v1
              |                               |
        Team Service                    Application Service
       /      |       \                         |
 Team Store   |    Session Driver (M7.3)     Run Service
              |       |                         |
       Exploration Driver (M7.1)       Workflow/Context/Routing
              |       |
              +-------+---- external Harness
```

The services share a Control Plane process and user state root, but they do not
call into each other's state machines. The Team Service may use a narrow
read-only `RunLookup` port to validate Handoff bindings; it never mutates Run
snapshots or starts a Workflow on the Host's behalf.

The one-shot `ExplorationDriver` is distinct from the existing Workflow
`agent.AgentDriver`, whose `Start` contract requires an `AttemptEnvelope` and
Run/Node/Attempt identity. M7 may share domain-neutral process supervision,
observation, and cancellation primitives below those ports, but it must not
construct a fake Attempt envelope. The resumable `SessionDriver` is a later,
separate capability used only by `session` mode.

## 5. Context, Memory, and Workspace Boundary

### 5.1 TeamSession context

Fishyume provides each initial participant only:

- topic;
- participant role;
- explicit exploration constraints;
- project path when repository inspection is requested;
- a bounded response contract;
- selected public message references for a directed follow-up.

The external Harness decides how to maintain its own conversation history.
Fishyume stores the opaque execution handle and, when supported, the opaque
session handle plus public turn results. It does not store the full Harness
prompt or conversation.

### 5.2 No implicit M5 integration

TeamSession does not:

- compile Context Envelopes;
- select or consume project Memory;
- write Memory usage receipts;
- inject Workflow ancestor Results;
- persist Context manifests or hashes;
- rehydrate a lost session by reconstructing hidden context.

If a resumable external session is lost, Fishyume marks it lost. It does not
pretend a new process with a reconstructed transcript is the same session.

### 5.3 Repository access

M7 Panel and TeamSession launches use `read-only` sandbox by default and cannot
request `workspace-write` through the public Team API in this milestone.

The exploration launch profile is engine-owned: no participant-controlled
approval escalation, writable tool connector, or externally mutating MCP/tool
integration is attached. Child-command network access is disabled unless the
Harness can prove an equivalent no-side-effect policy while retaining its own
Provider transport. Required Harness credentials may remain process-local but
are never copied into Team prompts, handles, events, or persisted environment
maps. If the installed Harness cannot enforce this profile, M7.0 does not pass.

The acceptance gate must verify that a Panel can inspect a repository while the
tracked and untracked workspace state remains byte-for-byte unchanged, excluding
external Fishyume state directories.

Any task requiring edits, commits, external side effects, or deployment must be
promoted to a formal Workflow.

## 6. Contract and Compatibility Strategy

### 6.1 Separate public contract

M7 introduces `fishyume.team/v1` rather than adding methods to the frozen
`fishyume.application/v1` baseline.

Initial methods:

```text
team.capabilities
team.start
team.list
team.get
team.events
team.messages
team.action
team.handoff.create
team.handoff.get
team.handoff.list
team.handoff.bindRun
```

M7.1 froze the names and request/response schemas for this complete v1 method
set, including Handoff schemas, while enabling only the Panel subset. M7.2 now
supplies the persistence and behavior behind that already-frozen Handoff
surface, and `team.capabilities` reports it as available. This staging did not
expand `fishyume.team/v1` or the frozen Run/Application contracts.

MCP and Machine CLI expose identical request/response JSON. The local Control
Plane may route both contract families over the same IPC connection; transport
sharing does not merge their versions or domain semantics.

`team.get` returns bounded aggregate and participant state, not the complete
message history. `team.messages` pages canonical public messages with
`afterSequence` and `limit`; `team.events` uses its own event cursor plus the
existing bounded-wait pattern.

`system.capabilities` remains unchanged. `team.capabilities` advertises Team
schema versions, limits, supported modes, available participant targets, and
resumable-session capability. It also returns the ordered default participant
templates and the trusted catalog hash used to resolve them, so clients can
display the actual models and roles before starting a Team. Feature flags cover
Panel, Handoff, resumable Session, and each Team action independently.

### 6.2 Mutations and preconditions

`team.start` uses durable `clientRequestId` idempotency.

Canonical request shape:

```json
{
  "schemaVersion": "fishyume.team/v1",
  "clientRequestId": "01J...",
  "project": "E:\\project",
  "mode": "panel",
  "topic": "Compare two approaches for the requested change",
  "instructions": "Prioritize recovery behavior and contract compatibility.",
  "participants": [
    {
      "label": "architect",
      "role": "Propose the smallest coherent architecture.",
      "modelId": "codex/local/gpt-5.6"
    },
    {
      "label": "reviewer",
      "role": "Challenge assumptions and identify failure modes.",
      "modelId": "codex/local/gpt-5.6-luna"
    }
  ],
  "costGrant": 1000
}
```

The Team API requires `schemaVersion`, `clientRequestId`, `project`, `mode`, and
`topic`; the service resolves and persists the canonical local project path
before dispatch. `instructions`, `participants`, and `costGrant` are
optional; omitted instructions become empty and omitted cost grant uses the
advertised default. M7.1 accepts only `panel`; a `session` request returns
`capability_unavailable` until M7.4. The human CLI defaults project to the
current working directory and mode to `panel` before it constructs the request.
For the common comparison case, callers may omit `participants`; the service
then resolves the deterministic two-model default advertised by
`team.capabilities`. It must persist the normalized request, resolved
participants, trusted catalog hash, and cost grant before dispatch. Recovery
and idempotent replay use that persisted resolution even if catalog contents or
future defaults have changed.

Every later mutation uses:

```text
actionId
teamId
expectedStateVersion
action type
action-specific payload
```

The same action ID and canonical request returns the committed response. The
same ID with a different payload returns `conflict`. Recovery must distinguish
prepared intent, applied state, and committed response without duplicating an
external Agent launch.

### 6.3 Team actions

The complete version-one action discriminator is frozen with M7.1:

- `follow_up`: address one or more participants with bounded host content and
  selected public message references;
- `cancel_turn`: request confirmed cancellation of an active participant turn;
- `close`: stop accepting new turns and close after active turns settle;
- `cancel`: cancel active turns and close the TeamSession.

There is no generic participant-authored send operation and no autonomous
debate action in v1. M7.1 advertises and enables only `cancel`; requests for
the other already-defined action shapes return `capability_unavailable` without
mutation. M7.4 enables `follow_up`, `cancel_turn`, and `close` only after the
Session Driver gate passes. This staging does not expand or reinterpret the
frozen action schema.

### 6.4 Error model

The Team contract owns stable codes rather than changing M6 error meanings:

```text
invalid_argument
not_found
conflict
capability_unavailable
quota_exceeded
not_ready
session_lost
protocol_mismatch
internal
```

All error data remains bounded and excludes credentials, full prompts, and
complete environment maps.

### 6.5 Initial limits

The contract-freeze increment must confirm these conservative defaults:

| Limit | Proposed value |
| --- | ---: |
| participants per TeamSession | 2-4 |
| canonical project path | 4 KiB |
| topic | 16 KiB |
| instructions | 16 KiB |
| participant label | 64 bytes |
| participant role | 2 KiB |
| one public message/contribution | 32 KiB |
| retained public messages | 256 |
| retained public message bytes | 2 MiB |
| total participant turns | 64 |
| concurrently active turns | 4 |
| retained mutation receipts | 256 |
| default Team cost grant | 1,000 coarse catalog units |
| maximum Team cost grant | 6,400 coarse catalog units |
| Handoff selected messages | 32 |
| Handoff encoded size | 128 KiB |
| event page size | 100 |
| bounded wait | 30 seconds |

Quota exhaustion returns `quota_exceeded`; Fishyume never silently truncates a
committed message. Closed TeamSessions can be explicitly archived in a future
contract, but M7 v1 does not introduce silent retention or compaction.

## 7. Persistence and Recovery

Team state uses the same user-owned state root but a separate aggregate tree:

```text
<state-root>/
  runs/<run-id>/...                   # existing, unchanged
  teams/<team-id>/
    team.json
    events.jsonl
    messages.jsonl
    participants/<participant-id>.json
    turns/<turn-id>/turn.json
    turns/<turn-id>/execution.json
    handoffs/<handoff-id>.json
    handoff-bindings.json
    action-intents/<digest>.json
```

Rules:

1. `messages.jsonl` is the canonical public-content log.
2. Team events contain message IDs and bounded summaries, not duplicate full
   message content.
3. Participant execution artifacts live under `teams`, never under synthetic
   Workflow Run/Node/Attempt paths.
4. Snapshot writes use temp-file plus atomic replacement; logs use append-only
   JSONL with strict sequence validation.
5. Team mutations use a durable journal equivalent to Run start/action
   discipline.
6. A Team controller lease is separate from a Workflow Run lease.
7. Control Plane startup reconciles non-closed TeamSessions before dispatching
   new participant turns.
8. A persisted external handle is observed before any relaunch decision.
9. Missing terminal evidence becomes `indeterminate` or `session_lost`; process
   exit and output text cannot fabricate a response.
10. Events, messages, snapshots, and receipts are validated independently so a
    partial write cannot be interpreted as a committed contribution.

The Codex process layer currently hard-codes artifacts under
`runs/<run>/nodes/<node>/attempts/<n>`. M7 must extract an internal execution
identity, result-binding, and artifact-location abstraction before Team
execution. It must not create fake Run IDs or fake Node snapshots to reuse that
path.

## 8. Delivery Plan

### M7.0: Feasibility and Boundary Spikes

Purpose: prove the two external assumptions before freezing a public contract.

Tasks:

1. Probe two explicit trusted catalog models through current Codex execution in
   `read-only` mode and verify distinct bounded contributions.
   Verify the engine-owned launch profile blocks workspace writes, approval
   elevation, child-command network access, and configured mutating tools.
2. Probe the installed Codex CLI resume surface:
   - how a session ID is obtained;
   - whether resume works non-interactively;
   - whether model, workspace, sandbox, and output schema remain enforceable;
   - behavior after process exit and Control Plane restart;
   - cancellation and lost-session evidence.
3. Determine whether Driver-owned conversation state can be resumed without
   Fishyume reconstructing hidden context.
4. Identify the minimal refactor needed to replace Run/Node/Attempt-bound
   process identity, result binding, and artifact paths with domain-neutral
   internal execution primitives while preserving the Workflow adapter.
5. Record exact capability results and reject unsupported semantics instead of
   designing around assumed CLI behavior.

Exit gate:

- one-shot Panel is feasible with current Driver and two model IDs;
- repository remains unchanged under read-only execution;
- resume capability is classified as supported, unsupported, or unstable with
  captured version evidence;
- no public Team contract is frozen until this gate is reviewed.

If resume is unsupported or unstable, M7.1 still proceeds and M7.2 Handoff
also proceeds after the Panel is accepted. M7.3/M7.4 pause;
Fishyume must not emulate resume by silently injecting an expanding transcript
into new one-shot processes.

### M7.1: One-round Multi-model Panel MVP

Status: completed on 2026-08-25. Contract, persistence, process-backed
execution, recovery, cancellation, MCP/Machine parity, and human CLI acceptance
evidence are recorded in [M7.1 acceptance](fishyume-m7.1-acceptance.md).

Purpose: solve the immediate low-cost comparison use case without Session
resume, Web, Workflow, or M5 Context.

#### M7.1.0 Contract package

- add a pure `internal/teamcontract` package;
- define `TeamSessionV1`, `ParticipantV1`, `ParticipantTurnV1`,
  `TeamMessageV1`, `ContributionV1`, `TeamTurnUsageV1`, capability/limit types,
  `HandoffArtifactV1`, Handoff binding types, validation, canonical JSON, and
  hash;
- freeze `panel` lifecycle, participant terminal states, and all four v1 action
  request shapes while capability-gating the three resumable-session actions;
- add golden and negative fixtures, including CJK and exact byte boundaries;
- reject unknown fields at public decode boundaries.

#### M7.1.1 Store and journal

- add Team paths, snapshots, logs, turn records, and start/action journals;
- implement strict sequence and quota enforcement;
- keep event payloads free of full contribution duplication;
- add crash-window tests for every durable mutation boundary;
- add future-schema fail-closed and historical-read fixtures from the first Team
  schema onward.

#### M7.1.2 Execution and recovery

- extract domain-neutral execution identity and artifact-location primitives
  from the Codex process layer while preserving the existing Workflow adapter;
- define a narrow internal `ExplorationDriver` port for capabilities, doctor,
  one-shot start, observe, output, and confirmed cancel using Team-turn
  identity rather than `AttemptEnvelope`;
- implement a Codex exploration adapter over the shared process primitives;
- add a Team execution controller that uses persisted resolved model IDs and
  the engine-owned read-only exploration profile;
- reserve each turn's trusted catalog cost before external launch and persist
  cumulative Team accounting;
- launch initial participants in parallel within Team and Driver concurrency
  limits;
- persist prepared turn identity before launch and durable handle before
  observation;
- reconcile active turns after restart without duplicate launch;
- normalize terminal output into bounded public contributions without treating
  them as Workflow Results.

#### M7.1.3 Public surface

- implement `team.capabilities`, `team.start`, `team.list`, `team.get`,
  `team.events`, `team.messages`, and `team.action` with the initial `cancel`
  action;
- expose MCP and Machine CLI parity;
- add a human command:

```powershell
fishyume team start "Compare two approaches for the requested change"
```

The short form uses the current directory and the advertised deterministic
two-model default. Explicit selection remains available:

```powershell
fishyume team start `
  --project "E:\project" `
  --participant codex/local/gpt-5.6:architect `
  --participant codex/local/gpt-5.6-luna:reviewer `
  "Compare two approaches for the requested change"
```

- provide bounded plain-text output and `--json`; no new full-screen UI is
  required for M7.1;
- make `fishyume team start` wait for the Panel and print contributions by
  default; `--detach` returns immediately, and Ctrl+C detaches observation
  without cancelling participant turns;
- provide list/show/cancel commands over the same Team contract.

Acceptance:

- no Workflow Run is created;
- two distinct models produce independently identified contributions;
- partial participant failure remains visible and does not erase good output;
- restart after handle persistence does not duplicate a participant turn;
- `team.events` pagination is monotonic and bounded;
- `team.messages` pages canonical public content without embedding it in every
  event;
- explicit cancellation confirms active participant termination before closing
  the Panel;
- MCP and Machine JSON are identical;
- unsupported v1 follow-up actions return `capability_unavailable` and create
  no mutation receipt, message, or turn;
- no Team state contains M5 Context/Memory artifacts;
- the target repository is unchanged.

### M7.2: Handoff and Explicit Workflow Promotion

Status: complete. Closure evidence is recorded in
[`fishyume-m7.2-acceptance.md`](fishyume-m7.2-acceptance.md).

Purpose: connect the accepted Panel to formal execution without an automatic
Planner and without depending on resumable sessions.

Tasks:

- use the frozen Handoff validation/canonicalization/hashing contract and
  implement its storage, service behavior, and listing;
- implement `team.handoff.create/get/list/bindRun` with durable idempotency;
- require every selected message ID and hash to match the retained Team log;
- expose a bounded Host-facing authoring instruction that explains how to turn
  a Handoff into `fishyume/v2`;
- keep Workflow generation in the Host Agent;
- preserve the existing sequence:

```text
team.handoff.get
  -> Host authors Workflow
  -> workflow.validate
  -> workflow.explain
  -> user confirms
  -> run.start
  -> team.handoff.bindRun
```

- show the bound Run ID from Team/Handoff views without adding Team fields to
  the frozen Run contract.

Acceptance:

- same Handoff input produces the same canonical hash;
- changing selected messages or decisions changes the hash;
- a missing or altered source message is rejected;
- creating a Handoff never starts a Run;
- binding is retry-safe and cannot bind an unknown Run;
- Host-created Workflow still passes the unchanged M5.6 authoring golden path;
- M5 Context/Memory begins only after `run.start` creates formal Attempts.

### M7.3: AgentSession Driver Contract

Purpose: add provider-neutral resumable conversation semantics. The Codex
app-server v2 live gate now proves one policy-preserving Harness implementation;
see [M7.3 capability evidence](fishyume-m7.3-capability-gate.md).

Tasks:

1. Define a versioned internal Session Driver port, separate from both
   Workflow `AgentDriver` and the M7.1 one-shot `ExplorationDriver`:

```text
Capabilities
StartSession
StartTurn
ObserveTurn
ParkSession
ResumeSession
CancelTurn
CloseSession
```

2. Freeze capability flags separately:

```text
supportsResume
supportsPark
supportsRecovery
supportsDirectedInput
supportsConfirmedCancel
```

3. Implement the Codex adapter only for semantics demonstrated by M7.0.
4. Reuse domain-neutral execution handles and observation/cancellation types
   where their semantics match; do not widen either earlier Driver interface.
5. Persist opaque, versioned session handles with executable identity and
   workspace/model/sandbox binding.
6. Define session-generation and turn identity so a replaced/lost session
   cannot be mistaken for the old one.
7. Keep `cancel_turn`, `park`, and `close_session` distinct. Cancellation is not
   renamed parking.
8. Add contract tests usable by future built-in Claude/OpenCode Drivers.

Acceptance:

- a participant can answer an initial prompt, park, resume, and answer one
  directed follow-up without Fishyume rebuilding hidden history;
- restart recovery observes the same session/turn before any new launch;
- wrong session identity and stale turn actions return conflict/lost errors;
- confirmed cancellation remains evidence-based;
- existing Workflow Agent Driver contracts and M6 routing behavior do not
  change.

### M7.4: Multi-turn TeamSession

Status: complete on 2026-08-25. The frozen Team contract is enabled only for
eligible resumable Session Drivers. Acceptance evidence is recorded in
[M7.4 acceptance](fishyume-m7.4-acceptance.md).

Purpose: turn the one-round Panel into bounded, user-directed exploration.

The detailed implementation decisions and acceptance matrix are frozen in the
[M7.4 implementation plan](fishyume-m7.4-implementation-plan.md).

Tasks:

- enable `session` mode only for participants whose Driver advertises resume;
- enable the already-frozen `team.action` shapes for `follow_up`, `cancel_turn`,
  and `close`; retain the existing `cancel` behavior;
- let the host address one or more participants and reference selected public
  messages;
- wake only addressed participants; do not create autonomous reply loops;
- record host messages and participant contributions in one canonical sequence;
- enforce total turns, message bytes, active turns, and session lifetime bounds;
- expose participant/session capability and lost-state diagnostics in
  `team.get`.

Acceptance:

- two participants complete an initial round and a directed cross-review round;
- the host, not a model, determines recipients and when another round starts;
- stale `expectedStateVersion` and stale participant-turn identity are rejected;
- quota exhaustion is explicit and leaves committed history readable;
- closing waits for or explicitly cancels active turns according to the chosen
  action;
- no discussion text is interpreted as Workflow completion evidence.

### M7.5: Optional Web Team Client

Status: complete on 2026-08-25. Acceptance evidence is recorded in
`docs/fishyume-m7.5-acceptance.md`. The client remains optional; no M7 or core
Workflow path requires installing it.

Purpose: add a richer human view only after the headless product path is
accepted.

Architecture:

- ship a separate optional package containing the Web app and local sidecar;
- the core Control Plane continues to listen only on Named Pipe/Unix Socket;
- the sidecar connects to that IPC and explicitly exposes loopback HTTP on an
  ephemeral port;
- no TCP listener is enabled by installing or using core Fishyume commands.

Required security:

- bind only canonical loopback addresses;
- random per-launch bearer token delivered in URL fragment, not query string;
- exact Origin/Host validation, no wildcard CORS, CSRF-resistant mutations;
- restrictive CSP and no remote script/font dependency;
- bounded request bodies and event waits;
- token rotation on restart and no durable token storage;
- sidecar exits when its owning command exits unless explicitly detached.

Initial views:

- Team list and participant status;
- ordered contributions and directed follow-up composer;
- Handoff selection/review;
- linked Workflow Run topology and existing `run.action` controls.

The Web client consumes Team and Application contracts. It owns no scheduler,
state machine, or persistence truth.

Acceptance:

- the Team flow remains complete without installing the Web package;
- unauthorized, wrong-Origin, wrong-Host, and non-loopback access are rejected;
- Web and CLI observe the same state versions and action conflicts;
- browser refresh and sidecar restart do not duplicate mutations;
- the Web bundle introduces no dependency into the core `fishyume` package.

## 9. Driver and Model Scope

M7.1 uses explicit model IDs from the trusted M6 catalog. This avoids an opaque
classifier choosing supposedly diverse participants and makes comparison
intent auditable.

Initial supported distinction:

```text
codex/local/gpt-5.6
codex/local/gpt-5.6-luna
```

A second Provider requires a built-in Driver with:

- one-shot execution contract evidence;
- read-only sandbox or equivalent permission enforcement;
- executable/session identity and recovery;
- bounded structured contribution output;
- confirmed cancellation behavior;
- explicit model catalog entries;
- Windows and Ubuntu package/CI coverage where supported.

That work may follow M7.1 or M7.2, but M7 documentation must not describe the
product as multi-provider before those gates pass. Third-party discovery,
hot-loading, and a public SDK remain a separate future milestone.

## 10. Expected Component Map

The exact filenames may change during implementation, but ownership should
remain close to this map:

```text
wf-engine/internal/teamcontract/    pure public Team contracts and fixtures
wf-engine/internal/team/            Team lifecycle, controller, recovery
wf-engine/internal/store/           Team persistence and mutation journal
wf-engine/internal/explorationdriver/ one-shot Exploration Driver port
wf-engine/internal/sessiondriver/   provider-neutral resumable Session port
wf-engine/internal/driver/codex/    existing Workflow plus new Team adapters
wf-engine/internal/controlplane/    composition and startup recovery only
wf-engine/internal/rpc/             transport routing, no Team business logic

wf/src/bridge/                       Team client types and IPC calls
wf/src/mcp/                          Team tools over the same contract
wf/src/commands/team*.ts             human/Machine Team commands
wf/src/tui/                          no required M7.1 changes

future optional Web package/         sidecar, browser client, security tests
```

Dependency rules:

- `teamcontract` imports neither Run nor Driver implementations;
- Team lifecycle depends on Exploration/Session Driver ports, not Codex or the
  Workflow `AgentDriver` directly;
- shared process primitives know domain-neutral execution identity and artifact
  locations, never synthetic Run/Node/Attempt values;
- Session Driver code does not import Workflow scheduler packages;
- Team Store and Run Store may share atomic-file/journal helpers but not record
  types or state transitions;
- MCP, Machine CLI, human CLI, and Web remain adapters;
- optional Web code never enters the core package dependency graph.

## 11. Cross-cutting Verification

Every implementation increment must run the repository baseline:

```powershell
go test ./wf-engine/...
go vet ./wf-engine/...
npm --prefix wf run verify
git diff --check
```

Risk-proportional gates additionally cover:

- deterministic contract fixtures and exact byte limits;
- start/action journal crash windows;
- controller lease loss and restart reconciliation;
- no duplicate external launch after persisted handle;
- stale state-version and stale turn conflicts;
- quota exhaustion and bounded response size;
- future Team schema fail-closed behavior;
- historical Workflow Run compatibility;
- no Team regression to M5.6 authoring or M6 routing;
- no credential, complete prompt, hidden reasoning, or environment-map leak;
- read-only repository integrity;
- MCP/Machine parity;
- Windows/Ubuntu deterministic CI without Provider credentials.

Authenticated Provider and resumable-session tests remain explicit local live
gates. Public CI uses fake Exploration/Session Drivers and crash fixtures.

## 12. Decision Gates and Stop Conditions

### Gate A: after M7.0

M7.0 passed the technical gate: both trusted Codex models produced bounded
one-shot read-only contributions in an isolated project, and the project
remained unchanged. M7.1 is approved to begin behind the frozen Team contract.

The original `exec resume` path remains unsupported because it cannot accept
the required sandbox/workspace policy controls. The later Codex app-server v2
gate passed start, process park, policy-preserving resume, recovered-turn
observation, directed follow-up, lost/stale identity rejection, and confirmed
turn interruption. The M7.3 internal Session Driver and Codex adapter now pass
their reusable lifecycle and recovery contract. M7.4 has integrated that port
with durable Team state and the frozen public Team actions; see the
[M7.4 acceptance](fishyume-m7.4-acceptance.md).

### Gate B: after M7.1 dogfooding

Use the Panel on real comparison tasks. Continue to multi-turn Session only if
the observed limitation is genuinely lack of follow-up continuity, rather than
poor roles, prompts, model diversity, or presentation.

Handoff may proceed independently once Panel value is accepted.

### Gate C: before M7.5

Approve the Web package only if MCP/CLI Team usage proves valuable and the
terminal surface is the remaining material usability constraint.

### Stop conditions

Pause or redesign if any increment requires:

- weakening the frozen Workflow Result or recovery contract;
- treating discussion text as formal completion evidence;
- reconstructing a lost external conversation while claiming identity
  continuity;
- granting repository write access to make exploration useful;
- introducing an autonomous unbounded debate loop;
- duplicating Team state in a Web database;
- adding Team methods silently to `fishyume.application/v1`.

## 13. Definition of M7 Completion

M7 is complete when:

1. A user can start a two-to-four participant read-only Panel without writing a
   Workflow.
2. Participant models, roles, contributions, partial failures, and costs are
   visible and durable.
3. If the supported Harness permits resume, a user can run bounded directed
   follow-up turns without Fishyume owning hidden conversation context.
4. A user or Host can create an immutable Handoff Artifact from selected public
   contributions.
5. A Host Agent can explicitly author, validate, explain, and start a formal
   Workflow from that Handoff.
6. The resulting Workflow uses unchanged M5 Context/Memory, M6 routing, Run
   recovery, Approval, and Result contracts.
7. MCP and Machine CLI expose identical Team semantics.
8. No M7 feature requires the optional Web client.
9. Historical Runs and the frozen M6 contract gates remain green.

If resumable Harness support is not reliable, M7 may close with Panel + Handoff
and explicitly defer multi-turn AgentSession. It must not ship a false resume
abstraction merely to satisfy the original milestone outline.
