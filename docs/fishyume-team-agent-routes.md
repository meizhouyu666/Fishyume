# Fishyume Team Agent Routes

Fishyume Team talks to installed Agent harnesses, not directly to model APIs.
The Host selects a trusted route by model ID; Codex, Claude Code, or OpenCode
owns authentication, Provider settings, aliases, and conversation storage.

Team Panel and TeamSession support `codex`, `claude`, and `opencode`. Workflow
Nodes remain Codex-only.

## Zero-configuration defaults

On first Engine start, Fishyume creates persistent built-in Team routes and
discovers the three Agent executables without making a model request. Missing
Claude Code or OpenCode installations do not prevent the Engine, Codex
Workflow, or other Team workers from starting.

`fishyume setup` refreshes discovery automatically. A Host can also call
`team.routes.refresh`; the equivalent advanced CLI is:

```powershell
fishyume team routes refresh
fishyume team routes
```

The built-in Claude Code and OpenCode routes use model `default`. Fishyume then
omits `--model`, allowing the Agent's own trusted configuration to select its
default. Built-in Codex role routes use the product-qualified Codex model.

## Explicit models

Hosts normally read `team.routes.get` and `team.capabilities`, then select a
returned `modelId`. To add an explicit model route through the advanced CLI:

```powershell
fishyume team routes set `
  --route claude/default/sonnet `
  --driver claude `
  --provider default `
  --model sonnet

fishyume team routes set `
  --route opencode/deepseek/deepseek-chat `
  --driver opencode `
  --provider deepseek `
  --model deepseek/deepseek-chat
```

Route changes are stored atomically under the Fishyume state directory and
apply to new Teams immediately. Existing Teams retain their original Catalog
hash and recovery binding. Built-in routes can be disabled but not removed;
user routes can be removed with `fishyume team routes remove <route-id>`.

`target.provider` is a trusted local profile name. `target.model` is the model
argument passed to the harness, except for the reserved `default` value:

| Driver | Explicit model form | Session mechanism |
| --- | --- | --- |
| Codex | `gpt-5.6-sol` | app-server thread |
| Claude Code | `sonnet` or a full Claude model ID | `--session-id` / `--resume` |
| OpenCode | `provider/model` | created Session ID / `--session` |

Fishyume records the requested route. It does not attest which upstream model
an Agent alias, proxy, or Provider fallback eventually used.

## Legacy catalog import

`FISHYUME_AGENT_ROUTES_FILE` remains a compatibility bootstrap. When no
persistent Team route configuration exists, Fishyume imports the strict
absolute-path `fishyume.capability-catalog/v1` file once and then owns the
persisted copy. The environment variable is not required on later starts.

The legacy file is bounded to 1 MiB and rejects unknown fields, trailing JSON,
relative paths, invalid capabilities, and duplicate routes.

## Security boundary

Never put API keys, base URLs, tokens, or environment variables in a Team
Route. The public mutation API accepts only Driver, profile, model, route ID,
and enabled state; it does not accept executable paths or credentials.

Team workers are read-only research and discussion participants. Claude Code
starts with only `Read`, `Glob`, and `Grep`. OpenCode receives an in-memory
primary Agent policy that denies every permission except `read`, `glob`, and
`grep`. Both use durable process supervision and confirmed process-tree
cancellation.

All three Team Drivers return ordinary Markdown. Fishyume validates its size
and wraps it locally as `ContributionV1`; Agent harnesses do not need to emit a
JSON Schema response. Formal Workflow Nodes retain their strict structured
result contract.
