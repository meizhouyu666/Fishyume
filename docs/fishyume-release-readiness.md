# Fishyume 0.2.1-alpha.1 release readiness

Fishyume is prepared, not published. Publishing npm packages, creating release archives on a public host, committing, pushing, or creating a PR requires separate authorization.

## Package family

- `fishyume` — CLI/TUI/RPC client, with `fishyume` and `wf` bins.
- `fishyume-engine-win32-x64` — Windows x64 Engine only.
- `fishyume-engine-linux-x64` — Linux x64/WSL Engine only.

All packages use the exact version `0.2.1-alpha.1`. The CLI has no `postinstall` downloader. Engine resolution is `FISHYUME_ENGINE_PATH`, installed platform package, development checkout, then `WF_ENGINE_PATH`.

Before launching Fishyume-managed workers, an administrator must create a dedicated non-interactive CC-Panes launch profile and set `FISHYUME_CCPANES_PROFILE_ID` to its exact ID. Fishyume passes the ID to `launch_task`; it does not resolve a PATH shim, create a profile, bind a workspace/global default, or silently enable unrestricted execution. `WF_CCPANES_PROFILE_ID` is a compatibility alias and loses to the Fishyume-named variable.

The Linux platform package intentionally declares `bin/fishyume-engine` as its npm `bin` entry so npm installs the native Engine with executable permissions. Its local-only `prepack` hook normalizes that already-built binary to mode `0755` before the Ubuntu release gate creates the tarball and rejects non-Linux packing, where executable mode cannot be represented reliably. It performs no download and is not an install/postinstall hook. The main CLI still resolves and spawns the exact file inside `fishyume-engine-linux-x64`; it does not search `PATH` or execute an arbitrary shim. The Windows package continues to expose its `.exe` through the exact platform-package path used by the resolver.

## Build archives

```powershell
./wf/scripts/build-engine-artifacts.ps1 -Target windows-amd64
./wf/scripts/build-engine-artifacts.ps1 -Target linux-amd64
```

The script cross-compiles with `CGO_ENABLED=0`, `-trimpath`, stripped symbols, creates a Windows zip or Linux tar.gz, copies the matching binary into the platform package staging directory, and writes SHA-256 checksums. Generated outputs are ignored by Git.

## Manual live smoke

Run only against the already registered `my-agent` project; never run this gate in CI.

```powershell
$env:FISHYUME_ENGINE_PATH = (Resolve-Path ./wf-engine/wf-engine.exe).Path
$env:FISHYUME_STATE_DIR = 'E:\tmp\fishyume-live-smoke'
$env:FISHYUME_CCPANES_PROFILE_ID = '<exact-dedicated-profile-id>'
fishyume run --workflow ./docs/examples/fishyume-smoke.yaml --project 'E:\meizhouyu\agentstudy\my-agent'
fishyume resume <run-id> --approve approve
fishyume status <run-id> --json
fishyume cancel <run-id>
```

Verify the first Engine exits at approval, resume uses a separate Engine process, no existing Attempt is relaunched during reconcile, and all temporary Engine/Node processes exit. The first Ctrl+C detaches; it does not cancel the active Agent.

The Windows Go race binary may terminate with loader status `0xc0000139`; keep that platform issue documented rather than treating it as a successful race gate. `go test -race ./...` remains a required Ubuntu CI gate.

## Compatibility and safety

`fishyume/v1` is canonical and `wf/v1` remains accepted. New state uses Fishyume-named roots and explicit `stateSchemaVersion: 1`; missing state schema values are also read as version 1. This field is independent from RPC protocol version 2. Legacy roots are a read-only status fallback and are never automatically deleted or destructively migrated.

Provider credentials, full environment maps, complete prompts, and terminal histories must not be packaged or persisted. Alpha has no automatic updates, daemon, parallel Agents, new Backend, GUI, or retry policy.
