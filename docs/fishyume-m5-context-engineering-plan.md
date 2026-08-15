# Fishyume M5: Context Engineering and Memory

> Status: M5.0 contract/evaluation baseline completed on 2026-08-15; M5.1 production source-registry work is next. The accepted M4.5 Developer Preview remains the product baseline.

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

### M5.2: Memory store and lifecycle

Add project-scoped typed memory with provenance, creation reason, supersession,
retention bounds, and explicit deletion. Do not persist full conversation history as
memory. Reads and writes must be inspectable through the Application API.

### M5.3: Attention budget compiler

Allocate bounded context by required/important/optional tiers, preserve component
boundaries, record omissions, and produce a manifest/hash without logging the complete
prompt. Compilation must be replayable from the same approved inputs.

### M5.4: Run integration and product surface

Persist only bounded Context metadata on Attempts, expose explain/inspect operations to
Host Agents and the TUI, add migration behavior for Context Compiler v1 snapshots, and
run real Codex single/parallel acceptance without making Provider credentials a public
CI dependency.

## M5 entry gates

- [x] M4 final independent review has no P0/P1/P2 findings.
- [x] Context Envelope v2 and Memory records are approved before production code changes.
- [x] Golden/evaluation fixtures exist before optimization claims.
- No model-routing or plugin-ecosystem work is mixed into M5 commits.
- Public CI remains Provider-independent on Windows and Ubuntu.
