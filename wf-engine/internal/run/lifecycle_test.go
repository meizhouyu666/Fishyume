package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	if err := ValidateAttemptSnapshot(AttemptSnapshot{Number: 1, Phase: NodePhaseWaiting, Reason: ReasonCompletionMissing, LaunchState: LaunchSessionPersisted}); err != nil {
		t.Fatal(err)
	}
	current := AttemptSnapshot{Number: 1, Phase: NodePhaseRunning, Backend: "direct", LaunchState: LaunchHandlePersisted,
		Execution: &backend.ExecutionHandle{Backend: "direct", SchemaVersion: 1, ID: "process-1", Data: json.RawMessage(`{"pid":1}`)}}
	if err := ValidateAttemptSnapshot(current); err != nil {
		t.Fatal(err)
	}
	current.Execution.Backend = "ccpanes"
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

func TestM211SnapshotFixturesRemainReadable(t *testing.T) {
	type fixture struct {
		Name    string           `json:"name"`
		Run     WorkflowSnapshot `json:"run"`
		Node    NodeSnapshot     `json:"node"`
		Attempt *AttemptSnapshot `json:"attempt,omitempty"`
	}

	data, err := os.ReadFile(filepath.Join("testdata", "m2.1.1-snapshots.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"taskBindingId"`) {
		t.Fatal("historical fixture no longer preserves the M2.1.1 TaskBinding field")
	}
	var fixtures []fixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 5 {
		t.Fatalf("fixture count=%d, want 5", len(fixtures))
	}
	want := map[string]struct {
		phase      Phase
		conclusion Conclusion
		reason     Reason
	}{
		"completed":          {PhaseCompleted, ConclusionSucceeded, ""},
		"approval-waiting":   {PhaseWaiting, "", ReasonApprovalRequired},
		"active-attempt":     {PhaseRunning, "", ""},
		"completion-missing": {PhaseWaiting, "", ReasonCompletionMissing},
		"cancel-failed":      {PhaseWaiting, "", ReasonCancelFailed},
	}
	for _, item := range fixtures {
		expected, ok := want[item.Name]
		if !ok {
			t.Fatalf("unexpected fixture %q", item.Name)
		}
		if err := ValidateWorkflowSnapshot(item.Run); err != nil {
			t.Fatalf("fixture %s run: %v", item.Name, err)
		}
		if err := ValidateNodeSnapshot(item.Node); err != nil {
			t.Fatalf("fixture %s node: %v", item.Name, err)
		}
		if item.Run.Phase != expected.phase || item.Run.Conclusion != expected.conclusion || item.Run.Reason != expected.reason {
			t.Fatalf("fixture %s run=%+v", item.Name, item.Run)
		}
		if item.Attempt == nil {
			if item.Name != "approval-waiting" {
				t.Fatalf("fixture %s unexpectedly omitted Attempt", item.Name)
			}
			continue
		}
		if err := ValidateAttemptSnapshot(*item.Attempt); err != nil {
			t.Fatalf("fixture %s attempt: %v", item.Name, err)
		}
		if item.Attempt.legacyExecution == nil || item.Attempt.legacyExecution.SessionID == "" || item.Attempt.legacyExecution.Metadata["bindingId"] == "" {
			t.Fatalf("fixture %s lost historical CC-Panes identity: %+v", item.Name, item.Attempt)
		}
		if item.Name == "completed" && !item.Attempt.ResultConsumed {
			t.Fatal("historical bindingConsumed=true was not converted to resultConsumed")
		}
		encoded, err := json.Marshal(item.Attempt)
		if err != nil {
			t.Fatal(err)
		}
		for _, legacyField := range []string{`"session"`, `"taskBindingId"`, `"launchMetadata"`, `"bindingConsumed"`, `"session_persisted"`, `"finished_without_session"`} {
			if strings.Contains(string(encoded), legacyField) {
				t.Fatalf("fixture %s re-serialized legacy field %s: %s", item.Name, legacyField, encoded)
			}
		}
	}
}

func TestM212BaselineSnapshotFixturesRemainReadable(t *testing.T) {
	type fixture struct {
		Name    string
		Run     WorkflowSnapshot
		Node    NodeSnapshot
		Attempt *AttemptSnapshot
	}
	data, err := os.ReadFile(filepath.Join("testdata", "m2.1.2-snapshots.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []fixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 8 {
		t.Fatalf("fixture count=%d, want 8", len(fixtures))
	}
	for _, item := range fixtures {
		t.Run(item.Name, func(t *testing.T) {
			if err := ValidateWorkflowSnapshot(item.Run); err != nil {
				t.Fatalf("run: %v", err)
			}
			if err := ValidateNodeSnapshot(item.Node); err != nil {
				t.Fatalf("node: %v", err)
			}
			if item.Attempt != nil {
				if err := ValidateAttemptSnapshot(*item.Attempt); err != nil {
					t.Fatalf("attempt: %v", err)
				}
			}
		})
	}
}
