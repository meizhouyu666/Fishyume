package team

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wf.local/wf-engine/internal/explorationdriver"
	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/teamcontract"
)

type immediateDriver struct {
	mu          sync.Mutex
	starts      []explorationdriver.StartRequest
	cancels     int
	output      string
	activeFirst bool
}

func (d *immediateDriver) Name() string { return "codex" }
func (d *immediateDriver) Capabilities() explorationdriver.DriverCapabilities {
	return explorationdriver.DriverCapabilities{Targets: []string{"local"}, SupportsOutput: true, SupportsRecovery: true, SupportsConfirmedCancel: true, SupportsConcurrentCancel: true, MaxConcurrentTurns: 2}
}
func (d *immediateDriver) Doctor(context.Context, explorationdriver.DoctorRequest) explorationdriver.DoctorReport {
	return explorationdriver.DoctorReport{Driver: d.Name(), Ready: true}
}
func (d *immediateDriver) Start(_ context.Context, request explorationdriver.StartRequest) (*explorationdriver.ExecutionHandle, error) {
	d.mu.Lock()
	d.starts = append(d.starts, request)
	d.mu.Unlock()
	return &explorationdriver.ExecutionHandle{Driver: d.Name(), Target: request.Target, SchemaVersion: 1, ID: request.Identity.TurnID, Data: json.RawMessage(`{"turnId":"` + request.Identity.TurnID + `"}`)}, nil
}

func (d *immediateDriver) Observe(context.Context, explorationdriver.ExecutionHandle) (*explorationdriver.Observation, error) {
	d.mu.Lock()
	if d.activeFirst {
		d.mu.Unlock()
		return &explorationdriver.Observation{State: explorationdriver.ObservationActive}, nil
	}
	d.mu.Unlock()
	return &explorationdriver.Observation{State: explorationdriver.ObservationTerminal}, nil
}
func (d *immediateDriver) Output(context.Context, explorationdriver.ExecutionHandle, int) (string, error) {
	return d.output, nil
}
func (d *immediateDriver) Cancel(context.Context, explorationdriver.ExecutionHandle) (*explorationdriver.CancelResult, error) {
	d.mu.Lock()
	d.cancels++
	d.mu.Unlock()
	return &explorationdriver.CancelResult{State: explorationdriver.CancelConfirmed}, nil
}

func startRequest(project, requestID string) teamcontract.TeamStartRequestV1 {
	return teamcontract.TeamStartRequestV1{SchemaVersion: teamcontract.SchemaVersion, ClientRequestID: requestID, Project: project, Topic: "Compare two approaches"}
}

func TestServiceUsesInjectedAgentRouteCatalog(t *testing.T) {
	catalog := routing.CapabilityCatalogV1{
		SchemaVersion: routing.CapabilityCatalogV1Version,
		PolicyVersion: routing.RoutingPolicyV1Version,
		Models: []routing.ModelCapabilityV1{
			{ID: "claude/default/sonnet", Target: routing.Target{Driver: "claude", Provider: "default", Model: "sonnet"}, Capabilities: []routing.Capability{routing.CapabilityRepoRead}, ContextLimitBytes: 256 * 1024, MaxOutputBytes: 64 * 1024, Quality: routing.QualityPremium, Cost: routing.CostHigh, Latency: routing.LatencyBalanced, SupportsCancellation: true},
			{ID: "opencode/deepseek/deepseek-chat", Target: routing.Target{Driver: "opencode", Provider: "deepseek", Model: "deepseek-chat"}, Capabilities: []routing.Capability{routing.CapabilityRepoRead}, ContextLimitBytes: 128 * 1024, MaxOutputBytes: 32 * 1024, Quality: routing.QualityBalanced, Cost: routing.CostLow, Latency: routing.LatencyFast, SupportsCancellation: true},
		},
	}
	service, err := NewServiceWithCatalog(store.New(t.TempDir()), catalog)
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := service.Capabilities()
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilities.ParticipantTemplates) != 2 || capabilities.ParticipantTemplates[0].ModelID != "claude/default/sonnet" || capabilities.ParticipantTemplates[1].ModelID != "opencode/deepseek/deepseek-chat" {
		t.Fatalf("capabilities did not use injected catalog: %+v", capabilities.ParticipantTemplates)
	}
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(context.Background(), startRequest(project, "configured-routes"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Team.CostUsed != 101 || result.Team.Participants[0].Driver != "claude" || result.Team.Participants[1].Driver != "opencode" {
		t.Fatalf("Team did not bind injected routes: %+v", result.Team)
	}
	wantHash, _ := routing.CatalogHash(routing.CanonicalCatalogV1(catalog))
	if result.Team.CatalogHash != wantHash {
		t.Fatalf("catalog hash = %s, want %s", result.Team.CatalogHash, wantHash)
	}
}

func TestCapabilitiesExposeEveryConfiguredAgentRoute(t *testing.T) {
	models := make([]routing.ModelCapabilityV1, 0, 3)
	for _, route := range []struct{ id, driver, provider, model string }{
		{"claude/default/sonnet", "claude", "default", "sonnet"},
		{"codex/local/gpt-5.6-sol", "codex", "local", "gpt-5.6-sol"},
		{"opencode/deepseek/deepseek-chat", "opencode", "deepseek", "deepseek/deepseek-chat"},
	} {
		models = append(models, routing.ModelCapabilityV1{ID: route.id, Target: routing.Target{Driver: route.driver, Provider: route.provider, Model: route.model}, Capabilities: []routing.Capability{routing.CapabilityRepoRead}, ContextLimitBytes: 128 * 1024, MaxOutputBytes: 32 * 1024, Quality: routing.QualityBalanced, Cost: routing.CostLow, Latency: routing.LatencyFast, SupportsCancellation: true})
	}
	service, err := NewServiceWithCatalog(store.New(t.TempDir()), routing.CapabilityCatalogV1{SchemaVersion: routing.CapabilityCatalogV1Version, PolicyVersion: routing.RoutingPolicyV1Version, Models: models})
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := service.Capabilities()
	if err != nil {
		t.Fatal(err)
	}
	if len(capabilities.ParticipantTemplates) != 3 || capabilities.ParticipantTemplates[2].ModelID != "opencode/deepseek/deepseek-chat" {
		t.Fatalf("configured routes are not discoverable: %+v", capabilities.ParticipantTemplates)
	}
}

func TestServiceRejectsCatalogThatCannotFormATeam(t *testing.T) {
	catalog := routing.CapabilityCatalogV1{SchemaVersion: routing.CapabilityCatalogV1Version, PolicyVersion: routing.RoutingPolicyV1Version, Models: []routing.ModelCapabilityV1{{ID: "codex/local/model", Target: routing.Target{Driver: "codex", Provider: "local", Model: "model"}, Capabilities: []routing.Capability{routing.CapabilityRepoRead}, ContextLimitBytes: 1024, MaxOutputBytes: 512, Quality: routing.QualityBalanced, Cost: routing.CostLow, Latency: routing.LatencyFast, SupportsCancellation: true}}}
	if _, err := NewServiceWithCatalog(store.New(t.TempDir()), catalog); err == nil {
		t.Fatal("single-route Team catalog was accepted")
	}
}

func TestHistoricalCatalogRestoresPreparedTeamAfterRouteRefresh(t *testing.T) {
	model := func(id, driver, provider, name string) routing.ModelCapabilityV1 {
		return routing.ModelCapabilityV1{ID: id, Target: routing.Target{Driver: driver, Provider: provider, Model: name}, Capabilities: []routing.Capability{routing.CapabilityRepoRead}, ContextLimitBytes: 128 * 1024, MaxOutputBytes: 32 * 1024, Quality: routing.QualityBalanced, Cost: routing.CostLow, Latency: routing.LatencyFast, SupportsCancellation: true}
	}
	oldCatalog := routing.CanonicalCatalogV1(routing.CapabilityCatalogV1{SchemaVersion: routing.CapabilityCatalogV1Version, PolicyVersion: routing.RoutingPolicyV1Version, Models: []routing.ModelCapabilityV1{model("claude/default/sonnet", "claude", "default", "sonnet"), model("opencode/default/default", "opencode", "default", "default")}})
	newCatalog := routing.CanonicalCatalogV1(routing.CapabilityCatalogV1{SchemaVersion: routing.CapabilityCatalogV1Version, PolicyVersion: routing.RoutingPolicyV1Version, Models: []routing.ModelCapabilityV1{model("codex/architect/gpt-5.6-sol", "codex", "local", "gpt-5.6-sol"), model("codex/reviewer/gpt-5.6-sol", "codex", "local", "gpt-5.6-sol")}})
	root := t.TempDir()
	state := store.New(root)
	first, err := NewServiceWithCatalog(state, oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	started, err := first.Start(context.Background(), startRequest(project, "historical-catalog"))
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewServiceWithCatalog(state, newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.AddHistoricalCatalog(oldCatalog); err != nil {
		t.Fatal(err)
	}
	restarted.mu.Lock()
	_, err = restarted.prepareInitialTurnsLocked(started.Team.TeamID)
	restarted.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	turnIDs, err := state.ListTeamTurnIDs(started.Team.TeamID)
	if err != nil || len(turnIDs) != 2 {
		t.Fatalf("turn IDs=%v err=%v", turnIDs, err)
	}
	for _, turnID := range turnIDs {
		var turn teamcontract.ParticipantTurnV1
		if err := state.ReadTeamTurn(started.Team.TeamID, turnID, &turn); err != nil {
			t.Fatal(err)
		}
		if turn.Driver == "codex" {
			t.Fatalf("historical Team was rebound to refreshed Catalog: %+v", turn)
		}
	}
}

func TestStartPreparesDefaultPanelWithoutCreatingWorkflowRun(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	state := store.New(t.TempDir())
	service := NewService(state)
	result, err := service.Start(context.Background(), startRequest(project, "request-1"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed {
		t.Fatal("first start was marked replayed")
	}
	team := result.Team
	if team.State != teamcontract.LifecycleCreated || len(team.Participants) != 2 || team.CostUsed != 101 || team.CostGrant != teamcontract.DefaultCostGrant {
		t.Fatalf("prepared Team=%+v", team)
	}
	catalogHash, err := routing.CatalogHash(routing.BuiltinCatalogV1())
	if err != nil || team.CatalogHash != catalogHash || team.RequestHash == "" {
		t.Fatalf("catalog/request binding: %+v", team)
	}
	if _, err := os.Stat(state.RunDir(team.TeamID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workflow run was created: %v", err)
	}
	events, err := state.ReadTeamEvents(team.TeamID)
	if err != nil || len(events) != 1 || events[0].Type != teamcontract.EventTeamCreated {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	participants, err := state.ListTeamParticipantIDs(team.TeamID)
	if err != nil || len(participants) != 2 {
		t.Fatalf("participants=%v err=%v", participants, err)
	}
}

func TestStartIsIdempotentAndDetectsClientRequestConflict(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	state := store.New(t.TempDir())
	service := NewService(state)
	request := startRequest(project, "request-1")
	first, err := service.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Start(context.Background(), request)
	if err != nil || !second.Replayed || second.Team.TeamID != first.Team.TeamID {
		t.Fatalf("replay=%+v err=%v", second, err)
	}
	request.Topic += " changed"
	if _, err := service.Start(context.Background(), request); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict error=%v", err)
	}
}

func TestStartRequiresExistingProjectAndTrustedDistinctModels(t *testing.T) {
	state := store.New(t.TempDir())
	service := NewService(state)
	missing := startRequest(filepath.Join(t.TempDir(), "missing"), "missing-project")
	if _, err := service.Start(context.Background(), missing); err == nil {
		t.Fatal("missing project was accepted")
	}
	project := t.TempDir()
	duplicate := startRequest(project, "duplicate-model")
	duplicate.Participants = []teamcontract.ParticipantSpecV1{{Label: "one", Role: "one", ModelID: "codex/local/gpt-5.6"}, {Label: "two", Role: "two", ModelID: "codex/local/gpt-5.6"}}
	if _, err := service.Start(context.Background(), duplicate); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate model error=%v", err)
	}
}

func TestStartRejectsLegacySessionModeWithoutDispatch(t *testing.T) {
	request := startRequest(t.TempDir(), "legacy-session")
	request.Mode = "session"
	if _, err := NewService(store.New(t.TempDir())).Start(context.Background(), request); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("session mode error = %v", err)
	}
}

func TestStartRejectsGrantBelowTrustedInitialReservation(t *testing.T) {
	project := t.TempDir()
	request := startRequest(project, "small-grant")
	request.CostGrant = 100
	if _, err := NewService(store.New(t.TempDir())).Start(context.Background(), request); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("quota error=%v", err)
	}
}

func TestDispatchInitialPersistsHandlesAndPublicContributionsExactlyOnce(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	contribution, err := json.Marshal(teamcontract.ContributionV1{SchemaVersion: teamcontract.SchemaVersion, Status: teamcontract.ContributionCompleted, ContentMarkdown: "bounded answer"})
	if err != nil {
		t.Fatal(err)
	}
	driver := &immediateDriver{output: string(contribution)}
	state := store.New(t.TempDir())
	service := NewService(state)
	if err := service.SetDriver(driver); err != nil {
		t.Fatal(err)
	}
	started, err := service.Start(context.Background(), startRequest(project, "dispatch-1"))
	if err != nil {
		t.Fatal(err)
	}
	finished, err := service.DispatchInitial(context.Background(), started.Team.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != teamcontract.LifecycleClosed || finished.CloseReason != teamcontract.ClosePanelSettled {
		t.Fatalf("finished Team=%+v", finished)
	}
	messages, err := state.ReadTeamMessages(finished.TeamID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	turnIDs, err := state.ListTeamTurnIDs(finished.TeamID)
	if err != nil || len(turnIDs) != 2 {
		t.Fatalf("turn IDs=%v err=%v", turnIDs, err)
	}
	for _, turnID := range turnIDs {
		turn, err := readTurn(state, finished.TeamID, turnID)
		if err != nil || turn.State != teamcontract.TurnResponded || turn.ContributionMessage == "" {
			t.Fatalf("turn=%+v err=%v", turn, err)
		}
		if _, err := state.ReadTeamExecution(finished.TeamID, turnID); err != nil {
			t.Fatalf("execution handle %s: %v", turnID, err)
		}
	}
	if _, err := service.DispatchInitial(context.Background(), finished.TeamID); err != nil {
		t.Fatal(err)
	}
	driver.mu.Lock()
	starts := len(driver.starts)
	driver.mu.Unlock()
	if starts != 2 {
		t.Fatalf("external starts=%d want 2", starts)
	}
}

func TestDispatchInvalidContributionFailsTurnAndClosesPanelWithoutMessage(t *testing.T) {
	driver := &immediateDriver{output: `{"schemaVersion":"fishyume.team/v1","status":"completed","unexpected":true}`}
	state := store.New(t.TempDir())
	service := NewService(state)
	if err := service.SetDriver(driver); err != nil {
		t.Fatal(err)
	}
	started, err := service.Start(context.Background(), startRequest(t.TempDir(), "invalid-output"))
	if err != nil {
		t.Fatal(err)
	}
	finished, dispatchErr := service.DispatchInitial(context.Background(), started.Team.TeamID)
	if dispatchErr == nil {
		t.Fatal("invalid contribution dispatch unexpectedly succeeded")
	}
	if finished.State != teamcontract.LifecycleClosed {
		t.Fatalf("invalid-output Team=%+v", finished)
	}
	messages, err := state.ReadTeamMessages(finished.TeamID)
	if err != nil || len(messages) != 0 {
		t.Fatalf("invalid output created messages=%+v err=%v", messages, err)
	}
}

func TestStartReplayRepairsPreparedParticipantAndCreationEvent(t *testing.T) {
	project := t.TempDir()
	state := store.New(t.TempDir())
	service := NewService(state)
	request := startRequest(project, "repair-start")
	started, err := service.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(state.TeamParticipantPath(started.Team.TeamID, "participant-1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(state.TeamEventsPath(started.Team.TeamID)); err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Start(context.Background(), request)
	if err != nil || !replayed.Replayed {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	if err := state.ReadTeamParticipant(started.Team.TeamID, "participant-1", &teamcontract.ParticipantV1{}); err != nil {
		t.Fatal(err)
	}
	events, err := state.ReadTeamEvents(started.Team.TeamID)
	if err != nil || len(events) != 1 || events[0].Type != teamcontract.EventTeamCreated {
		t.Fatalf("events=%+v err=%v", events, err)
	}
}

func TestDispatchReconcilesActiveHandleWithoutLaunchingAgain(t *testing.T) {
	driver := &immediateDriver{output: `{"schemaVersion":"fishyume.team/v1","status":"completed","contentMarkdown":"recovered"}`, activeFirst: true}
	state := store.New(t.TempDir())
	first := NewService(state)
	if err := first.SetDriver(driver); err != nil {
		t.Fatal(err)
	}
	started, err := first.Start(context.Background(), startRequest(t.TempDir(), "active-recovery"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		_, _ = first.DispatchInitial(ctx, started.Team.TeamID)
	}()
	awaitAllTurnsState(t, first, started.Team.TeamID, teamcontract.TurnActive)
	cancel()
	<-dispatched
	turnIDs, err := state.ListTeamTurnIDs(started.Team.TeamID)
	if err != nil || len(turnIDs) == 0 {
		t.Fatalf("turns=%v err=%v", turnIDs, err)
	}
	turn, err := readTurn(state, started.Team.TeamID, turnIDs[0])
	if err != nil || turn.State != teamcontract.TurnActive {
		t.Fatalf("active turn=%+v err=%v", turn, err)
	}

	recoveryDriver := &immediateDriver{output: `{"schemaVersion":"fishyume.team/v1","status":"completed","contentMarkdown":"recovered"}`}
	recovered := NewService(state)
	if err := recovered.SetDriver(recoveryDriver); err != nil {
		t.Fatal(err)
	}
	finished, err := recovered.DispatchInitial(context.Background(), started.Team.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != teamcontract.LifecycleClosed {
		t.Fatalf("recovered Team=%+v", finished)
	}
	recoveryDriver.mu.Lock()
	launches := len(recoveryDriver.starts)
	recoveryDriver.mu.Unlock()
	if launches != 0 {
		t.Fatalf("active recovery relaunched external turn: %d", launches)
	}
}

func awaitAllTurnsState(t *testing.T, service *Service, teamID string, want teamcontract.TurnState) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		view, err := service.GetView(teamcontract.TeamGetRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: teamID})
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			t.Fatal(err)
		}
		turnIDs := make([]string, 0, len(view.Turns))
		all := len(view.Turns) > 0
		for _, turn := range view.Turns {
			turnIDs = append(turnIDs, turn.TurnID)
			if turn.State != want {
				all = false
				break
			}
		}
		if all && len(view.Turns) == len(view.Team.Participants) {
			return turnIDs
		}
		if time.Now().After(deadline) {
			t.Fatalf("Team turns did not all reach %q", want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readTurn(state *store.Store, teamID, turnID string) (teamcontract.ParticipantTurnV1, error) {
	var turn teamcontract.ParticipantTurnV1
	err := state.ReadTeamTurn(teamID, turnID, &turn)
	return turn, err
}
