# Fishyume M6.7: Routing Preflight

M6.7 makes the M6.3 deterministic routing decision inspectable before a Run is
created. It extends the existing `workflow.validate` and `workflow.explain`
Application responses with a bounded `routingPreviews` array. Each Agent Node
has one preview containing:

- the resolved Driver and target;
- the effective `fishyume.routing-requirement/v1`;
- the selected target, catalog hash, reason codes, budget, and fallback policy;
- or a stable routing issue when the trusted catalog cannot satisfy the request.

The preview uses the Engine-owned immutable catalog and the same resolver inputs
as a new Attempt. It does not contact a Provider, inspect credentials, start a
Driver, write a journal, or persist an Attempt. `run.start` remains the only
execution and persistence boundary.

Hosts should send the same `workflow`, `inputs`, `driver`, `target`, and
`contextBindings` to `workflow.validate`, `workflow.explain`, and `run.start`.
The MCP tool and machine CLI paths are projections of the same Application
contract, so a preview observed through either interface is identical.

## Acceptance

- Every Agent Node has exactly one bounded preview; Approval Nodes do not.
- A satisfiable requirement returns a validated `RoutingDecisionV1` with the
  trusted catalog hash.
- An unsatisfiable requirement returns a routing issue without making the
  static Workflow invalid or mutating state.
- Historical Runs and Attempts are unchanged.
- Public tests remain Provider-independent.
