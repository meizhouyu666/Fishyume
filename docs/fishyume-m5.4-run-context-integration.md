# Fishyume M5.4 Run Context Integration Contract

Status: approved implementation boundary for M5.4 (2026-08-19).

## Compatibility

- New Attempts compile with `context-compiler/v2`.
- Existing `context-compiler/v1` Attempt snapshots remain readable and resumable via
  the existing v1 path. Reading or resuming them does not rewrite their metadata.
- A new Attempt never downgrades to v1 merely because an older Attempt exists for the
  same node or Run.
- The existing `agent.AttemptEnvelope` wire contract remains stable. M5.4 adds a
  deterministic adapter from the v2 envelope to that contract instead of changing every
  Driver at once.

## Assembly inputs

The Run layer constructs a complete, untruncated v2 compiler input from these ordered
source classes:

1. project instructions;
2. workflow policy/defaults;
3. the current node task;
4. dependency results explicitly allowed by the node/workflow;
5. the current user answer, when a pending question was answered;
6. explicitly selected Memory record IDs.

The engine appends its execution and output contracts as required components. It does not
perform semantic Memory search, automatic result-to-Memory promotion, model selection, or
prompt optimization in this milestone.

## Durable boundary

An Attempt persists only bounded context metadata: compiler/schema versions, canonical
manifest, envelope hash, budget and usage, component identity/kind/tier/provenance,
truncation, and omissions. Component bodies, rendered Provider instructions, credentials,
and `PromptHash` are never written to snapshots, events, receipts, or inspect responses.

## Inspect surface

`run.get` and its Application/RPC/MCP/TUI projections may expose compiler version, hash,
budget/usage, component IDs and kinds, omissions, and truncation. They must not expose
component content or a complete prompt. Inspect output is bounded and deterministic.

## Verification boundary

M5.4 must cover fake-driver single-node and parallel acceptance, v1 snapshot read/resume,
v2 metadata persistence and redaction, deterministic adapter behavior, and inspect/TUI
projections. Real Codex single/parallel acceptance is a controlled local gate only; public
CI remains Provider-independent and must run the standard Windows/Ubuntu preflight.
