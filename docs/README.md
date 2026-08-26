# Fishyume Documentation

This index separates the current product baseline from milestone evidence and
historical design records. Milestone documents remain in place so existing
links and review evidence stay stable.

## Current baseline

- [M7 Team exploration plan](fishyume-m7-session-native-web-team-console-plan.md):
  M7.1 Panel, M7.2 Handoff promotion, and the internal M7.3 Session Driver are complete.
- [M7.1 Panel acceptance](fishyume-m7.1-acceptance.md): frozen Team API,
  delivered surfaces, boundary decisions, and verification evidence.
- [M7.2 Handoff acceptance](fishyume-m7.2-acceptance.md): immutable source
  evidence, durable idempotency, existing-Run binding, and transport parity.
- [M7.3 AgentSession capability gate](fishyume-m7.3-capability-gate.md):
  policy-preserving Codex app-server resume, directed follow-up, identity
  rejection, and confirmed cancellation evidence.
- [M7.3 AgentSession acceptance](fishyume-m7.3-acceptance.md): internal Driver
  contract, Codex adapter, durable recovery, identity, and cancellation closure.
- [M7.4 implementation plan](fishyume-m7.4-implementation-plan.md): approved
  TeamSession lifecycle, private Session persistence, actions, recovery, and
  acceptance increments.
- [M7.4 acceptance](fishyume-m7.4-acceptance.md): Host-directed multi-turn
  TeamSession, recovery, loss handling, cancellation, close, and transport
  verification evidence.
- [M7.5 implementation plan](fishyume-m7.5-implementation-plan.md): optional
  loopback sidecar, browser operator workspace, security contract, and
  acceptance matrix.
- [M7.5 acceptance](fishyume-m7.5-acceptance.md): optional Web package,
  authenticated sidecar, real Engine projection, responsive browser evidence,
  and package/CI verification.
- [M7.6 Host-Web Continuity](fishyume-m7.6-host-web-continuity.md):
  Host-directed Web launch, sidecar reuse, protected focus handoff, and
  Team/Handoff/Run continuity.
- [M7.8 Codex dynamic routing correction](fishyume-m7.8-codex-dynamic-routing-plan.md):
  persistent route configuration, Codex model discovery and active probes,
  product-qualified GPT-5.6 profiles, historical Catalog compatibility, and
  safe availability fallback.
- [M7.8 Codex dynamic routing acceptance](fishyume-m7.8-acceptance.md):
  automated cross-layer gates and isolated live Codex discovery/probe evidence.
- [Core contract freeze](fishyume-m6-core-contract-freeze.md): frozen public
  contracts and compatibility policy for the closed M6 baseline.
- [Core stabilization record](fishyume-core-stabilization.md): M6 closure,
  repository cleanup, verification evidence, and deferred validation work.
- [Core readiness](fishyume-core-readiness.md): Provider-independent failure
  evidence accepted for M6 closure and separately deferred live gates.
- [M6 capability and routing](fishyume-m6-capability-model-routing-plan.md):
  closed deterministic routing milestone and delivery record.
- [M6.7 routing preflight](fishyume-m6.7-routing-preflight.md): pre-Run routing
  explainability contract.
- [M5 context engineering](fishyume-m5-context-engineering-plan.md): completed
  Context, Memory, authoring, activity, and operator-console baseline.
- [M4 Agent-native control plane](fishyume-m4-agent-native-control-plane.md):
  Application, Driver, Control Plane, MCP, machine CLI, and TUI architecture.
- [Release readiness](fishyume-release-readiness.md): automated and live
  acceptance evidence.

## Operator and developer guides

- [Team Agent routes: Codex, Claude Code, and OpenCode](fishyume-team-agent-routes.md)
- [M7.7 live three-Agent test and repair acceptance](fishyume-m7.7-live-team-repair-acceptance.md)
- [Workflow authoring and Node granularity](fishyume-workflow-authoring.md)
- [Development and verification](fishyume-development.md)
- [Distribution and first run](fishyume-distribution-first-run.md)
- [Developer preview](fishyume-m4.5-developer-preview.md)
- [Agent selection migration](fishyume-m4-migration-guide.md)
- [Live Provider smoke](fishyume-m4-live-smoke.md)
- [Workflow examples and purpose labels](examples/README.md)

## Milestone evidence

- M2: backend independence and parallel scheduling documents prefixed
  `fishyume-m2` plus `workflow-engine-m2-architecture.md`.
- M3: TUI productization and operator-console documents prefixed `fishyume-m3`.
- M4: implementation, migration, smoke, and preview documents prefixed
  `fishyume-m4`.
- M5: Context and Memory increments prefixed `fishyume-m5`.
- M6: capability and routing increments prefixed `fishyume-m6`.
- M7: Team exploration and promotion increments prefixed `fishyume-m7`.

These files describe decisions at their milestone date. When wording conflicts
with the current baseline, the core contract freeze and root README take
precedence.

## Historical plans

Early delegated implementation plans formerly stored in tool-specific
`.claude/plans` metadata are preserved under
[`history/early-plans/`](history/early-plans/). They are historical evidence,
not current implementation instructions.
