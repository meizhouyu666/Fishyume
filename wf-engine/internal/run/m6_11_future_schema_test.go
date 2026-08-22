package run

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"wf.local/wf-engine/internal/store"
)

func TestM611FutureStateSchemaFailsClosedWithoutMutation(t *testing.T) {
	state := store.New(t.TempDir())
	const runID = "run-future-schema"
	now := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	run := WorkflowSnapshot{
		ProtocolVersion: 2, StateSchemaVersion: stateSchemaVersion + 1, StateVersion: 1,
		ID: runID, WorkflowName: "future", Project: t.TempDir(), ResolvedDriver: "fake", ResolvedTarget: "local",
		Phase: PhasePaused, Reason: ReasonControllerDetach, TopologicalOrder: []string{"work"},
		Nodes:    map[string]NodeSummary{"work": {ID: "work", Type: "agent", Phase: NodePhaseRunning, CurrentAttempt: 1}},
		StateDir: state.RunDir(runID), CreatedAt: now, UpdatedAt: now,
	}
	node := NodeSnapshot{ProtocolVersion: 2, StateSchemaVersion: 1, RunID: runID, ID: "work", Type: "agent", Phase: NodePhaseRunning, CurrentAttempt: 1, CreatedAt: now, UpdatedAt: now}
	if err := state.InitWorkflowRun(runID); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteSnapshot(runID, run); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteNode(runID, "work", node); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(state.SnapshotPath(runID))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(&fakeWorkflowBackend{}, state)
	if _, err := service.Status(runID); err == nil {
		t.Fatal("future state schema was accepted by Status")
	}
	if _, err := service.Resume(context.Background(), ResumeRequest{RunID: runID}); err == nil {
		t.Fatal("future state schema was accepted by Resume")
	}
	after, err := os.ReadFile(state.SnapshotPath(runID))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("future state schema failure rewrote the Run snapshot")
	}
}

func TestM611HistoricalMissingSchemaStillNormalizesForRead(t *testing.T) {
	if err := ValidateWorkflowSnapshot(WorkflowSnapshot{Phase: PhaseCreated}); err != nil {
		t.Fatalf("missing schema was rejected: %v", err)
	}
	if err := ValidateNodeSnapshot(NodeSnapshot{Phase: NodePhasePending}); err != nil {
		t.Fatalf("missing node schema was rejected: %v", err)
	}
	if err := ValidateAttemptSnapshot(AttemptSnapshot{Number: 1, Phase: NodePhaseWaiting, Reason: ReasonCompletionMissing}); err != nil {
		t.Fatalf("missing Attempt schema was rejected: %v", err)
	}
}
