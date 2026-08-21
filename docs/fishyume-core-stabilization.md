# Fishyume Core Stabilization Record

Date: 2026-08-22

## Outcome

The M6 core is frozen and the first repository stabilization pass is complete.
No M7 Driver ecosystem or Native Harness capability was added.

The completed changes are:

- `13c1165`: machine-readable core contract freeze with Go and TypeScript gates;
- `cd2ed87`: product CLI migration to the frozen Application API and retirement
  of legacy mutation RPC methods;
- `436cd93`: Codex subprocess and scheduler-adapter consolidation under the
  internal Driver boundary;
- `7dd0bf8`: repository documentation index, historical-plan archive, local
  tool metadata cleanup, and repository hygiene/link gates.

New state is created only through `run.start` and mutated only through
`run.action`. The non-advertised `run.status` method remains read-only for
protocol-v1 snapshots that cannot be represented by `run.get`.

## Verification

The following gates passed from a clean worktree:

- `go test ./...`;
- `go vet ./...`;
- `npm run verify`, including TypeScript typecheck/build, 90 Node tests, and
  dry-run/real package audits;
- `npm run smoke:install`;
- `node scripts/stress.mjs`, with 20 repetitions each for `internal/run`,
  `internal/store`, `internal/controlplane`, and
  `internal/driver/codexprocess`;
- repository hygiene and relative Markdown link checks;
- `git diff --check`.

The public gates remain Provider-independent. A real authenticated Codex
Provider smoke was not run during this cleanup and remains an explicit local
release gate documented in
[`fishyume-m4-live-smoke.md`](fishyume-m4-live-smoke.md).

## Remaining stabilization work

This pass does not claim that product stabilization is finished. The next work
should focus on real business Workflow acceptance, long-running state growth,
Provider failure drills, install/upgrade compatibility, and operator-facing
diagnostics. The third-party Driver SDK, dynamic discovery, Web/Desktop UI, and
Native Harness remain outside the frozen core.
