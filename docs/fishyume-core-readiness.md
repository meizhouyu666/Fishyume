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
- downgrade against a future immutable published tag/artifact, and rollback of
  Workflows that may already have produced external side effects.

The event log is currently an append-only audit history. Retention or
compaction would change pagination semantics and therefore requires an explicit
future contract decision; this batch does not silently prune historical events.

The upgrade compatibility gate also verifies that a retry against a historical
state-schema-v1 Run preserves the old Attempt byte-for-byte while the new
Attempt is written with the current state/context schema.

The packed install smoke now performs a Provider-independent state rollback
drill around an Approval-only Run. It waits for durable Approval state, stops
the Control Plane, snapshots the external state directory, reinstalls both
packages into the same prefix, and proves that `run.start` idempotency, the Run
snapshot, and the event sequence remain unchanged. It then completes the Run,
restores the waiting snapshot, verifies that observation is byte-preserving,
and completes the restored Run again with a new action ID and the same terminal
state contract. The Approval-only workflow deliberately has no Provider or
external side effect, so the restore cannot duplicate business execution.

This drill proves that the current package can restore and reconcile a stopped
current-schema snapshot. It does not claim that an arbitrary older executable
can read state created by a newer release.

## Historical package downgrade rehearsal

The explicit local gate `npm --prefix wf run smoke:downgrade` rebuilds packages
from the accepted M5.6 commit
`391dc2c3a788b7754b52d4234fbfc80c5d5a3dae` and from the current checkout. It
verifies the installed CLI implementation and Engine binary hashes after every
replacement, so npm cache or the shared alpha version cannot silently turn the
test into a same-package reinstall.

The historical package creates an Approval-only Run and snapshot. The current
package reads the historical state, replays the same `clientRequestId`, and
completes it. The gate then stops the current Control Plane, installs the
historical package pair, restores the matching historical snapshot, replays the
same start receipt, and completes the Run with a distinct action ID. Both paths
must converge to `succeeded` with the same terminal state version. No Provider,
network, credential, or external business side effect enters the drill.

The repository has no release tag and both commits identify themselves as
`0.2.1-alpha.1`; this is therefore a cross-commit package rehearsal, not proof
about a published release artifact. The base can be overridden explicitly with
`FISHYUME_DOWNGRADE_BASE` when testing a future immutable tag. A shallow clone
that lacks the selected commit fails with a fetch-history diagnostic rather
than falling back to current source.

The remaining live and published-artifact downgrade gates are intentionally
separate and must not become prerequisites for Provider-independent public CI.

Rollback safety now also fails closed on a future `stateSchemaVersion`: an old
reader may continue reading known historical schemas and omitted schema fields,
but it will not mutate a snapshot whose required semantics it cannot know.
Control Plane handshakes reject future state schemas and incompatible RPC
protocols, and Application journal recovery rejects future journal versions
without rewriting the original record.
