# Fishyume M2.1.1 Productization Architecture

> Status: approved direction, ready for implementation planning
> Product: Fishyume
> Milestone: M2.1.1 — first installable Alpha

## 1. Goal

M2.1.1 turns the M2.1 workflow engine into a usable, installable Fishyume Alpha. It does not expand scheduling semantics. The milestone validates the existing single-concurrency DAG engine in real CC-Panes usage, gives it a stable product identity, and produces reproducible release artifacts.

The milestone is complete when a user can install Fishyume on Windows x64 or Linux x64/WSL, run an ad-hoc task or YAML workflow, approve/resume/cancel it from a separate process, and diagnose failures without knowing a CC-Panes session ID.

## 2. Product identity

- Display name: `Fishyume`
- Primary command: `fishyume`
- Compatibility command: `wf`
- Primary npm package: `fishyume`
- Engine package family: `fishyume-engine-*`
- New workflow schema identifier: `fishyume/v1`
- Compatibility schema identifier: `wf/v1`
- New environment variables: `FISHYUME_ENGINE_PATH`, `FISHYUME_STATE_DIR`
- Compatibility environment variables: `WF_ENGINE_PATH`, `WF_STATE_DIR`
- Alpha version target: `0.2.1-alpha.1`

The source directories `wf/` and `wf-engine/` remain unchanged during this milestone to avoid a noisy repository rename. Public names, generated package metadata, docs, and user-facing diagnostics use Fishyume.

The npm name `fishyume` was unregistered during planning. Publishing or reserving it is an explicit release action and is not part of implementation.

## 3. Installation and distribution

The primary distribution is an npm CLI package with platform-specific optional Engine packages:

```text
fishyume
fishyume-engine-win32-x64
fishyume-engine-linux-x64
```

The CLI package contains the TypeScript bridge, commands, TUI, and compatibility alias. The platform package contains only the matching Go Engine binary. Exact versions are pinned across the package family.

The installer must not use a postinstall script to download executable code. Unsupported platforms receive the CLI package but `fishyume doctor` reports the missing Engine and explains `FISHYUME_ENGINE_PATH`.

Engine resolution order:

1. `FISHYUME_ENGINE_PATH`;
2. the installed platform package;
3. a development checkout path;
4. legacy `WF_ENGINE_PATH`.

GitHub-style release archives are a secondary, offline-friendly format. The build produces Windows zip, Linux tar.gz, and a SHA-256 checksum file. The repository currently has no remote, so M2.1.1 prepares these artifacts but does not publish them.

## 4. Compatibility and state migration

RPC remains protocol version 2 and keeps the existing `run.*` method names. The product rename must not break existing M2 state or scripts:

- parse both `fishyume/v1` and `wf/v1`, normalizing to the Fishyume schema in new snapshots;
- accept both Fishyume and legacy `WF_*` path overrides;
- keep `wf` as a command alias;
- read legacy state roots and M1 snapshots as status-only where applicable;
- add an explicit `stateSchemaVersion` independent from RPC protocol and product version;
- treat missing `stateSchemaVersion` on existing M2 snapshots as version 1;
- never silently rewrite or delete legacy state.

New default state is Fishyume-named. Legacy state lookup is read-only fallback until an explicit migration command is designed. No automatic destructive migration is allowed.

## 5. CLI contract

The Alpha must expose:

```text
fishyume --help
fishyume --version
fishyume doctor
fishyume run --project <path> "<task>"
fishyume run --workflow <file> --project <path> [--input key=value]
fishyume status <run-id> [--json]
fishyume resume <run-id> [--approve|--reject|--retry ...]
fishyume cancel <run-id>
```

`wf` accepts the same commands as a compatibility alias. `--json` remains one machine-readable object. Exit codes remain stable and documented. The first Ctrl+C remains detach; `cancel` is an explicit Workflow-level command.

## 6. Verification gates

Automated gates:

- Go tests, vet, build, and Linux race tests;
- TypeScript typecheck, tests, and build;
- npm package creation without network download hooks;
- platform Engine package smoke checks;
- `git diff --check` and clean generated-file policy.

Manual release gate, restricted to the already registered `my-agent` project:

1. Agent → Approval reaches waiting;
2. first Engine exits cleanly;
3. a new Engine approves and resumes;
4. downstream Agent completes;
5. detach, cancel, and crash/reconcile behavior are checked;
6. no test Engine or Node process remains.

The live gate is never run automatically in CI and never targets an unregistered project.

## 7. Security and release policy

- no credentials, complete environment maps, or terminal histories are added to packages or snapshots;
- no postinstall network execution;
- release archives include checksums;
- no automatic update mechanism in Alpha;
- no npm publish, GitHub release, commit, or push without explicit authorization;
- license: Apache-2.0; implementation may add the license files, but public release still requires explicit authorization.

## 8. Explicit non-goals

M2.1.1 does not add `maxConcurrency > 1`, parallel cancellation, new Backends, GUI redesign, automatic retry, remote service hosting, SQLite, dynamic nodes, or `agent-team-workflow`.
