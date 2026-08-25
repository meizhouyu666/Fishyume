# Fishyume M7.3 AgentSession Driver Acceptance

> Status: complete
>
> Date: 2026-08-25
>
> Scope: internal provider-neutral Session Driver and built-in Codex adapter

## Delivered Slice

M7.3 adds the internal resumable conversation boundary required by a future
multi-turn TeamSession. It remains separate from both the frozen Workflow
`AgentDriver` and the M7.1 one-shot `ExplorationDriver`.

The version-one Driver port contains:

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

Capabilities explicitly advertise resume, park, restart recovery, directed
input, confirmed cancellation, supported targets, and the one-active-turn
limit. The reusable contract test can be applied to future built-in Drivers.

## Codex Adapter

The Codex implementation uses the policy-preserving app-server v2 surface
proven by the M7.3 capability gate:

- `thread/start`, `thread/resume`, and `thread/read` preserve the external
  thread identity and hidden Harness history;
- every start and resume reasserts the canonical workspace, trusted model,
  read-only sandbox, no-network turn policy, and `never` approval policy;
- executable path and SHA-256 bind a durable Session to the same Harness;
- `turn/start` records launch intent before external launch and uses the
  Fishyume Turn ID as `clientUserMessageId` for crash reconciliation;
- `turn/interrupt` is reported as confirmed only after the exact external Turn
  is observed in the `interrupted` state.

Fishyume persists policy, lifecycle, external identities, bounded public output,
and bounded diagnostics. It does not persist prompts or hidden transcripts.

## Recovery And Identity

- each mutation returns a new revision-bound Session handle;
- Session generation, durable revision, logical Turn ID, external thread ID,
  and external Turn ID reject stale or tampered actions;
- an active Turn is observed across adapter-process restart before any new
  external launch;
- a lost `turn/start` response is reconciled through the persisted logical Turn
  identity and is never replayed from the prompt;
- launch intent with no matching persisted external Turn becomes `lost`;
- used logical Turn IDs cannot be reused, with a maximum of 256 per Session;
- park, cancel-turn, and close remain distinct lifecycle operations.

Records and handles use strict JSON decoding, bounded fields, and atomic durable
updates. Server-initiated JSON-RPC requests are rejected without allowing their
numeric IDs to collide with pending client responses.

## Acceptance Evidence

| Requirement | Evidence |
| --- | --- |
| initial response and directed follow-up | reusable Driver lifecycle contract |
| park and policy-preserving resume | follow-up starts only after park/resume |
| park is not cancellation | active-Turn park returns a classified conflict |
| restart recovery | same external Turn is observed with no duplicate launch |
| launch crash reconciliation | lost response test retains one external Turn |
| stale and wrong identity rejection | generation, thread, revision, and Turn tamper tests |
| confirmed cancellation | exact Turn reaches `interrupted` before confirmation |
| bounded public evidence | oversized output maps to `failed` with no output |
| no prompt persistence | record and both handle types exclude the prompt |
| future Driver portability | provider-neutral reusable contract test package |
| architectural isolation | Session contract dependency check excludes Workflow, Run, and Team packages |

Closure gates:

```powershell
go test ./wf-engine/... -count=1
go vet ./wf-engine/...
npm --prefix wf run verify
git diff --check
```

Process-backed Session tests also pass twenty repeated lifecycle and recovery
runs. The race detector is verified on Ubuntu CI because the local Windows Go
race runtime cannot start in the current environment.

## Deferred Public Surface

M7.3 is internal infrastructure only. `fishyume.team/v1` still rejects
`session`, `follow_up`, `cancel_turn`, and `close` as unavailable. M7.4 must
integrate this Driver with durable Team state, addressed follow-ups, public
message ordering, recovery, and the already-frozen Team action schemas before
any multi-turn capability is advertised.
