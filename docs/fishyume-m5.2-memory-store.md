# Fishyume M5.2: Project Memory ledger

> Status: implemented on the M5.1 baseline `6a35a262945b97d928cbbe57f52168559c468da9`; final full-gate and CI evidence is recorded in the acceptance section after the implementation commit.

M5.2 adds a local, typed, auditable Memory ledger. It is deliberately separate from
Run execution. No M5.2 code selects Memory for an Attempt, increments `useCount`,
changes Workflow parsing, discovers a Context source, promotes a Node Result, or writes
Memory into a project repository.

## Storage and project identity

The default state root remains the existing Fishyume local state directory:

- Windows: `%LOCALAPPDATA%\fishyume`
- Linux: `$XDG_STATE_HOME/fishyume` or `~/.local/state/fishyume`
- macOS: `~/Library/Application Support/fishyume`

For an existing project directory, Fishyume resolves an absolute path and all symlinks.
The identity input is the cleaned path with `/` separators and, on Windows only,
lower-case characters. Its lower-case hexadecimal SHA-256 is the project hash. Data is
stored only at:

```text
<state-root>/memory/projects/<canonical-project-hash>/catalog-v1.json
<state-root>/memory/projects/<canonical-project-hash>/catalog.lock
```

Aliases and symlinks of one project therefore share one ledger. Different canonical
projects cannot share it. The canonical project path is retained in every record for
auditability, but the project hash is the directory authority. A missing, non-directory,
unresolvable, invalid-UTF-8, padded, or over-4,096-byte project input is rejected.

## Exact catalog-v1 schema

The complete JSON shape is:

```json
{
  "schemaVersion": "fishyume.memory-catalog/v1",
  "project": "<canonical absolute project path>",
  "projectHash": "<64 lower-case SHA-256 hex characters>",
  "revision": 1,
  "records": ["<fishyume.memory/v1 objects, sorted by id>"],
  "receipts": [
    {
      "mutationId": "<caller mutation identity>",
      "requestHash": "<64 lower-case SHA-256 hex characters>",
      "operation": "create | supersede | delete",
      "writer": "user | host_agent | migration",
      "reason": "<bounded audit reason>",
      "revision": 1,
      "recordId": "<affected primary record id>",
      "affectedIds": ["<sorted record ids>"],
      "createdAt": "<RFC3339Nano UTC>"
    }
  ]
}
```

The record object is the frozen `fishyume.memory/v1` contract from M5.0. M5.2 does
not add lifecycle fields or accept a caller-supplied record. Its exact fields remain:

```text
schemaVersion, id, project, type, scope, content?, contentHash, sensitivity,
provenance{writer,source,sourceVersion,sourceHash,reason}, createdAt, updatedAt,
supersedes[], state, stateReason?, useCount, retention{expiresAt?,maxUses?}
```

Catalog invariants are checked on every read before any mutation:

- unknown JSON fields, duplicate JSON object keys, trailing values, invalid UTF-8,
  malformed/truncated JSON, and unsupported versions fail closed;
- the catalog identity must match its hashed directory;
- `records` is non-null, strictly ID-sorted, duplicate-free, and limited to 2,048;
- every record validates against `ValidateMemoryRecordV1`, has literal scope
  `project`, belongs to the canonical catalog project, and has a non-null
  `supersedes` array;
- `receipts` is non-null, revision-ordered, mutation-ID-unique, and limited to 4,096.
  Receipt hashes are canonical lower-case SHA-256 and `affectedIds` is non-null.
  A bounded 2,048-record ledger permits at most 2,048 record-creating mutations and
  2,048 deletions, so this retains every valid successful mutation for lifetime replay;
- catalog revision is durable, equals the final retained receipt revision, and increases
  exactly once per committed new mutation; revision zero permits no records or receipts;
- content is valid UTF-8, non-blank, at most 16 KiB, and never labelled `sensitive`;
- supersession targets are sorted, unique, and limited to 1..16;
- `useCount` is created as zero and M5.2 never changes it.

The catalog has a 64 MiB encoded safety ceiling, comfortably above the worst permitted
2,048-record payload while still bounding corrupt-file reads. The deterministic fixture
is `wf-engine/internal/store/testdata/memory-catalog-v1.json`.

## Commit, locking, and recovery protocol

Every read and write opens `catalog.lock` and obtains an exclusive operating-system file
lock. Windows uses `LockFileEx`; Unix uses `flock`. This serializes independent Store
instances and processes, not only goroutines.

While holding the lock Fishyume:

1. removes only abandoned `.catalog-v1-*.tmp` files in that project state directory;
2. strictly reads and validates the existing catalog, or constructs revision zero when
   no catalog exists;
3. validates the whole proposed catalog;
4. writes a mode-`0600` temporary file beside the destination;
5. flushes the temporary file with `Sync`, closes it, and atomically replaces the live
   catalog;
6. uses `MoveFileEx(REPLACE_EXISTING|WRITE_THROUGH)` on Windows, or `rename` followed by
   directory `Sync` on Unix.

Pre-replace failures remove the temporary file and leave the previous catalog intact.
Post-replace response loss leaves a committed receipt, so an exact retry converges.
Corrupt or unsupported catalogs are never renamed aside, repaired, truncated, or
overwritten automatically.

Deletion replaces the live catalog with a version in which the record content is empty.
Receipts contain only a request hash and audit metadata, never a copy of content. The
same locked operation removes abandoned Memory temporary files. Acceptance therefore
means no deleted plaintext remains in the live catalog or an abandoned Memory temp file;
it does not claim forensic SSD secure erase.

## Lifecycle semantics

All writes require a non-empty `mutationId` (valid UTF-8, trimmed, at most 256 bytes).
Fishyume hashes the compact JSON encoding of the canonical request with SHA-256. A retained receipt with the same ID and hash
returns its original revision/record identities with `replayed: true`. The same ID with
a different operation or canonical request returns `conflict`. Catalog data and the
receipt are one atomic JSON replacement.

`memory.create`

- creates exactly one `active` record;
- computes ID, canonical project, scope, timestamps, hashes, provenance source, state,
  `useCount`, and lifecycle fields;
- accepts only type, content, public/project sensitivity, audit reason, and optional
  `expiresAt`/`maxUses` policy.

Record IDs are `memory-` plus the first 16 SHA-256 bytes (lower-case hex) of
`projectHash + NUL + operation + NUL + mutationId`. Content hashes are SHA-256 over the
exact UTF-8 content bytes. Source hashes are SHA-256 over
`source + NUL + sourceVersion`.

`memory.supersede`

- requires 1..16 unique existing `active` records from the same project;
- atomically creates one active replacement whose sorted `supersedes` names those
  records and marks every target `superseded` with one timestamp/reason;
- performs no partial change if any target is missing, non-active, invalid, or capacity
  would be exceeded.

`memory.delete`

- accepts an active or superseded record;
- clears content and changes state to `deleted`, retaining content hash, original
  provenance, retention metadata, supersession links, and the explicit deletion reason;
- rejects a new deletion of an existing tombstone, while an exact retry replays through
  its receipt.

Expiry and `maxUses` are metadata only. They do not cause background writes. M5.2 has no
eligibility endpoint, so it does not imply a current-time view; future eligibility work
must require an explicit `asOf`. M5.2 does not increment `useCount`.

## Application, RPC, CLI, and MCP surfaces

The Go Application Service exposes `MemoryCreate`, `MemoryGet`, `MemoryList`,
`MemorySupersede`, and `MemoryDelete`. JSON-RPC advertises the corresponding exact
methods:

```text
memory.create  memory.get  memory.list  memory.supersede  memory.delete
```

Mutation responses contain `apiVersion`, committed `revision`, primary `recordId`,
sorted `affectedIds`, and `replayed`. `memory.get` returns one full record. `memory.list`
returns only metadata, never `content`, with a default limit of 50 and hard limit of 100.
Exact optional filters are `type`, `state`, `sensitivity`, and `writer`; there is no
semantic search. A cursor binds the filter hash, catalog revision, and last ID. A changed
filter or revision returns `conflict`, preventing unstable page drift.

Writer is a facade property, not a public request field:

- public JSON-RPC and `fishyume memory ...` writes are fixed to `user`;
- MCP `memory.*` write tools route internally to a non-advertised host adapter and are
  fixed to `host_agent`;
- a caller-supplied `writer` JSON field is rejected by strict RPC request decoding;
- `migration` exists only in the internal Store contract and has no CLI/MCP operation.

Both facade sources and their source hashes are computed by Fishyume:
`fishyume.cli` for user writes and `fishyume.mcp` for host writes, at source version
`fishyume.application/v1`. Host writes require a non-empty audit reason; the same bounded
reason requirement is applied to user writes for a uniform ledger.

CLI writes intentionally have no inline content flag:

```powershell
Get-Content .\memory.txt -Raw | fishyume memory create --stdin --project E:\project --mutation-id m-1 --type fact --reason "confirmed project fact"
fishyume memory create --file .\memory.txt --project E:\project --mutation-id m-2 --type constraint --reason "approved constraint"
fishyume memory get <record-id> --project E:\project
fishyume memory list --project E:\project --state active --limit 50
fishyume memory supersede --file .\replacement.txt --project E:\project --mutation-id m-3 --supersedes <record-id> --type decision --reason "new decision"
fishyume memory delete <record-id> --project E:\project --mutation-id m-4 --reason "obsolete"
```

Every CLI invocation emits one bounded JSON object. MCP returns the same public response
types as structured content plus bounded JSON text.

## Errors and compatibility

The Store preserves the frozen Memory meanings `memory_invalid_record`,
`memory_conflict`, `memory_not_found`, and `context_version_unsupported`, with additive
catalog diagnostics `memory_catalog_corrupt` and `memory_store_unavailable`. Application
mapping remains on the existing stable codes:

| Store condition | Application code |
| --- | --- |
| invalid request/record | `invalid_argument` |
| mutation, lifecycle, capacity, or cursor conflict | `conflict` |
| missing record | `not_found` |
| corrupt/unsupported/unavailable catalog | `internal` |

M5.0 `fishyume.memory/v1`, existing Application errors, RPC protocol version 2, and all
Run compatibility routes remain unchanged. The Application capabilities response only
adds Memory bounds. No database/native dependency, migration, export/import, vector,
embedding, model call, plugin, Web/Desktop, TUI manager, or prompt optimization is added.

## Acceptance evidence

Focused deterministic tests cover canonical/symlink identity, cross-project isolation,
goroutine and process writers, no lost updates, create/supersede/delete replay and ID
conflicts, all-or-nothing supersession, pre/post-replace fault stages, stale temp cleanup,
strict corruption rejection, bounds, invalid UTF-8, sensitive rejection, tombstone
plaintext removal, revision-bound pagination, retention non-mutation, fixed facade
writers, MCP audit reason, and Windows/Unix lock/replace implementations.

Local acceptance on Windows completed on 2026-08-17:

- focused Store/Application/RPC and MCP/CLI tests passed;
- the deterministic high-risk stress script passed 20 repetitions each for Run, Store,
  control plane, and direct CLI backend without retrying a failure;
- the exact repository preflight passed: `go test ./... -count=1 -timeout 10m`,
  `go vet ./...`, `go build ./cmd/wf-engine`, 73 TypeScript tests, TypeScript build,
  dry and real 84-file package audits, and `git diff --check`;
- production operation references across Run, Workflow, Driver, Context Compiler v1,
  and Context Source Registry v2 were `0`; TUI Memory-manager references were `0`;
- an optional local Windows `go test -race` binary could not start with OS status
  `0xc0000139` before executing tests. It was not retried or treated as code evidence;
  the required Ubuntu GitHub race job is the authoritative race gate.

The final handoff records the implementation commit, GitHub workflow result, remote
parity, and clean worktree after the resulting Windows/Ubuntu CI reaches a terminal
state.
