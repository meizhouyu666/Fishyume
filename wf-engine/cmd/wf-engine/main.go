package main

import (
	"context"
	"fmt"
	"os"

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
	backend, backendErr := ccpanes.New()
	if backendErr != nil {
		backend = ccpanes.NewUnavailable(backendErr)
	}
	server := rpc.NewServer(os.Stdin, os.Stdout, run.NewService(backend, state))
	if err := server.Serve(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
