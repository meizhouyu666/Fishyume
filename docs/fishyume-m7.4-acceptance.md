# Fishyume M7.4 Multi-turn TeamSession Acceptance

> Status: complete
>
> Date: 2026-08-25
>
> Scope: frozen `fishyume.team/v1` Session mode and Host-directed actions

## Delivered Slice

M7.4 enables the already-frozen Session action shapes without changing the
public Team contract. A Session Team owns one resumable external conversation
per participant. The Host explicitly selects recipients for each follow-up;
participants never form an autonomous reply loop.

The enabled Session surface is:

```text
team start --mode session
team follow-up
team cancel-turn
team close
team cancel
```

Session mode is advertised only when every resolved participant Driver supports
resume, park, recovery, directed input, and confirmed cancellation. Codex is
wired through the M7.3 app-server v2 Session Driver.

## Durable State And Recovery

- public Team, participant, Turn, event, and message records remain strict and
  bounded under the frozen schema;
- opaque Session handles and per-Turn execution envelopes live under private
  `sessions/` and `turns/*/session-execution.json` paths;
- prompts and hidden Driver transcripts are never persisted;
- recovery observes persisted active Turns before any launch decision and does
  not call `StartTurn` for an already persisted execution envelope;
- dispatching Turns without an envelope retain the same logical Turn ID for
  Driver reconciliation;
- a lost Session is persisted as `lost`, its current Turn as `indeterminate`,
  history stays readable, and later follow-up returns `session_lost` without
  creating a replacement Session;
- the internal 24-hour lifetime enters graceful `closing` during recovery or
  follow-up processing.

Action intents are strict and idempotent across these windows: intent before
message, message before event, partial Turn preparation, snapshot before
receipt, and closed Team before receipt completion. Replays do not duplicate
public messages, events, Turns, or external launches.

## Lifecycle Semantics

```text
created -> running -> open -> closing -> closed/host_closed
                    |
                    +-> cancelling -> closed/cancelled
```

`close` waits for active Turns and never calls Driver cancellation. `cancel`
confirms cancellation of active Turns before closing. `cancel_turn` targets one
exact current active Turn and requires Driver evidence before reaching
`cancelled`. Session rounds return to `open` after terminal Turns; Panel mode
keeps its existing one-round closure.

## Acceptance Evidence

| Requirement | Evidence |
| --- | --- |
| eligible capability gate | Session mode remains unavailable without all required Driver capabilities |
| two-participant initial Session round | both Sessions and Turns persist, contributions are canonical, Team becomes `open` |
| Host-directed follow-up | only listed participants receive a resumed Turn |
| canonical discussion | Host and contribution messages share one strictly ordered public sequence |
| stale and exact identity safety | state-version, participant, Session, and Turn checks reject stale actions |
| quota safety | cost, message, prompt, and Turn quota failures happen before public or external mutation |
| restart safety | persisted active execution is observed with no duplicate `StartTurn` |
| lost Session safety | retained history, `indeterminate` Turn, durable `lost` Session, no replacement |
| partial action recovery | public message/event/Turn/receipt recovery is idempotent |
| graceful close | active Turns settle without cancellation; Sessions close afterward |
| exact cancellation | only the requested active Turn is cancelled and confirmed |
| whole-Team cancel | active Turns are confirmed cancelled and Team closes `cancelled` |
| private storage bounds | strict identity decoding, opaque JSON bounds, invalid/oversized rejection |
| transport parity | RPC maps `session_lost`; MCP, Machine, and human CLI expose the frozen action surface |

## Verification

```powershell
cd wf-engine
go test ./... -count=1 -timeout 10m
go vet ./...

cd ..\wf
npm run verify

cd ..
git diff --check
```

Local verification passed on 2026-08-25: all Go packages, Go vet, TypeScript
typecheck/tests/build/package audits, and the 107-test TypeScript suite. The
Codex app-server fake fixture covers Session start, resume, directed Turn,
restart observation, crash-window reconciliation, loss, and confirmed
cancellation. Linux race verification remains a public CI gate because the
Windows race runtime is unavailable in this environment.

M7.5 Web Team Client remains optional and is not required for this headless
Session capability.
