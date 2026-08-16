# Fishyume M5.1 Context Source Registry

> Status: implemented and covered by golden, evaluation, and negative fixtures on 2026-08-17. The registry remains side-by-side with the production `context-compiler/v1` Run path.

## Purpose and boundary

M5.1 resolves caller-approved Context sources into the frozen M5.0 `ContextComponentV2` and `ContextOmissionV2` contracts. Resolution is local, deterministic, and independently testable. It does not allocate an attention budget, compile an Envelope, persist Memory, or connect v2 Context to Run, Application, RPC, Workflow parsing, Drivers, or the TUI.

The only registry is `BuiltinContextSourceRegistryV2()`. Its ordered source kinds are immutable and there is no dynamic registration or plugin hook:

1. project instructions;
2. Workflow policy;
3. current Node task;
4. user answers;
5. explicitly declared dependency Results;
6. explicitly selected Memory records.

Resolved Components use the full M5.0 canonical kind order, then stable Component ID order within a kind. Omissions use stable Component ID order. The caller's slice or map iteration order cannot change the result.

## Typed resolution API

`ContextSourceResolutionInputV2` accepts typed declarations for each built-in source. `ContextSourceRegistryV2.Resolve` returns `ContextSourceResolutionV2`, containing only M5.0 Components and Omissions. It computes `sourceHash`, `contentHash`, byte counts, and truncation from the exact resolved UTF-8 bytes; callers cannot inject those fields.

Every declaration supplies a stable Component ID, source version, bounded non-secret selection reason, attention tier, and sensitivity. The Node task additionally supplies its validated Workflow Node ID so provenance is `workflow:node/<nodeId>`. Project instructions, the current Node task, and a present user answer must use `required`. Workflow policies and dependency Results may use `required` or `important`. Memory is always `optional` and inherits sensitivity from its validated record. M5.1 never truncates content.

An absent current Node task or empty required source returns `context_required_missing`. Any non-Memory source explicitly marked unavailable returns `context_source_unavailable`, even when it is important rather than required. Invalid declarations return a frozen M5.0 contract error. Duplicate Component IDs, cross-kind ID collisions, more than one current Node task, and multiple declarations for one upstream dependency fail explicitly.

## Project instruction files

Project instructions may be supplied inline or by one explicit relative file path. File resolution:

- canonicalizes the declared project root;
- evaluates the selected file path and symbolic links before reading;
- rejects absolute paths and any canonical target outside the project root;
- accepts only regular files bounded by `MaxProjectInstructionFileBytes` (64 KiB);
- reads only the declared file and never scans or imports the repository;
- records normalized project-relative provenance without persisting the project file body.

A missing, unreadable, oversized, non-regular, escaping, or cross-boundary symlink target returns `context_source_unavailable` with a bounded error that does not contain source content.

## Dependency isolation

`DependencyResultSourceV2` accepts one explicitly named upstream Node and one validated `workflow.Result`. There is no resolver input for all Run results, ancestors, or siblings. The canonical Result JSON becomes the Component content, and two declarations for the same upstream Node are a conflict. Unrelated parallel sibling output therefore cannot enter resolution unless the caller explicitly and incorrectly declares it as a dependency.

## Explicit Memory selection

M5.1 has no Memory Store and performs no discovery. The caller supplies only explicitly selected `MemoryRecordV1` values, an explicit selection reason, the canonical project root, and an explicit `AsOf` timestamp. Each record must pass `ValidateMemoryRecordV1`, belong to the same canonical project, and have a unique stable ID.

Lifecycle mapping is fixed:

| Record condition | Resolution |
|---|---|
| `active`, before expiry, below `maxUses` | included as optional Memory |
| `superseded` | `superseded` omission |
| `deleted` tombstone | `unavailable` omission with zero original bytes |
| expiry reached or `maxUses` reached | `expired` omission |

The resolver never reads the system clock. Omitting `AsOf` when Memory is selected is an error. It does not increment use counts or change records. Agent Results have no Memory-write path; only already validated records from trusted M5.0 writers can be selected.

## Persistence and compatibility

Registry output is ephemeral source material. Durable metadata must continue through `BuildContextManifestV2`, which excludes Component content. Tests include a sensitive user-answer marker and prove it is absent from the durable manifest.

`context-compiler/v1` production behavior, Attempt snapshots, Run scheduling, Application/RPC contracts, Workflow schemas, and Driver prompts are unchanged. M5.2 may add the separately approved Memory Store and lifecycle API. M5.3 may consume this registry for deterministic attention allocation. M5.4 remains responsible for all production integration and inspect surfaces.

## Verification fixtures

- `testdata/source-registry-v2.json` freezes all six source types, hashes, provenance, byte accounting, ordering, and active/superseded/deleted/expired Memory behavior.
- `testdata/evaluation-v1.json` now includes an explicit expired-Memory regression fixture in addition to the original six risk classes.
- Negative tests cover missing and unavailable sources, duplicates and conflicts, traversal and symbolic-link escape, file bounds, invalid/cross-project Memory, dependency isolation, and sensitive durable-metadata leakage.
