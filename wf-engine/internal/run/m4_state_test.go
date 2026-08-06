package run

import (
	"context"
	"os"
	"strings"
	"testing"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
)

func TestNewRunPersistsDriverContextWithoutLegacyOrCCPanesFields(t *testing.T) {
	terminal := backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{Status: "succeeded", Summary: "done"}}
	candidate := newRoutingBackend("codex", terminal)
	state := store.New(t.TempDir())
	service := NewService(candidate, state)
	doc := `apiVersion: fishyume/v1
name: m4-state
defaults:
  agent: {driver: codex, target: local}
execution: {maxConcurrency: 1}
nodes:
  implement: {type: agent, task: implement, requiredSkills: [go]}
`
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Filename: "workflow.yaml", Content: doc})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	waitForControllers(t, service)

	var attempt AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "implement", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	if attempt.ResolvedDriver != "codex" || attempt.ResolvedTarget != "local" || attempt.ContextCompilerVersion != "context-compiler/v1" || len(attempt.ContextManifest.Components) == 0 || len(attempt.ContextHash) != 64 {
		t.Fatalf("attempt metadata=%+v", attempt)
	}
	for _, path := range []string{state.SnapshotPath(started.ID), state.WorkflowPath(started.ID), state.AttemptPath(started.ID, "implement", 1)} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, forbidden := range []string{`"backend"`, `"tool"`, `"runtime"`, `"taskBindingId"`, `"session"`, `"ccpanes"`} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("new state %s retained forbidden field %s: %s", path, forbidden, text)
			}
		}
	}
}

func TestNewRunRejectsCCPanesSelection(t *testing.T) {
	registry := registryWith(t, newRoutingBackend("codex"), newRoutingBackend("ccpanes"))
	service := NewServiceWithRegistry(registry, "codex", store.New(t.TempDir()))
	_, err := service.Start(context.Background(), StartRequest{Project: t.TempDir(), Driver: "ccpanes", Task: "do work"})
	if err == nil || !strings.Contains(err.Error(), "retired for new Runs") {
		t.Fatalf("error=%v", err)
	}
}

func TestHistoricalCCPanesStatusIsReadableButUnrecoverableWithoutLegacyAdapter(t *testing.T) {
	var fixture historicalFixture
	for _, candidate := range loadHistoricalFixtures(t) {
		if candidate.Name == "active-attempt" {
			fixture = candidate
			break
		}
	}
	if fixture.Name == "" {
		t.Fatal("active historical fixture is missing")
	}
	state, historical := installHistoricalFixture(t, fixture)
	service := NewService(newRoutingBackend("codex"), state)
	view, err := service.Status(historical.ID)
	if err != nil || view.Run == nil || view.Run.ID != historical.ID {
		t.Fatalf("status=%+v err=%v", view, err)
	}
	_, err = service.Resume(context.Background(), ResumeRequest{RunID: historical.ID})
	if err != nil {
		t.Fatal(err)
	}
	waitForControllers(t, service)
	view, err = service.Status(historical.ID)
	if err != nil || view.Run == nil || (!strings.Contains(view.Run.Summary, "ccpanes") && !strings.Contains(view.Run.Summary, "CC-Panes")) {
		t.Fatalf("resume status=%+v err=%v", view, err)
	}
}
