# Fishyume M7.8 Codex Dynamic Routing Correction

> Status: completed; see [M7.8 acceptance](fishyume-m7.8-acceptance.md)
>
> Date: 2026-08-27
>
> New public contract: `fishyume.config/v1`
>
> Frozen contracts: `fishyume.application/v1`, `fishyume.team/v1`, and the
> historical `fishyume.routing-decision/v1` wire shape remain compatible.

## 1. Outcome

M7.8 replaces Workflow's static two-model assumption with an observable,
persisted, and failure-tolerant Codex routing system. Fishyume discovers the
models exposed by the installed Codex Agent, verifies which selected models
the current upstream can actually execute, and routes only through the
intersection of product-qualified, discovered, enabled, and available routes.

The first qualified family is:

- `gpt-5.6-sol`
- `gpt-5.6-terra`
- `gpt-5.6-luna`

Other models returned by Codex remain inspectable but are not selected
automatically until Fishyume ships a trusted product profile for them.

Workflow remains Codex-only in M7.8. Team keeps its independent Codex, Claude,
and OpenCode Agent adapters. Adding Workflow Drivers for Claude or OpenCode is
explicitly out of scope.

## 2. Historical Catalog Compatibility

Changing an active Catalog must never make an existing Run or Attempt
unreadable. Fishyume therefore uses an immutable Catalog registry:

1. every Catalog is canonicalized and addressed by its SHA-256 hash;
2. the frozen M6 Catalog remains registered permanently;
3. the current effective Catalog is registered before it can issue decisions;
4. Attempt validation, cost accounting, recovery, retry, and fallback resolve
   the exact Catalog named by the persisted decision hash;
5. an unknown historical hash fails closed and reports a compatibility error;
6. Catalog snapshots used by durable Runs are stored under the Fishyume state
   root so future product versions can recover locally configured Catalogs.

`routing-decision/v1` is not mutated. Reasoning effort is recorded in a new,
versioned execution profile associated with the decision and Attempt.

## 3. Product Profiles And Routing

Each qualified model has Engine-owned metadata for capabilities, context and
output bounds, relative quality, cost, latency, supported reasoning efforts,
and its default effort. These are product policy, not claims copied blindly
from the local Agent.

Initial effort policy:

| Workload | Preferred route | Effort |
| --- | --- | --- |
| simple | Sol | `low` |
| standard | Sol | `medium` |
| complex | Sol | `high` or `xhigh` |
| explicit maximum budget | Sol | `max` or `ultra` |

Terra and Luna remain eligible alternatives when discovered, enabled, and
available. The resolver continues to honor explicit candidates, capability
requirements, quality, latency, context, output, and cost limits. Every
selection and fallback is persisted and auditable.

## 4. Availability State

Model state is represented independently:

- `qualified`: Fishyume has a trusted product profile;
- `discovered`: the installed Codex `model/list` reports the model;
- `enabled`: effective configuration permits the route;
- `available`: a bounded active probe recently proved upstream execution.

Availability is one of `available`, `unavailable`, or `unknown`. A recent
successful real execution may preserve a degraded `available` observation
until its TTL expires, but discovery alone never proves upstream access.

Discovery uses Codex app-server `model/list` with pagination. An active probe
starts a minimal read-only, ephemeral Codex execution for one model, performs
no repository mutation, has a strict timeout, and records a sanitized result.
Probes have model cost, so Fishyume runs them only during explicit refresh,
setup, or when a selected route has stale or unknown availability. It does not
probe every model before every Run.

## 5. Safe Fallback Boundary

Automatic fallback is allowed only when all conditions hold:

1. the Workflow requirement opted into model fallback;
2. the failed Attempt reports no side effects;
3. the Driver classified the failure as a model-unavailable pre-execution
   error rather than a prompt, tool, repository, auth, or generic failure;
4. the next target was already persisted in the routing decision;
5. the next route is currently enabled and available;
6. the cumulative cost grant remains valid.

Fishyume never retries a possibly mutating Attempt merely because another
model exists.

## 6. Persistent Configuration

Configuration is stored atomically beneath the Fishyume state root and
contains no credentials, tokens, API keys, or upstream base URLs.

```text
<state-root>/config/routing.json
<state-root>/routing/catalogs/<sha256>.json
<state-root>/routing/availability.json
```

`routing.json` uses `fishyume.config/v1`, an integer revision, and mutation
IDs. Updates use optimistic concurrency and idempotent mutation replay.

Precedence is:

```text
environment override > persistent configuration > product defaults
```

The existing environment Catalog remains a temporary operator override. Team
and Workflow read the same configuration store but apply different eligibility
rules: Team is read-only and conversational; Workflow requires the Codex
Driver, repository editing, strict structured output, and durable recovery.

## 7. Host And Operator Surfaces

The versioned config service exposes:

- `driver.list`
- `driver.models.discover`
- `driver.models.probe`
- `routing.config.get`
- `routing.config.update`
- `routing.availability`
- `routing.catalog.effective`

The Host Agent can inspect local support before launching work and can explain
why a route is excluded. Mutations require a stable mutation ID and expected
revision.

CLI equivalents are:

```text
fishyume drivers inspect
fishyume routing show
fishyume routing enable <route>
fishyume routing disable <route>
fishyume routing refresh --probe
```

Doctor reports the Codex binary, discovery health, active Catalog hash,
configuration revision, probe freshness, and whether at least one Workflow
route is usable. Web presents the same canonical state and controls through
the local authenticated gateway; it does not implement its own routing logic.

## 8. Migration

On first M7.8 startup without `routing.json`, Fishyume creates product defaults
without probing or silently disabling routes. Until a successful probe exists,
route availability is `unknown`. Before a Workflow launch, an unknown selected
route receives one bounded on-demand probe. If it fails, routing chooses a
confirmed available fallback or fails before execution with a concrete
diagnostic.

The removed fictional route name `gpt-5.6` remains only in the frozen M6
Catalog registry for historical validation. It is never emitted by the new
effective Catalog.

## 9. Acceptance Matrix

| Requirement | Required evidence |
| --- | --- |
| historical safety | old M6 hash and Attempt validate after the active Catalog changes |
| durable snapshots | effective Catalog reloads by hash after process restart |
| strict config | atomic writes, revision conflict, mutation replay, no secrets |
| discovery | paginated `model/list`, bounded decoding, sanitized errors |
| real availability | discovery cannot mark a route available; probe can |
| qualification | unprofiled discovered models stay visible but unroutable |
| effort audit | selected effort is persisted and reaches Codex execution |
| safe fallback | only classified pre-execution unavailability with no side effects advances |
| Host parity | RPC, MCP, CLI, Web, and Doctor show the same effective state |
| degraded startup | one unavailable model cannot make Fishyume unusable when another route works |
| compatibility | frozen application/team contracts and historical fixtures still pass |
| release quality | Go, TypeScript, Web, install smoke, and cross-platform CI pass |

M7.8 is complete only after a live local acceptance proves that Codex discovery
can list Sol, Terra, and Luna while the probe correctly narrows the current
usable Workflow Catalog to the models accepted by the configured upstream.
