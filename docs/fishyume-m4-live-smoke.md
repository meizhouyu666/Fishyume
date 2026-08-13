# M4 Host Agent Smoke

The automated acceptance test `wf/src/integration/mcp-host-agent.test.ts` exercises the
Agent-facing path end to end without Provider credentials:

```text
MCP Client (Host Agent) -> MCP adapter -> local Control Plane -> Application Service
  -> Codex Driver -> repository fake Agent
```

It verifies `system.capabilities`, `workflow.validate`, `workflow.explain`, idempotent
`run.start`, `run.events`, Approval, `needs_input`/`answer`, and terminal `run.result`.
The fake Agent is only a deterministic protocol fixture; it does not claim that a real
Codex Provider login has passed.

Run it from the repository root:

```powershell
npm --prefix wf run test:mcp-host
```

For the real Host Agent/Provider gate, keep the same request sequence and replace the
fake Agent with a locally authenticated `codex exec --ephemeral --json` installation.
That gate is manual and must record the Codex version, project path, Driver readiness,
the exact Workflow, event/result transcript, and whether the Control Plane was reused
or started fresh. It must never be required by public CI or silently fall back to an
interactive TUI/MCP approval.
