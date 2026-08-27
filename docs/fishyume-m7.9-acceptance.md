# Fishyume M7.9 Team Zero-Configuration Routing Acceptance

> Status: accepted on 2026-08-27
> Scope: local Team Agent discovery, persistent Team routes, and Host surfaces

## Accepted Outcome

A new Fishyume installation no longer requires
`FISHYUME_AGENT_ROUTES_FILE`, a hand-written Agent catalog, or absolute CLI
paths for normal Team use. Fishyume discovers installed Codex, Claude Code,
and OpenCode executables, creates durable built-in routes, and exposes the
same state to RPC, MCP, Machine CLI, setup, and Web.

Fishyume still does not install or authenticate Agent CLIs, store Provider
credentials, or attest the upstream model that an Agent ultimately uses.
`model=default` deliberately delegates model selection to that Agent.

## Live Discovery Evidence

An isolated state root was started without
`FISHYUME_AGENT_ROUTES_FILE`. Discovery resolved all three installed CLIs:

```text
Claude:   C:\Users\zhang\AppData\Roaming\npm\claude.cmd
Codex:    C:\Users\zhang\AppData\Roaming\npm\codex.cmd
OpenCode: C:\Users\zhang\AppData\Roaming\npm\opencode.cmd
```

No Agent process or model request was used for discovery. Fishyume generated
four effective routes:

```text
claude/default/default
codex/architect/gpt-5.6-sol
codex/reviewer/gpt-5.6-sol
opencode/default/default
```

`team.capabilities` reported both `panel` and `session` modes with the same
four templates. Claude Code and OpenCode default routes omit `--model`; an
explicit route passes its configured model argument unchanged.

## Persistence And Compatibility

- Team route configuration is stored under the Fishyume state root with an
  optimistic revision, bounded mutation replay, and atomic replacement.
- Legacy Agent route files are imported only when persistent Team routing is
  absent; the imported result survives restart without the environment file.
- Refresh and mutation apply to newly created Teams. Historical Catalogs are
  retained by hash so existing Team snapshots remain recoverable.
- A missing optional Claude Code or OpenCode executable degrades only that
  Driver and does not prevent Engine, Codex Workflow, MCP, CLI, or Web startup.
- `fishyume.team/v1` and `fishyume.application/v1` remain unchanged.
- Workflow Nodes remain Codex-only. M7.9 adds no Claude Code or OpenCode
  Workflow Driver.

## Automated Verification

The following gates passed on 2026-08-27:

```text
cd wf-engine && go test ./...
cd wf-engine && go vet ./...
npm --prefix wf run verify
npm --prefix fishyume-web run verify
```

These gates cover the Go Engine, route persistence and migration, Team
recovery, frozen contracts, RPC/MCP/CLI projection, Web security and build,
package contents, and repository compatibility checks.

## User Path

After installing and signing in to the desired Agent CLIs, a Host Agent only
needs to run `fishyume setup`. A later Agent installation can be picked up by
`fishyume team routes refresh`; `fishyume team routes` and the Web routing
page expose the effective state. JSON and environment-variable configuration
are compatibility and deployment options, not normal onboarding steps.
