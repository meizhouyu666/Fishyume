# Fishyume M7.5 Optional Web Team Client Implementation Plan

> Status: complete on 2026-08-25; acceptance evidence is recorded in
> `fishyume-m7.5-acceptance.md`.
>
> Date: 2026-08-25
>
> Public contracts: unchanged `fishyume.team/v1` and
> `fishyume.application/v1`

## 1. Outcome

M7.5 adds an optional, local browser client for inspecting and controlling
Team exploration and linked Workflow Runs. It is a projection over existing
Engine contracts, not a scheduler, state machine, persistence layer, or new
public API.

The headless `fishyume` package remains complete and has no dependency on the
new package.

## 2. Package And Process Boundary

The new `fishyume-web` package contains:

```text
Node sidecar -> existing EngineBridge -> Named Pipe / Unix Socket
      |
      +-> 127.0.0.1:<ephemeral-port> -> static browser app + bounded RPC
```

The sidecar bundles the existing bridge implementation at package build time.
It does not enable an Engine TCP listener. Starting the Web client is an
explicit command and the sidecar remains owned by that foreground command.

## 3. Security Contract

1. Listen only on `127.0.0.1` with port `0` by default.
2. Generate 256 bits of entropy for every launch.
3. Deliver the token only in the URL fragment and accept it only through the
   `Authorization: Bearer` header.
4. Require the exact launch `Host` and `Origin` on every API request.
5. Reject non-loopback peers before parsing a body.
6. Expose no wildcard CORS response and reject cross-origin preflight.
7. Apply a restrictive CSP, frame denial, no-sniff, referrer denial, and
   no-store policy.
8. Accept one strict JSON RPC envelope with a 64 KiB body, eight concurrent
   calls, a 15 second response wait, and a 2 MiB response ceiling.
9. Allow only the existing Team and Application methods needed by the views.
10. Keep the bearer token in page memory and the URL fragment only; never write
    it to local or session storage.

Mutation action IDs are retained in `sessionStorage` independently of the
token. A failed request after an uncertain commit reuses the same action ID and
payload after browser refresh or sidecar restart.

## 4. Initial Product Views

- Team list with mode, lifecycle, participant count, cost, and update time;
- Team detail with participant/Turn status and canonical ordered discussion;
- directed follow-up composer with explicit recipients and public message
  references;
- exact Turn cancellation plus graceful close and whole-Team cancel;
- immutable Handoff list and review, including linked Run navigation;
- Workflow Run list and topology view with existing approval, answer, retry,
  reject, and cancel actions.

The interface is a dense operator workspace: persistent navigation, scan-first
lists, an unframed main detail surface, stable status colors, and responsive
single-column fallbacks. It contains no marketing landing page.

## 5. State And Refresh Rules

- list/detail reads always come from current Engine state;
- current Team or Run refreshes on a bounded interval while the page is visible;
- mutations include the displayed state version and show Engine conflicts;
- one mutation can be pending at a time per view;
- a successful mutation refreshes canonical state before controls re-enable;
- browser state never fabricates participant, Turn, Handoff, or Run outcomes.

## 6. Acceptance Matrix

| Requirement | Required evidence |
| --- | --- |
| optional boundary | core package manifest and pack audit remain unchanged |
| loopback only | listener address and peer validation tests |
| bearer isolation | missing/wrong token tests and fragment-only launch URL |
| CSRF resistance | exact Host/Origin and preflight rejection tests |
| bounded gateway | body, concurrency, timeout, method, and response bounds |
| state parity | Web requests carry existing state versions and action shapes |
| mutation replay | stable action ID survives uncertain response and refresh |
| useful Team flow | list, discussion, follow-up, cancellation, close, Handoff |
| linked Run control | topology plus current `run.action` controls |
| responsive UI | Playwright desktop/mobile screenshots and overlap checks |
| package quality | typecheck, tests, build, pack audit, and install smoke |

M7.5 is complete only after both packages pass their independent verification,
the repository CI passes on Windows and Ubuntu, and the browser client remains
fully optional.
