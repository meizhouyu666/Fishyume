# Fishyume M2.1.1 Finalization Hotfix

> Status: approved for implementation
> Scope: CC-Panes execution permissions, startup idle reconciliation, final smoke
> Architecture owner: Leader
> Implementation owner: delegated Codex Worker

## Objective

Close the two defects exposed by the registered-project live smoke without changing M2.1 scheduling semantics:

1. Engine-managed hidden Agent nodes must be launchable with an explicitly configured non-interactive CC-Panes profile so mandatory control-plane MCP calls do not require per-call user approval.
2. A transient idle state during Codex/MCP startup must not immediately become `waiting/completion_missing`.

## Required implementation

### CC-Panes execution profile

- Launch through the CC-Panes `launch_task` control-plane call rather than the legacy ctl `launch` surface when needed to carry profile metadata.
- Add a Fishyume-named profile configuration entry, with legacy alias only if consistent with existing compatibility policy.
- Fishyume configuration must take precedence over the legacy alias.
- Pass the exact configured profile ID to `launch_task`; do not resolve or execute arbitrary PATH shims.
- Preserve existing project/tool/runtime/title/prompt behavior and opaque session metadata.
- Do not create or bind a global profile from repository code. Profile creation remains an explicit CC-Panes administrative action.
- Document that the dedicated profile is non-interactive and should be scoped to Fishyume-managed workers. Do not silently enable a global unrestricted profile.
- Add payload/precedence/compatibility tests and actionable diagnostics for invalid configuration or launch rejection.

### Startup idle reconciliation

- TaskBinding terminal truth always wins.
- Do not classify the first observed `idle` after launch as completion missing.
- Perform bounded, cancellation-aware reconciliation that rechecks both TaskBinding and session state.
- If the session becomes active/initializing/thinking/tool-running/compacting, continue normal waiting without launching another Agent.
- If a terminal Binding appears, consume it exactly once.
- Only sustained idle with a non-terminal Binding may become `waiting/completion_missing`.
- Keep waitingInput, exited/lost, error, detach, cancel, and indeterminate semantics truthful.
- Use deterministic tests; avoid long wall-clock sleeps.

## Verification

- `go test ./...`
- `go vet ./...`
- `go build ./cmd/wf-engine`
- TS typecheck, 15 tests, production build, dry/real 33-file pack audits
- `git diff --check`
- Existing package/artifact checks remain green
- Registered `my-agent` smoke must cover Agent -> Approval -> Agent, separate-process resume, no duplicate launch, explicit cancel, and no Engine/CLI residue
- Windows race `0xc0000139` remains documented; Linux race remains an Ubuntu CI gate

## Constraints

- Keep maxConcurrency exactly 1.
- Do not change Plan/Architecture files from earlier milestones.
- Do not add a daemon, new Backend, GUI, auto-update, or agent-team-workflow.
- Do not publish, commit, push, or create a PR.
- Leave the working tree uncommitted.
