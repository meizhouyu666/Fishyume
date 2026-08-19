# Fishyume M5.7: Agent Activity Observability

> Status: implemented after the M5.6 product closure. M5.7 is an additive
> observability increment; it does not reopen the execution or Context contracts.

M5.7 lets an external Host Agent and the human TUI see what a headless Node Agent
is currently doing without turning Fishyume into an embedded multi-Agent Harness.
The ownership boundary remains:

```text
independent headless Node process
  -> Fishyume captures and exposes safe bounded activity
  -> Host Agent and TUI continuously observe
  -> existing cancel, retry, answer, and replan paths remain the controls
```

## Public view

`run.get` may include `attempt.activity` for the current Attempt:

```json
{
  "schemaVersion": "fishyume.attempt-activity/v1",
  "summary": "正在执行命令：go test ./...",
  "items": [
    {"kind": "command", "status": "running", "message": "正在执行命令：go test ./..."}
  ],
  "truncated": false
}
```

The view contains at most 12 normalized items. Each message is bounded to about
240 UTF-8 bytes and the parser reads only a bounded tail of the existing Attempt
`output.log`. Unknown or malformed JSONL is ignored. Historical output with no
known structured events simply omits `activity`.

Only known Codex lifecycle and item types are normalized. Command text and intended
Agent messages are bounded and common credentials are redacted. Raw reasoning,
`aggregated_output`, prompts, thread identifiers, unlimited stderr, and unknown
event payloads are never projected. Structured final JSON is represented only as
“Codex 已生成结构化结果”.

## Product behavior

The live Console already refreshes authoritative `run.get` state once per second.
It now shows the latest activity in a running Workflow row and up to six recent
items under the selected Node's `当前活动` detail. The 80/120/160-column and
Unicode/ASCII bounds remain unchanged. A Host Agent receives the same bounded view
through MCP and can summarize it for the user.

Activity remains observational data. It does not increment `stateVersion`, append
one permanent Run event per output line, or trigger reconciliation progress. M5.7
adds no prompt injection, live correction protocol, checkpoint handshake, model
routing, conversational Harness, new action, or new MCP tool.

