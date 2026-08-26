# Fishyume M7.7 Live Team Repair Acceptance

> Status: complete on 2026-08-26

M7.7 records the first user-driven Host Agent test of the installed three-Agent
Team path and the repairs required by that test. The frozen
`fishyume.team/v1` and `fishyume.application/v1` contracts are unchanged.

## Original Failure Evidence

The Host started Panel `team-a647bdf6f52105838c04ebce` with Claude, Codex
local, and OpenCode. Claude and OpenCode responded, while Codex failed before
performing the task. Its retained `direct-events.jsonl` reported:

```text
Invalid schema for response_format 'codex_output_schema':
In context=('properties', 'usageEstimates'), 'required' is required to be
supplied and to include every property. Missing 'inputTokens'.
```

This proved that authentication, executable discovery, and process launch were
not the cause. The Codex Team adapter had reused the formal Workflow Direct
output-schema path. The invalid strict schema was rejected upstream before the
Codex Agent could execute the contribution.

The adapter then attempted to open the absent `direct-result.json`, replacing
the useful upstream failure with a missing-file diagnostic. Existing fake-Agent
tests did not validate Codex structured-output schemas and therefore missed the
failure.

## Repaired Boundary

- Codex Team exploration now requests public Markdown from the Codex Agent and
  omits `--output-schema`.
- The Codex Driver validates the bounded Markdown and locally wraps it in the
  same `ContributionV1` envelope used by Codex AgentSession, Claude, and
  OpenCode.
- Formal Workflow Nodes retain their strict structured result path. M7.7 does
  not weaken the Workflow execution contract.
- Codex `error` and `turn.failed` events are retained as the participant
  diagnostic. A missing result file no longer hides the upstream failure.
- `web.open` is included in the Codex-owned MCP approval tool list.

## Additional Live-Test Repairs

The first post-fix Panel, `team-6ee62b17b3d03d04b9f2b02c`, proved the Codex
repair: both Claude and Codex responded, and the Codex artifact contained plain
Markdown with no `direct-result.schema.json`. OpenCode completed successfully
with `exitCode: 0`, but Fishyume observed the process between its exit and the
atomic `exit.json` rename and marked it lost.

The managed-process exit-evidence grace window is now 250 ms. A deterministic
test delays signed exit evidence by 100 ms after both process identities
disappear and verifies that the observation resolves to `exited`, not `lost`.

The local install rehearsal also found that `install-fishyume.ps1` still parsed
the retired English Dashboard text `N active`. The Dashboard is now Chinese, so
the safety check could not verify an idle Control Plane. The installer now
queries `run.list` for every non-terminal Run phase and parses the Application
JSON response before stopping an Engine.

## Final Live Acceptance

The current source was built and installed locally as `0.2.1-alpha.1`, with
`FISHYUME_AGENT_ROUTES_FILE` set to the documented absolute route file. Panel
`team-c0782b473a459bf95e6a3932` then ran against the real installed Agent
harnesses:

| Participant | Driver / target | Result |
| --- | --- | --- |
| `claude-smoke` | `claude/default` | `responded` |
| `codex-smoke` | `codex/local` | `responded` |
| `opencode-smoke` | `opencode/deepseek` | `responded` |

The Team closed with `panel_settled`, retained three independently identified
contributions, and used the trusted catalog hash
`2f15079527cd1ecaafc74581f7d574bd69b9e2db306f5db1e7a728ce94cf9a34`.
All workers performed the requested read-only repository check. No Workflow Run
or project-file mutation was created by the Panel.

## Automated Verification

The repair is covered by:

- Codex Team argument tests proving `--output-schema` is absent.
- Natural Markdown wrapping, empty/oversized output, failure-event diagnostic,
  artifact recovery, cancellation, and handle-integrity tests.
- Partial Panel integration preserving a successful peer when another fails.
- Delayed managed-process exit-evidence recovery.
- Codex MCP policy completeness including `web.open`.
- Structured active-Run safety checks in the Windows preview installer.

Repository gates completed successfully:

```text
go test ./...                         passed
go vet ./...                          passed
npm --prefix wf run verify            111 tests passed; build and pack audits passed
npm --prefix fishyume-web run verify  8 tests passed; build and pack audit passed
npm production dependency audits      0 vulnerabilities in both packages
```
