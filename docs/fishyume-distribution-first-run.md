# Fishyume Distribution and First Run

This batch closes the minimum installed-product surface without freezing M6
Application, MCP, Driver, Memory, or Context evolution.

The intended human entry points are:

```text
fishyume setup    one-time Codex Host setup and readiness check
fishyume          daily Dashboard and Run attachment
fishyume demo     offline topology-console preview
fishyume doctor   actionable diagnostics
```

`fishyume setup codex` remains compatible, while the shorter command is the
documented product path. `fishyume demo` is deterministic and Provider-free: it
does not start the Control Plane, mutate state, use credentials, or call a model.

The packed-package smoke builds the current platform Engine, packs both npm
packages, installs them into an empty prefix, and verifies help, setup command
identity, the offline topology demo, zero-argument Dashboard, and Doctor. Public
Windows and Ubuntu platform-install jobs repeat the installed-package checks.

This is distribution readiness, not a public npm publication or stable API
freeze. Version publication and a GitHub Release remain explicit later actions.
