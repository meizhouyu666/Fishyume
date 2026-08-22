package run

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

func TestM610RetryOnHistoricalStatePreservesOldAttemptAndWritesCurrentAttempt(t *testing.T) {
	state := store.New(t.TempDir())
	const runID = "run-m610-upgrade"
	now := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	run := WorkflowSnapshot{
		ProtocolVersion: 2, StateSchemaVersion: 1, StateVersion: 4, ID: runID, WorkflowName: "upgrade",
		Project: t.TempDir(), ResolvedDriver: "fake", ResolvedTarget: "local", Backend: "fake",
		Phase: PhaseCompleted, Conclusion: ConclusionFailed, Reason: ReasonUpstreamFailed,
		TopologicalOrder: []string{"work"}, Nodes: map[string]NodeSummary{
			"work": {ID: "work", Type: "agent", Phase: NodePhaseCompleted, Conclusion: ConclusionFailed, CurrentAttempt: 1},
		}, StateDir: state.RunDir(runID), CreatedAt: now, UpdatedAt: now,
	}
	node := NodeSnapshot{ProtocolVersion: 2, StateSchemaVersion: 1, RunID: runID, ID: "work", Type: "agent", Phase: NodePhaseCompleted, Conclusion: ConclusionFailed, CurrentAttempt: 1, CreatedAt: now, UpdatedAt: now}
	document := workflow.Document{
		APIVersion: workflow.APIVersion, Name: run.WorkflowName,
		Defaults:  workflow.Defaults{Backend: "fake", Tool: "codex", Runtime: "local"},
		Execution: workflow.Execution{MaxConcurrency: 1}, Nodes: map[string]workflow.Node{"work": {Type: "agent", Task: "historical task"}},
	}
	normalized := workflow.Normalized{Document: document, Inputs: map[string]any{}, TopologicalOrder: []string{"work"}}
	historicalAttempt := json.RawMessage(`{"protocolVersion":2,"stateSchemaVersion":1,"runId":"run-m610-upgrade","nodeId":"work","number":1,"phase":"completed","conclusion":"failed","backend":"fake","launchState":"handle_persisted","execution":{"backend":"fake","schemaVersion":1,"id":"historical-handle","data":{}},"contextCompilerVersion":"context-compiler/v1","contextManifest":{"compilerVersion":"context-compiler/v1","components":[{"name":"node-task","source":"workflow.node.task","version":"v1"}]},"contextHash":"historical-v1-hash","promptHash":"historical-v1-hash","resultConsumed":true,"startedAt":"2026-08-22T00:00:00Z","updatedAt":"2026-08-22T00:00:01Z","completedAt":"2026-08-22T00:00:01Z"}`)
	if err := state.InitWorkflowRun(runID); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteSnapshot(runID, run); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteNode(runID, "work", node); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteWorkflow(runID, normalized); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteAttempt(runID, "work", 1, historicalAttempt); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(state.AttemptPath(runID, "work", 1))
	if err != nil {
		t.Fatal(err)
	}

	service := NewService(&fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "succeeded", Summary: "upgraded retry"}}}, state)
	expectedAttempt := 1
	if _, err := service.Resume(context.Background(), ResumeRequest{RunID: runID, Action: &ResumeAction{Type: "retry", NodeID: "work", ExpectedAttempt: &expectedAttempt}}); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, service, runID, func(snapshot WorkflowSnapshot) bool {
		return snapshot.Phase == PhaseCompleted && snapshot.Nodes["work"].CurrentAttempt == 2
	})
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("upgraded retry=%+v", final)
	}
	waitForControllers(t, service)

	after, err := os.ReadFile(state.AttemptPath(runID, "work", 1))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("historical Attempt was rewritten during upgrade retry")
	}
	var current AttemptSnapshot
	if err := state.ReadAttempt(runID, "work", 2, &current); err != nil {
		t.Fatal(err)
	}
	if current.StateSchemaVersion != 3 || current.ContextCompilerVersionV2 == "" || current.ContextManifestV2 == nil {
		t.Fatalf("new Attempt did not use current state/context schema: %+v", current)
	}
	numbers, err := state.ListAttempts(runID, "work")
	if err != nil || len(numbers) != 2 || numbers[0] != 1 || numbers[1] != 2 {
		t.Fatalf("attempt history=%v err=%v", numbers, err)
	}
}
