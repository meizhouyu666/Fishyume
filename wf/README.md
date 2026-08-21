# Fishyume CLI

Install `fishyume` to get the primary `fishyume` command and compatible `wf` alias. Installation does not download executable code; matching platform Engine packages are optional exact-version dependencies. Windows + Codex is the first fully productized Developer Preview combination; Ubuntu remains an install and CI target.

Fishyume `0.2.1-alpha.1` implements the M4.0-M4.3 Agent-native Control Plane. A Host Agent calls the same Application API through MCP (`fishyume mcp`) or the one-response Machine CLI (`fishyume machine`). Humans attach the Calm Operator TUI to the same durable Run with `fishyume attach <run-id>` or `fishyume status <run-id> --watch`. The headless `codex` Driver executes Agent Attempts on the `local` target. New Runs do not require or use CC-Panes.

CLI/TUI clients discover or detached-start the user-level service, then connect over a Windows Named Pipe or Linux/macOS Unix Domain Socket. Closing a client does not stop a Run. The Control Plane owns durable state, serializes mutations, and reconciles persisted Attempts before scheduling after restart. Direct `wf-engine` invocation retains stdio JSON-RPC for tests and controlled embedding.

## First use

```powershell
fishyume setup codex
fishyume doctor --project "E:\project"
fishyume
```

`fishyume setup` idempotently registers the canonical absolute Node/installed-CLI transport through the official Codex CLI, applies Fishyume's bounded tool approval policy, and runs the product Doctor. Historical `fishyume setup codex` remains an alias. `--print` only emits the low-level transport command, and `--force` is required to replace a conflicting entry. `fishyume demo` renders the Chinese topology console without starting an Engine or calling a model. Doctor checks Engine/protocol/Driver/project plus Codex CLI, login, canonical MCP transport, and approval policy with an executable recovery command for every failure. After a Host Agent starts work, zero-argument `fishyume` opens the Chinese Run Dashboard; select with arrows or `J`/`K` and press `Enter` to attach. The Console shows safe bounded Node Agent activity, prioritizes the first actionable node, and gives prominent Chinese approval/input/retry guidance. `A/Y` approves, `X/N` rejects, `T` retries, `C` cancels the Run, and `Q` only detaches observation. The normal path does not require a handwritten Workflow or copied Run ID.

## Start and observe a Run

```powershell
fishyume doctor --driver codex --project "E:\project"
fishyume run --driver codex --target local --project "E:\project" "Implement the requested change"
fishyume attach <run-id>
```

For a Workflow file, use canonical Agent selection:

```yaml
defaults:
  agent:
    driver: codex
    target: local
```

`execution.maxConcurrency` provides bounded parallel Agent execution. The effective capacity is bounded by the Workflow request, Driver capability, and Fishyume safety ceiling. Only structured Results complete Attempts, and cancellation requires Driver confirmation against the persisted process fingerprint.

## Agent interfaces

The MCP server and Machine CLI expose the current Application API: `system.capabilities`, read-only `routing.catalog`, `workflow.validate`, `workflow.explain`, `run.start`, `run.list`, `run.get`, `run.events`, `run.action`, `run.result`, and project-scoped `memory.create/get/list/supersede/delete`.

```powershell
fishyume machine system.capabilities --params '{}'
fishyume machine routing.catalog --params '{}'
fishyume machine run.get --params '{"runId":"<run-id>"}'
fishyume mcp
```

Memory writes require a durable `mutationId` and explicit audit reason. CLI content is
read from `--stdin` or `--file`, never an inline shell-history flag. CLI writes are fixed
to writer `user`; MCP writes are fixed to `host_agent`. `memory.list` returns bounded
metadata, while `memory.get` returns one full record. See
[`../docs/fishyume-m5.2-memory-store.md`](../docs/fishyume-m5.2-memory-store.md).

Call `system.capabilities` before authoring automation. Its bounded
`fishyume.authoring-guide/v1` data gives the complete safe Host sequence and advertises
the trusted immutable routing catalog summary. Call `routing.catalog` to inspect the
exact static model capabilities and hash; it does not select a model or report live
Provider availability. The guide also advertises
`fishyume/v2`. For v2, `dependsOn` controls scheduling and
`context.dependencies` explicitly controls dependency-result injection. A Host may
select Memory, but must pass the same `workflow`, `inputs`, `driver`, `target`, and
`contextBindings` to `workflow.validate`, `workflow.explain`, and `run.start`.

Agent Nodes may optionally declare `agent.routing` using
`fishyume.routing-requirement/v1`. The requirement is validated and projected by
`workflow.validate`/`workflow.explain`; it is not a model-selection decision.
Legacy Workflows receive the bounded standard/balanced default, with fallback
disabled. M6.2 does not query Providers or change Driver startup.

M6.3 adds a pure deterministic resolver in the Engine routing package. It
requires the upper layer's bounded `BudgetGrantV1`, verifies the catalog hash,
matches capabilities and model limits, and emits an auditable
`RoutingDecisionV1`. It does not contact Providers or start a Driver; target
propagation is completed by M6.4. New Attempts persist that decision, pass it
through the Agent envelope and Driver launch spec, and the Codex Driver forwards
the selected model to the direct CLI. Historical Attempts without routing
metadata remain readable and are never recomputed.

M6.5 reserves a bounded `RoutingUsageV1` for every new routed Attempt and
validates its route, coarse catalog cost units, and cumulative cost against the
trusted catalog and Node budget. Explicit `retry` is the fallback approval
boundary. Only a failed Attempt with Driver evidence of `sideEffectStatus:
none` advances to the next persisted fallback; missing, truncated, tool-active,
or indeterminate evidence never changes route implicitly. Catalog cost units
are deterministic routing allocations, not Provider billing estimates.

M6.6 exposes the immutable routing decision, usage receipt, and Driver
side-effect evidence through `run.get`, which is shared by Host, MCP, machine
CLI, and compatibility status. The Chinese topology TUI renders route,
reason, budget, fallback approval, and evidence in bounded Node detail. Fake
Driver matrices and Windows/Linux CI plus install smoke are release gates.

M6.7 adds deterministic routing preflight to the existing `workflow.validate`
and `workflow.explain` responses. Their bounded `routingPreviews` list contains
the resolved Driver/Target, effective requirement, selected model, reason codes,
budget, and fallback policy for every Agent Node. If the trusted catalog cannot
satisfy a requirement, the preview returns a stable routing issue while the
static Workflow remains valid. The preview is read-only, does not contact a
Provider, and does not persist an Attempt; pass the same request to `run.start`
to create the durable Run.

`run.start` is idempotent by caller-owned `clientRequestId`; reuse an ID only for the
identical request. `run.action` requires a unique `actionId` and preconditions from the
latest `run.get`. Paginate `run.events` with `afterSequence`, read `run.result` only
after terminal state, and use the returned `attach` command to open the human TUI on
that same durable Run. The canonical Workflow and exact request set are
[`../docs/examples/fishyume-v2-host.yaml`](../docs/examples/fishyume-v2-host.yaml) and
[`../docs/examples/fishyume-v2-host-requests.json`](../docs/examples/fishyume-v2-host-requests.json).
Machine output is one Application response JSON object; MCP returns the same response
types.

## Human console

The TUI presents one Run as a topology-first stage graph, with dependency IDs, parallel-stage markers, safe bounded activity for headless Node Agents, one selected-node detail view, and only currently valid actions. At 120+ columns it uses a topology/detail split; at 80 columns it stays vertical and width-bounded. `a` approves or answers, `r` rejects, `R` retries, and `c` cancels after confirmation. `d`, `q`, and `Ctrl+C` disconnect observation without pausing or cancelling the Run. Non-TTY/CI output remains line-oriented; `status --watch` requires an interactive terminal, and `status --json` emits one object.

The console supports 80/120/160 columns, CJK display width, TrueColor through monochrome, `NO_COLOR`, and ASCII fallback through `TERM=dumb` or `FISHYUME_ASCII=1`.

## Compatibility window

Deprecated CLI `--backend/--tool/--runtime`, Workflow `defaults.backend/tool/runtime` and node `tool/runtime`, plus `FISHYUME_BACKEND`, remain compatibility inputs and emit warnings. `direct` normalizes to Driver `codex`; the supported current target is `local`. Historical CC-Panes snapshots remain readable for status and receive an explicit diagnostic if their old execution cannot be recovered. Compatibility does not destructively migrate or delete stored state.

Use `--driver/--target` and `defaults.agent.driver/target` for all new automation. See [`../docs/fishyume-m4-migration-guide.md`](../docs/fishyume-m4-migration-guide.md) for exact mappings and conflict behavior.

M4 is technically closed: the product/migration release surface, deterministic and real Codex MCP Host flows, local real-Codex single-node and parallel Driver smokes, rendered Host/PTY stale-action conflict, two-client MCP Host/TUI-controller acceptance, Provider-independent Application crash/restart, independent review, and all public CI jobs passed. M4.5 accepted the zero-argument Dashboard, one-command Codex setup, product Doctor, and installed-package golden path. M5.0-M5.6 are complete: v2 Context Policy, explicit Host Memory bindings, exactly-once usage receipts, bounded context inspection, self-discovering authoring guidance, canonical examples, and the Provider-independent Host/TUI golden path. No package publication or GitHub Release was performed, and public CI never requires Provider credentials.
