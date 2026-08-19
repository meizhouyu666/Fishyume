# Fishyume M5: Context Engineering and Memory

> Status: M5.0-M5.6 completed. M5.6 Agent-native Authoring and Acceptance is the accepted M5 product-closure batch: the public contract is self-discovering for external Host Agents through the bounded versioned authoring guide, canonical v2 examples, and a Provider-independent Host/TUI golden path. The accepted M4.5 Developer Preview remains the product baseline.

M5 turns the existing deterministic Context Compiler skeleton into the product layer
that gives each Agent Attempt the smallest sufficient, auditable context. Fishyume
continues to orchestrate external headless Agent processes; it does not become a chat
harness or embed a model tool loop.

## Outcomes

- A versioned Context Envelope describes every component delivered to an Attempt.
- Project, Workflow, Node, dependency-result, user-input, and bounded historical memory
  sources have explicit precedence, provenance, limits, and hashes.
- Memory writes are typed, reviewable, deduplicated, bounded, and separated from raw
  transcripts. Sensitive full prompts remain absent from general logs and snapshots.
- Attention allocation is deterministic first: required instructions and current task
  cannot be displaced by optional memory. Truncation and omission are observable.
- Prompt components are reusable data with schemas and versions, not an opaque library
  of untracked strings.

## Non-goals

- Model selection, fallback, cost routing, and task-complexity classification belong to
  M6 Capability and Model Routing.
- Third-party Driver discovery, hot loading, and a public plugin SDK belong to M7.
- A Native Harness or end-user conversational Agent is not introduced in M5.
- Web/Desktop UI and generic Shell/HTTP/container Workflow nodes remain out of scope.

## Delivery order

### M5.0: Contract freeze and evaluation fixtures

Freeze Context Envelope v2, component provenance, sensitivity labels, memory record
types, limits, stable errors, canonical hashing, and golden fixtures. Define evaluation
tasks that detect missing instructions, stale memory, irrelevant context, leakage, and
nondeterminism before changing production compilation.

Completed in [`fishyume-m5.0-context-contracts.md`](./fishyume-m5.0-context-contracts.md): the v2/v1 contracts, metadata-only manifest, hard limits, trusted Memory writers, golden hash/records, six-class evaluation suite, and mutation tests are frozen side-by-side with the unchanged production v1 compiler.

### M5.1: Context source registry

Implement typed sources for project instructions, Workflow policy, Node task,
dependency Results, user answers, and explicitly selected memory. Resolution remains
local, deterministic, ordered, and independently testable.

Completed in [`fishyume-m5.1-context-source-registry.md`](./fishyume-m5.1-context-source-registry.md): the immutable six-source registry, canonical project-file boundary, explicit dependency isolation, selected `MemoryRecordV1` lifecycle resolution, golden fixture, and negative security tests are implemented without changing the production v1 Run path.

### M5.2: Memory store and lifecycle

Add project-scoped typed memory with provenance, creation reason, supersession,
retention bounds, and explicit deletion. Do not persist full conversation history as
memory. Reads and writes must be inspectable through the Application API.

Completed in [`fishyume-m5.2-memory-store.md`](./fishyume-m5.2-memory-store.md):
canonical project identity, bounded strict JSON catalogs, cross-process locking,
atomic revision/receipt commits, create/get/list/supersede/delete lifecycle, and fixed
user/host-agent CLI/MCP writers are implemented. Production Run, Attempt, Workflow,
Driver, and Context Compiler integration remains unchanged for M5.4.

### M5.3: Attention budget compiler

Allocate bounded context by required/important/optional tiers, preserve component
boundaries, record omissions, and produce a manifest/hash without logging the complete
prompt. Compilation must be replayable from the same approved inputs. Implemented in
`internal/contextcompiler` with the stable `BudgetPolicyV1` default and additive
`CompileContextV2`; production integration was completed in M5.4.

### M5.4: Run integration and product surface

M5.4 integrates the deterministic compiler into production Run/Attempt creation and
inspection without turning the compiler into a Provider prompt logger. New Attempts use
`context-compiler/v2`; historical v1 snapshots remain readable and resumable through the
v1 compatibility path and are never rewritten. A deterministic v2 adapter renders the
ephemeral `ContextEnvelopeV2` into the existing `agent.AttemptEnvelope` contract. Full
rendered instructions exist only in process memory and the driver launch payload; durable
state stores bounded metadata (compiler version, manifest, hash, budget/usage,
component identities, truncation and omissions), never component content or a complete
prompt.

The Run assembly layer supplies the six approved source classes explicitly: project
instructions, workflow policy, the rendered node task, explicitly allowed dependency
results, the current user answer, and explicitly selected Memory IDs. Memory is not
automatically searched or promoted from node results. M5.4 uses the engine-owned default
`BudgetPolicyV1`; model/target-aware budgets and routing remain later work.

`run.get`/Application/RPC/MCP and the TUI expose a bounded context inspect view (compiler
version, hash, budget/usage, component IDs/kinds, omissions and truncation) while
redacting all component content and complete prompts. Provider credentials are not needed
by public CI; Codex single-node and parallel acceptance is a local/controlled gate.

### M5.5: Context Policy and Memory usage closure

M5.5 adds the explicit `fishyume/v2` Context Policy, host-supplied per-node Memory
bindings, exactly-once Memory consumption receipts, and bounded inspect/TUI
metadata. v1 workflows and historical Runs remain compatible. Model routing,
automatic retrieval, embeddings, prompt optimization, plugin ecosystems, and
result-to-Memory promotion remain outside M5.

### M5.6: Agent-native Authoring and Acceptance

M5.6 makes the existing public contract self-describing for Codex, Claude, and other
Host Agents. It adds a bounded versioned authoring guide to `system.capabilities`,
canonical v2 examples, and one Provider-independent end-to-end Host/TUI acceptance path.
It does not add a new MCP tool, model routing, automatic Memory retrieval, prompt
optimization, or an embedded harness. The approved preparation and delivery batches are
defined in [`fishyume-m5.6-agent-native-authoring-acceptance.md`](./fishyume-m5.6-agent-native-authoring-acceptance.md).

Completed in [`fishyume-m5.6-agent-native-authoring-acceptance.md`](./fishyume-m5.6-agent-native-authoring-acceptance.md): the bounded versioned `fishyume.authoring-guide/v1` ships in `system.capabilities`, MCP tool descriptions carry the v2 Context Policy and exact preflight/action/result preconditions, the canonical v2 Workflow and Host request set are copyable, and the Provider-independent MCP Host/TUI acceptance proves identical validate/explain/start Context bindings, exactly-once Memory usage with omitted Memory not consumed, idempotent start, Approval, `needs_input`, stale-`stateVersion` conflict, event pagination, terminal result, attach, and metadata-leak hygiene. No new MCP tool, RPC protocol, Codex approval, or Provider credential was added.

Local preflight (`go test ./...`, `go vet ./...`, `go build ./cmd/wf-engine`,
`npm --prefix wf run verify`, `git diff --check`) passed, and public Windows/Ubuntu CI
run [32243762097](https://github.com/meizhouyu666/Fishyume/actions/runs/32243762097)
passed all six jobs (Windows/Ubuntu verify, Windows/Ubuntu platform-install, artifacts,
and deterministic stress). No package or GitHub Release was published.

## M5 entry gates

- [x] M4 final independent review has no P0/P1/P2 findings.
- [x] Context Envelope v2 and Memory records are approved before production code changes.
- [x] Golden/evaluation fixtures exist before optimization claims.
- No model-routing or plugin-ecosystem work is mixed into M5 commits.
- Public CI remains Provider-independent on Windows and Ubuntu.
