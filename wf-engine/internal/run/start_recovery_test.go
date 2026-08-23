package run

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
)

const recoverableStartWorkflow = `apiVersion: wf/v1
name: recoverable-start
defaults:
  agent: {driver: fake, target: local}
execution: {maxConcurrency: 1}
nodes:
  first: {type: approval, prompt: approve first}
  second: {type: approval, prompt: approve second, dependsOn: [first]}
`

func TestStartWorkflowRecoversEveryInitializationFaultWindow(t *testing.T) {
	tests := []struct {
		name  string
		match func(string, string) bool
	}{
		{name: "workflow", match: func(operation, path string) bool {
			return operation == "write_json" && strings.HasSuffix(path, "workflow.json")
		}},
		{name: "first_node", match: func(operation, path string) bool {
			return operation == "write_json" && isNodeSnapshotPath(path, "first")
		}},
		{name: "second_node", match: func(operation, path string) bool {
			return operation == "write_json" && isNodeSnapshotPath(path, "second")
		}},
		{name: "run_snapshot", match: func(operation, path string) bool {
			return operation == "write_json" && strings.HasSuffix(path, "run.json")
		}},
		{name: "initial_event", match: func(operation, _ string) bool { return operation == "append_event" }},
		{name: "lease_and_controller", match: func(operation, _ string) bool { return operation == "lease_acquire" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := store.New(t.TempDir())
			backend := &fakeWorkflowBackend{observations: map[string][]backend.Observation{}}
			initializedAt := time.Date(2026, 8, 24, 10, 11, 12, 0, time.UTC)
			request := StartWorkflowRequest{RunID: "run-recoverable", InitializationTime: initializedAt, Project: "project", Content: recoverableStartWorkflow}
			state.SetFaultInjectorForTest(failOnce(test.match))
			if _, err := NewService(backend, state).StartWorkflow(context.Background(), request); err == nil {
				t.Fatal("start succeeded despite injected initialization failure")
			}
			state.SetFaultInjectorForTest(nil)

			recoveredService := NewService(backend, state)
			recovered, err := recoveredService.StartWorkflow(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.StateVersion != 1 || !recovered.CreatedAt.Equal(initializedAt) || !recovered.UpdatedAt.Equal(initializedAt) {
				t.Fatalf("recovered initial response = %+v", recovered)
			}
			waiting := waitForRun(t, recoveredService, recovered.ID, func(snapshot WorkflowSnapshot) bool {
				return snapshot.Phase == PhaseWaiting && snapshot.Reason == ReasonApprovalRequired
			})
			if waiting.CreatedAt != initializedAt {
				t.Fatalf("durable CreatedAt = %s, want %s", waiting.CreatedAt, initializedAt)
			}
			if got := countEventType(t, state, recovered.ID, "run.created"); got != 1 {
				t.Fatalf("run.created event count = %d, want 1", got)
			}
			for _, nodeID := range []string{"first", "second"} {
				var node NodeSnapshot
				if err := state.ReadNode(recovered.ID, nodeID, &node); err != nil {
					t.Fatal(err)
				}
				if !node.CreatedAt.Equal(initializedAt) {
					t.Fatalf("node %s CreatedAt = %s, want %s", nodeID, node.CreatedAt, initializedAt)
				}
			}
		})
	}
}

func TestStartWorkflowRejectsMismatchedResidualInitialization(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *store.Store, StartWorkflowRequest)
		want    string
	}{
		{
			name: "workflow",
			prepare: func(t *testing.T, state *store.Store, request StartWorkflowRequest) {
				failStartAt(t, state, request, func(operation, path string) bool {
					return operation == "write_json" && isNodeSnapshotPath(path, "first")
				})
				if err := os.WriteFile(state.WorkflowPath(request.RunID), []byte(`{"mismatch":true}`), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "normalized workflow",
		},
		{
			name: "node",
			prepare: func(t *testing.T, state *store.Store, request StartWorkflowRequest) {
				failStartAt(t, state, request, func(operation, path string) bool {
					return operation == "write_json" && strings.HasSuffix(path, "run.json")
				})
				var node NodeSnapshot
				if err := state.ReadNode(request.RunID, "first", &node); err != nil {
					t.Fatal(err)
				}
				node.Phase = NodePhaseReady
				if err := state.WriteNode(request.RunID, "first", node); err != nil {
					t.Fatal(err)
				}
			},
			want: "initial snapshot for node \"first\"",
		},
		{
			name: "run",
			prepare: func(t *testing.T, state *store.Store, request StartWorkflowRequest) {
				failStartAt(t, state, request, func(operation, _ string) bool { return operation == "append_event" })
				var snapshot WorkflowSnapshot
				if err := state.ReadSnapshot(request.RunID, &snapshot); err != nil {
					t.Fatal(err)
				}
				snapshot.Project = "other"
				if err := state.WriteSnapshot(request.RunID, snapshot); err != nil {
					t.Fatal(err)
				}
			},
			want: "initial snapshot for run",
		},
		{
			name: "started_node_identity",
			prepare: func(t *testing.T, state *store.Store, request StartWorkflowRequest) {
				failStartAt(t, state, request, func(operation, _ string) bool { return operation == "lease_acquire" })
				var node NodeSnapshot
				if err := state.ReadNode(request.RunID, "first", &node); err != nil {
					t.Fatal(err)
				}
				node.RunID = "run-other"
				if err := state.WriteNode(request.RunID, "first", node); err != nil {
					t.Fatal(err)
				}
			},
			want: "node \"first\" does not match",
		},
		{
			name: "initial_event",
			prepare: func(t *testing.T, state *store.Store, request StartWorkflowRequest) {
				failStartAt(t, state, request, func(operation, _ string) bool { return operation == "lease_acquire" })
				event := WorkflowEvent{ProtocolVersion: protocolVersion, RunID: request.RunID, Sequence: 1, Type: "other", Phase: PhaseCreated, Timestamp: request.InitializationTime}
				encoded, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(state.EventsPath(request.RunID), append(encoded, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "initial event",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := store.New(t.TempDir())
			request := StartWorkflowRequest{RunID: "run-mismatch", InitializationTime: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC), Project: "project", Content: recoverableStartWorkflow}
			test.prepare(t, state, request)
			_, err := NewService(&fakeWorkflowBackend{observations: map[string][]backend.Observation{}}, state).StartWorkflow(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("mismatch error = %v, want %q", err, test.want)
			}
		})
	}
}

func failStartAt(t *testing.T, state *store.Store, request StartWorkflowRequest, match func(string, string) bool) {
	t.Helper()
	state.SetFaultInjectorForTest(failOnce(match))
	_, err := NewService(&fakeWorkflowBackend{observations: map[string][]backend.Observation{}}, state).StartWorkflow(context.Background(), request)
	state.SetFaultInjectorForTest(nil)
	if err == nil {
		t.Fatal("fixture start did not fail")
	}
}

func countEventType(t *testing.T, state *store.Store, runID, eventType string) int {
	t.Helper()
	count := 0
	if err := state.ReadEvents(runID, func(raw json.RawMessage) error {
		var event WorkflowEvent
		if err := json.Unmarshal(raw, &event); err != nil {
			return err
		}
		if event.Type == eventType {
			count++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestStartWorkflowRejectsEventsWithoutRunSnapshot(t *testing.T) {
	state := store.New(t.TempDir())
	request := StartWorkflowRequest{RunID: "run-orphan-event", InitializationTime: time.Now().UTC(), Project: "project", Content: recoverableStartWorkflow}
	if err := state.InitWorkflowRun(request.RunID); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendEvent(request.RunID, map[string]any{"type": "orphan"}); err != nil {
		t.Fatal(err)
	}
	_, err := NewService(&fakeWorkflowBackend{observations: map[string][]backend.Observation{}}, state).StartWorkflow(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "events without an initial snapshot") {
		t.Fatalf("orphan event error = %v", err)
	}
}
