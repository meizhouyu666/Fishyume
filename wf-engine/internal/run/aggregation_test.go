package run

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
)

type aggregateBackend struct {
	mu          sync.Mutex
	starts      []string
	cancels     int
	failedNode  string
	waitingNode string
	release     chan struct{}
}

func (*aggregateBackend) Name() string { return "aggregate" }
func (*aggregateBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true, MaxConcurrentAgents: 2, SupportsConcurrentCancel: true}
}
func (b *aggregateBackend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: b.Name(), Ready: true}
}
func (b *aggregateBackend) Start(_ context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	b.mu.Lock()
	b.starts = append(b.starts, spec.NodeID)
	b.mu.Unlock()
	data, _ := json.Marshal(map[string]any{"node": spec.NodeID})
	return &backend.ExecutionHandle{Backend: b.Name(), SchemaVersion: 1, ID: "handle-" + spec.NodeID, Data: data}, nil
}
func (b *aggregateBackend) Observe(_ context.Context, handle backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	nodeID := handle.ID[len("handle-"):]
	if nodeID == b.failedNode {
		return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{Status: "failed", Summary: nodeID + " failed"}}, nil
	}
	if nodeID == b.waitingNode {
		return &backend.ExecutionObservation{State: backend.ObservationWaitingInput, Diagnostic: "input required"}, nil
	}
	select {
	case <-b.release:
		return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{Status: "succeeded", Summary: nodeID + " done"}}, nil
	default:
		return &backend.ExecutionObservation{State: backend.ObservationActive}, nil
	}
}
func (*aggregateBackend) Output(context.Context, backend.ExecutionHandle, int) (string, error) {
	return "", nil
}
func (b *aggregateBackend) Cancel(context.Context, backend.ExecutionHandle) (*backend.CancelResult, error) {
	b.mu.Lock()
	b.cancels++
	b.mu.Unlock()
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}

func TestFailureStopsSchedulingAndDrainsActiveSibling(t *testing.T) {
	b := &aggregateBackend{failedNode: "a", release: make(chan struct{})}
	defer func() {
		select {
		case <-b.release:
		default:
			close(b.release)
		}
	}()
	state := store.New(t.TempDir())
	service := NewService(b, state)
	doc := `apiVersion: wf/v1
name: drain
defaults: {tool: codex, runtime: local}
execution: {maxConcurrency: 2}
nodes:
  a: {type: agent, task: a}
  b: {type: agent, task: b}
  c: {type: agent, task: c}
`
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: doc})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		view, viewErr := service.Status(started.ID)
		if viewErr == nil {
			states := map[string]NodeSnapshot{}
			for _, node := range view.Nodes {
				states[node.ID] = node
			}
			if states["a"].Conclusion == ConclusionFailed && states["b"].Phase == NodePhaseRunning && states["c"].Reason == ReasonFailurePolicy {
				if view.Run.Phase != PhaseRunning {
					t.Fatalf("run concluded before sibling drain: %+v", view.Run)
				}
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("failure drain state not reached: err=%v", viewErr)
		}
		time.Sleep(time.Millisecond)
	}
	b.mu.Lock()
	starts, cancels := append([]string(nil), b.starts...), b.cancels
	b.mu.Unlock()
	if fmt.Sprint(map[string]bool{starts[0]: true, starts[1]: true}) != "map[a:true b:true]" || cancels != 0 {
		t.Fatalf("starts=%v cancels=%d", starts, cancels)
	}
	close(b.release)
	final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionFailed {
		t.Fatalf("final=%+v", final)
	}
}

func TestApprovalCoexistsWithActiveAgent(t *testing.T) {
	b := &aggregateBackend{release: make(chan struct{})}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	doc := `apiVersion: wf/v1
name: approval-parallel
defaults: {tool: codex, runtime: local}
execution: {maxConcurrency: 1}
nodes:
  approve: {type: approval, prompt: approve}
  worker: {type: agent, task: work}
`
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: doc})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		view, viewErr := service.Status(started.ID)
		if viewErr == nil && view.Run.Phase == PhaseRunning && len(view.WaitingApprovals) == 1 && len(view.ActiveAttempts) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("approval/agent coexistence not reached: view=%+v err=%v", view, viewErr)
		}
		time.Sleep(time.Millisecond)
	}
	close(b.release)
	waiting := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool {
		return run.Phase == PhaseWaiting && run.Reason == ReasonApprovalRequired
	})
	if waiting.ActiveNodeID != "approve" {
		t.Fatalf("waiting=%+v", waiting)
	}
}

func TestUnchangedActiveObservationDoesNotAppendAggregateEvents(t *testing.T) {
	b := &aggregateBackend{release: make(chan struct{})}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	cycles := make(chan struct{})
	advance := make(chan struct{})
	service.testHooks.idleReconcileDelay = func(ctx context.Context) error {
		select {
		case cycles <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-advance:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: parallelWorkflow(1)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-cycles:
	case <-time.After(3 * time.Second):
		t.Fatal("first active observation cycle did not complete")
	}
	first := countRunEvents(t, state, started.ID)
	advance <- struct{}{}
	select {
	case <-cycles:
	case <-time.After(3 * time.Second):
		t.Fatal("second active observation cycle did not complete")
	}
	second := countRunEvents(t, state, started.ID)
	if second != first {
		t.Fatalf("unchanged active observation appended events: first=%d second=%d", first, second)
	}
	close(b.release)
	advance <- struct{}{}
	final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
}

func TestUnchangedWaitingObservationWithActiveSiblingIsBounded(t *testing.T) {
	b := &aggregateBackend{waitingNode: "a", release: make(chan struct{})}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	cycles := make(chan struct{})
	advance := make(chan struct{})
	service.testHooks.idleReconcileDelay = func(ctx context.Context) error {
		select {
		case cycles <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		select {
		case <-advance:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: parallelWorkflow(2)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-cycles:
	case <-time.After(3 * time.Second):
		t.Fatal("waiting/active reconciliation did not reach bounded delay")
	}
	first := countRunEvents(t, state, started.ID)
	advance <- struct{}{}
	select {
	case <-cycles:
	case <-time.After(3 * time.Second):
		t.Fatal("second waiting/active reconciliation did not reach bounded delay")
	}
	second := countRunEvents(t, state, started.ID)
	if second != first {
		t.Fatalf("unchanged waiting observation appended events: first=%d second=%d", first, second)
	}
	close(b.release)
	advance <- struct{}{}
}

func countRunEvents(t *testing.T, state *store.Store, runID string) int {
	t.Helper()
	count := 0
	if err := state.ReadEvents(runID, func(json.RawMessage) error {
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count
}
