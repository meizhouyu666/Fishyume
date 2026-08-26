# Fishyume Team Agent Routes

Fishyume Team talks to installed Agent harnesses, not directly to model APIs. The Host selects a trusted route by model ID; the harness owns authentication, provider settings, and conversation storage.

Supported Team Panel and TeamSession Drivers are `codex`, `claude`, and `opencode`. Workflow Nodes remain Codex-only.

## Configure routes

Set `FISHYUME_AGENT_ROUTES_FILE` to an absolute path before starting the Fishyume Engine. A complete local example is available at [`docs/examples/agent-routes.json`](examples/agent-routes.json).

Without a route file, Team uses two independent Codex roles backed by `gpt-5.6-sol`; the frozen M6 Workflow catalog remains unchanged. Add a route file when the Host should compare different Agent harnesses or provider profiles.

The file is strict JSON using `fishyume.capability-catalog/v1`. Routes are sorted canonically before hashing. Unknown fields, trailing JSON, relative paths, invalid capabilities, and files larger than 1 MiB are rejected.

`target.provider` is a trusted local profile name. `target.model` is the model value passed to that harness:

| Driver | Model form | Session mechanism |
| --- | --- | --- |
| Codex | `gpt-5.6-sol` | app-server thread |
| Claude Code | `sonnet` or a full Claude model ID | `--session-id` / `--resume` |
| OpenCode | `provider/model` | created Session ID / `--session` |

Do not put API keys, base URLs, tokens, or environment variables in this file. Fishyume inherits the harness environment and never copies credentials into Team snapshots.

## Read-only boundary

Team Session workers are research and discussion participants. Claude starts in safe mode with only `Read`, `Glob`, and `Grep`. OpenCode receives an in-memory primary Agent policy that denies every permission except `read`, `glob`, and `grep`. Both use a durable process supervisor for recovery and confirmed process-tree cancellation.

For all three Team Drivers, the model response is ordinary Markdown. Fishyume validates its size and locally wraps it as `ContributionV1`; the harness is not required to produce a JSON Schema response. Formal Workflow Nodes keep their separate strict structured-result contract.
