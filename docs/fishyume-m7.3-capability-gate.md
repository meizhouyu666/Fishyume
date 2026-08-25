# Fishyume M7.3 AgentSession Capability Gate

> Status: complete; M7.3 Session Driver implementation approved
>
> Date: 2026-08-25
>
> Harness: `codex-cli 0.148.0`, app-server v2

## Purpose

This gate determines whether Fishyume can implement a real resumable
`AgentSession` without reconstructing hidden conversation history or weakening
workspace, model, sandbox, identity, or cancellation boundaries.

It is acceptance evidence for beginning M7.3. It does not add a public Team API
and does not mean the M7.3 Session Driver is complete.

## Harness decision

`codex exec resume` remains unsuitable because it cannot reassert `--cd` and
`--sandbox`. The supported implementation candidate is the Codex app-server v2
protocol documented by the [official Codex app-server documentation](https://developers.openai.com/codex/app-server/)
and emitted by the installed CLI through `codex app-server
generate-json-schema`.

The version-matched schema exposes:

```text
thread/start
thread/resume
thread/read
turn/start
turn/interrupt
```

`thread/start` and `thread/resume` accept explicit workspace, model, sandbox,
and approval policy. Their responses report the effective values. `turn/start`
accepts explicit workspace, model, approval policy, and sandbox policy for the
turn and subsequent turns.

## Automated gate

The opt-in live gate is:

```powershell
$env:FISHYUME_LIVE_CODEX = '1'
npm --prefix wf run smoke:codex-session -- --model gpt-5.6-luna
```

It uses a new temporary workspace, a random continuity marker, and two separate
app-server processes:

1. start one persisted thread under the explicit read-only policy;
2. complete an initial turn that exercises a denied write;
3. stop the first app-server process, which is the gate's park boundary;
4. start a new app-server process and reject a random unknown thread ID;
5. resume the exact persisted thread while reasserting workspace, model,
   sandbox, and approval policy;
6. verify that the original completed turn is recovered;
7. issue a directed follow-up that does not contain the random marker and
   require the same marker from Harness-owned conversation history;
8. reject an interrupt against the completed first turn;
9. start a long-running turn, interrupt its exact identity, and require an
   `interrupted` terminal notification;
10. verify that the denied file is absent, the sentinel hash is unchanged, and
    the temporary workspace entry set is unchanged.

Fishyume never serializes or replays the hidden transcript in this probe.

## Live evidence

The authenticated Windows gate passed on 2026-08-25 with
`gpt-5.6-luna`:

| Claim | Evidence |
| --- | --- |
| `supportsResume` | `thread/resume` returned the original thread identity |
| `supportsPark` | the original app-server exited before a new process resumed the thread |
| `supportsRecovery` | the resumed response contained the original completed turn |
| `supportsDirectedInput` | a follow-up omitted the random marker and returned it exactly |
| `supportsConfirmedCancel` | the exact active turn terminated as `interrupted` |
| Workspace binding | start, resume, and turn requests asserted the same absolute cwd |
| Model binding | start, resume, and turn requests asserted `gpt-5.6-luna` |
| Sandbox binding | effective policy was `readOnly`; the deliberate write was denied |
| Session identity | thread and session IDs were unchanged across process restart |
| Lost/stale rejection | unknown thread returned `no rollout found`; completed turn returned `no active turn to interrupt` |

The sentinel SHA-256 was:

```text
3ca4f61fe787843126fa42de044896b44018e373365659e47b597f6cf9d8a18a
```

The public test suite uses a fake app-server with durable state across two
processes. Public CI never requires Provider credentials. The live gate remains
explicit and local.

## Decision

The current Codex app-server v2 surface passes the M7.3 capability gate. M7.3
may now implement the internal `AgentSession` Driver contract against this
surface.

The implementation must still:

- keep the Session Driver separate from Workflow `AgentDriver` and the M7.1
  one-shot `ExplorationDriver`;
- persist an opaque versioned handle containing executable, protocol, workspace,
  model, sandbox, thread, session-generation, and last-turn identity;
- reassert and verify policy on every resume and directed turn;
- distinguish park, cancel-turn, lost, and close transitions;
- reject stale local generation/turn identities before calling the Harness;
- advertise capabilities only after Driver contract tests pass;
- keep `fishyume.team/v1` session mode disabled until M7.4.
