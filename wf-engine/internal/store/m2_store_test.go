package store

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestM2StoreRoundTripAndAttemptPreservation(t *testing.T) {
	state := New(t.TempDir())
	if err := state.InitWorkflowRun("run-safe_1"); err != nil {
		t.Fatal(err)
	}
	workflow := map[string]any{"document": map[string]any{"apiVersion": "wf/v1"}, "topologicalOrder": []string{"Plan_A"}}
	if err := state.WriteWorkflow("run-safe_1", workflow); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteSnapshot("run-safe_1", map[string]any{"protocolVersion": 2, "phase": "created"}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteNode("run-safe_1", "Plan_A", map[string]any{"phase": "running"}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteAttempt("run-safe_1", "Plan_A", 1, map[string]any{"number": 1}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteResult("run-safe_1", "Plan_A", 1, map[string]any{"summary": "done"}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteNodeOutput("run-safe_1", "Plan_A", 1, "recent output"); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteAttempt("run-safe_1", "Plan_A", 2, map[string]any{"number": 2}); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteAttempt("run-safe_1", "Plan_A", 2, map[string]any{"number": 999}); err == nil {
		t.Fatal("expected existing attempt to be immutable")
	}
	numbers, err := state.ListAttempts("run-safe_1", "Plan_A")
	if err != nil || len(numbers) != 2 || numbers[0] != 1 || numbers[1] != 2 {
		t.Fatalf("attempts=%v err=%v", numbers, err)
	}
	var result map[string]any
	if err := state.ReadResult("run-safe_1", "Plan_A", 1, &result); err != nil {
		t.Fatal(err)
	}
	if result["summary"] != "done" {
		t.Fatalf("result=%v", result)
	}
	if _, err := os.Stat(state.NodeOutputPath("run-safe_1", "Plan_A", 1)); err != nil {
		t.Fatal(err)
	}
	output, err := state.ReadNodeOutput("run-safe_1", "Plan_A", 1, 8)
	if err != nil || output != "t output" {
		t.Fatalf("bounded output=%q err=%v", output, err)
	}
	kind, err := state.DetectSnapshot("run-safe_1")
	if err != nil || kind != SnapshotM2 {
		t.Fatalf("kind=%s err=%v", kind, err)
	}
}

func TestInitializationEnsureMethodsReuseMatchesAndRejectMismatches(t *testing.T) {
	state := New(t.TempDir())
	if err := state.InitWorkflowRun("run-ensure"); err != nil {
		t.Fatal(err)
	}
	workflow := map[string]any{"document": map[string]any{"name": "same"}}
	node := map[string]any{"id": "node", "phase": "pending"}
	run := map[string]any{"id": "run-ensure", "phase": "created"}
	for index := 0; index < 2; index++ {
		if err := state.EnsureWorkflow("run-ensure", workflow); err != nil {
			t.Fatal(err)
		}
		if err := state.EnsureNode("run-ensure", "node", node); err != nil {
			t.Fatal(err)
		}
		if err := state.EnsureSnapshot("run-ensure", run); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.EnsureWorkflow("run-ensure", map[string]any{"document": map[string]any{"name": "other"}}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("workflow mismatch error = %v", err)
	}
	if err := state.EnsureNode("run-ensure", "node", map[string]any{"id": "node", "phase": "ready"}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("node mismatch error = %v", err)
	}
	if err := state.EnsureSnapshot("run-ensure", map[string]any{"id": "run-ensure", "phase": "running"}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("run mismatch error = %v", err)
	}
}

func TestEnsureInitialEventIsAtomicAcrossConcurrentRetries(t *testing.T) {
	state := New(t.TempDir())
	if err := state.InitWorkflowRun("run-event"); err != nil {
		t.Fatal(err)
	}
	event := map[string]any{"runId": "run-event", "sequence": 1, "type": "run.created"}
	var wg sync.WaitGroup
	errors := make(chan error, 16)
	for index := 0; index < cap(errors); index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := state.EnsureInitialEvent("run-event", event)
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	if err := state.ReadEvents("run-event", func(json.RawMessage) error { count++; return nil }); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("event count = %d, want 1", count)
	}
	if _, err := state.EnsureInitialEvent("run-event", map[string]any{"runId": "run-event", "sequence": 1, "type": "other"}); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("event mismatch error = %v", err)
	}
}

func TestM2StoreRejectsTraversalAndCorruption(t *testing.T) {
	state := New(t.TempDir())
	for _, test := range []struct{ runID, nodeID string }{
		{"../outside", "safe"}, {"run-safe", "../outside"}, {"run-safe", "x/y"},
	} {
		if err := state.WriteNode(test.runID, test.nodeID, map[string]any{}); err == nil {
			t.Fatalf("accepted unsafe ids %#v", test)
		}
	}
	if err := state.InitWorkflowRun("run-corrupt"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.SnapshotPath("run-corrupt"), []byte(`{"protocolVersion":2,"phase":`), 0o600); err != nil {
		t.Fatal(err)
	}
	var target map[string]any
	if err := state.ReadSnapshot("run-corrupt", &target); err == nil || !strings.Contains(err.Error(), "decode snapshot") {
		t.Fatalf("corruption error=%v", err)
	}
}

func TestLegacySnapshotDetection(t *testing.T) {
	state := New(t.TempDir())
	if err := state.InitRun("run-legacy"); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteSnapshot("run-legacy", map[string]any{"protocolVersion": 1, "status": "paused"}); err != nil {
		t.Fatal(err)
	}
	kind, err := state.DetectSnapshot("run-legacy")
	if err != nil || kind != SnapshotLegacyM1 {
		t.Fatalf("kind=%s err=%v", kind, err)
	}
}
