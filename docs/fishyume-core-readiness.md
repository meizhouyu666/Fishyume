# Fishyume Core Readiness

Status: the first Provider-independent failure matrix was added after the M6
core contract freeze. This is an acceptance record for failure evidence, not a
claim that live Provider smoke or long-running production soak is complete.

## Failure matrix

The Driver evidence is classified before any retry or route decision:

| Driver evidence | Durable Run result | Retry / fallback boundary |
| --- | --- | --- |
| launch returns an error without a durable handle | `completed / indeterminate` | explicit retry must acknowledge duplicate risk |
| observation transport fails | `waiting / completion_missing` | explicit retry is allowed; no implicit route change |
| execution is lost | `completed / indeterminate` | explicit retry must acknowledge duplicate risk |
| observation state is unsupported or absent | `waiting / completion_missing` | explicit retry is allowed; no implicit route change |
| terminal `failed` with no side-effect evidence | `completed / failed`, evidence `unknown` | fallback is not eligible |
| terminal `failed` with `sideEffectStatus=none` | `completed / failed`, evidence `none` | explicit retry may select the persisted fallback route |
| terminal `indeterminate` | `completed / indeterminate`, evidence `unknown` | explicit retry must acknowledge duplicate risk |

The matrix is implemented by
[`m6_8_core_readiness_test.go`](../wf-engine/internal/run/m6_8_core_readiness_test.go).
It uses an in-memory fake Driver, so public CI does not require Provider
credentials, network access, or a live model.

## Verification

Run the focused gate from the repository root:

```powershell
go test ./wf-engine/internal/run -run M68CoreReadinessFailureMatrix
```

The broader stabilization gate remains:

```powershell
go test ./wf-engine/...
go vet ./wf-engine/...
npm --prefix wf run verify
```

The steady-state growth gate runs 64 unchanged reconciliation cycles and
requires the durable event count to remain constant until the Driver reports
new terminal evidence:

```powershell
go test ./wf-engine/internal/run -run M69SteadyStateReconciliationDoesNotGrowEventLog
```

Still separate from this gate:

- authenticated real Codex Provider smoke;
- long-running event, output, journal, and state-growth soak beyond the
  Provider-independent 64-cycle steady-state gate;
- rollback and historical-state migration drills beyond the state-schema-v1
  retry and same-prefix package-upgrade checks recorded below.

The event log is currently an append-only audit history. Retention or
compaction would change pagination semantics and therefore requires an explicit
future contract decision; this batch does not silently prune historical events.

The upgrade compatibility gate also verifies that a retry against a historical
state-schema-v1 Run preserves the old Attempt byte-for-byte while the new
Attempt is written with the current state/context schema. The packed install
smoke repeats installation into the same prefix after stopping an idle Control
Plane and verifies that the external state directory survives replacement.

The remaining live and rollback gates are intentionally separate and must not
become prerequisites for Provider-independent public CI.

Rollback safety now also fails closed on a future `stateSchemaVersion`: an old
reader may continue reading known historical schemas and omitted schema fields,
but it will not mutate a snapshot whose required semantics it cannot know.
Control Plane handshakes reject future state schemas and incompatible RPC
protocols, and Application journal recovery rejects future journal versions
without rewriting the original record.
