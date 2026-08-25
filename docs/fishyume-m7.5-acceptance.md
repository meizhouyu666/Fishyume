# Fishyume M7.5 Optional Web Team Client Acceptance

> Status: complete on 2026-08-25

M7.5 delivers `fishyume-web`, an optional local browser projection over the
frozen Team and Application contracts. It does not add a TCP Engine listener,
move state ownership into the browser, or add a dependency to the core
`fishyume` package.

## Delivered Boundary

- `fishyume-web` is a separate package with a Node sidecar and static browser
  bundle.
- The sidecar connects through the existing EngineBridge and binds only
  `127.0.0.1` on an ephemeral port.
- Each launch creates a 256-bit token delivered only in the URL fragment.
- RPC requires the exact loopback peer, `Host`, `Origin`, and bearer token.
- The gateway has strict method allowlisting, 64 KiB request, 2 MiB response,
  eight concurrent request, and 15 second response bounds.
- Security headers deny framing, remote code, referrers, caching, and wildcard
  CORS.
- Mutation IDs are retained in `sessionStorage`; the bearer token is never
  stored there or on disk.

## Browser Evidence

The local sidecar was run against a freshly built current Engine in an isolated
temporary state directory. Existing Run snapshots were copied read-only for
the check, and a repository fake app-server produced one disposable
`TeamSession`; the user's default Control Plane and project files were not
modified.

- Desktop viewport: 1440x900. Team list, participants, ordered messages,
  Handoff tab, linked Run tab, follow-up composer, and Run topology rendered
  with Lucide icons and no visible overlap.
- Mobile viewport: 500x844. Team controls and two-column metrics collapse to a
  readable single-column flow; long Run topology content stays inside the
  viewport. A 390px Chromium smoke also confirmed the narrow navigation state.
- Real Run projection loaded completed Runs and rendered existing node result,
  conclusion, parallel layers, and diagnostic text without inventing state.
- Wrong bearer requests returned HTTP 403. The browser token remained in the
  fragment and was sent to the API only as `Authorization: Bearer`.

## Automated Verification

`fishyume-web` passed:

```text
npm run typecheck
npm test                 # 7 tests passed
npm run build
npm run pack:audit       # 8 package files, no source/test leakage
npm run smoke:install
```

The package tests cover loopback and Host/Origin/token authorization, strict
RPC envelopes, CSP and no-store policy, platform package Engine resolution,
concurrency, request/response bounds, and timeout behavior.

The core package remains independently verifiable with `npm --prefix wf run
verify`; the repository CI workflow runs both package gates on Windows and
Ubuntu, while the core Engine and deterministic stress gates remain unchanged.

## Deferred Boundaries

M7.5 does not add remote collaboration, multi-user authentication, generic
Shell/HTTP/container nodes, a second built-in Provider, or a Web-owned
scheduler/state machine. A real Provider smoke remains an explicit local gate.
