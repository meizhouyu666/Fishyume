# Fishyume CLI

Install the `fishyume` package to use the primary `fishyume` command or the compatible `wf` alias. Installation does not download executable code; matching Engine packages are optional exact-version dependencies.

Fishyume currently ships two Agent Backends:

- `ccpanes` is the default. It requires a dedicated non-interactive launch profile in `FISHYUME_CCPANES_PROFILE_ID` (`WF_CCPANES_PROFILE_ID` is a lower-precedence compatibility alias).
- `direct` runs an installed and authenticated Codex CLI locally, without CC-Panes. Use `fishyume doctor --backend direct`, optionally set `FISHYUME_CODEX_PATH`, and control its sandbox with `FISHYUME_DIRECT_SANDBOX`.

Backend selection order is `--backend`, Workflow `defaults.backend`, `FISHYUME_BACKEND`, then `ccpanes`. See the repository README for workflow examples, recovery guarantees, state compatibility, security, release artifacts, and live smoke instructions.
