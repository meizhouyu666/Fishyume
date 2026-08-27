# Fishyume M7.9 Team Zero-Configuration Routing Plan

> Status: complete on 2026-08-27
> Public configuration contract: additive `fishyume.config/v1`
> Frozen contracts retained: `fishyume.team/v1`, `fishyume.application/v1`

## 1. Outcome

A new Fishyume installation must be able to use locally installed Codex,
Claude Code, and OpenCode Team workers without requiring the user to author a
JSON catalog, set an absolute-path environment variable, or restart the Host
after every configuration change.

Fishyume owns route selection and durable local route preferences. Each Agent
harness continues to own its executable installation, authentication,
Provider endpoint, credentials, aliases, and upstream fallback behavior.
Fishyume passes the selected model argument to the harness but does not attempt
to attest which upstream model eventually served the request.

## 2. Product Boundary

M7.9 includes:

- safe executable discovery for `codex`, `claude`, and `opencode`;
- built-in default Team routes for every discovered Agent;
- durable enable/disable and custom route configuration in Fishyume state;
- Host-facing inspect, refresh, upsert, and remove operations;
- immediate application of route changes to new Teams;
- unavailable Driver isolation instead of whole-Engine startup failure;
- legacy `FISHYUME_AGENT_ROUTES_FILE` import compatibility;
- Web projection of Driver and Team route readiness;
- historical Team Catalog retention for recovery.

M7.9 does not:

- install Agent CLIs or write their authentication/provider configuration;
- store API keys, tokens, base URLs, or inherited environment values;
- probe a paid model merely to discover an executable;
- verify the final upstream model behind an Agent alias;
- add Claude Code or OpenCode as Workflow Node Drivers;
- change the frozen Team or Application wire shapes.

## 3. Configuration Model

Fishyume persists Team routes under its existing state root. The file uses
`fishyume.config/v1`, an optimistic integer revision, bounded mutation replay,
and atomic replacement. Each route contains only:

- stable `routeId`;
- `driver` (`codex`, `claude`, or `opencode`);
- trusted local `provider` profile name;
- model argument, or the reserved `default` value to inherit the Agent's
  configured default;
- enabled state and origin (`builtin`, `user`, or `legacy_import`).

On first load, Fishyume creates built-in routes. If the legacy environment
file is present and no persistent Team configuration exists, its valid routes
are imported once. The environment file remains a deployment bootstrap, not a
per-launch dependency.

## 4. Discovery And Availability

Discovery resolves executables without running model turns. Driver state is
one of `available` or `unavailable`, with a bounded diagnostic. A missing
Claude Code or OpenCode executable does not prevent Codex Workflow, Team,
MCP, Machine CLI, or Web startup.

The effective Team Catalog is the intersection of enabled routes and locally
available Drivers. Built-in Codex role aliases preserve the existing default
two-participant Panel. Claude Code and OpenCode expose Agent-default routes so
a correctly authenticated harness works without Fishyume-specific model
configuration. Hosts may add explicit model routes later.

## 5. Runtime Application

Route refresh and mutation rebuild the effective Team Catalog for newly
created Teams and refresh the registered exploration/session adapters.
Existing Team snapshots retain their Catalog hash and resolve against an
in-memory historical Catalog registry. Active processes are not cancelled by
configuration changes.

If a Driver disappears after discovery, its participant fails independently
with a retained diagnostic; successful peer contributions remain available.

## 6. Public Operations

The additive configuration methods are:

- `team.routes.get`: inspect persistent and effective Team routes;
- `team.routes.refresh`: rediscover local Agent executables and apply routes;
- `team.routes.upsert`: idempotently add or replace one trusted route;
- `team.routes.remove`: idempotently remove a user route;
- existing `driver.list`: project Workflow and Team readiness together.

Host Agent, Machine CLI, and Web use the same RPC methods. Product setup calls
route refresh after configuring the Codex MCP entry. Normal Team creation uses
`team.capabilities` and never requires the user to handle route files.

## 7. Compatibility And Safety

- Existing Team snapshots, Handoffs, and Workflow Runs remain readable.
- `fishyume.team/v1` participant `modelId` remains the requested route ID.
- Existing M7.8 Codex Workflow routing files and revisions are unchanged.
- Unknown fields, unsupported Drivers, invalid identities, oversized files,
  duplicate routes, stale revisions, and mutation-ID reuse are rejected.
- Route configuration never contains secrets or arbitrary executable paths.
- `default` omits `--model`; explicit models remain explicit harness arguments.

## 8. Acceptance

M7.9 is complete when:

1. no environment variable produces a usable two-Codex Team default;
2. installed Claude Code/OpenCode CLIs appear automatically after refresh;
3. missing optional CLIs do not prevent Engine startup;
4. Host upsert/enable/disable/remove survives Engine restart;
5. route changes affect new Teams without invalidating historical Teams;
6. Claude Code/OpenCode default routes omit `--model`, while explicit routes
   pass the configured model exactly;
7. RPC, MCP, Machine CLI, setup, and Web expose consistent state;
8. Go, TypeScript, Web, package, repository, and compatibility gates pass.

The completed acceptance evidence is recorded in
[M7.9 Team zero-configuration routing acceptance](fishyume-m7.9-acceptance.md).
