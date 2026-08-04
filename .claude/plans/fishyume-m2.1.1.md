# Fishyume M2.1.1 Implementation Plan

> Plan status: APPROVED FOR IMPLEMENTATION
> Architecture: `docs/fishyume-m2.1.1-productization.md`
> Implementation owner: delegated existing Codex Worker
> Leader: architecture, review, verification, and acceptance

## 1. Objective

Productize the existing M2.1 single-concurrency workflow engine as Fishyume `0.2.1-alpha.1` without changing its scheduling semantics. Produce an installable npm CLI with platform Engine packages, preserve `wf` compatibility, add explicit state schema versioning, and verify real detach/resume/cancel behavior on the registered `my-agent` project.

## 2. Non-negotiable constraints

- Go remains the only Workflow parser, scheduler, state owner, and CC-Panes caller.
- TypeScript remains CLI/TUI and JSON-RPC client only.
- `maxConcurrency` remains exactly 1.
- Do not add a daemon, new Backend, automatic retry, GUI redesign, SQLite, dynamic nodes, or `agent-team-workflow`.
- Do not use npm postinstall scripts to download executable code.
- Do not persist credentials, complete environment maps, or full session histories.
- Preserve M1 doctor, ad-hoc run, legacy status, and M2 state readability.
- Keep `wf` and `WF_*` compatibility aliases during Alpha.
- Do not publish, commit, push, or modify unrelated repositories.

## 3. Phase 1 — product naming and compatibility

Implement Fishyume-facing names without renaming the source directories:

- add `fishyume` as the primary CLI binary and keep `wf` as an alias;
- update product/version/help/diagnostic text to Fishyume;
- accept both `fishyume/v1` and `wf/v1` Workflow documents;
- introduce `FISHYUME_ENGINE_PATH` and `FISHYUME_STATE_DIR` with `WF_*` fallback;
- update default state-root naming and add safe legacy-root lookup;
- add `stateSchemaVersion` to new persisted M2 snapshots, treating missing values as version 1;
- keep RPC protocol version 2 and method names unchanged.

Tests must cover old and new schema identifiers, environment precedence, old state visibility, and no destructive migration.

## 4. Phase 2 — CLI product surface

Add and test:

- `fishyume --help`;
- `fishyume --version`;
- command help for run/status/resume/cancel/doctor;
- compatibility invocation through `wf`;
- stable non-TTY output and documented exit codes;
- actionable Engine-not-found diagnostics;
- `--json` output remains exactly one object.

Do not change the meaning of Ctrl+C: first interrupt detaches and leaves the active Agent session running.

## 5. Phase 3 — npm packaging

Convert the current private CLI package into a publishable package layout while retaining the existing `wf` source directory:

- package name `fishyume`;
- version `0.2.1-alpha.1`;
- `bin.fishyume` and compatibility `bin.wf`;
- optional platform dependencies for Windows x64 and Linux x64 Engine packages;
- exact version alignment between CLI and Engine packages;
- no postinstall download hook;
- resolver order: `FISHYUME_ENGINE_PATH`, platform package, development path, `WF_ENGINE_PATH`;
- `npm pack --dry-run` and clean-install tests.

Add package metadata sufficient for a future Apache-2.0 release, but do not publish.

## 6. Phase 4 — Engine release artifacts

Add reproducible build scripts for:

- Windows x64 Engine executable;
- Linux x64 Engine executable;
- zip/tar.gz release archives;
- SHA-256 checksums;
- ignored generated outputs.

The scripts must not include provider credentials or workspace state. Unsupported platforms must fail with a clear doctor diagnostic rather than silently selecting the wrong binary.

## 7. Phase 5 — CI and verification

Add CI-ready checks for:

- `go test ./...`;
- `go test -race ./...` on Linux;
- `go vet ./...`;
- `go build ./cmd/wf-engine`;
- `npm run typecheck`;
- `npm test`;
- `npm run build`;
- package dry-run and artifact checksum verification;
- `git diff --check`.

Windows local race failure with `0xc0000139` remains an environment limitation and must be documented, not hidden.

## 8. Phase 6 — real smoke and cleanup

Create a manual, opt-in smoke procedure restricted to the registered `my-agent` project:

1. run a minimal Agent → Approval → Agent Workflow;
2. verify the first Engine exits when approval is reached;
3. use a new Engine process to approve and resume;
4. verify the downstream Agent and final Run conclusion;
5. exercise detach and explicit `fishyume cancel <run-id>`;
6. check crash/reconcile does not relaunch an existing Attempt;
7. verify all temporary Engine and test Node processes exit.

The smoke must not run in CI, must not target another project, and must not weaken TaskBinding truthfulness.

## 9. Phase 7 — documentation and release readiness

Update README and add:

- Fishyume quick start;
- installation matrix;
- package and archive release instructions;
- compatibility notes for `wf` and `wf/v1`;
- state schema and legacy lookup behavior;
- security and no-postinstall policy;
- Alpha limitations and upgrade guidance;
- example Agent → Approval → Agent Workflow.

Add Apache-2.0 license files and package metadata. This authorizes repository preparation only; it does not authorize npm or archive publication.

## 10. Acceptance checklist

- [ ] Fishyume naming is consistent in public CLI/package/docs.
- [ ] `fishyume --help` and `--version` work; `wf` compatibility works.
- [ ] New and legacy Workflow identifiers parse correctly.
- [ ] New and legacy environment variables resolve predictably.
- [ ] New state snapshots carry `stateSchemaVersion`; legacy state is not destroyed.
- [ ] npm package dry-run contains no postinstall download hook.
- [ ] Windows x64 and Linux x64 Engine artifacts build reproducibly.
- [ ] Go/TS checks and Linux race pass.
- [ ] Real registered-project smoke passes, including separate-process resume.
- [ ] No temporary Engine/Node processes remain.
- [ ] README and release notes describe the actual behavior.
- [ ] Apache-2.0 license and package metadata are present.
- [ ] No publish/commit/push was performed.

## 11. Worker reporting requirements

At completion, report changed files grouped by phase, exact commands/results, package/artifact contents, compatibility evidence, live smoke status, remaining limitations, and the Linux race result. Before reporting, update the Worker TaskBinding to completed/100 and call `report_to_leader`. Leave the tree uncommitted.
