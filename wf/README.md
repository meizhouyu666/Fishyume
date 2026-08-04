# Fishyume CLI

Install the `fishyume` package to use the primary `fishyume` command or the compatible `wf` alias. The package does not download executable code during installation; matching Engine packages are optional exact-version dependencies.

Fishyume-managed Agent sessions require a dedicated non-interactive CC-Panes launch profile created by an administrator. Set `FISHYUME_CCPANES_PROFILE_ID` to its exact profile ID (`WF_CCPANES_PROFILE_ID` remains a lower-precedence compatibility alias). Fishyume passes the ID to `launch_task` and never creates or globally binds an unrestricted profile.

See the repository README for workflows, state compatibility, security, release artifacts, and manual smoke instructions.
