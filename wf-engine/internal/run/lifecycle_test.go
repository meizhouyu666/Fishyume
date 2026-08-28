package run

import (
	"encoding/json"
	"testing"

	"wf.local/wf-engine/internal/backend"
)

func TestLifecycleValidationSeparatesPhaseAndConclusion(t *testing.T) {
	valid := []WorkflowSnapshot{
		{Phase: PhaseCreated}, {Phase: PhaseRunning}, {Phase: PhaseWaiting, Reason: ReasonApprovalRequired},
		{Phase: PhaseCompleted, Conclusion: ConclusionSucceeded},
	}
	for _, snapshot := range valid {
		if err := ValidateWorkflowSnapshot(snapshot); err != nil {
			t.Fatalf("valid snapshot %+v: %v", snapshot, err)
		}
	}
	invalid := []WorkflowSnapshot{
		{Phase: PhaseRunning, Conclusion: ConclusionSucceeded},
		{Phase: PhaseCompleted},
	}
	for _, snapshot := range invalid {
		if err := ValidateWorkflowSnapshot(snapshot); err == nil {
			t.Fatalf("accepted invalid snapshot %+v", snapshot)
		}
	}
	if err := ValidateNodeSnapshot(NodeSnapshot{Phase: NodePhaseSkipped}); err == nil {
		t.Fatal("skipped node without reason accepted")
	}
	if err := ValidateNodeSnapshot(NodeSnapshot{Phase: NodePhaseCompleted, Conclusion: ConclusionFailed}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAttemptSnapshot(AttemptSnapshot{Number: 1, Phase: NodePhaseWaiting, Reason: ReasonCompletionMissing, LaunchState: LaunchHandlePersisted}); err != nil {
		t.Fatal(err)
	}
	current := AttemptSnapshot{Number: 1, Phase: NodePhaseRunning, Backend: "direct", LaunchState: LaunchHandlePersisted,
		Execution: &backend.ExecutionHandle{Backend: "direct", SchemaVersion: 1, ID: "process-1", Data: json.RawMessage(`{"pid":1}`)}}
	if err := ValidateAttemptSnapshot(current); err != nil {
		t.Fatal(err)
	}
	current.Execution.Backend = "other-driver"
	if err := ValidateAttemptSnapshot(current); err == nil {
		t.Fatal("accepted an execution handle for a different Backend")
	}
	if err := ValidateAttemptSnapshot(AttemptSnapshot{Number: 1, Phase: NodePhaseRunning, LaunchState: "unknown"}); err == nil {
		t.Fatal("unknown launch state accepted")
	}
	if err := ValidateAttemptSnapshot(AttemptSnapshot{Number: 1, Phase: NodePhaseCompleted}); err == nil {
		t.Fatal("completed attempt without conclusion accepted")
	}
}
