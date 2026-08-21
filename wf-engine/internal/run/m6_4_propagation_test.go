package run

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
)

type m64CaptureBackend struct {
	mu     sync.Mutex
	starts []backend.AgentExecutionSpec
}

func (*m64CaptureBackend) Name() string { return "codex" }
func (*m64CaptureBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true, SupportsWaitingInput: true}
}
func (*m64CaptureBackend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: "codex", Ready: true}
}
func (b *m64CaptureBackend) Start(_ context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	b.mu.Lock()
	b.starts = append(b.starts, spec)
	b.mu.Unlock()
	return &backend.ExecutionHandle{Backend: "codex", Target: spec.Runtime, SchemaVersion: 1, ID: "m6-4-execution"}, nil
}
func (b *m64CaptureBackend) Observe(_ context.Context, _ backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{Status: "succeeded", Summary: "m6.4 propagation"}}, nil
}
func (*m64CaptureBackend) Output(context.Context, backend.ExecutionHandle, int) (string, error) {
	return "", nil
}
func (*m64CaptureBackend) Cancel(context.Context, backend.ExecutionHandle) (*backend.CancelResult, error) {
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}

func TestM64PersistsAndPropagatesRoutingDecisionToNewAttempt(t *testing.T) {
	backendImpl := &m64CaptureBackend{}
	state := store.New(t.TempDir())
	service := NewService(backendImpl, state)
	project := t.TempDir()
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: project, Filename: "workflow.yaml", Content: `apiVersion: fishyume/v1
name: m6-4
defaults:
  agent: {driver: codex, target: local}
execution: {maxConcurrency: 1}
nodes:
  work: {type: agent, task: route this attempt}
`})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	waitForControllers(t, service)

	var attempt AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "work", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	if attempt.RoutingDecision == nil || attempt.RoutingDecision.Selected.Model != "gpt-5.6-luna" {
		t.Fatalf("attempt routing decision = %+v", attempt.RoutingDecision)
	}
	if attempt.RoutingUsage == nil || attempt.RoutingUsage.CostUnits != 1 || attempt.RoutingUsage.CumulativeCostUnits != 1 {
		t.Fatalf("attempt routing usage = %+v", attempt.RoutingUsage)
	}
	if err := ValidateAttemptSnapshot(attempt); err != nil {
		t.Fatal(err)
	}
	backendImpl.mu.Lock()
	starts := append([]backend.AgentExecutionSpec(nil), backendImpl.starts...)
	backendImpl.mu.Unlock()
	if len(starts) != 1 || starts[0].Model != "gpt-5.6-luna" || starts[0].Envelope == nil || starts[0].Envelope.RoutingDecision == nil {
		t.Fatalf("captured launch specs = %+v", starts)
	}
	if starts[0].Envelope.RoutingDecision.Selected != attempt.RoutingDecision.Selected {
		t.Fatalf("Attempt and envelope selected targets differ: attempt=%+v envelope=%+v", attempt.RoutingDecision.Selected, starts[0].Envelope.RoutingDecision.Selected)
	}

	raw, err := os.ReadFile(state.AttemptPath(started.ID, "work", 1))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"routingDecision"`) {
		t.Fatalf("persisted Attempt omitted routing decision: %s", raw)
	}
}

func TestM64ReadsHistoricalAttemptWithoutRoutingDecision(t *testing.T) {
	legacy := []byte(`{"number":1,"phase":"running","resolvedDriver":"codex","resolvedTarget":"local","backend":"codex","launchState":"prepared"}`)
	var attempt AttemptSnapshot
	if err := json.Unmarshal(legacy, &attempt); err != nil {
		t.Fatal(err)
	}
	if attempt.RoutingDecision != nil {
		t.Fatalf("historical Attempt unexpectedly gained routing decision: %+v", attempt.RoutingDecision)
	}
	if err := ValidateAttemptSnapshot(attempt); err != nil {
		t.Fatal(err)
	}
}

func TestM64ParallelLaunchPropagatesRoutingDecision(t *testing.T) {
	backendImpl := &m64CaptureBackend{}
	state := store.New(t.TempDir())
	service := NewService(backendImpl, state)
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Filename: "workflow.yaml", Content: `apiVersion: fishyume/v1
name: m6-4-parallel
defaults:
  agent: {driver: codex, target: local}
execution: {maxConcurrency: 2}
nodes:
  first: {type: agent, task: first}
  second: {type: agent, task: second}
`})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	waitForControllers(t, service)
	for _, nodeID := range []string{"first", "second"} {
		var attempt AttemptSnapshot
		if err := state.ReadAttempt(started.ID, nodeID, 1, &attempt); err != nil {
			t.Fatal(err)
		}
		if attempt.RoutingDecision == nil || attempt.RoutingDecision.Selected.Model != "gpt-5.6-luna" {
			t.Fatalf("parallel node %q routing decision = %+v", nodeID, attempt.RoutingDecision)
		}
		if attempt.RoutingUsage == nil || attempt.RoutingUsage.CostUnits != 1 || attempt.RoutingUsage.CumulativeCostUnits != 1 {
			t.Fatalf("parallel node %q routing usage = %+v", nodeID, attempt.RoutingUsage)
		}
	}
	backendImpl.mu.Lock()
	defer backendImpl.mu.Unlock()
	if len(backendImpl.starts) != 2 {
		t.Fatalf("parallel launch count = %d, want 2", len(backendImpl.starts))
	}
	for _, spec := range backendImpl.starts {
		if spec.Model != "gpt-5.6-luna" || spec.Envelope == nil || spec.Envelope.RoutingDecision == nil {
			t.Fatalf("parallel launch did not carry routing decision: %+v", spec)
		}
	}
}
