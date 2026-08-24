# Fishyume M6: Capability and Model Routing

M6 adds auditable model selection to the existing external-Agent control plane.
Fishyume remains an orchestration engine: it does not become a conversational
harness, embed a model loop, or replace the Driver that launches a headless
Agent process.

> Status: closed on 2026-08-24. M6.0 contract freeze, M6.1 trusted
> capability catalog, M6.2 declarative Node routing requirements, M6.3
> deterministic resolver, M6.4 Driver/Attempt propagation, M6.5
> fallback/accounting, M6.6 operator/release gates, and M6.7 routing
> preflight are complete. Post-M6.7 stabilization did not add another public
> capability increment or change the frozen contracts.

M6 is not held open for authenticated Provider smoke, long-running soak,
dogfooding, or downgrade against a future published artifact. Those remain
useful validation and release activities, but they are not prerequisites for
starting M7.

## M6.0 Contract Freeze

The first M6 batch freezes the provider-neutral values in
`wf-engine/internal/routing`:

- `CapabilityCatalogV1` describes a bounded, canonically ordered set of model
  capabilities, context/output limits, and coarse cost/latency/quality classes.
- `RoutingRequirementV1` describes what a Node needs. Candidate order is
  meaningful and represents Host/Workflow preference; the catalog itself is
  canonicalized by model ID.
- `BudgetGrantV1` is the upper-layer grant passed to a future resolver. It may
  only reduce the Run/Host/model limits; selecting a larger model never expands
  the caller's budget.
- `RoutingDecisionV1` records the selected external target, catalog hash,
  reason codes, budget grant, fallback policy, and optional Prompt Profile ID.
- `FallbackPolicyV1` makes automatic fallback eligible only when the Attempt has
  no known side effect. An eligible policy requires explicit no-side-effect
  protection and may require approval.
- `PromptProfileV1` is a versioned list of context component IDs. It selects
  auditable prompt components; it is not a free-form prompt rewrite or an
  embedded model optimizer.

The package exposes pure validation and canonical JSON/hash helpers. M6.0 does
not alter `AttemptEnvelope`, Driver startup, Run persistence, MCP, or TUI.

## Delivery Order

1. **M6.0 Contract Freeze**: complete in this batch with golden fixtures and
   negative validation tests.
2. **M6.1 Capability Catalog**: complete. The Engine-owned immutable catalog is
   summarized by `system.capabilities` and exposed in full through the read-only
   `routing.catalog` Application/MCP/Machine API. The response includes the
   exact catalog hash, contract limits, stable routing errors, and
   `dynamicAvailability: false`; Driver readiness remains a separate runtime
   report. No project file, environment variable, credential, Provider API, or
   model selection participates in catalog construction.
3. **M6.2 Node Routing Requirements**: complete. Additive Workflow/Node fields
   declare capabilities, complexity, quality, latency, candidate preference,
   prompt profile, fallback intent, and bounded budgets. `workflow.validate`
   returns every Agent Node's effective requirement in topological order and
   `workflow.explain` projects the same requirement on Agent Nodes. Approval
   Nodes cannot declare routing. Omitted routing uses a conservative,
   compatibility default and never enables automatic fallback. M6.2 does not
   select a model, query a Provider, start a different Driver, or persist a
   routing decision.
4. **M6.3 Deterministic Resolver**: complete as a pure resolver. It validates
   the caller-owned `BudgetGrantV1`, verifies the catalog hash, filters models
   by required capabilities and effective context/output/cost ceilings, then
   ranks by explicit candidate order, complexity quality floor, quality,
   latency, cost, and canonical model ID. It emits an auditable
   `RoutingDecisionV1`; declared fallback is represented as eligible only with
   no-side-effect protection and approval required. No LLM classifier, clock,
   filesystem, network price lookup, Provider query, or Driver startup is
   involved. Application/Attempt propagation is implemented separately in M6.4.
5. **M6.4 Driver/Attempt Propagation**: complete. New Attempts capture the
   immutable `RoutingDecisionV1`; the same decision is passed through the Agent
   envelope and Driver launch spec. The Codex Driver forwards the selected model
   to the direct CLI. Attempts written before M6.4 remain readable with no
   routing field, and an Attempt's decision is never recomputed during recovery.
6. **M6.5 Fallback and Accounting**: complete. Every new routed Attempt reserves
   a bounded `RoutingUsageV1` against the immutable catalog decision before
   launch. The receipt records route index, catalog cost units, and cumulative
   cost; recovery validates it against the trusted catalog and Node budget.
   Catalog cost units are coarse routing allocations, not Provider invoices or
   token-price estimates. Existing explicit `retry` is the approval boundary
   for fallback. A failed Attempt advances to its next persisted fallback only
   when Driver evidence says `sideEffectStatus: none`; missing, truncated, or
   tool-active evidence is `unknown` and retains the current route. An
   `indeterminate` Attempt never changes route implicitly and still requires
   `acknowledgeDuplicateRisk` for explicit retry. M6.4 Attempts without usage
   receipts or side-effect evidence remain readable and are conservatively
   accounted from their persisted selected target.
7. **M6.6 Operator Surface and Release Gate**: complete. `run.get` exposes the
   immutable routing decision, usage receipt, and Driver side-effect evidence
   through the shared Application projection used by Host, MCP, and machine
   CLI. The compatibility status view and plain reporter preserve the same
   bounded routing record. The Chinese topology TUI shows selected target,
   route index, reason codes, cumulative/max cost, fallback approval state, and
   side-effect evidence in focused Node detail. Fake Driver fallback,
   accounting, unknown-evidence, replay, and historical compatibility tests are
   release gates; Windows/Ubuntu CI also runs the routing/operator acceptance
   patterns and installed-package smoke.
8. **M6.7 Routing Preflight and Explainability**: complete. The existing
   `workflow.validate` and `workflow.explain` projections now include bounded
   `routingPreviews` for every Agent Node. Each preview uses the same trusted
   catalog and deterministic resolver as a new Attempt, returning the resolved
   Driver/Target, effective budget, fallback policy, reason codes, and selected
   model, or a stable routing issue when no model satisfies the request. The
   preview is read-only, Provider-independent, and never persists an Attempt;
   `run.start` remains the authoritative mutation boundary.

## Complexity and Prompt Policy

Complexity is supplied by the Host Agent or declared in the Workflow. M6 does
not use an opaque LLM classifier as a control-plane dependency. An unknown
complexity is conservatively treated as `standard` by a future resolver.

For M6.2, the Host may declare a complete `agent.routing` object or omit it.
The declaration is a requirement and preference, not a decision: candidate
order is preserved, capabilities must use canonical order, and all budget
fields are validated before a Workflow can start. The effective requirement is
only a validation/explain projection until the deterministic resolver lands in
M6.3.

Prompt Profiles are IDs and component selections. The M5 Context Compiler
continues to perform deterministic assembly, budgeting, truncation, manifest,
and hash generation. Fishyume never persists a complete rendered prompt.

## Explicit Non-goals

- third-party Driver discovery, hot loading, or a public plugin SDK (M7);
- Web/Desktop UI;
- AutoGen-style embedded sub-agents or a Fishyume chat harness;
- unbounded or side-effect-unsafe automatic fallback;
- black-box prompt rewriting or automatic Memory retrieval.
