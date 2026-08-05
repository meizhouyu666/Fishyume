package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

type historicalFixture struct {
	Name    string          `json:"name"`
	Run     json.RawMessage `json:"run"`
	Node    json.RawMessage `json:"node"`
	Attempt json.RawMessage `json:"attempt,omitempty"`
}

type legacyMatrixBackend struct {
	mu       sync.Mutex
	starts   int
	observes []backend.ExecutionHandle
	cancels  []backend.ExecutionHandle
}

func (*legacyMatrixBackend) Name() string { return "ccpanes" }
func (*legacyMatrixBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true, SupportsWaitingInput: true}
}
func (*legacyMatrixBackend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: "ccpanes", Ready: true}
}
func (b *legacyMatrixBackend) Start(context.Context, backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	b.mu.Lock()
	b.starts++
	b.mu.Unlock()
	return nil, fmt.Errorf("historical Attempt must not be launched again")
}
func (b *legacyMatrixBackend) Observe(_ context.Context, handle backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	b.mu.Lock()
	b.observes = append(b.observes, handle)
	b.mu.Unlock()
	return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{Status: "succeeded", Summary: "historical execution reconciled"}}, nil
}
func (*legacyMatrixBackend) Output(context.Context, backend.ExecutionHandle, int) (string, error) {
	return "historical output", nil
}
func (b *legacyMatrixBackend) Cancel(_ context.Context, handle backend.ExecutionHandle) (*backend.CancelResult, error) {
	b.mu.Lock()
	b.cancels = append(b.cancels, handle)
	b.mu.Unlock()
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}
func (b *legacyMatrixBackend) DecodeLegacySession(session backend.Session) (*backend.ExecutionHandle, error) {
	data, err := json.Marshal(map[string]any{"metadata": session.Metadata})
	if err != nil {
		return nil, err
	}
	return &backend.ExecutionHandle{Backend: b.Name(), SchemaVersion: 1, ID: session.ID, Data: data}, nil
}

func loadHistoricalFixtures(t *testing.T) []historicalFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "m2.1.1-snapshots.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []historicalFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	return fixtures
}

func installHistoricalFixture(t *testing.T, item historicalFixture) (*store.Store, WorkflowSnapshot) {
	t.Helper()
	state := store.New(t.TempDir())
	var run WorkflowSnapshot
	if err := json.Unmarshal(item.Run, &run); err != nil {
		t.Fatal(err)
	}
	var featuredNode NodeSnapshot
	if err := json.Unmarshal(item.Node, &featuredNode); err != nil {
		t.Fatal(err)
	}
	if err := state.InitWorkflowRun(run.ID); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteSnapshot(run.ID, item.Run); err != nil {
		t.Fatal(err)
	}
	doc := workflow.Document{
		APIVersion: workflow.APIVersion, Name: run.WorkflowName, Inputs: map[string]workflow.InputDeclaration{},
		Defaults:  workflow.Defaults{Backend: run.Backend, Tool: "codex", Runtime: "local"},
		Execution: workflow.Execution{MaxConcurrency: 1}, Nodes: make(map[string]workflow.Node, len(run.TopologicalOrder)),
	}
	for index, nodeID := range run.TopologicalOrder {
		summary := run.Nodes[nodeID]
		definition := workflow.Node{Type: summary.Type, Task: "fixture task"}
		if summary.Type == "approval" {
			definition.Task, definition.Prompt = "", "fixture approval"
		}
		if index > 0 {
			definition.DependsOn = []string{run.TopologicalOrder[index-1]}
		}
		doc.Nodes[nodeID] = definition
		if nodeID == featuredNode.ID {
			if err := state.WriteNode(run.ID, nodeID, item.Node); err != nil {
				t.Fatal(err)
			}
			continue
		}
		node := NodeSnapshot{
			ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, RunID: run.ID, ID: nodeID, Type: summary.Type,
			Phase: summary.Phase, Conclusion: summary.Conclusion, Reason: summary.Reason, CurrentAttempt: summary.CurrentAttempt,
			CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
		}
		if err := state.WriteNode(run.ID, nodeID, node); err != nil {
			t.Fatal(err)
		}
	}
	normalized := workflow.Normalized{Document: doc, Inputs: map[string]any{}, TopologicalOrder: append([]string(nil), run.TopologicalOrder...)}
	if err := state.WriteWorkflow(run.ID, normalized); err != nil {
		t.Fatal(err)
	}
	if len(item.Attempt) > 0 {
		var attemptHeader struct {
			NodeID string `json:"nodeId"`
			Number int    `json:"number"`
		}
		if err := json.Unmarshal(item.Attempt, &attemptHeader); err != nil {
			t.Fatal(err)
		}
		if err := state.WriteAttempt(run.ID, attemptHeader.NodeID, attemptHeader.Number, item.Attempt); err != nil {
			t.Fatal(err)
		}
	}
	return state, run
}

func assertNoLegacyAttemptFields(t *testing.T, state *store.Store, run WorkflowSnapshot) {
	t.Helper()
	for _, summary := range run.Nodes {
		if summary.CurrentAttempt < 1 {
			continue
		}
		data, err := os.ReadFile(state.AttemptPath(run.ID, summary.ID, summary.CurrentAttempt))
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{`"session"`, `"taskBindingId"`, `"launchMetadata"`, `"bindingConsumed"`, `"session_persisted"`, `"finished_without_session"`} {
			if bytes.Contains(data, []byte(field)) {
				t.Fatalf("rewritten Attempt retained legacy field %s: %s", field, data)
			}
		}
		var rewritten AttemptSnapshot
		if err := json.Unmarshal(data, &rewritten); err != nil {
			t.Fatal(err)
		}
		if rewritten.StateSchemaVersion != 1 {
			t.Fatalf("historical Attempt schema changed to %d", rewritten.StateSchemaVersion)
		}
	}
}

func TestM211StatusReadsSnapshotsWithoutRewritingThem(t *testing.T) {
	for _, item := range loadHistoricalFixtures(t) {
		t.Run(item.Name, func(t *testing.T) {
			state, expected := installHistoricalFixture(t, item)
			service := NewService(&legacyMatrixBackend{}, state)
			var attemptPath string
			var before []byte
			if len(item.Attempt) > 0 {
				var attempt struct {
					NodeID string `json:"nodeId"`
					Number int    `json:"number"`
				}
				if err := json.Unmarshal(item.Attempt, &attempt); err != nil {
					t.Fatal(err)
				}
				attemptPath = state.AttemptPath(expected.ID, attempt.NodeID, attempt.Number)
				before, _ = os.ReadFile(attemptPath)
			}
			view, err := service.Status(expected.ID)
			if err != nil {
				t.Fatal(err)
			}
			if view.Legacy || view.Run == nil || view.Run.Phase != expected.Phase || view.Run.Reason != expected.Reason || view.Run.Conclusion != expected.Conclusion {
				t.Fatalf("status=%+v", view)
			}
			if attemptPath != "" {
				after, err := os.ReadFile(attemptPath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(before, after) {
					t.Fatal("status rewrote a historical Attempt")
				}
				if view.ActiveAttempt == nil || view.ActiveAttempt.LaunchState != LaunchHandlePersisted {
					t.Fatalf("decoded active Attempt=%+v", view.ActiveAttempt)
				}
			}
		})
	}
}

func TestM211ResumeCompatibilityMatrix(t *testing.T) {
	for _, item := range loadHistoricalFixtures(t) {
		t.Run(item.Name, func(t *testing.T) {
			state, historical := installHistoricalFixture(t, item)
			candidate := &legacyMatrixBackend{}
			service := NewService(candidate, state)
			var err error
			switch item.Name {
			case "approval-waiting":
				_, err = service.Resume(context.Background(), ResumeRequest{RunID: historical.ID, Action: &ResumeAction{Type: "approve", NodeID: "approve"}})
			case "cancel-failed":
				_, err = service.Resume(context.Background(), ResumeRequest{RunID: historical.ID})
				if err == nil || !strings.Contains(err.Error(), "cancellation pending") {
					t.Fatalf("resume error=%v", err)
				}
				return
			default:
				_, err = service.Resume(context.Background(), ResumeRequest{RunID: historical.ID})
			}
			if err != nil {
				t.Fatal(err)
			}
			if item.Name == "completed" {
				candidate.mu.Lock()
				starts, observes := candidate.starts, len(candidate.observes)
				candidate.mu.Unlock()
				if starts != 0 || observes != 0 {
					t.Fatalf("completed fixture touched Backend: starts=%d observes=%d", starts, observes)
				}
				return
			}
			completed := waitForRun(t, service, historical.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
			if completed.Conclusion != ConclusionSucceeded {
				t.Fatalf("resumed=%+v", completed)
			}
			candidate.mu.Lock()
			starts := candidate.starts
			observes := append([]backend.ExecutionHandle(nil), candidate.observes...)
			candidate.mu.Unlock()
			if starts != 0 {
				t.Fatalf("historical Attempt relaunched %d times", starts)
			}
			if item.Name == "active-attempt" || item.Name == "completion-missing" {
				if len(observes) != 1 || !strings.HasPrefix(observes[0].ID, "session-") {
					t.Fatalf("observed handles=%+v", observes)
				}
				assertNoLegacyAttemptFields(t, state, historical)
			} else if len(observes) != 0 {
				t.Fatalf("approval resume observed Backend: %+v", observes)
			}
		})
	}
}

func TestM211CancelCompatibilityMatrix(t *testing.T) {
	for _, item := range loadHistoricalFixtures(t) {
		t.Run(item.Name, func(t *testing.T) {
			state, historical := installHistoricalFixture(t, item)
			candidate := &legacyMatrixBackend{}
			service := NewService(candidate, state)
			cancelled, err := service.Cancel(context.Background(), historical.ID)
			if err != nil {
				t.Fatal(err)
			}
			candidate.mu.Lock()
			starts := candidate.starts
			cancels := append([]backend.ExecutionHandle(nil), candidate.cancels...)
			candidate.mu.Unlock()
			if starts != 0 {
				t.Fatalf("cancel relaunched historical Attempt %d times", starts)
			}
			if item.Name == "completed" {
				if cancelled.Conclusion != ConclusionSucceeded || len(cancels) != 0 {
					t.Fatalf("completed cancel=%+v handles=%+v", cancelled, cancels)
				}
				return
			}
			if cancelled.Conclusion != ConclusionCancelled || cancelled.Reason != ReasonUserRequested {
				t.Fatalf("cancelled=%+v", cancelled)
			}
			if item.Name == "approval-waiting" {
				if len(cancels) != 0 {
					t.Fatalf("approval cancellation touched Backend: %+v", cancels)
				}
				return
			}
			if len(cancels) != 1 || !strings.HasPrefix(cancels[0].ID, "session-") {
				t.Fatalf("cancel handles=%+v", cancels)
			}
			assertNoLegacyAttemptFields(t, state, historical)
		})
	}
}

func TestLegacyAttemptMetadataPrecedenceAndNormalization(t *testing.T) {
	tests := []struct {
		name        string
		wire        string
		wantBinding string
		wantLaunch  string
		wantState   LaunchState
	}{
		{
			name:        "top-level binding fallback",
			wire:        `{"session":{"id":"session-1","metadata":{"project":"p"}},"taskBindingId":"binding-top","launchState":"session_persisted"}`,
			wantBinding: "binding-top", wantState: LaunchHandlePersisted,
		},
		{
			name:        "session metadata wins",
			wire:        `{"session":{"id":"session-2","metadata":{"bindingId":"binding-session"}},"taskBindingId":"binding-top","launchState":"session_persisted"}`,
			wantBinding: "binding-session", wantState: LaunchHandlePersisted,
		},
		{
			name:        "session overrides launch metadata",
			wire:        `{"session":{"id":"session-3","metadata":{"bindingId":"binding-session","launchId":"launch-session"}},"launchMetadata":{"bindingId":"binding-launch","launchId":"launch-old"},"launchState":"finished_without_session"}`,
			wantBinding: "binding-session", wantLaunch: "launch-session", wantState: LaunchFinishedWithoutHandle,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var attempt AttemptSnapshot
			if err := json.Unmarshal([]byte(test.wire), &attempt); err != nil {
				t.Fatal(err)
			}
			if attempt.legacyExecution == nil || attempt.legacyExecution.Metadata["bindingId"] != test.wantBinding || attempt.legacyExecution.Metadata["launchId"] != test.wantLaunch || attempt.LaunchState != test.wantState {
				t.Fatalf("decoded=%+v legacy=%+v", attempt, attempt.legacyExecution)
			}
			encoded, err := json.Marshal(attempt)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"session", "taskBindingId", "launchMetadata", "bindingConsumed", "session_persisted", "finished_without_session"} {
				if strings.Contains(string(encoded), forbidden) {
					t.Fatalf("encoded legacy field %q: %s", forbidden, encoded)
				}
			}
		})
	}
}
