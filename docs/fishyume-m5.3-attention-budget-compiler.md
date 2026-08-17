# Fishyume M5.3 Attention Budget Compiler

Status: implemented on 2026-08-17. M5.3 is an additive internal compiler and remains side-by-side with production `context-compiler/v1`.

## Inputs and boundary

`CompileContextV2` accepts an Attempt identity, the M5.1 `ContextSourceResolutionV2`, two engine-owned `ContextComponentV2` values (`execution_contract` and `output_contract`), and a resolved `AttentionBudgetV2`. M5.1 candidates must be complete and untruncated (`none`, with equal original/included byte counts); M5.3 alone performs budget truncation. It performs no I/O, filesystem or Memory-store discovery, clock access, Provider/model lookup, tokenization, ranking, persistence, mutation, prompt rendering, or logging. Source resolution cannot provide or replace either engine-owned contract; duplicate and included/omitted identities fail closed.

`BudgetPolicyV1` is internal. Its stable model-independent default is 128 KiB total: 64 KiB required, 48 KiB important, and 16 KiB optional (4:3:1). Product users and Host Agents do not pass budgets through CLI/MCP. The pure compiler also accepts an already-resolved `AttentionBudgetV2`, allowing a later Driver-aware resolver without changing allocation semantics.

## Allocation

Tiers are isolated. Required content is copied in full and is never truncated, omitted, or funded by unused important/optional capacity. If required bytes exceed required capacity, compilation returns `context_budget_unsatisfiable`.

Important components are canonically ordered by frozen component kind rank and ID, then allocated with integer max-min water filling. The largest common byte level whose sum fits the tier is found by bounded binary search; leftover bytes are assigned one at a time in canonical ID order. Small components therefore remain whole while larger components share capacity fairly. A truncated component uses `tail` mode and selects the longest valid UTF-8 suffix no larger than its allocation; a code point is never split. An allocation with zero valid bytes is an omission with `budget_exhausted`.

Optional Memory consumes only optional capacity. Records are considered in stable ID order and use whole-record semantics: a record is included only when all content bytes fit the remaining optional budget. A non-fitting record is omitted with `budget_exhausted`, and later records are still considered. There is no half-record output and no invented optional source kind.

Existing M5.1 omissions are validated, preserved, and merged with compiler omissions, then sorted by Component ID. Duplicate IDs, identity overlap, invalid order, unsupported kinds/versions, invalid hashes, invalid UTF-8, bounds, and arithmetic overflow are explicit errors using frozen M5.0 codes.

## Envelope, manifest, and security

The result is a validated ephemeral `ContextEnvelopeV2`, a metadata-only `ContextManifestV2`, and the canonical SHA-256 envelope hash. Canonical JSON is compact UTF-8 with HTML escaping disabled and no trailing newline. The manifest contains identity, budget/usage, IDs, kinds, tiers, sensitivity, provenance references, hashes, byte counts, truncation, and omissions only; it never contains component content or a complete prompt. M5.3 does not persist, render a Provider prompt, construct `agent.AttemptEnvelope`, or alter Run/Attempt/Workflow/Driver/Application/RPC/CLI/MCP/TUI paths.

## Compatibility and evidence

Frozen M5.0 schemas, error codes, limits, order, and v1 behavior remain unchanged. The machine-readable golden fixture is `wf-engine/internal/contextcompiler/testdata/attention-compiler-v2.json`. Focused tests cover the exact golden hash/usage, 100-repeat determinism, input permutation and aliasing, required overflow/no borrowing, balanced UTF-8-safe truncation, whole-record Memory selection, omission merging, policy validation, and metadata leakage. Existing M5 evaluation fixtures continue to run unchanged.
