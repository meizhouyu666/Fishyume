# Fishyume M5.5: Context Policy and Memory Usage Closure

Status: implemented on the M5.4 baseline; final gates are recorded in the release handoff.

M5.5 closes the caller-selected Context Policy and Memory lifecycle loop without
introducing model routing, prompt optimization, automatic retrieval, embeddings,
or result-to-Memory promotion.

## Workflow contract

`fishyume/v2` workflows may declare a document-level `context` policy and a node
override:

```yaml
context:
  projectInstructions: [AGENTS.md]
  dependencies: [plan]
```

`dependsOn` remains scheduling-only. Context dependency data flow is explicit in
`context.dependencies`, and must name an ancestor. v1 workflows and historical
runs retain their legacy fixed instruction discovery and ancestor-result behavior.

## Run bindings

The host Agent supplies dynamic Memory IDs at `run.start`:

```json
{"contextBindings":{"memoryByNode":{"implement":[{"id":"memory-abc","reason":"existing error handling convention"}]}}}
```

Bindings are validated, bounded to 32 records per node, persisted with the
normalized workflow, and never cause Fishyume to search or rank Memory.

## Exactly-once usage

Fishyume compiles the Context Envelope first. Only Memory components present in
the resulting Envelope are consumed. An engine-owned idempotent receipt uses the
run, node, attempt, and context hash as its mutation identity. The receipt is
committed before Driver launch and is recorded in the Attempt as metadata only.
Replay after a crash reuses the same mutation ID and cannot increment `useCount`
twice. Expired, superseded, deleted, or exhausted records fail atomically.

Inspect and TUI surfaces expose bounded component/omission metadata, byte usage,
truncation, and Memory record IDs/commit state; they never expose prompt or
component content.
