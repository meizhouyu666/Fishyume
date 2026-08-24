package team

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/teamcontract"
)

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
