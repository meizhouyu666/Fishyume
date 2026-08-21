# Fishyume Documentation

This index separates the current product baseline from milestone evidence and
historical design records. Milestone documents remain in place so existing
links and review evidence stay stable.

## Current baseline

- [Core contract freeze](fishyume-m6-core-contract-freeze.md): frozen public
  contracts and compatibility policy after M6.7.
- [Core stabilization record](fishyume-core-stabilization.md): repository
  cleanup changes, verification evidence, and remaining stabilization work.
- [M6 capability and routing](fishyume-m6-capability-model-routing-plan.md):
  completed deterministic routing design and delivery record.
- [M6.7 routing preflight](fishyume-m6.7-routing-preflight.md): pre-Run routing
  explainability contract.
- [M5 context engineering](fishyume-m5-context-engineering-plan.md): completed
  Context, Memory, authoring, activity, and operator-console baseline.
- [M4 Agent-native control plane](fishyume-m4-agent-native-control-plane.md):
  Application, Driver, Control Plane, MCP, machine CLI, and TUI architecture.
- [Release readiness](fishyume-release-readiness.md): automated and live
  acceptance evidence.

## Operator and developer guides

- [Development and verification](fishyume-development.md)
- [Distribution and first run](fishyume-distribution-first-run.md)
- [Developer preview](fishyume-m4.5-developer-preview.md)
- [Agent selection migration](fishyume-m4-migration-guide.md)
- [Live Provider smoke](fishyume-m4-live-smoke.md)
- [Workflow examples](examples/)

## Milestone evidence

- M2: backend independence and parallel scheduling documents prefixed
  `fishyume-m2` plus `workflow-engine-m2-architecture.md`.
- M3: TUI productization and operator-console documents prefixed `fishyume-m3`.
- M4: implementation, migration, smoke, and preview documents prefixed
  `fishyume-m4`.
- M5: Context and Memory increments prefixed `fishyume-m5`.
- M6: capability and routing increments prefixed `fishyume-m6`.

These files describe decisions at their milestone date. When wording conflicts
with the current baseline, the core contract freeze and root README take
precedence.

## Historical plans

Early delegated implementation plans formerly stored in tool-specific
`.claude/plans` metadata are preserved under
[`history/early-plans/`](history/early-plans/). They are historical evidence,
not current implementation instructions.
