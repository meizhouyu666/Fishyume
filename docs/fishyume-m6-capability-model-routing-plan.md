# Fishyume M6: Capability and Model Routing

M6 adds auditable model selection to the existing external-Agent control plane.
Fishyume remains an orchestration engine: it does not become a conversational
harness, embed a model loop, or replace the Driver that launches a headless
Agent process.

> Status: M6.0 contract freeze, M6.1 trusted capability catalog, and M6.2
> declarative Node routing requirements are complete.

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
4. **M6.3 Deterministic Resolver**: match capabilities and limits using pure
   rules. No LLM classifier and no network price lookup.
5. **M6.4 Driver/Attempt Propagation**: include the resolved target and routing
   metadata in new Attempts while preserving historical snapshots.
6. **M6.5 Fallback and Accounting**: enforce preconditions, persist bounded
   route/cost usage, and never retry an indeterminate side effect implicitly.
7. **M6.6 Operator Surface and Release Gate**: expose route/reason/budget/
   fallback status to Host, MCP, CLI, and the Chinese topology TUI; run fake
   Driver matrices, installed-package smoke, and Windows/Ubuntu CI.

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
