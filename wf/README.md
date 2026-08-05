# Fishyume CLI

Install the `fishyume` package to use the primary `fishyume` command or the compatible `wf` alias. Installation does not download executable code; matching Engine packages are optional exact-version dependencies.

Fishyume currently ships two Agent Backends:

- `ccpanes` is the default. It requires a dedicated non-interactive launch profile in `FISHYUME_CCPANES_PROFILE_ID` (`WF_CCPANES_PROFILE_ID` is a lower-precedence compatibility alias).
- `direct` runs an installed and authenticated Codex CLI locally, without CC-Panes. Use `fishyume doctor --backend direct`, optionally set `FISHYUME_CODEX_PATH`, and control its sandbox with `FISHYUME_DIRECT_SANDBOX`.

Backend selection order is `--backend`, Workflow `defaults.backend`, `FISHYUME_BACKEND`, then `ccpanes`. See the repository README for workflow examples, recovery guarantees, state compatibility, security, release artifacts, and live smoke instructions.

Fishyume M2.2 supports bounded parallel Agent execution through `execution.maxConcurrency`. `fishyume status` reports the effective capacity, every active Attempt, waiting Approval, and per-node cancellation or recovery diagnostic. Direct and CC-Panes use the same scheduling and cancellation semantics.

On an interactive terminal, `fishyume run` uses the Calm Operator Console: a complete Run Header, compact Workflow rows, one selected-node Focus Detail, a low-noise status strip, and a context-sensitive footer. Active Attempt, Approval, result, and diagnostic data are merged into their Workflow node instead of repeated in separate panels. Reattach with `fishyume status <run-id> --watch`; use `j`/`k` or arrows to traverse every Workflow node and `Enter` to fold or expand detail. Action keys only appear for the selected Engine-actionable node: `a` approves, `r` rejects with a reason, `R` confirms retry, and `c` confirms cancellation. Indeterminate retries require explicit duplicate-risk acknowledgement and remain pinned by `nodeId/kind/duplicateRisk`. `d`, `q`, and `Ctrl+C` detach a started run or stop watch-only observation without implicitly cancelling it.

The console targets 80/120/160 columns, handles CJK and long paths by display width, and degrades from TrueColor to ANSI256/ANSI16 or monochrome. `TERM=dumb` and `FISHYUME_ASCII=1` select the ASCII fallback. Non-TTY/CI output remains the stable line-oriented reporter. Non-interactive `--watch` is rejected with an actionable diagnostic, `--watch --json` is invalid, and plain `fishyume status --json` remains a single machine-readable object. Run `npm run gallery` to regenerate the six canonical visual fixtures in `docs/fishyume-m3.3-canonical-gallery.txt`.
