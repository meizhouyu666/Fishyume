# Fishyume M4 Agent selection migration

M4 names execution in Agent terms: a **Driver** implements the headless Agent process protocol, and a **target** identifies where that Driver runs. New CLI, Workflow, Machine, MCP, and persisted Run surfaces use `driver/target` only. The currently supported selection is `codex + local`.

## Mapping

| Legacy input | Current input | Notes |
| --- | --- | --- |
| CLI `--backend direct` | `--driver codex` | `direct` is normalized to `codex`. |
| CLI `--tool codex` | `--driver codex` | Tool selection was an execution identity; use Driver. |
| CLI `--runtime local` | `--target local` | `wsl` and `ssh` legacy values are parsed for compatibility but are not current Codex targets. |
| Workflow `defaults.backend: direct` | `defaults.agent.driver: codex` | Do not keep both if they disagree. |
| Workflow `defaults.tool: codex` | `defaults.agent.driver: codex` | Move under `defaults.agent`. |
| Workflow `defaults.runtime: local` | `defaults.agent.target: local` | Move under `defaults.agent`. |
| Node `tool: codex` | Node `agent.driver: codex` | Node overrides remain optional. |
| Node `runtime: local` | Node `agent.target: local` | Node overrides remain optional. |
| `FISHYUME_BACKEND=direct` | Explicit `--driver codex`, or Workflow `defaults.agent.driver: codex` | There is no replacement global Driver environment default. Prefer explicit, auditable configuration. |

Before:

```yaml
defaults:
  backend: direct
  tool: codex
  runtime: local
```

After:

```yaml
defaults:
  agent:
    driver: codex
    target: local
```

Before:

```powershell
$env:FISHYUME_BACKEND = 'direct'
fishyume run --backend direct --tool codex --runtime local --workflow .\workflow.yaml
```

After:

```powershell
fishyume run --driver codex --target local --workflow .\workflow.yaml
```

## Compatibility window and warnings

Legacy CLI flags, Workflow fields, and `FISHYUME_BACKEND` are still accepted at compatibility entry points in this Alpha. They emit deprecation warnings and normalize before a new Run is persisted. `direct` maps to `codex`; omitted target selection resolves to `local` where the current contract supplies that default.

Do not send legacy keys to the Application API. `system.capabilities`, Machine requests, MCP tool schemas, Application responses, and new state expose only `driver/target`. Host Agents should call `system.capabilities`, then use its advertised Driver targets.

When both current and legacy inputs are supplied, they must resolve to the same selection. A conflicting Driver or target is rejected instead of silently choosing one. Remove legacy inputs once the current form is deployed so warning output remains actionable.

The compatibility window has no removal version committed in this batch. Removal requires a separately announced release and migration gate; do not treat acceptance today as a permanent API guarantee.

## Historical state

Legacy M2 snapshots and CC-Panes execution records remain readable through compatibility code and fixtures. Fishyume does not rewrite, delete, or bulk-convert those files. A historical CC-Panes Attempt that cannot be recovered returns an explicit diagnostic; it is not converted into a new Codex execution and is never restarted under a different Driver identity.

New Runs persist `resolvedDriver`, `resolvedTarget`, Driver handle and Context metadata. They do not persist a CC-Panes Profile, TaskBinding, or managed Session identity. Back up the state directory before any operator-managed archival, and keep legacy fixtures while the compatibility window is supported.
