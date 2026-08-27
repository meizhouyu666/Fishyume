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
	"wf.local/wf-engine/internal/driver/harnesssession"
	"wf.local/wf-engine/internal/driver/scheduleradapter"
	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/routingconfig"
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
	routingService, err := routingconfig.NewService(state.Root(), codexDriver)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := registry.Register(scheduleradapter.New(codexDriver)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	service := run.NewServiceWithRegistryAndCatalogs(registry, "codex", routingService, state)
	applicationService := application.NewServiceWithCatalogs(service, "codex", routingService, state)
	teamCatalog, err := routingService.TeamCatalog()
	if err != nil {
		// Keep diagnostics and setup available even when no Agent CLI is installed.
		teamCatalog = routing.BuiltinTeamCatalogV1()
	}
	teamService, err := team.NewServiceWithCatalog(state, teamCatalog)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	historicalTeamCatalogs, err := routingService.TeamCatalogHistory()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, historical := range historicalTeamCatalogs {
		if err := teamService.AddHistoricalCatalog(historical); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if err := teamService.SetRunLookup(func(runID string) (string, error) {
		snapshot, err := service.Get(runID)
		return snapshot.Project, err
	}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	runtime := &teamRouteRuntime{stateRoot: state.Root(), team: teamService, codex: codexDriver}
	if err := routingService.SetTeamRouteApplier(runtime); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(os.Args) == 2 && os.Args[1] == "serve" {
		if err := serveControlPlane(state, service, applicationService, teamService, routingService); err != nil {
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
	server := rpc.NewServerWithTeamAndConfig(os.Stdin, os.Stdout, service, applicationService, teamService, routingService)
	if err := server.Serve(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func catalogHasDriver(catalog routing.CapabilityCatalogV1, name string) bool {
	for _, model := range catalog.Models {
		if model.Target.Driver == name {
			return true
		}
	}
	return false
}

type teamRouteRuntime struct {
	stateRoot string
	team      *team.Service
	codex     *codex.Driver
}

func (r *teamRouteRuntime) ApplyTeamRoutes(catalog routing.CapabilityCatalogV1, statuses []routingconfig.TeamDriverStatus) error {
	if err := r.team.SetCatalog(catalog); err != nil {
		return err
	}
	available := make(map[string]bool, len(statuses))
	for _, status := range statuses {
		available[status.Driver] = status.Available
	}
	if available["codex"] && catalogHasDriver(catalog, "codex") {
		if err := r.team.ReplaceDriver(r.codex.Exploration()); err != nil {
			return err
		}
		if err := r.team.ReplaceSessionDriver(r.codex.Session()); err != nil {
			return err
		}
	} else if !available["codex"] {
		r.team.RemoveDriver("codex")
		r.team.RemoveSessionDriver("codex")
	}
	if err := r.applyHarness("claude", available["claude"], catalog); err != nil {
		return err
	}
	return r.applyHarness("opencode", available["opencode"], catalog)
}

func (r *teamRouteRuntime) applyHarness(name string, available bool, catalog routing.CapabilityCatalogV1) error {
	if !available {
		r.team.RemoveDriver(name)
		r.team.RemoveSessionDriver(name)
		return nil
	}
	if !catalogHasDriver(catalog, name) {
		return nil
	}
	config := harnesssession.Config{StateRoot: r.stateRoot, Catalog: catalog}
	var driver *harnesssession.Driver
	var err error
	if name == "claude" {
		driver, err = harnesssession.NewClaude(config)
	} else {
		driver, err = harnesssession.NewOpenCode(config)
	}
	if err != nil {
		r.team.RemoveDriver(name)
		r.team.RemoveSessionDriver(name)
		return nil
	}
	if err := r.team.ReplaceSessionDriver(driver); err != nil {
		return err
	}
	return r.team.ReplaceDriver(driver.Exploration())
}

func serveControlPlane(state *store.Store, service *run.Service, applicationService *application.Service, teamService *team.Service, routingService *routingconfig.Service) error {
	owner, err := controlplane.AcquireOwner(state.Root(), rpc.EngineVersion, rpc.ProtocolVersion)
	if err != nil {
		return err
	}
	defer owner.Close()
	server, err := controlplane.NewServerWithTeamAndConfig(owner, service, applicationService, teamService, routingService)
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
