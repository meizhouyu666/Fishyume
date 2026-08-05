package main

import (
	"context"
	"fmt"
	"os"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/backend/ccpanes"
	"wf.local/wf-engine/internal/rpc"
	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/store"
)

func main() {
	state, err := store.NewDefault()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ccpanesBackend, backendErr := ccpanes.NewAdapter()
	if backendErr != nil {
		ccpanesBackend = ccpanes.NewUnavailableAdapter(backendErr)
	}
	registry := backend.NewRegistry()
	if err := registry.Register(ccpanesBackend); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	server := rpc.NewServer(os.Stdin, os.Stdout, run.NewServiceWithRegistry(registry, "ccpanes", state))
	if err := server.Serve(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
