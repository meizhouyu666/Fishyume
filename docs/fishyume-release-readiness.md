# Fishyume M4 release readiness

Fishyume M4 is technically closed. M4.4 includes the product/migration release surface, deterministic and real Codex MCP Host, rendered Host/PTY handoff, Host/TUI-controller concurrency, Application crash/restart acceptance, and local real-Codex single-node and parallel Driver smoke. Independent review of `b6aa2752c76642a9eaf3235df1a3e43d1dcd1804` returned APPROVE with P0/P1/P2 `0/0/0`, and GitHub Actions run [`31783982777`](https://github.com/meizhouyu666/Fishyume/actions/runs/31783982777) passed all six jobs. A real Provider crash record remains optional supplemental evidence. No version bump, publish, or GitHub Release is part of this closure.

M4.5 is an accepted Developer Preview product gate layered on this frozen technical baseline. It adds the zero-argument Dashboard, idempotent Codex setup, full product Doctor, and real packed-install golden path without changing M4 execution contracts. M5 production work may proceed from the accepted gate in [`fishyume-m4.5-developer-preview.md`](./fishyume-m4.5-developer-preview.md).

The 2026-08-14 M4.5 live record now covers global Windows Preview installation and safe idle-service upgrade, canonical Codex MCP transport plus explicit Fishyume tool authorization, a real installed-configuration `system.capabilities` call without interactive approval, and a real Host-created Run selected through the zero-argument Dashboard before PTY approval. The isolated Host/TUI evidence converged terminal with exact stale-action conflict and full temporary cleanup.

## M5 closure (Agent-native Context Engineering)

M5 closes the Agent-native product path: an external Host Agent can discover, author,
preflight, execute, observe, interact with, and collect a `fishyume/v2` Workflow through
the public contract, with the human able to attach the TUI to the same durable Run. The
bounded versioned `fishyume.authoring-guide/v1` ships in `system.capabilities`; MCP tool
descriptions carry v2 Context Policy and exact preflight/action/result preconditions; the
canonical v2 Workflow and Host request set are copyable from
[`docs/examples/fishyume-v2-host.yaml`](./examples/fishyume-v2-host.yaml) and
[`docs/examples/fishyume-v2-host-requests.json`](./examples/fishyume-v2-host-requests.json).
The Provider-independent MCP Host/TUI acceptance proves identical validate/explain/start
Context bindings, exactly-once Memory usage (omitted Memory is not consumed), idempotent
`clientRequestId` start, Approval, `needs_input`, stale-`stateVersion` conflict, event
pagination, terminal result, attach, and metadata-leak hygiene. No new MCP tool, RPC
protocol, Codex approval, or Provider credential was added; v1/history compatibility and
all M5.5 exactly-once guarantees are unchanged. Acceptance and gates are recorded in
[`fishyume-m5.6-agent-native-authoring-acceptance.md`](./fishyume-m5.6-agent-native-authoring-acceptance.md).
Local preflight (`go test ./...`, `go vet ./...`, `go build ./cmd/wf-engine`,
`npm --prefix wf run verify`, `git diff --check`) passed, and the public Windows CI
run recorded the accepted preview gates. No package or GitHub Release was published.

## Current release surface

- `fishyume`: Agent-facing MCP and Machine CLI, human CLI/TUI, and the compatible `wf` bin.
- `fishyume-engine-win32-x64`: Windows x64 Engine.
All packages remain `0.2.1-alpha.1`. The current Developer Preview publishes
Windows x64 only. Installation performs no executable download. Engine
resolution is `FISHYUME_ENGINE_PATH`, the installed Windows package, a
development checkout, then compatibility `WF_ENGINE_PATH`.

New Runs use the formal `codex` Driver on target `local`. The Host Agent controls Fishyume through the Application API exposed by MCP or Machine CLI; humans attach the TUI to the same durable Run. Release packages require no CC-Panes Profile, CC-Panes control plane, project registration, TaskBinding, or managed Session. CC-Panes execution support is retired; old milestone records are historical evidence only.

## Automated evidence

Provider-independent public CI runs on Windows:

- `go test ./...`, `go vet ./...`, and `go build ./cmd/wf-engine`;
- Driver contracts, fake Codex execution, process identity, recovery, cancellation, bounded logs, structured Result, concurrency, journal, Application, IPC, and MCP/Machine parity are run in the local full verification command;
- a two-client MCP Host/TUI-controller acceptance gate covering shared state, stale-version action conflict, detach/close semantics, monotonic events, and non-duplicated Attempts;
- TypeScript typecheck, build, dry-run/real package audits;
- Windows package installation checks;
- Windows archive and SHA-256 checksum verification.

These gates use fakes and fixtures. They do not require Provider credentials, Codex authentication, CC-Panes, Docker, or network access to a model Provider.

The repository example `docs/examples/fishyume-smoke.yaml` uses `defaults.agent.driver/target` and is validated and explained through the real Application service in an automated Go test.

## Historical evidence

M2.1/M2.2 live records from 2026-08-05 demonstrated the former Direct and CC-Panes execution paths, including parallel execution, restart recovery, approval, cancellation confirmation, and process cleanup. Those records remain useful regression history, but they predate the M4 Application API, Local Control Plane ownership, formal Driver naming, Host Agent MCP flow, and current TUI attach behavior.

Historical evidence must not be presented as current M4.4 acceptance. Release instructions must not ask users to configure `FISHYUME_CCPANES_PROFILE_ID`, a CC-Panes Profile, or a registered CC-Panes project.

## M4.4 live acceptance status

The live gates now have the following status:

- [x] A real Codex Host Agent calls capabilities, validates/explains, starts, observes, acts on, and reads the result of a Workflow through MCP.
- [x] A real Host Agent plus rendered human PTY session shares one Run with `fishyume attach`; the TUI action succeeds, the Host stale action receives `conflict`, both converge terminal, and detach preserves the result.
- [x] Provider-independent Application crash/restart acceptance verifies live fake-Driver Attempt reconciliation, action-receipt replay, and no duplicate Start. A real Provider/live crash record remains optional evidence.
- [x] Final independent review reports no blocking P0/P1/P2 findings against the M4 acceptance criteria.

M4 is closed as an accepted technical baseline. It is not a published release; package publication and a GitHub Release remain separate explicitly authorized operations.

## Closure evidence record (2026-08-14)

The repeatable real Host Agent gate is now available as
`npm --prefix wf run smoke:codex-host-mcp`. It launches `codex exec --ephemeral --json`
with only the local `fishyume mcp` stdio server, a temporary `CODEX_HOME` containing
only the locally authenticated `auth.json`, the configured relay provider, and the
read-only sandbox. On this machine `codex-cli 0.147.0` completed the full seven-tool
sequence (`system.capabilities`, `workflow.validate`, `workflow.explain`, `run.start`,
`run.events`, `run.action`, `run.result`) without a manual MCP allow. The temporary
Control Plane and Codex home were removed, and no credential, prompt, or full JSONL
was persisted. An earlier rendered Host/TUI gate used an external Windows PTY at
120 columns; that harness is historical. `codex-cli 0.147.0` started Run
`run-b45156ced1fce99208dd2228`, the TUI approved it, and the Host's completed MCP
payload proved retained waiting version `3`, exact `error.code: "conflict"`, current
version `5`, and a matching terminal `run.result` with conclusion `succeeded`.
`smoke:codex-host-pty:auto` then detached the observer and only reported success after
the temporary directory was removed. The external PTY harness is historical and is
no longer a Fishyume execution dependency.

To repeat these supplemental local gates on a machine with valid Codex authentication:

1. Run `codex login status`. If invalid, authenticate with `codex login --device-auth`
   or `codex login --with-api-key` without pasting the credential into the repository
   or chat. If the endpoint is unreachable, set `HTTP_PROXY`, `HTTPS_PROXY`, and
   `ALL_PROXY` to `http://127.0.0.1:7897` for the login command only.
2. From any PowerShell directory, set the checkout explicitly and run
   `$repo='E:\meizhouyu\agentstudy\my-agent'; $env:FISHYUME_LIVE_CODEX='1'; npm --prefix (Join-Path $repo 'wf') run smoke:codex-host-mcp`.
   Retain only its final JSON summary (Codex version, Run ID, tool sequence, sandbox,
   cleanup).
3. Run the existing real Driver parallel gate:
   `$env:FISHYUME_LIVE_CODEX='1'; npm --prefix wf run smoke:codex-live:parallel`.
4. From a real PTY, run `npm --prefix wf run smoke:codex-host-pty:auto`, press `a`
   when the Approval appears, and retain only the bounded final JSON. It must report
   `ptyHandoff: true`, `conflictCode: "conflict"`, matching retained/current versions,
   `resultConclusion: "succeeded"`, `sandbox: "read-only"`, and
   `temporaryDirectoryRemoved: true`.

## Security and release checklist

- [x] Credentials: packages, archives, fixtures, logs, docs, and CI output contain no Provider tokens, auth files, complete environment maps, or local profile secrets.
- [x] Prompt and log bounds: complete prompts are not persisted in general logs; request frames, summaries, Results, artifacts, event reads, schemas, errors, and Driver output retain enforced byte/item bounds.
- [x] State and IPC ownership: one compatible owner holds each state directory; Windows Named Pipe ACL and Unix directory/socket permissions are verified; endpoints are identity-bound and TCP is not enabled by default.
- [x] Durable actions: `clientRequestId`, `actionId`, `expectedStateVersion`, and `expectedAttempt` behavior remains crash-safe and conflict-detecting.
- [x] Process safety: Driver executable identity, PID/fingerprint reuse protection, cancellation confirmation, and crash reconciliation are covered on supported platforms.
- [x] Artifacts and checksums: the Windows archive is built with `CGO_ENABLED=0`, `-trimpath`, stripped symbols, expected package contents, and a non-empty SHA-256 checksum file.
- [x] Provider-independent CI: Windows public gates pass without Provider login, live Codex calls, CC-Panes, Docker, or private infrastructure.
- [x] Release independence: package audits and current instructions contain no CC-Panes Profile/control-plane requirement; CC-Panes execution code is retired.
- [x] Live evidence: real Host MCP, Host/PTY conflict, and real Driver checks are recorded with versions, bounded logs, state cleanup, and no credentials; deterministic crash/restart is covered by `npm --prefix wf run test:restart`.
- [x] Review and repository: independent review approved exact SHA `b6aa2752c76642a9eaf3235df1a3e43d1dcd1804`; `main == origin/main`, CI passed, and the closure follow-up is documentation-only.

## Build artifacts

```powershell
./wf/scripts/build-engine-artifacts.ps1 -Target windows-amd64
./wf/scripts/verify-engine-artifacts.ps1
```

Generated archives and checksums remain ignored by Git. Publishing packages or creating a release remains a separate, explicitly authorized operation outside this M4 technical closure.
