# Fishyume Development

## Local gates

From the repository root, run the cross-platform preflight entry point:

```powershell
node wf/scripts/preflight.mjs
```

It runs, in order, `go test ./...`, `go vet ./...`, `go build ./cmd/wf-engine`, `npm --prefix wf run verify`, and `git diff --check`. Each step has a visible boundary and stops immediately on failure. Use `--step NAME` to run one primitive (`go-test`, `go-vet`, `go-build`, `typescript`, or `diff-check`); `--dry-run` validates arguments and prints commands without running them.

For high-risk lifecycle packages, run the deterministic stress gate before delivery:

```powershell
node wf/scripts/stress.mjs
```

The default is `go test -count=20 -timeout 10m` over `internal/run`, `internal/store`, `internal/controlplane`, and `internal/driver/codexprocess`. Set `--count N` or `--timeout DURATION` for a focused local check. This is one bounded test invocation, not retry-on-failure: a failure always fails the gate.

## Test lifecycle

Controller, server, goroutine, and subprocess tests must explicitly shut down and join every background worker before returning. Reaching a business state is not test completion. Synchronize with channels, hooks, or system APIs rather than `Sleep` for correctness. A `t.TempDir()` must remain alive until all background writers have stopped and joined.

## Delivery policy

Use this sequence: implementation branch -> local preflight -> temporary branch CI -> reviewed exact SHA -> `main`. Do not add automatic destructive hooks. Local Ubuntu, WSL, and Docker are not required; the Linux race gate remains CI-only when those environments are unavailable.
