# Fishyume M4 release readiness

Fishyume M4.0-M4.3 is implemented. M4.4 now includes the product/migration release surface, deterministic MCP Host, Host/TUI-controller, and Application crash/restart acceptance, plus a local real-Codex single-node smoke. This is not a release approval: the full real Host/PTY and parallel Driver Workflow, real Provider/live crash record, and final independent review remain pending. No version bump, publish, or GitHub Release is part of this batch.

## Current release surface

- `fishyume`: Agent-facing MCP and Machine CLI, human CLI/TUI, and the compatible `wf` bin.
- `fishyume-engine-win32-x64`: Windows x64 Engine.
- `fishyume-engine-linux-x64`: Linux x64/WSL Engine.

All packages remain `0.2.1-alpha.1`. Installation performs no executable download. Engine resolution is `FISHYUME_ENGINE_PATH`, the exact installed platform package, a development checkout, then compatibility `WF_ENGINE_PATH`. The Linux package uses a local `prepack` permission fix; it is not an install hook.

New Runs use the formal `codex` Driver on target `local`. The Host Agent controls Fishyume through the Application API exposed by MCP or Machine CLI; humans attach the TUI to the same durable Run. Release packages require no CC-Panes Profile, CC-Panes control plane, project registration, TaskBinding, or managed Session. Historical CC-Panes compatibility implementation and tests remain for reading old state and diagnostics, not as a current product dependency.

## Automated evidence

Provider-independent public CI runs on both Windows and Ubuntu:

- `go test ./...`, `go vet ./...`, and `go build ./cmd/wf-engine` on both platforms;
- `go test -race ./...` on Ubuntu without weakening the Windows test/build gates;
- Driver contracts, fake Codex execution, process identity, recovery, cancellation, bounded logs, structured Result, concurrency, journal, Application, IPC, MCP/Machine parity, and historical compatibility fixtures;
- a two-client MCP Host/TUI-controller acceptance gate covering shared state, stale-version action conflict, detach/close semantics, monotonic events, and non-duplicated Attempts;
- TypeScript typecheck, tests, build, dry-run/real package audits;
- Windows and Linux package installation checks;
- cross-compiled archives and SHA-256 checksum verification.

These gates use fakes and fixtures. They do not require Provider credentials, Codex authentication, CC-Panes, Docker, or network access to a model Provider.

The repository example `docs/examples/fishyume-smoke.yaml` uses `defaults.agent.driver/target` and is validated and explained through the real Application service in an automated Go test.

## Historical evidence

M2.1/M2.2 live records from 2026-08-05 demonstrated the former Direct and CC-Panes execution paths, including parallel execution, restart recovery, approval, cancellation confirmation, and process cleanup. Those records remain useful regression history, but they predate the M4 Application API, Local Control Plane ownership, formal Driver naming, Host Agent MCP flow, and current TUI attach behavior.

Historical evidence must not be presented as current M4.4 acceptance. The legacy snapshots and compatibility tests remain intentionally; release instructions must not ask users to configure `FISHYUME_CCPANES_PROFILE_ID`, a CC-Panes Profile, or a registered CC-Panes project.

## Pending M4.4 live acceptance

The following gates are explicitly unverified by batch 1:

- a real Codex Host Agent calls capabilities, validates/explains, starts, observes, acts on, and reads the result of a Workflow through MCP;
- the real headless Codex Driver completes a parallel Agent -> Approval -> Agent Workflow on `local`;
- a manual real Host Agent plus rendered human PTY session repeats the automated MCP Host/TUI-controller concurrency gate (the production adapter, controller, and action binding are already covered without Provider credentials);
- a Provider-independent Application crash/restart acceptance now verifies live fake-Driver Attempt reconciliation, action-receipt replay, and no duplicate Start; a separate real Provider/live environment record remains optional evidence;
- final independent review reports no blocking P0/P1/P2 findings against the M4 acceptance criteria.

Until these pass, describe the product as M4.0-M4.3 implemented with M4.4 batch-1 productization complete, not as M4 fully accepted or release-ready.

## Security and release checklist

- [ ] Credentials: packages, archives, fixtures, logs, docs, and CI output contain no Provider tokens, auth files, complete environment maps, or local profile secrets.
- [ ] Prompt and log bounds: complete prompts are not persisted in general logs; request frames, summaries, Results, artifacts, event reads, schemas, errors, and Driver output retain enforced byte/item bounds.
- [ ] State and IPC ownership: one compatible owner holds each state directory; Windows Named Pipe ACL and Unix directory/socket permissions are verified; endpoints are identity-bound and TCP is not enabled by default.
- [ ] Durable actions: `clientRequestId`, `actionId`, `expectedStateVersion`, and `expectedAttempt` behavior remains crash-safe and conflict-detecting.
- [ ] Process safety: Driver executable identity, PID/fingerprint reuse protection, cancellation confirmation, and crash reconciliation are covered on supported platforms.
- [ ] Artifacts and checksums: archives are built with `CGO_ENABLED=0`, `-trimpath`, stripped symbols, expected package contents, executable Linux mode, and non-empty SHA-256 checksum files.
- [ ] Provider-independent CI: Windows and Ubuntu public gates pass without Provider login, live Codex calls, CC-Panes, Docker, or private infrastructure.
- [ ] Release independence: published packages and current instructions contain no CC-Panes Profile/control-plane requirement; retained CC-Panes code is historical compatibility only.
- [ ] Live evidence: remaining real Host/Driver/PTY/crash checks are recorded with versions, bounded logs, state cleanup, and no credentials; the deterministic crash/restart gate is covered by `npm --prefix wf run test:restart`.
- [ ] Review and repository: final independent review passes, `main == origin/main`, the worktree is clean, and the reviewed commit is the release candidate.

## Build artifacts

```powershell
./wf/scripts/build-engine-artifacts.ps1 -Target windows-amd64
./wf/scripts/build-engine-artifacts.ps1 -Target linux-amd64
./wf/scripts/verify-engine-artifacts.ps1
```

Generated archives and checksums remain ignored by Git. Publishing packages or creating a release is a separate, explicitly authorized operation after every pending gate above is satisfied.
