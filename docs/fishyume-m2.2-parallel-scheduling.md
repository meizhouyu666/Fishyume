# Fishyume M2.2 Parallel Scheduling Architecture

> Status: approved on 2026-08-05
>
> Prerequisite: M2.1.2 Backend independence and two-Backend evidence are complete
>
> Scope: finish the remaining M2 parallel scheduling work; do not implement a third-party plugin SDK or runtime plugin loading

## 1. Decision

M2.2 completes the unfinished part of M2: bounded parallel execution of independent Agent Nodes. Fishyume remains an Agent and human-decision orchestration engine. This milestone does not expand into a generic workflow platform and does not begin the third-party Backend ecosystem.

The product strategy is **plugin-ready, not plugin-enabled**. Parallel scheduling must strengthen the platform-neutral Backend boundary instead of introducing CC-Panes- or Direct-specific scheduler behavior.

## 2. Non-negotiable Backend foundations

All M2.2 design and implementation must preserve these constraints:

1. Fishyume Engine owns Run, Node, Attempt, Approval, Resume, Retry and Cancel semantics. No core lifecycle rule may depend on CC-Panes sessions or Direct CLI processes.
2. All Backends implement one shared contract and pass the same conformance suite. The scheduler must not branch on Backend names or grant a built-in Backend hidden privileges.
3. Backend differences are expressed through platform-neutral capabilities and resource limits. Capability validation happens before execution; runtime observations remain part of the normal Backend contract.
4. Persisted state contains stable Backend identity, versioned capabilities when required, and opaque execution handles. It must not persist implementation objects, credentials, full environments, full prompts or terminal history.

## 3. Existing M2 remainder

The original M2 architecture deferred the following work from M2.1:

- accept `execution.maxConcurrency > 1`;
- start independent ready Agent Nodes in parallel;
- reconcile multiple active Attempts after Engine restart;
- cancel multiple active executions without reporting false success;
- aggregate Run phase, conclusion and diagnostics from concurrent Node states;
- enforce Workflow and Backend resource limits.

M2.1.2 intentionally did not implement these items. It supplied the required platform-neutral Backend contract and proved the same Workflow/state model on CC-Panes and Direct.

## 4. Current implementation constraints

The current code is intentionally single-concurrency and cannot be safely extended by only adding goroutines:

- `workflow.MaxConcurrency` is fixed at `1`, and validation rejects every other value;
- `WorkflowSnapshot.ActiveNodeID` and `StatusView.ActiveAttempt` model only one active execution;
- the controller calls `findActiveNode`, reconciles one Attempt, and then calls `scheduleOne`;
- an Agent or Approval waiting state currently becomes the Run-wide waiting state;
- cancellation locates and cancels only the first active Attempt;
- several failure paths immediately complete the Run and skip every unstarted Node;
- the event stream is globally ordered and persistence is serialized, which must remain true under parallel Backend I/O.

These are state-model and lifecycle changes, not only scheduler-loop changes.

## 5. Proposed M2.2 execution model

### 5.1 Deterministic bounded scheduling

The scheduler derives a ready set from persisted Node state and the immutable normalized Workflow. It orders ready Nodes by the existing deterministic topological order and starts at most the remaining execution capacity.

`execution.maxConcurrency` limits active Agent Attempts in one Run. Approval Nodes do not consume an Agent execution slot. An Agent in `waiting_input`, `result_pending`, or another non-terminal execution state continues to consume its slot because the Backend execution still exists.

The effective limit is the minimum of:

- the Workflow `maxConcurrency`;
- the selected Backend's declared per-Run concurrency limit;
- a Fishyume safety ceiling.

The exact public ceiling and representation of an unknown/unlimited Backend limit remain implementation-plan decisions and require tests before being exposed as stable API.

### 5.2 Single state owner, parallel Backend I/O

Fishyume keeps one logical Run controller and one serialized state-commit path. Backend `Start`, `Observe`, `Output`, and `Cancel` operations may execute concurrently, but their results are converted into deterministic state transitions before persistence.

The controller must not hold the state mutation lock while waiting on Backend I/O. Every completion is checked against Run controller generation, Node ID and Attempt number before it may mutate state. This preserves the existing protection against stale controllers and duplicate starts.

### 5.3 Active state and compatibility

Active work is derived from Node/Attempt snapshots rather than one authoritative scalar `ActiveNodeID`. Public status gains collections for active Nodes and Attempts. The existing singular fields remain readable for M2.1/M2.1.2 state and may be populated when exactly one Node is active.

If the persisted snapshot shape changes, M2.2 increments `stateSchemaVersion` and provides explicit compatibility decoding. Existing M2.1.1 and M2.1.2 Runs remain status-readable and retain their existing resume/cancel guarantees.

### 5.4 Approval aggregation

Multiple branches may reach Approval Nodes while other Agent Nodes are still running:

- waiting Approval Nodes are represented per Node;
- the Run remains `running` while any Agent Attempt is active or another ready Node can start;
- the Run becomes `waiting: approval_required` only when no execution can progress without a human decision;
- resume actions continue to name the exact Approval Node and remain idempotent.

### 5.5 Failure policy

M2.2 uses **stop scheduling and drain active siblings**:

- after a terminal failure, Fishyume launches no new Nodes that cannot contribute to an explicit eligible failure branch;
- already-running independent siblings are observed to a durable terminal or waiting state instead of being implicitly killed;
- descendants of failed Nodes are skipped with `upstream_failed`;
- the Run concludes only after active sibling state is known.

This avoids treating best-effort Backend cancellation as a safe consequence of an unrelated Node failure. Explicit user cancellation remains the operation that requests cancellation of all active executions.

This policy was explicitly approved before implementation.

### 5.6 Concurrent cancellation

Run cancellation snapshots the complete active Attempt set and sends bounded concurrent Cancel requests. The Run may conclude `cancelled` only when every active execution is already terminal or its Backend confirms cancellation.

If any cancellation is unconfirmed, Fishyume persists per-Node diagnostics and enters a recoverable `waiting: cancel_failed` state. It must never mark the whole Run cancelled while an execution may still be alive.

Repeated cancel commands are idempotent and retry only unresolved executions.

## 6. Backend capability evolution

The existing capability model already declares tools, runtimes, structured output and waiting-input support. M2.2 extends it only with the minimum scheduling information required by the Engine, such as a per-Run execution limit and any necessary cancellation/reconciliation guarantees.

Capabilities describe behavior; they do not expose CC-Panes session quotas, process internals or plugin ABI. Dynamic availability remains a normal Start/Observe result and must not be inferred from a Backend name.

Both built-in Backends must pass the expanded conformance suite. A fake Backend remains the deterministic failure/concurrency test fixture, while CC-Panes and Direct provide executable and controlled live evidence.

## 7. Acceptance evidence

M2.2 is complete only when all of the following are demonstrated:

1. A Workflow with two independent Agent Nodes overlaps their execution when `maxConcurrency: 2`.
2. `maxConcurrency: 1` preserves M2.1 scheduling order and behavior.
3. The scheduler never exceeds the effective Workflow/Backend limit.
4. Engine restart reconciles multiple active Attempts without duplicate starts.
5. Completion order does not change the deterministic final state or corrupt event sequence ordering.
6. Approval on one branch coexists correctly with an active Agent on another branch.
7. Failure aggregation and downstream skipping follow the approved policy.
8. User cancellation targets every active execution and never reports false cancellation.
9. CC-Panes and Direct pass the same expanded Backend contract suite.
10. The same parallel Workflow completes through both real Backends in controlled smoke tests.
11. M2.1.1 and M2.1.2 persisted Runs remain compatible.
12. Scheduler, workflow, store and RPC packages contain no Backend-name branching.

## 8. Explicitly out of scope

- third-party Backend SDK, discovery, installation or runtime loading;
- mixing different Backends inside one Workflow;
- automatic retry, timeout, model fallback or budget policies;
- Shell, HTTP, container, database, timer or message-queue Nodes;
- dynamic Planner-created Nodes;
- distributed scheduling or cross-host resource coordination;
- GUI redesign.

## 9. Implementation preparation

The approved implementation sequence is maintained in `docs/fishyume-m2.2-implementation-plan.md` and is split into reviewable batches:

1. concurrency and failure semantics tests before state changes;
2. additive status/state compatibility model;
3. multi-active reconciliation without parallel launch;
4. bounded parallel launch and deterministic commits;
5. concurrent cancellation and retryable partial failure;
6. capability/resource validation;
7. CLI/TUI status updates;
8. Direct and CC-Panes executable tests and live smoke;
9. compatibility, race, Windows and Ubuntu release gates.

No plugin-runtime code is part of these batches.
