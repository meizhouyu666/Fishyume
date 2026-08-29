# Fishyume Native DSH Plugin UI Plan

Status: proposed. This document is an implementation plan, not an
authorization to change the client yet.

The design is based on the DSH plugin, UI slot, and layout guidance currently
published in the DeepSeek Harness repository:

- [Plugin basics](https://github.com/deepseek-ai/deepseek-harness/tree/master/docs/user/develop/basic)
- [UI Slots](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/client/ui-slots/README.zh.md)
- [UI Layout](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/client/ui-layout/README.zh.md)
- [Architecture](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/architecture.md)

DSH does not currently provide a separate, comprehensive visual design manual.
The enforceable guidance is the shell/slot/layout contract, lifecycle, and
use of native client services. This plan treats those contracts as the design
baseline and leaves exact slot names to the installed DSH version probe.

## 1. Decision Summary

Fishyume will become a native DSH client plugin. The current DSH release does
not expose a public `shell.sidebar` or `shell.details` registration slot for
third-party plugins. The supported composition is therefore an official
`sidebar.footer.action` entry that opens a native React workspace in
`shell.overlay`. The panel is docked and shell-aware, but it is not an iframe,
an independent SPA, or a second application shell.

The standalone `fishyume-web` client remains available as a compatibility and
diagnostic surface. It will share the domain state and API adapter with the
native client, but it will keep its own document shell because it is not
running inside DSH.

The recommended product shape is:

```text
DSH shell
  sidebar.footer.action: Fishyume entry
  conversation: unchanged DSH conversation
  shell.overlay: Fishyume dock/workspace panel
    Team | Run | Routing
    overlay inside panel: dialogs, confirmations, transient errors only
```

This preserves DSH navigation and lets a user observe Fishyume beside the
conversation that caused the work.

## 2. Problem and Goals

The current client registers one `shell.overlay` slot and loads the complete
Fishyume SPA in an iframe. This creates a second application shell inside DSH:
it owns its own top bar, navigation, sizing, connection state, and routing.
That is why it behaves like a floating window rather than a DSH plugin.

Goals:

- make Fishyume discoverable from the native DSH sidebar footer;
- keep the DSH conversation visible while inspecting Team, Handoff, or Run;
- use the standard DSH layout and client connection/runtime facilities;
- preserve the Host -> Team -> Handoff -> Run focus flow;
- keep Team, Run, and Routing state in one small entry store;
- preserve the optional standalone Web client and current host gateway;
- provide explicit loading, empty, disconnected, unavailable, and conflict
  states;
- make the native integration testable without starting an iframe or browser
  sidecar.

Non-goals:

- changing the Team, Handoff, Workflow, or Run public contracts;
- making the Web client an execution engine or a second Host Agent;
- adding provider credentials, billing, remote execution, IDE features, or
  automatic Team-to-Workflow promotion;
- redesigning the DSH shell itself;
- replacing the existing gateway security checks.

## 3. Information Architecture

### Sidebar entry

Register one stable Fishyume navigation item in the DSH sidebar footer using
the official `sidebar.footer.action` list slot. Selecting it opens the
Fishyume dock/workspace panel and sets the entry store's `view` to `teams` by
default. The item shows a small activity indicator only when there is active
Team or Run work; it must not create a second top-level window.

The item must have a stable registration id, for example
`dsh-fishyume-sidebar`, and a predictable order relative to other tools. Keep
the slot name and entry shape in a small compatibility adapter. A main-sidebar
navigation row is explicitly out of scope until DSH publishes a supported
slot; do not use DOM surgery as a replacement.

### Fishyume workspace

The dock/workspace view has three primary modes:

| Mode | Collection | Detail |
| --- | --- | --- |
| Team | Team list with All/Active/Closed filter | Overview, Discussion, Handoffs |
| Run | Run list with status filter | Summary, workflow graph, events, actions |
| Routing | Drivers and effective routes | Availability, route diagnostics, configuration |

The mode switch is a compact tab or segmented control inside the Fishyume
workspace, not a replacement for DSH global navigation.

### Team, Handoff, and Run relationship

- A Team is the parent exploration object.
- A Handoff is a selected Team subview. It is never a new global page and is
  always shown with its source Team and immutable evidence.
- A Run is a separate primary mode. When a Run references a Handoff, the
  detail header links back to that Team/Handoff without duplicating state.
- Actions that create or mutate durable state remain explicit and respect the
  authoritative state version returned by the Engine.

### Routing

Routing is an operator/configuration view, not a second execution surface. It
reports discovered drivers, effective routes, disabled/unavailable reasons,
and catalog diagnostics. Team start uses the same route data through the entry
store; it must not maintain a second copy of routing policy.

## 4. DSH Slot and Layout Composition

The native plugin will register small components through `ctx.slots.register`
and compose them with the installed shell layout behavior:

1. `sidebar.footer.action`: Fishyume entry and activity badge.
2. `shell.overlay`: one native React dock/workspace panel containing the
   collection pane and detail pane.
3. Panel-local dialogs/confirmations and short-lived error/toast presentation.

The plugin must not register an iframe or a complete independent document in
`shell.overlay`. The overlay entry is a native component whose geometry,
stacking context, and lifecycle are owned by the DSH frame. On wide screens it
should dock beside the conversation and let the conversation yield space when
the supported shell marker is available; on narrow screens it becomes an
inset overlay with an explicit close/collapse control.

The workspace should fill the dimensions supplied by DSH and use the shell's
spacing, typography, colors, focus rings, and scroll containers. It should
not define a second viewport, global `body` styling, or an independent
application header.

## 5. Layer Boundaries

### Host plane

Keep `src/plugin.ts` responsible for:

- lazy EngineBridge creation;
- gateway, token, focus, and static routes needed by the standalone client;
- DSH service lifecycle and route registration;
- existing host/origin/token security behavior.

The native client now mounts a small Fishyume Typert Remote and should not
construct an iframe URL. The same-origin host token and RPC route remain as a
compatibility adapter for the standalone client and older DSH profiles; the
token is held in memory and never placed in a document URL. The existing
static routes remain for the standalone compatibility client.

### Client transport

Add a narrow client API adapter that calls the DSH client connection/remote
facility when running natively. The adapter exposes typed methods such as
`listTeams`, `getTeam`, `listMessages`, `listHandoffs`, `listRuns`,
`getRun`, `getRouting`, and the supported mutations. It must translate DSH
connection errors into the same typed error shape used by the standalone
gateway adapter.

The standalone adapter continues to call `/api/rpc` through the existing
authenticated gateway. Both adapters implement the same domain-facing
interface; views do not know which transport is active.

### Entry store

Create a small store owned by the native plugin entry. It contains:

- `view`: `teams | runs | routing`;
- filter and selected Team/Run/Handoff ids;
- detail tab and graph scale;
- loading/refreshing/busy flags;
- connection and Engine availability state;
- the latest focus revision and pending focus target.

The store owns selection and refresh orchestration. Components remain mostly
presentational and dispatch typed commands. Do not introduce a global store
or persistence for UI state unless DSH already requires it.

### Domain/view split

Extract reusable domain types, API calls, selectors, and render-independent
helpers from `src/client/main.ts`. Build native views from those modules. Keep
the existing standalone HTML shell as a thin adapter over the same modules.
`team-view.ts` and `workflow-view.ts` should remain reusable render helpers
where their current contracts are useful; DOM-specific code should move out
of the domain layer.

## 6. Navigation and Focus

The native entry store is the source of truth for navigation. A focus target
from Host `web.open` is applied as follows:

| Target | Mode | Selection | Tab |
| --- | --- | --- | --- |
| Team | teams | `selectedTeam` | discussion/overview default |
| Handoff | teams | Team + Handoff | handoffs |
| Run | runs | `selectedRun` | run |

Focus updates must be monotonic and idempotent. Reapplying the same revision
does nothing. A focus request for a missing object leaves the user in the
requested mode and shows a bounded not-found state rather than silently
selecting another item.

Use the DSH layout/navigation state when it is available. The current release
has no supported plugin page route, so keep route state in the entry store and
expose only stable internal route keys (`fishyume/teams`, `fishyume/runs`,
`fishyume/routing`) to the adapter. Do not encode bearer tokens or durable
identifiers in a document URL.

## 7. Migration Plan

### Phase 0: Runtime contract probe

- Confirm the installed names and signatures for `sidebar.footer.action`,
  `shell.overlay`, and client connection/remote services. Record the absence
  of public sidebar-main/details slots as a compatibility constraint.
- Add a small typed compatibility module for those APIs and a Typert manifest
  for the Fishyume Remote.
- Add registration tests that assert slot ids, order, and cleanup behavior.

### Phase 1: Extract shared client core

- Move RPC method definitions, response normalization, focus handling, and
  entry state out of the monolithic DOM `main.ts`.
- Add native and standalone transport implementations.
- Keep current gateway and all existing API/security tests unchanged.

### Phase 2: Implement native workspace

- Add Sidebar footer entry and native dock/workspace root.
- Implement Team collection/detail first, then Run, then Routing.
- Move Team start and destructive confirmations to DSH overlay dialogs.
- Use DSH-provided layout dimensions and visual primitives.

### Phase 3: Integrate continuity

- Connect Host focus revisions to the native entry store.
- Verify Team -> Handoff -> Run navigation and back-links.
- Ensure native mode never loads the static `/plugins/dsh-fishyume/` document
  or creates an iframe. Until a real DSH profile mounts the Remote, token/RPC
  requests remain same-origin and memory-only.

### Phase 4: Preserve and simplify standalone Web

- Rewire the standalone page to the shared core.
- Keep the sidecar and authenticated gateway as a supported diagnostic path.
- Update package and README wording from “embedded console” to “native DSH
  workspace plus optional standalone Web client”.
- Remove iframe-specific client code only after native and standalone
  acceptance passes; do not remove host routes prematurely.

## 8. Edge States and Responsive Behavior

Every primary view must define these states:

- initial loading with stable skeleton dimensions;
- empty collection with one relevant next action;
- disconnected DSH/Engine with retry action and last successful snapshot;
- Engine unavailable or missing executable with diagnostic detail;
- mutation conflict (state version mismatch) with refresh-and-review action;
- not found or stale focus target;
- partial Routing data, where each failed method is shown independently;
- busy mutation state that disables only the affected action.

The details workspace must remain usable in narrow DSH windows. At the
smallest supported width, the collection pane can collapse behind a standard
DSH panel control, but it must not become a viewport-fixed floating window.
Long identifiers, route labels, and error messages must wrap or truncate
with accessible titles. Keyboard focus, Escape-to-close for dialogs, and
visible focus rings are required.

## 9. Verification and Acceptance

### Automated

- Typecheck and unit-test the shared domain store and both transport adapters.
- Test native slot registration, cleanup, and no-iframe behavior.
- Test focus revision mapping for Team, Handoff, Run, duplicate revisions,
  and missing targets.
- Preserve all gateway, security, Team, Run, and routing tests.
- Build package and run package-audit and standalone install smoke tests.

### DSH web acceptance

- Plugin loads in a web profile without opening a floating overlay.
- Sidebar footer entry is visible and opens the Fishyume native dock/workspace
  panel in the standard shell overlay region.
- DSH conversation remains visible and functional.
- Team, Handoff, Run, and Routing modes render in the same shell.
- Host focus opens the correct native selection without a reload.
- No native code loads the static `/plugins/dsh-fishyume/` document or creates
  an iframe; temporary token/RPC transport remains same-origin and memory-only.
- Missing Engine, disconnected transport, empty data, and conflicts produce
  understandable in-shell states.

### Standalone acceptance

- `fishyume-web --no-open` still serves the standalone client.
- Authenticated RPC, focus, static routes, and security headers remain
  correct.
- The standalone page still supports Team, Handoff, Run, and Routing flows.

Visual verification should cover a normal desktop DSH window and a narrow
window. The key criterion is that Fishyume reads as one DSH workspace, with
no independent app chrome or floating-console behavior.

## 10. Key Trade-offs and Risks

Using native slots makes the plugin dependent on DSH client API evolution.
The compatibility module limits that risk. The current public surface is
`sidebar.footer.action` plus `shell.overlay`; a future public details slot can
replace the dock without changing the domain store or views.

Keeping the standalone client costs some shared-adapter work, but it protects
the existing diagnostic and headless-adjacent workflow while native UI
stabilizes. Removing it now would make transport and UI migration harder to
debug.

Using a docked native overlay instead of the conversation region preserves
DSH's primary conversation workflow. It gives Fishyume less horizontal space
than a full-screen app, so graph and event views must be information-dense and
use collapsible sections rather than expanding the shell or introducing
another window.

The host gateway remains because it is the correct boundary for the optional
standalone client. Native DSH calls should use the client runtime so bearer
tokens do not leak into UI URLs or browser fragments.

## 11. Current Implementation Note

The Phase 0 runtime probe is complete for the installed DSH `0.1.1-rc.2`
profile: `sidebar.footer.action`, `shell.overlay`, Typert registry/gateway,
client `remote`, and the shared `webServer` are present. The Fishyume host now
registers a small Typert manifest and the client mounts its Remote when that
service is available. The remaining work is real profile reload and browser
trial, not another speculative transport design. No host route is removed
while the standalone Web client still uses it.
