# Fishyume M7.4 Multi-turn TeamSession Implementation Plan

> Status: complete
>
> Date: 2026-08-25
>
> Contract: unchanged `fishyume.team/v1`
>
> Acceptance: [M7.4 acceptance](fishyume-m7.4-acceptance.md)

## 1. Outcome

M7.4 turns the one-round Panel into a bounded, Host-directed TeamSession. A
Session participant owns one Driver-backed external conversation and can receive
multiple directed turns without Fishyume replaying hidden history.

This increment enables the already-frozen `session` mode and `follow_up`,
`cancel_turn`, and `close` actions. It does not add methods, fields, action
variants, error codes, Workflow state, autonomous model-to-model messaging, or
participant repository writes.

## 2. Frozen Decisions

1. Only a registered Session Driver advertising resume, recovery, directed
   input, and confirmed cancellation can serve `session` mode.
2. Codex uses app-server v2 through the M7.3 Session Driver. `codex exec resume`
   remains unsupported.
3. The Host chooses every follow-up recipient and when another round begins.
4. A follow-up performs complete participant, message-reference, state, turn,
   cost, and storage quota preflight before committing public state.
5. After commit, addressed participants execute independently; one failure does
   not erase the Host message or successful peer contributions.
6. `close` durably enters `closing`, accepts no more turns, and reaches
   `closed/host_closed` after active turns settle. It does not cancel them.
7. `cancel` durably enters `cancelling`, confirms cancellation of active turns,
   and reaches `closed/cancelled`.
8. `cancel_turn` targets one exact current Turn and succeeds only with
   evidence-based Driver confirmation.
9. A lost Session is never silently replaced. Its public history remains
   readable; the current Turn becomes `indeterminate` with a bounded diagnostic,
   and later follow-up returns `session_lost`.
10. Every participant Turn consumes its trusted M6 catalog cost before launch.
    Insufficient aggregate cost rejects a follow-up before its Host message is
    committed.
11. The internal Session lifetime is 24 hours from Team creation. Expiry enters
    the same graceful `closing` path as Host close. This bound is internal
    because the frozen v1 limits object has no lifetime field.
12. Discussion messages and contributions are untrusted exploration evidence;
    they are never interpreted as Workflow completion.

## 3. Durable Model

Existing public Team snapshots, participants, turns, messages, events, and
action intents remain canonical. M7.4 adds private Driver state beneath the Team
aggregate:

```text
teams/<team-id>/
  sessions/<participant-id>.json
  turns/<turn-id>/session-execution.json
```

The participant Session record contains only schema version, participant and
generation identity, lifecycle, and the latest opaque Session handle. A Turn
execution record contains the exact Session and Turn handles returned by the
Driver. Neither record contains a prompt or hidden transcript.

Initial input is reconstructed from the durable Team topic, instructions, and
participant role. Follow-up input is reconstructed from the already-committed
Host message and its referenced public messages. The external Harness remains
the only owner of hidden conversation context.

## 4. State Transitions

```text
created -> running -> open -> closing -> closed/host_closed
                    |       
                    +-> cancelling -> closed/cancelled

prepared -> dispatching -> active -> responded|failed|indeterminate|cancelled
```

The initial Session round starts one external Session per participant and then
one Turn. When every initial Turn is terminal, the Team becomes `open`, not
closed. A follow-up creates one Host message and one prepared Turn per addressed
participant. Participants with terminal prior Turns can be addressed; a
participant with an active Turn or lost/closed Session cannot.

State-version changes serialize public mutations. Driver handle revisions and
Session generation serialize external actions. Replayed action IDs return the
stored response without another Driver mutation.

## 5. Recovery Order

On Control Plane startup, for every non-closed Session Team:

1. load and strictly validate Team, participant Session, Turn, and action intent
   records;
2. for a Turn with a persisted execution envelope, call `ObserveTurn` before
   any launch decision and persist returned handles;
3. for a dispatching Turn without an envelope, repeat `StartTurn` with the same
   logical Turn ID so the Driver reconciles its durable launch intent;
4. for an initial participant without a Session record, repeat `StartSession`
   only while its Team Turn is still `prepared`;
5. resume incomplete follow-up, cancel-turn, close, or cancel action intents;
6. settle `closing` after all Turns are terminal and enforce lifetime expiry.

Missing or mismatched external identity is terminal `session_lost`; recovery
does not create a replacement Session or replay an expanding transcript.

## 6. Implementation Increments

### M7.4.0 Design and private storage

- freeze this plan and the 24-hour internal lifetime;
- add strict private Session/Turn execution records and Store paths;
- add Session Driver registration without changing Exploration Driver wiring.

### M7.4.1 Session initial round

- advertise `session` only when an eligible Session Driver is registered;
- start, persist, observe, park, and recover one Session per participant;
- settle the initial round to `open` and retain partial failures.

### M7.4.2 Directed follow-up

- preflight recipients, public references, active Turns, quotas, and M6 cost;
- commit one canonical Host message and addressed prepared Turns durably;
- resume only addressed Sessions, dispatch independently, commit contributions,
  and return to `open` after the round settles.

### M7.4.3 Cancellation and close

- implement exact `cancel_turn` with confirmed cancellation;
- implement graceful asynchronous `close` and retain whole-Team `cancel`;
- make every action receipt idempotent and restart recoverable.

### M7.4.4 Product surfaces

- expose enabled capabilities through `team.capabilities`;
- route all frozen actions through RPC, MCP, Machine CLI, and human CLI;
- add CLI commands for follow-up, turn cancellation, and graceful close.

### M7.4.5 Acceptance and closure

- add process-backed two-participant initial and cross-review acceptance;
- cover stale versions/Turn identities, lost Sessions, cost/message/turn quota,
  crash windows, close versus cancel, and no duplicate external launch;
- run Go tests/vet, TypeScript verify, repeated Session stress, Linux race CI,
  package audit, and frozen-contract checks;
- publish M7.4 acceptance evidence while keeping M7.5 optional.

## 7. Acceptance Matrix

| Requirement | Required evidence |
| --- | --- |
| two-round continuity | initial round plus addressed cross-review on the same external Sessions |
| Host-directed execution | only listed recipients receive a new Turn |
| canonical discussion | Host and contribution messages have one strict sequence |
| stale mutation rejection | Team version plus Session generation and Turn identity tests |
| quota safety | rejection occurs before Host-message commit or Driver launch |
| restart safety | observe/reconcile precedes launch and produces no duplicate Turn |
| lost Session safety | retained history, `indeterminate` evidence, no replacement Session |
| close semantics | graceful close does not call cancel; whole-Team cancel does |
| exact cancellation | only the requested active Turn reaches confirmed cancelled |
| architectural boundary | no Run, Attempt, Context, Memory, or Workflow contract changes |
| transport parity | RPC, MCP, Machine, and human CLI return the same action result |

M7.4 is complete only when the headless CLI/MCP flow is usable without M7.5 and
all public CI jobs, including Linux race verification, pass.
