# Fishyume M7.6 Host-Web Continuity

> Status: implemented as the M7.6 Host-Web continuity slice.

M7.6 keeps the Host Agent as the only intelligent product entry point while
making the optional Web client follow the Host-driven Team -> Handoff ->
Workflow Run journey.

## Boundary

- The Host Agent remains responsible for intent interpretation, Team actions,
  Handoff creation, Workflow authoring, validation, explanation, confirmation,
  and run.start.
- web.open is an adapter-level best-effort action. It creates no Team,
  Handoff, Workflow, or Run state.
- fishyume.team/v1 and fishyume.application/v1 are unchanged.
- The Web sidecar remains optional, loopback-only, and a projection over the
  existing Control Plane.
- The TUI remains a Workflow Run operator console. M7.6 does not add Team or
  Handoff views to it.

## Host flow

After the Host creates or advances a durable object, it may call:

    {
      "target": {
        "kind": "team",
        "teamId": "team-..."
      }
    }

The same tool accepts handoff (teamId + handoffId) and run (runId) targets.
The Host does not need to expose those identifiers to the human; it uses them
to keep the optional observation surface focused.

The MCP adapter starts fishyume-web on the first request, passes the target as
a launch fragment, and reuses the sidecar for later targets. A running
sidecar receives a protected /api/focus request instead of a second process.
If the optional package is unavailable, the tool returns unavailable with a
bounded reason and the Host flow remains fully headless.

## Web continuity

The sidecar owns only an in-memory focus target and monotonic revision. The
browser polls that endpoint while visible and navigates to the selected Team,
Handoff tab, or Run view. All Team, Handoff, and Run data still comes from the
Engine gateway. The browser never fabricates state and all mutations continue
to carry the authoritative state version.

Focus requests require the same exact loopback Host, Origin, and bearer token
checks as RPC requests. Targets are bounded to the existing durable identity
formats and are kept out of the query string; only the launch fragment is
used.

## Acceptance

- A Host web.open call is discoverable through MCP and returns opened,
  focused, or unavailable.
- The first target opens the Web client directly on Team, Handoff, or Run.
- Subsequent targets reuse the sidecar and move the existing browser view.
- Web and Host share the same Engine state and mutation conflicts.
- Missing Web installation never blocks Team or Workflow execution.
- Existing TUI, Team, Application, and package security tests remain green.
