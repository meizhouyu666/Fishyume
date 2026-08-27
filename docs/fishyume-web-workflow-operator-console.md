# Fishyume Web Workflow Operator Console

## Decision

Team and Workflow remain separate architectural layers. Team and Handoff may
optionally lead to a formal Workflow Run, but the Web client does not merge
their state machines or create a second persistence model.

The delivery order is fixed:

1. Complete Web Workflow functionality and contract coverage.
2. Verify parity with the Control Plane and TUI semantics.
3. Improve visual hierarchy, responsive layout, and interaction polish.

The current increment covers all three steps while preserving the same Control
Plane ownership boundary.

## Functional Scope

### Run state and events

- `run.get` remains the authoritative Run snapshot.
- The Web sidecar exposes `run.events` as a bounded, authenticated read method.
- The browser keeps an `afterSequence` cursor and refreshes the canonical Run
  snapshot after receiving events.
- Event payloads are presentation hints; the browser never derives or persists
  Workflow state.
- A periodic refresh remains as recovery for missed events, browser wake-up, or
  sidecar reconnection.

### DAG and node selection

- The graph uses persisted `parallelLayers`, `topologicalOrder`, and each
  node's `dependsOn` metadata.
- Missing topology metadata falls back to a deterministic ordering for
  historical Runs.
- Nodes are positioned by parallel stage on a left-to-right canvas. Dependency
  edges render as directed SVG curves behind the interactive node layer.
- Selecting a node updates the Node Inspector and highlights its complete
  upstream/downstream chain without changing Run state. Unrelated nodes and
  edges remain visible at reduced emphasis.
- Actual-size and fit-to-canvas controls support both readable inspection and a
  whole-graph overview.
- Unknown node IDs in historical or partially compatible metadata are ignored
  rather than fabricated.

### Node Inspector

The selected node view exposes only data already present in the Application
contract:

- lifecycle phase, conclusion, reason, diagnostic, dependencies, and parallel
  stage;
- current Attempt identity, driver/target, timestamps, side-effect status,
  routing decision, execution profile, routing usage, and failure class;
- bounded execution activity;
- safe Context summary (compiler/hash/counts/budget indicators), without full
  prompts, credentials, or hidden reasoning;
- result summary, decision, artifacts, checks, warnings, and waiting questions;
- existing approval, answer, retry, and Run cancellation actions using the
  existing state-version and attempt guards.

### Team/Handoff continuity

The Team associated-run view resolves Handoff bindings through
`team.handoff.get` and offers a direct action into the same Run detail view.
It does not create, mutate, or cache a duplicate Run representation.

## Ownership and Boundaries

```text
Control Plane / Application contract
  -> authenticated Web gateway
    -> browser read model and presentation
```

- No scheduler, state machine, or persistence truth is added to Web.
- The optional Web package remains outside the core Fishyume dependency graph.
- `run.start`, Handoff creation, and Handoff binding remain unavailable from the
  Web gateway unless explicitly added as a future, separately reviewed action.
- Web request, response, concurrency, timeout, and origin limits remain in
  force for `run.events`.

## Acceptance Gate

- Web typecheck, tests, build, and package audit pass.
- DAG projection is deterministic and supports historical fallback.
- `run.events` is authenticated and method-allowlisted.
- Team/Handoff navigation reaches the canonical Run detail view.
- Existing Run action bindings and conflict behavior remain unchanged.
- Core Workflow and Team contracts are unchanged.
- The dark operator-console layout remains responsive without changing the
  Workflow contract.

## Visual Theme Direction

The first visual pass uses a dark Geist + Linear hybrid:

- near-black graphite workspace surfaces and restrained gray borders for sustained
  scanning;
- blue for navigation, links, focus, selection, and primary actions;
- Fishyume green reserved for brand identity and successful states;
- amber for waiting/approval states and coral for failure/cancellation;
- compact radii, low-elevation dark shadows, explicit focus rings, and
  reduced-motion support for keyboard and accessibility users;
- a 252px collection rail, graph-dominant DAG/Inspector split, fixed node
  geometry, and clamped summaries so long output does not distort topology;
- an SVG edge layer, explicit arrowheads, whole-chain focus, and actual/fit
  canvas modes inspired by the relationship UI patterns in `dsh-agent-teams`;
- a grouped, borderless key/value Inspector instead of a cell grid, with a
  stable independently scrolling panel on wide screens.

The theme changes presentation only. It does not expand the public Workflow
contract or move state ownership into the browser. Future visual work can add a
light/high-contrast variant without changing the same semantic color roles.
