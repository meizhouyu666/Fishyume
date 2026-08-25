# Fishyume Web

Optional local browser client for Fishyume Team exploration and linked Workflow
Runs. It starts an authenticated loopback sidecar and connects to the existing
Fishyume Control Plane through Named Pipe or Unix Socket IPC.

```powershell
fishyume-web
```

The command prints and opens a launch URL containing an ephemeral bearer token
in its fragment. Keep the foreground command running while using the client.
No Engine TCP listener is enabled and no token is stored on disk.
