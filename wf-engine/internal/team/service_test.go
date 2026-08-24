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

	"wf.local/wf-engine/internal/explorationdriver"
	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/teamcontract"
)

type immediateDriver struct {
	mu       sync.Mutex
	starts   []explorationdriver.StartRequest
	output   string
	terminal bool
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
	return &explorationdriver.Observation{State: explorationdriver.ObservationTerminal}, nil
}
func (d *immediateDriver) Output(context.Context, explorationdriver.ExecutionHandle, int) (string, error) {
	return d.output, nil
}
func (d *immediateDriver) Cancel(context.Context, explorationdriver.ExecutionHandle) (*explorationdriver.CancelResult, error) {
	return &explorationdriver.CancelResult{State: explorationdriver.CancelConfirmed}, nil
}

func startRequest(project, requestID string) teamcontract.TeamStartRequestV1 {
	return teamcontract.TeamStartRequestV1{SchemaVersion: teamcontract.SchemaVersion, ClientRequestID: requestID, Project: project, Mode: teamcontract.ModePanel, Topic: "Compare two approaches"}
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
	session := startRequest(project, "session")
	session.Mode = teamcontract.ModeSession
	if _, err := service.Start(context.Background(), session); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("session error=%v", err)
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

func readTurn(state *store.Store, teamID, turnID string) (teamcontract.ParticipantTurnV1, error) {
	var turn teamcontract.ParticipantTurnV1
	err := state.ReadTeamTurn(teamID, turnID, &turn)
	return turn, err
}
