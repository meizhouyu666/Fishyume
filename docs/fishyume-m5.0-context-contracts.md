# Fishyume M5.0 Context Contracts

> Status: implemented and covered by golden/evaluation fixtures on 2026-08-15. This batch freezes contracts only; `context-compiler/v1` remains the production Run path until later M5 integration.

## Purpose

M5.0 defines the stable data and evaluation boundary for Fishyume Context Engineering. It prevents M5.1-M5.4 from becoming an opaque prompt-concatenation feature and gives later optimization claims a deterministic baseline.

Fishyume continues to orchestrate external headless Agent processes. Context selection is local and deterministic. M5.0 does not add a conversational harness, an embedded model loop, model routing, Driver plugins, or a production Memory store.

## Frozen versions

- Ephemeral Context Envelope: `fishyume.context/v2`
- Compiler identity reserved for later integration: `context-compiler/v2`
- Durable metadata-only manifest: `fishyume.context-manifest/v2`
- Typed Memory record: `fishyume.memory/v1`
- Evaluation suite: `fishyume.context-evaluation/v1`

The exact machine-readable samples live in `wf-engine/internal/contextcompiler/testdata/contracts-v2.json` and `evaluation-v1.json`. Their limits, enum order, errors, canonical envelope hash, and valid records are executable compatibility fixtures.

## Context Envelope v2

Every Component has a stable ID, kind, attention tier, sensitivity label, provenance, a bounded non-secret selection reason, source hash, included-content hash, original/included byte counts, and truncation mode. Content exists only in the ephemeral Envelope passed toward an Attempt; it is absent from the durable manifest.

Canonical Component order is:

1. `execution_contract`
2. `project_instructions`
3. `workflow_policy`
4. `node_task`
5. `user_answer`
6. `dependency_result`
7. `skill_instructions`
8. `memory`
9. `output_contract`

Components of the same kind are ordered by stable Component ID. Omissions are ordered by Component ID. The canonical hash is SHA-256 over compact UTF-8 JSON in the declared field order after validation, with HTML escaping disabled and no trailing newline. The golden fixture fixes the cross-implementation result.

### Attention tiers

| Tier | Meaning | Rules |
|---|---|---|
| `required` | The Attempt cannot be safely started without it | Cannot be truncated or omitted |
| `important` | Relevant policy, dependency, or skill material | May use tail truncation or an explicit omission |
| `optional` | Selected historical Memory | Uses only its own remaining budget |

Execution contract, project instructions when configured, current Node task, current user answer when present, and output contract are `required`. Workflow policy, dependency results, and skill instructions may be `required` or `important` according to the source declaration. Memory is always `optional` and therefore cannot displace the current task or mandatory instructions.

If a project has no project-instruction source, the Component is absent. If a source is declared but unavailable, later resolution must return `context_source_unavailable`; it must not silently pretend that the project has no instructions.

Each Envelope declares exact required/important/optional byte budgets whose sum equals its total budget. The total payload is bounded at 128 KiB. Budget allocation is implemented in M5.3; M5.0 freezes only the contract and validation behavior.

### Provenance and omission

Provenance source references are bounded, non-secret identifiers. `sourceHash` identifies the original source payload; `contentHash` identifies exactly the included bytes. Important or optional content may use `tail` truncation while preserving its Component boundary.

Stable omission reasons are `budget_exhausted`, `superseded`, `expired`, `irrelevant`, `unavailable`, `sensitivity_policy`, and `duplicate`. Required Components cannot be represented as omissions.

### Sensitivity and persistence

- `public`: safe for human inspection, while still excluding full prompt persistence.
- `project`: project-scoped content; inspect operations require project scope.
- `sensitive`: allowed only in the ephemeral Envelope and redacted from durable surfaces.

`fishyume.context-manifest/v2` stores identity, provenance, hashes, byte accounting, truncation, omissions, budgets, and usage. It has no Component content field. Complete prompts and Component bodies remain absent from Run snapshots, events, general logs, and action receipts.

## Memory Record v1

Memory is structured project knowledge, not a transcript archive. Stable record types are:

- `decision`: an approved architectural or product decision;
- `constraint`: an invariant that later work must obey;
- `fact`: a stable and verifiable project fact;
- `procedure`: a verified repeatable workflow;
- `preference`: a stable user or project preference.

Records include project/scope, content hash, provenance, creation and update times, sorted supersession references, lifecycle state/reason, use count, and optional expiry/use bounds. Lifecycle states are `active`, `superseded`, and `deleted`; deletion retains a content hash tombstone, state-change evidence, and no content.

Only `user`, `host_agent`, and `migration` are trusted writers. A Node Agent may return a Memory candidate as ordinary result data, but Fishyume must not silently promote it into long-term Memory. A Host or user must perform a later explicit write with provenance and reason.

Memory is not a credential store. `sensitive` records are rejected; sensitive data may only be supplied through an ephemeral Context source. Each record is bounded at 16 KiB, one Attempt may select at most 32 records, and the future project store is bounded at 2,048 records before retention/compaction policy.

## Stable context errors

- `context_invalid_component`
- `context_required_missing`
- `context_budget_unsatisfiable`
- `context_source_unavailable`
- `context_hash_mismatch`
- `context_version_unsupported`
- `memory_invalid_record`
- `memory_conflict`
- `memory_not_found`

These codes are frozen for M5 internals and future Application mapping. M5.0 does not add them to the current public Application API.

## Evaluation baseline

The v1 evaluation suite defines six required regression classes:

1. missing mandatory instruction;
2. superseded/stale Memory selection;
3. irrelevant Memory consuming attention;
4. sensitive text leaking into durable metadata;
5. identical approved inputs producing different hashes or order;
6. unrelated parallel-sibling results crossing dependency boundaries.

The evaluation harness checks required and forbidden Component identities, exact golden order, omission reasons, forbidden persisted markers, and repeated hash equality. Tests prove every fixture accepts its golden candidate and rejects a targeted mutation.

## Compatibility and next step

M5.0 is deliberately side-by-side with `context-compiler/v1`. Existing Runs keep their v1 Attempt Envelope, manifest, hash, resume, and recovery behavior. No snapshot is rewritten and no v2 state is emitted by production scheduling.

M5.1 may now implement a typed Context Source Registry against these contracts. Production compilation, Memory persistence, budget allocation, Application inspect operations, TUI integration, and v1-to-v2 Run behavior remain later M5 batches.

## Acceptance

- [x] Versioned Envelope, manifest, Memory, limits, errors, and order are executable contracts.
- [x] Required context cannot be truncated or omitted.
- [x] Durable manifest construction excludes all Component content.
- [x] Node Agent is not a trusted Memory writer and sensitive Memory is rejected.
- [x] Golden hash and Memory records validate from committed fixtures.
- [x] All six evaluation risk classes have mutation-detecting tests.
- [x] The production v1 compiler and M4 Run path are unchanged.
