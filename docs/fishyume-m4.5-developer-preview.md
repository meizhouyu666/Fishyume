# Fishyume M4.5 Developer Preview

> Status: accepted on 2026-08-14. M4 Core remains frozen; M5 production work may now begin from this product-experience baseline.

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

Until npm publication is explicitly authorized, Windows users install the preview from the repository root:

```powershell
.\install-fishyume.ps1
# When this network requires it:
.\install-fishyume.ps1 -Proxy "http://127.0.0.1:7897"
```

The installer verifies Node.js 24+, Go 1.26+, builds the current CLI and Engine into temporary package staging, installs both exact local tarballs globally, verifies the installed command surface, and removes staging. During an upgrade it stops a verified Control Plane only when the Dashboard reports zero active Runs; otherwise it refuses the upgrade. This prevents Windows from retaining a locked stale Engine package. `-Prefix <path>` provides a non-global acceptance mode used by CI.

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
- `fishyume setup codex --print` is mutation-free and prints the low-level transport command; only the full setup command applies the Fishyume tool approval policy.
- Setup is idempotent when the expected stdio entry already exists.
- A conflicting entry is preserved unless the user explicitly supplies `--force`.
- Fishyume never reads or prints Codex credentials, tokens, or complete environment variables.
- The opt-in `smoke:codex-installed-mcp` gate uses the user's installed Codex/Fishyume configuration, calls only `system.capabilities`, rejects extra tools or interactive approval, and retains only a redacted summary.

### Doctor

`fishyume doctor` checks the Engine version, Application protocol, Driver readiness, optional project readiness, Codex CLI/version, `codex login status`, and the Fishyume MCP entry. Every failed product check includes one executable recovery command.

## Acceptance gates

- TypeScript typecheck, tests, build, package-content audits, and `git diff --check` pass.
- A real `npm pack` CLI tarball and current platform Engine tarball install into a fresh temporary prefix.
- The installed package—not repository source—passes top-level help, Dashboard help, Codex setup print, Engine/protocol Doctor checks, and MCP recovery diagnostics.
- Temporary install directories and Control Plane processes are removed after the gate.
- Windows and Ubuntu platform-install CI exercise the same installed command surface.
- Public CI stays Provider-independent and never needs Codex credentials or Docker.

## Live acceptance record (2026-08-14)

- The repository-root Windows installer completed a real global install from current source-built CLI and Engine packages. A repeat upgrade detected and stopped a verified idle Control Plane before replacing the Engine, with no npm staging residue.
- `fishyume setup codex` registered the canonical absolute Node + installed `dist/cli.js` stdio transport and applied `required=true`, default approval, and explicit approval for all nine Fishyume tools. Other MCP and Provider sections were preserved.
- Global `fishyume doctor --project <repository>` passed Engine, protocol, Driver, project, Codex CLI, login, canonical MCP transport, and approval-policy checks with `codex-cli 0.147.0`.
- A real Codex process using the installed user configuration called only `system.capabilities`; the completed evidence reported `interactiveApproval=false` and `sandbox=read-only`.
- A separate real Codex Host created Run `run-6a8bf4c7ca480b68e0f7f47f` through MCP. A CC-Panes PTY displayed the zero-argument Dashboard, selected that waiting Run, attached with `Enter`, approved with `a`, and converged to `succeeded`. The Host's retained stale action returned exact `conflict` (`stateVersion 3 → 5`). The temporary Control Plane, Codex home, runner, and profile were removed. CC-Panes supplied only the PTY.

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
