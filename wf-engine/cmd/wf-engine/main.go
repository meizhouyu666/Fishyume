package main

import (
	"context"
	"fmt"
	"os"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/backend/driveradapter"
	"wf.local/wf-engine/internal/driver/codex"
	"wf.local/wf-engine/internal/rpc"
	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/store"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "direct-supervisor" {
		os.Exit(codex.RunSupervisor(os.Args[2]))
	}
	state, err := store.NewDefault()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	registry := backend.NewRegistry()
	if err := registry.Register(driveradapter.New(codex.New(codex.Config{StateRoot: state.Root()}))); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	server := rpc.NewServer(os.Stdin, os.Stdout, run.NewServiceWithRegistry(registry, "codex", state))
	if err := server.Serve(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
