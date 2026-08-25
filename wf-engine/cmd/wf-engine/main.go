package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"wf.local/wf-engine/internal/application"
	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/controlplane"
	"wf.local/wf-engine/internal/driver/codex"
	"wf.local/wf-engine/internal/driver/scheduleradapter"
	"wf.local/wf-engine/internal/rpc"
	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/team"
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
	codexDriver := codex.New(codex.Config{StateRoot: state.Root()})
	if err := registry.Register(scheduleradapter.New(codexDriver)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	service := run.NewServiceWithRegistry(registry, "codex", state)
	applicationService := application.NewService(service, "codex", state)
	teamService := team.NewService(state)
	if err := teamService.SetRunLookup(func(runID string) (string, error) {
		snapshot, err := service.Get(runID)
		return snapshot.Project, err
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := teamService.SetDriver(codexDriver.Exploration()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) == 2 && os.Args[1] == "serve" {
		if err := serveControlPlane(state, service, applicationService, teamService); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: wf-engine [serve]")
		os.Exit(2)
	}
	if err := teamService.Recover(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	server := rpc.NewServerWithTeam(os.Stdin, os.Stdout, service, applicationService, teamService)
	if err := server.Serve(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serveControlPlane(state *store.Store, service *run.Service, applicationService *application.Service, teamService *team.Service) error {
	owner, err := controlplane.AcquireOwner(state.Root(), rpc.EngineVersion, rpc.ProtocolVersion)
	if err != nil {
		return err
	}
	defer owner.Close()
	server, err := controlplane.NewServerWithTeam(owner, service, applicationService, teamService)
	if err != nil {
		return err
	}
	defer server.Close()
	if appErr := applicationService.Recover(context.Background()); appErr != nil {
		return fmt.Errorf("recover application journal: %w", appErr)
	}
	if err := service.Recover(context.Background()); err != nil {
		return fmt.Errorf("recover durable Runs: %w", err)
	}
	if err := teamService.Recover(context.Background()); err != nil {
		return fmt.Errorf("recover durable Teams: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Serve(ctx)
}
