# Fishyume M6 Core Contract Freeze

Status: frozen after M6.7. The machine-readable baseline is
[`../contracts/fishyume-core-v1.json`](../contracts/fishyume-core-v1.json).

## Scope

This freeze defines the stable core of Fishyume before Driver ecosystem work.
It covers the public Application API, Workflow authoring, Agent execution,
durable Run state, Context and Memory, and deterministic model routing.

The freeze applies to externally observable behavior. Internal package layout,
implementation details, diagnostics, performance, and presentation may change
without a contract version change when the behavior remains compatible.

## Compatibility policy

- New state is written only with the current contracts.
- Historical Workflow, Run, Node, and Attempt records remain readable. Cleanup
  must not rewrite historical snapshots merely to make them current.
- Additive optional fields may be introduced within a version when old readers
  can safely ignore them and old records remain valid.
- Removing or changing a required field, method, state transition, error
  meaning, hash input, or idempotency rule requires a new contract version and
  an explicit migration plan.
- Compatibility transports and decoders are not public authoring surfaces. New
  product calls use only the frozen Application methods.

## Frozen behavior

The public API is `fishyume.application/v1`. MCP and machine CLI expose the same
method set and response semantics. `run.start` is the only public boundary that
creates a Run; all mutations are idempotent and actions retain their durable
preconditions.

New Workflows use `fishyume/v2`. `dependsOn` controls scheduling and
`context.dependencies` controls dependency-result injection. Historical v1
Workflows remain readable through the compatibility path.

Every new Agent Attempt receives an immutable Context manifest/hash and routing
decision. Recovery never recompiles historical Context or recomputes a persisted
routing decision. Full rendered prompts do not enter general logs or durable
snapshots.

Fallback remains conservative: an explicit retry is the approval boundary and
a different persisted route is eligible only when Driver evidence proves that
the failed Attempt had no side effect. Missing or indeterminate evidence never
causes an implicit route change.

## Not frozen as public API

- Go package names and internal interfaces;
- JSON-RPC framing and local IPC implementation details;
- compatibility RPC names such as `run.status` and `run.resume`;
- deprecated `backend/tool/runtime` authoring aliases;
- TUI layout, wording, and visual tokens;
- fake Drivers, fixtures, smoke harnesses, and repository scripts.

These surfaces may be reorganized or removed after their product callers have
migrated to the frozen contracts. Historical read compatibility and the public
Provider-independent acceptance evidence must remain intact.

After the freeze, the product CLI migrated to `run.start`, `run.get`, and
`run.action`. The former `run.startWorkflow`, `run.resume`, `run.cancel`, and
`run.detach` mutation methods were removed. A non-advertised, read-only
`run.status` decoder remains solely for protocol-v1 snapshots that cannot be
represented by the frozen `run.get` response.

## Change gate

Go and TypeScript tests load the machine-readable baseline and compare it with
the implementation constants. Contract fixtures additionally validate complete
request and response shapes. A change to the baseline must therefore be an
explicit reviewed contract decision, not an incidental refactor side effect.
