# Fishyume 0.2.1-alpha.1 release readiness

Fishyume is prepared, not published. Publishing npm packages or creating a GitHub Release remains a separate operation and is not part of M2.1.2.

## Package family

- `fishyume` — CLI/TUI/RPC client, with `fishyume` and `wf` bins.
- `fishyume-engine-win32-x64` — Windows x64 Engine only.
- `fishyume-engine-linux-x64` — Linux x64/WSL Engine only.

All packages use the exact version `0.2.1-alpha.1`. The CLI has no `postinstall` downloader. Engine resolution is `FISHYUME_ENGINE_PATH`, installed platform package, development checkout, then `WF_ENGINE_PATH`.

The Linux platform package declares `bin/fishyume-engine` as its npm `bin` entry so npm preserves executable permissions. Its local-only `prepack` hook changes the already-built binary to mode `0755`; it performs no download and is not an install hook. The CLI resolves an exact Engine file and does not execute an arbitrary PATH shim.

## Agent Backends

Fishyume Engine currently registers two Backends behind the same platform-neutral contract:

- `ccpanes` remains the default and preserves the M2.1.1 control-plane path. It requires a dedicated non-interactive profile in `FISHYUME_CCPANES_PROFILE_ID`; Fishyume never creates or globally binds an unrestricted profile.
- `direct` supervises an installed Codex CLI directly. It currently supports Windows/Linux, tool `codex`, and runtime `local`. `FISHYUME_CODEX_PATH` can select the native executable and `FISHYUME_DIRECT_SANDBOX` defaults to `workspace-write`.

The selected Backend is persisted on the normalized Workflow, Run, and Attempt. Resume, observe, and cancel use that stored identity rather than a changed environment default. One Workflow cannot mix Backends yet, and third-party plugin loading is not part of this Alpha.

## Build archives

```powershell
./wf/scripts/build-engine-artifacts.ps1 -Target windows-amd64
./wf/scripts/build-engine-artifacts.ps1 -Target linux-amd64
```

The script cross-compiles with `CGO_ENABLED=0`, `-trimpath`, stripped symbols, creates a Windows zip or Linux tar.gz, copies the matching binary into the platform package staging directory, and writes SHA-256 checksums. Generated outputs are ignored by Git.

## Automated gates

Public CI runs on Windows and Ubuntu and includes:

- all Go tests, vet, build, and Ubuntu race tests;
- shared Backend contract tests for CC-Panes and Direct;
- executable fake Backend tests, including Direct supervisor recovery, cancellation, PID/fingerprint protection, bounded logs, and structured-result validation;
- the same Agent → Approval → Agent Workflow through both Backends, with a new Service instance resuming after approval and no duplicate Agent start;
- M2.1.1 historical state compatibility fixtures;
- TypeScript typecheck/tests/build and package audits;
- Windows and Linux platform-package installation checks.

Real Codex authentication and a live CC-Panes control plane are intentionally excluded from public CI.

The Windows Go race binary may terminate with loader status `0xc0000139`; this remains a documented platform issue rather than a successful gate. `go test -race ./...` is required on Ubuntu CI.

## Live smoke evidence

### Direct Codex CLI

The M2.1.2 live smoke passed on 2026-08-05 with `codex-cli 0.144.6` and a read-only sandbox:

- the first Engine exited while the Agent was still running;
- a later Engine recovered from the persisted Direct handle without launching a duplicate Attempt;
- the final Node succeeded with summary `direct-live-smoke-ok` and `resultConsumed=true`;
- the handle contained supervisor/child identity and executable integrity data, but no full prompt or result token;
- the supervisor and Codex child were confirmed stopped, and the temporary state directory was removed.

For a repeatable manual Workflow smoke:

```powershell
$env:FISHYUME_ENGINE_PATH = (Resolve-Path ./wf-engine/wf-engine.exe).Path
$env:FISHYUME_STATE_DIR = 'E:\tmp\fishyume-direct-smoke'
$env:FISHYUME_DIRECT_SANDBOX = 'read-only'
fishyume doctor --backend direct --project 'E:\meizhouyu\agentstudy\my-agent'
fishyume run --backend direct --workflow ./docs/examples/fishyume-smoke.yaml --project 'E:\meizhouyu\agentstudy\my-agent'
fishyume resume <run-id> --approve approve
fishyume status <run-id> --json
```

### CC-Panes

The CC-Panes live smoke was not rerun during this M2.1.2 closure because the current environment does not provide `FISHYUME_CCPANES_PROFILE_ID`. The prior M2.1.1 registered-project live evidence remains applicable, and this milestone adds executable Adapter tests plus the M2.1.1 compatibility matrix instead of fabricating a new live result.

To rerun it after supplying the dedicated profile:

```powershell
$env:FISHYUME_ENGINE_PATH = (Resolve-Path ./wf-engine/wf-engine.exe).Path
$env:FISHYUME_STATE_DIR = 'E:\tmp\fishyume-ccpanes-smoke'
$env:FISHYUME_CCPANES_PROFILE_ID = '<exact-dedicated-profile-id>'
fishyume doctor --backend ccpanes --project 'E:\meizhouyu\agentstudy\my-agent'
fishyume run --backend ccpanes --workflow ./docs/examples/fishyume-smoke.yaml --project 'E:\meizhouyu\agentstudy\my-agent'
fishyume resume <run-id> --approve approve
fishyume status <run-id> --json
```

The repository's `.codex/config.toml` keeps a disabled loopback placeholder for the `ccpanes` MCP server with `default_tools_approval_mode = "approve"`. CC-Panes replaces the URL and enables that server for managed sessions; Direct sessions do not connect to the placeholder.

## Compatibility and safety

`fishyume/v1` is canonical and `wf/v1` remains accepted. New state uses Fishyume-named roots and explicit `stateSchemaVersion: 1`; missing state schema values are also read as version 1. This field is independent from RPC protocol version 2. Legacy roots are a read-only status fallback and are never automatically deleted or destructively migrated.

Provider credentials, full environment maps, complete prompts, and terminal histories must not be packaged or persisted. Direct prompts are passed to the supervisor over stdin, output logs are bounded, process reuse is guarded by persisted fingerprints, and a successful process exit alone is not accepted as an Agent success.

The Alpha still has no automatic updates, daemon, parallel Agents, mixed-Backend Workflow, generic Shell/HTTP/container nodes, GUI, or automatic retry policy.
