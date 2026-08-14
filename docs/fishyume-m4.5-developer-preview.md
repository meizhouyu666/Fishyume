# Fishyume M4.5 Developer Preview

> Status: implementation and acceptance in progress. M4 Core remains frozen; M5 production work waits for this product-experience gate.

M4.5 turns the accepted M4 control plane into a coherent first-use product. The first complete product combination is Windows + Codex + the local `codex` Driver. Ubuntu remains a supported install and CI platform, but Windows terminal ergonomics are the reference experience for this preview.

## Golden path

```text
Install → fishyume setup codex → fishyume doctor
→ Host Agent authors and starts a Run through MCP
→ user runs fishyume with no arguments
→ Dashboard selects and attaches to the durable Run
→ user observes, approves, answers, retries, cancels, or detaches
```

The user should not need to hand-author a Workflow, copy a Run ID, choose a profile ID, or approve a per-call MCP permission prompt. A Host Agent first calls `system.capabilities`, then uses `workflow.validate` and `workflow.explain` before `run.start` when it authors a Workflow.

## Product surface

### Zero-argument Dashboard

- `fishyume` and `fishyume dashboard` open the same Run Dashboard.
- Active and waiting Runs appear before terminal history.
- Arrow keys or `j`/`k` select; `Enter` attaches; `r` refreshes; `q` or `Escape` exits.
- The list refreshes every two seconds and remains bounded at 80, 120, and 160 columns.
- Non-TTY use emits one finite summary with copyable `attach` and `doctor` commands.
- Empty state points to the Host/CLI next step instead of asking for a Run ID.

### Codex setup

- `fishyume setup codex` uses the official `codex mcp add` CLI contract.
- `fishyume setup codex --print` is mutation-free and prints one copyable command.
- Setup is idempotent when the expected stdio entry already exists.
- A conflicting entry is preserved unless the user explicitly supplies `--force`.
- Fishyume never reads or prints Codex credentials, tokens, or complete environment variables.

### Doctor

`fishyume doctor` checks the Engine version, Application protocol, Driver readiness, optional project readiness, Codex CLI/version, `codex login status`, and the Fishyume MCP entry. Every failed product check includes one executable recovery command.

## Acceptance gates

- TypeScript typecheck, tests, build, package-content audits, and `git diff --check` pass.
- A real `npm pack` CLI tarball and current platform Engine tarball install into a fresh temporary prefix.
- The installed package—not repository source—passes top-level help, Dashboard help, Codex setup print, Engine/protocol Doctor checks, and MCP recovery diagnostics.
- Temporary install directories and Control Plane processes are removed after the gate.
- Windows and Ubuntu platform-install CI exercise the same installed command surface.
- Public CI stays Provider-independent and never needs Codex credentials or Docker.

Run the local package gate from the repository root:

```powershell
$env:HTTP_PROXY = "http://127.0.0.1:7897"   # only when this network requires it
$env:HTTPS_PROXY = "http://127.0.0.1:7897"
npm --prefix wf run smoke:install
```

## Deferred beyond M4.5

- Context Memory and prompt-component policy remain M5.
- Complexity classification and model routing remain M6.
- Third-party Driver discovery and plugin SDK remain M7.
- A conversational Fishyume harness, Web/Desktop UI, generic Shell/HTTP/container nodes, and CC-Panes integration are not introduced here.

