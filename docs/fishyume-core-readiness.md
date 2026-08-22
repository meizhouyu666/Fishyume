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

Still separate from this gate:

- authenticated real Codex Provider smoke;
- long-running event, output, journal, and state-growth soak;
- install, upgrade, rollback, and historical-state migration drills.

Those are the next readiness batches and must not be made prerequisites for
Provider-independent public CI.
