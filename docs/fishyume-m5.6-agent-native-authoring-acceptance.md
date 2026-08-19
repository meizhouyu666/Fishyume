# Fishyume M5.6: Agent-native Authoring and Acceptance

> Status: approved preparation boundary. M5.5 CI run
> [32219717906](https://github.com/meizhouyu666/Fishyume/actions/runs/32219717906)
> passed all Windows, Ubuntu, install, artifact, and deterministic stress jobs.

M5.6 turns the completed M5 execution and Context Engineering capabilities into one
clear, repeatable Host Agent product path. It does not add another orchestration
runtime. Codex, Claude, Kimi, or another capable Host Agent remains responsible for
discussing the user's goal and authoring the Workflow; Fishyume validates, explains,
persists, schedules, observes, and reconciles it.

## Product outcome

A newly connected Host Agent can discover how to operate Fishyume from the existing
MCP surface, author a `fishyume/v2` Workflow, optionally select existing Memory,
validate and explain the exact intended request, start it idempotently, handle human
approval or Agent questions, and obtain a terminal result without relying on repository
documentation or hidden conventions.

The canonical sequence is:

```text
system.capabilities
  -> optional memory.list / memory.get
  -> workflow.validate
  -> workflow.explain
  -> run.start
  -> run.events + run.get
  -> optional run.action
  -> run.result
```

`run.start.attach` remains the handoff to the human-facing TUI. The Host Agent and TUI
observe the same durable Run; neither client owns the Agent processes.

## Decisions

### Extend `system.capabilities`; do not add a guide tool

`system.capabilities` already is the required first Host call and returns the Workflow
schema, limits, Drivers, actions, and minimal example. M5.6 adds a bounded, versioned
authoring guide to that response rather than adding `system.guide` or an MCP resource.

This keeps the MCP tool list and Codex approval policy unchanged, avoids another
interactive permission surface, preserves Machine/MCP response parity, and makes guide
discovery deterministic. The guide is structured data rather than a long prose prompt.

Proposed additive shape:

```json
{
  "authoringGuide": {
    "schemaVersion": "fishyume.authoring-guide/v1",
    "recommendedFlow": [
      "system.capabilities",
      "workflow.validate",
      "workflow.explain",
      "run.start",
      "run.events",
      "run.get",
      "run.action",
      "run.result"
    ],
    "workflowApiVersion": "fishyume/v2",
    "rules": [
      "dependsOn controls scheduling; context.dependencies controls result injection",
      "contextBindings are selected by the Host and must be passed to validate, explain, and start",
      "reuse a clientRequestId only for the identical run.start request",
      "derive actions from the latest run.get stateVersion"
    ]
  }
}
```

Rules and arrays are frozen, bounded, non-secret, and Provider-independent. Full manuals,
prompts, credentials, environment variables, and dynamic project content do not enter
the capability response.

### Validate and explain the exact start intent

The Host uses the same Workflow, inputs, Driver/target overrides, and Context bindings
for `workflow.validate`, `workflow.explain`, and `run.start`. M5.6 documentation and
fixtures must not demonstrate validating one request and starting a materially different
one.

`workflow.explain` is the preflight view. For every Node it exposes:

- scheduling dependencies and parallel layer;
- effective Context Policy version;
- explicit Context dependencies;
- project instruction paths;
- selected Memory IDs and non-secret reasons;
- resolved Driver and target;
- capability gaps and warnings.

It remains deterministic and performs no model call, Memory search, Driver launch, or
state mutation. Budget inclusion/omission is Attempt-time evidence and remains available
through `run.get`; explain must not pretend it can predict dynamic compiled byte usage.

### Keep Memory selection explicit

M5.6 may teach the Host to use `memory.list` metadata and `memory.get` for records it is
considering. It does not add semantic search, embeddings, automatic selection, or
result-to-Memory promotion. The Host records a bounded reason for every selected ID and
passes those bindings explicitly.

### Keep authoring separate from execution

Fishyume does not converse with the user and does not generate a Workflow using an
embedded LLM. A future specialized harness may wrap this same public contract, but M5.6
only makes the contract sufficiently self-describing for external Host Agents.

## Implementation batches

### M5.6.1: Authoring Guide contract

- Add the versioned bounded guide to Application `system.capabilities`.
- Update Go/TypeScript types, contract fixtures, schema bounds, Machine output, and MCP
  parity tests.
- Strengthen MCP tool descriptions for v2 Context Policy, exact preflight payloads,
  action preconditions, and terminal result retrieval.
- Keep the MCP tool list and Codex approval configuration unchanged.

### M5.6.2: Canonical v2 examples

- Add one copyable `fishyume/v2` Workflow using explicit project instructions,
  Context dependencies, Approval, and an Agent node.
- Add one exact Host request set covering optional Memory selection, validate, explain,
  idempotent start, events/get, approval or answer, and result.
- Update root/package README quick starts to route Host Agents through this golden path;
  retain human-authored Workflow examples as an advanced option.

### M5.6.3: Provider-independent acceptance

- Upgrade the MCP Host acceptance from v1 to the canonical v2 request.
- Prove validate/explain/start receive the same Context bindings.
- Prove selected included Memory reaches the fake Driver and increments `useCount` once;
  omitted Memory is not consumed.
- Cover Approval, `needs_input`, stale `stateVersion`, idempotent `clientRequestId`, event
  pagination, terminal result, attach command, and TUI observation of the same Run.
- Retain metadata-leak markers across snapshots, events, RPC, MCP, logs, and TUI.

### M5.6.4: M5 release closure

- Record exact local preflight and public Windows/Ubuntu CI evidence.
- Update the M5 plan and release-readiness document from `implemented batches` to an
  accepted Agent-native product path.
- Publish no package or GitHub Release unless separately authorized.

## Non-goals

- Model/target routing, task-complexity classification, fallback, and cost policies.
- Prompt optimization or a versioned Prompt Profile library.
- Automatic Memory retrieval, embeddings, ranking, or promotion.
- A conversational harness or embedded sub-Agent runtime.
- Third-party Driver discovery, dynamic plugins, Web/Desktop UI, or generic workflow
  node types.
- New MCP tools, RPC protocol versions, Provider credentials, or public-CI model calls.

## Acceptance gates

- A Host Agent can infer the complete safe call order from `system.capabilities` plus MCP
  tool descriptions without reading repository files.
- Capability output remains within the existing schema response bound and contains no
  credentials, prompts, Memory content, or project instruction content.
- Machine CLI and MCP return byte-equivalent Application response data.
- Existing Codex setup remains idempotent and introduces no new approval prompt.
- The canonical v2 Host path passes with fake Drivers on Windows and Ubuntu.
- A real Provider smoke remains optional/local and never gates public CI.
- Historical v1 Workflow/Run compatibility and all M5.5 exactly-once guarantees remain
  unchanged.
- Full Go tests, vet, Engine build, package verify, pack audit, diff check, deterministic
  stress gate, and platform-install jobs pass.

## Exit condition

M5 closes when an external Host Agent can discover, author, preflight, execute, observe,
interact with, and collect a v2 Workflow through the public Fishyume contract, with a
human able to attach the TUI, and with no undocumented step required for the golden path.
Only after this gate should M6 freeze model capability and routing contracts.
