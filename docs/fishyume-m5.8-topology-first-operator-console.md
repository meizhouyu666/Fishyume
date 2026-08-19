# Fishyume M5.8: Topology-first Operator Console

M5.8 makes the human console topology-first. A Run is shown as dependency
stages rather than as an unstructured list of node statuses. The console keeps
the existing safe action binding and bounded Agent activity from M5.7.

## Product behavior

- `run.get` projects each node's durable `dependsOn` list and zero-based
  `parallelLayer`.
- The Run view also exposes ordered `parallelLayers`, derived from the
  persisted normalized workflow. Historical or compatibility views continue to
  work with empty/default topology metadata.
- At 120 columns or wider, the left pane shows the stage graph and the right
  pane shows the selected node or action detail.
- At 80 columns, the same stage graph is rendered vertically. Each node shows
  its dependency IDs when available, and fan-out/fan-in are visible through
  stage labels, connectors, and the `并行 N` marker.
- Approval, answer, retry, and cancellation actions keep the existing
  nodeId/kind/duplicate-risk binding. Topology is presentation metadata; it
  does not add an intervention protocol or an embedded Agent runtime.

## Compatibility and bounds

The Application layer reads the normalized workflow through an optional Core
capability. This preserves old fakes and historical snapshots that cannot
provide the workflow document. The public Node JSON always contains
`dependsOn` and `parallelLayer`; `parallelLayers` is omitted when unavailable.

The renderer uses display-width-aware truncation for CJK, ASCII fallback, and
80/120/160-column layouts. Details may be shortened in a narrow pane, while
the authoritative data remains available through `run.get` and the selected
node detail model.

## Verification

The acceptance surface includes a four-stage fan-out/fan-in fixture, topology
metadata projection tests, 80/120/160-column width checks, Unicode/ASCII
connector checks, and preservation of the existing approval/retry/activity
tests. M5.8 does not change the Driver, RPC, MCP, Memory, or Context Compiler
contracts.
