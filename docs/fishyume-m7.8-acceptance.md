# Fishyume M7.8 Codex Dynamic Routing Acceptance

> Status: accepted locally
>
> Date: 2026-08-27
>
> Contract: `fishyume.config/v1`

## Delivered

M7.8 replaces the Workflow static model assumption with a persistent dynamic
Codex routing service. The shipped product profiles are `gpt-5.6-sol`,
`gpt-5.6-terra`, and `gpt-5.6-luna`; unqualified models returned by Codex stay
inspectable but cannot enter automatic Workflow routing.

The implementation includes:

- atomic revisioned route configuration and idempotent mutation replay;
- paginated Codex app-server `model/list` discovery;
- explicit bounded read-only active probes with cached expiry;
- product qualification, discovery, enablement, and availability state;
- immutable hash-addressed Catalog snapshots and frozen M6 hash resolution;
- persisted `fishyume.execution-profile/v1` reasoning effort;
- pre-Attempt availability gating and classified safe fallback;
- RPC, MCP, Machine CLI, operator CLI, Doctor, and Web parity;
- a Chinese Web routing view with explicit probe-cost confirmation.

Workflow remains Codex-only. Team keeps its independent Codex, Claude Code,
and OpenCode Agent routes.

## Automated Evidence

The following gates passed from the repository root on 2026-08-27:

```text
go test ./wf-engine/...
go vet ./wf-engine/...
npm --prefix wf run typecheck
npm --prefix wf test
npm --prefix wf run build
npm --prefix wf run pack:audit
npm --prefix wf run pack:audit:real
npm --prefix fishyume-web run verify
```

Focused tests additionally prove:

- old Attempts validate against their own historical Catalog hash;
- config revisions survive restart and mutation IDs replay exactly once;
- discovery alone never marks a route available;
- all-unavailable probes retain evidence and block execution before Attempt
  persistence;
- generic no-side-effect failures do not advance a dynamic fallback;
- classified pre-execution model failures still require the next route to pass
  the current availability gate;
- Sol is the default product preference and simple, standard, and complex work
  persists low, medium, and high reasoning effort respectively;
- all seven config methods retain response parity through RPC, MCP, Machine
  CLI, and the authenticated Web gateway.

## Live Codex Evidence

A live local acceptance used an isolated temporary Fishyume state root and the
installed Codex app-server. `model/list` returned eight entries, including all
three qualified GPT-5.6 routes plus unqualified inspect-only models.

The explicit active probe then executed one minimal read-only request per
qualified route. In this acceptance run, Sol, Terra, and Luna all returned
`available`; each observation received a 24-hour expiry and the active Catalog
hash remained:

```text
555fe487623cbe30012268e0a4a832e7ad26c98ed39bc88de37d24de3265ec49
```

This result is an observation of the configured upstream at acceptance time,
not a permanent capability claim. Future upstream changes are handled by the
same discovery and probe path rather than a hard-coded route assumption.

## Compatibility

`fishyume.application/v1`, `fishyume.team/v1`, and
`fishyume.routing-decision/v1` retain their existing wire contracts. New
Attempt evidence is additive and optional. The fictional historical `gpt-5.6`
route remains resolvable only through the frozen M6 Catalog and is not emitted
for new dynamic decisions.
