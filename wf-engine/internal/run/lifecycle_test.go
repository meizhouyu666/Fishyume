package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		if item.Attempt.TaskBindingID == "" || item.Attempt.Session == nil || item.Attempt.Session.Metadata["bindingId"] == "" {
			t.Fatalf("fixture %s lost historical CC-Panes identity: %+v", item.Name, item.Attempt)
		}
	}
}
