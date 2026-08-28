# Fishyume Product Scope

This document is the scope guard for the current developer preview. It is
deliberately narrower than the full set of historical milestone designs.

## Product in scope

Fishyume is a local control plane used by a Host Agent to coordinate work and
let a human observe it through the Web client.

The supported path is:

```text
Host Agent -> Team exploration (optional) -> accepted handoff (optional)
           -> Workflow Run -> Web observation
```

The essential user outcomes are:

- start a Team or Workflow from an Agent;
- preserve useful Team contributions and Workflow results;
- show current state, node activity, dependencies, and failures clearly;
- show per-Node estimated input/output/total token usage when the Agent
  returns usage metadata;
- resume or cancel work when the existing durable semantics support it;
- keep the Host Agent responsible for deciding what to do next.

Codex on a local Windows x64 machine is the only supported execution path for
this developer preview. Other drivers and platforms remain compatibility code,
not release targets.

The current Host Agent integration is Codex-only: Fishyume exposes its Agent
facing MCP server and setup flow to a local Codex Host. Team participants may
use discovered Codex, Claude Code, or OpenCode routes, but that does not make
those tools interchangeable Host Agents, and Workflow execution remains
Codex-only in this preview.

Token values are observational estimates supplied by the executing Agent.
Fishyume does not own model calls, credentials, pricing, or billing, and a
missing usage value is a normal case rather than an error.

## Explicitly deferred

These are not current product requirements and should not receive new
abstractions or UI until a concrete user workflow requires them:

- pause-and-steer with conversational intervention;
- arbitrary remote or distributed execution (WSL/SSH/cloud);
- Fishyume-owned provider credentials, billing, or model prompting;
- IDE features such as file browsing, editing, terminals, or artifact preview;
- automatic Team-to-Workflow promotion;
- general-purpose plugin and third-party driver ecosystems;
- new persistence backends or a database migration;
- additional context, memory, routing, or fallback policy beyond what the
  current tested path needs.

Existing compatibility code is retained only when it reads real historical
state or protects an existing public entry point. It is not a template for
new features.

## Change gate

Before adding a new abstraction, configuration field, state, or public API,
answer all four questions:

1. Which current user action requires it?
2. Which existing implementation cannot express that action locally?
3. What is the smallest observable behavior that proves it works?
4. What maintenance and compatibility surface does it add?

If the first two answers are unclear, defer the change. Prefer a local change
over a shared abstraction until there are at least two real callers. Prefer a
single tested path over speculative generality.

## Cleanup rule

Tests that protect current behavior, security, recovery, or historical state
are not redundant merely because they are old or named after a milestone.
Remove a test only when its behavior is covered elsewhere and the old contract
is intentionally retired. Generated binaries, package staging directories,
and local run state belong outside the source tree and may be regenerated.
