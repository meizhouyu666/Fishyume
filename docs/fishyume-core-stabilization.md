# Fishyume Core Stabilization Record

Date: 2026-08-24

## Outcome

M6 is closed. Its core contracts are frozen, the repository stabilization pass
is complete, and the Provider-independent readiness baseline is accepted as
sufficient to begin M7. No M7 capability was added during stabilization.

The completed changes are:

- `13c1165`: machine-readable core contract freeze with Go and TypeScript gates;
- `cd2ed87`: product CLI migration to the frozen Application API and retirement
  of legacy mutation RPC methods;
- `436cd93`: Codex subprocess and scheduler-adapter consolidation under the
  internal Driver boundary;
- `7dd0bf8`: repository documentation index, historical-plan archive, local
  tool metadata cleanup, and repository hygiene/link gates;
- `18ca9ff` through `9407c20`: failure classification, bounded steady-state
  growth, upgrade/rollback compatibility, future-schema fail-closed behavior,
  durable rollback, and cross-commit package downgrade gates;
- `63e634c` through `84e52d1`: product-facing README, practical Agent Node
  granularity, and a bundled long-running repository Workflow example;
- `f1f81c3` through `96f3ed2`: conditioned failure propagation, interrupted
  Run initialization recovery, controller heartbeat recovery, and authoritative
  Workflow validation for failed Agent results.

New state is created only through `run.start` and mutated only through
`run.action`. The non-advertised `run.status` method remains read-only for
protocol-v1 snapshots that cannot be represented by `run.get`.

## Verification

The closing baseline passed:

- `go test ./...`;
- `go vet ./...`;
- `npm run verify`, including TypeScript typecheck/build, 95 Node tests, and
  dry-run/real package audits;
- `git diff --check`.

Earlier stabilization passes also completed:

- `npm run smoke:install`;
- `node scripts/stress.mjs`, with 20 repetitions each for `internal/run`,
  `internal/store`, `internal/controlplane`, and
  `internal/driver/codexprocess`;
- repository hygiene and relative Markdown link checks;
- the historical package downgrade rehearsal.

The public gates remain Provider-independent. A real authenticated Codex
Provider smoke was not run during this cleanup and remains an explicit local
release gate documented in
[`fishyume-m4-live-smoke.md`](fishyume-m4-live-smoke.md).

## Milestone disposition

The M6 milestone is complete and does not remain open for additional feature or
stabilization increments. Authenticated real-Provider testing, longer event and
state-growth soak, user dogfooding, operator-diagnostic refinement, and
downgrade against a future immutable published artifact are deferred validation
or release work. They may be performed later without blocking M7 development.

The packed install gate already covers same-prefix replacement and an
Approval-only durable state snapshot/restore with terminal reconvergence. The
local cross-commit package downgrade rehearsal covers the accepted M5.6 commit
without presenting that untagged alpha snapshot as a published release.

The frozen M6 contracts remain the compatibility baseline for subsequent
milestones. Third-party Driver SDK work, dynamic discovery, Web/Desktop UI, and
Native Harness behavior are not retroactively part of M6; any such M7 work must
respect the contract change policy or introduce an explicit new version.
