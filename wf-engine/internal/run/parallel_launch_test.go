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

type barrierBackend struct {
	mu        sync.Mutex
	starts    []string
	active    int
	maxActive int
	entered   chan struct{}
	release   chan struct{}
	enterOnce sync.Once
}

func (b *barrierBackend) Name() string { return "barrier" }
func (b *barrierBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true, MaxConcurrentAgents: 2, SupportsConcurrentCancel: true}
}
func (b *barrierBackend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: b.Name(), Ready: true}
}
func (b *barrierBackend) Start(ctx context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	b.mu.Lock()
	b.starts = append(b.starts, spec.NodeID)
	b.active++
	if b.active > b.maxActive {
		b.maxActive = b.active
	}
	if b.active == 2 {
		b.enterOnce.Do(func() { close(b.entered) })
	}
	release := b.release
	b.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-release:
	}
	b.mu.Lock()
	b.active--
	b.mu.Unlock()
	data, _ := json.Marshal(map[string]any{"node": spec.NodeID})
	return &backend.ExecutionHandle{Backend: b.Name(), SchemaVersion: 1, ID: fmt.Sprintf("handle-%s", spec.NodeID), Data: data}, nil
}
func (b *barrierBackend) Observe(context.Context, backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{Status: "succeeded", Summary: "done"}}, nil
}
func (*barrierBackend) Output(context.Context, backend.ExecutionHandle, int) (string, error) {
	return "", nil
}
func (*barrierBackend) Cancel(context.Context, backend.ExecutionHandle) (*backend.CancelResult, error) {
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}

func parallelWorkflow(max int) string {
	return fmt.Sprintf(`apiVersion: wf/v1
name: parallel
defaults: {tool: codex, runtime: local}
execution: {maxConcurrency: %d}
nodes:
  a: {type: agent, task: a}
  b: {type: agent, task: b}
`, max)
}

func TestParallelStartOverlapsIndependentAgents(t *testing.T) {
	b := &barrierBackend{entered: make(chan struct{}), release: make(chan struct{})}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: parallelWorkflow(2)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("parallel starts did not overlap")
	}
	b.mu.Lock()
	if b.maxActive != 2 || len(b.starts) != 2 {
		t.Fatalf("starts=%v maxActive=%d", b.starts, b.maxActive)
	}
	b.mu.Unlock()
	close(b.release)
	final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
}

func TestSingleConcurrencyPreservesLaunchOrder(t *testing.T) {
	b := &barrierBackend{entered: make(chan struct{}), release: make(chan struct{})}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: parallelWorkflow(1)})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		b.mu.Lock()
		starts, maxActive := len(b.starts), b.maxActive
		b.mu.Unlock()
		if starts == 1 {
			if maxActive != 1 {
				t.Fatalf("single-concurrency maxActive=%d", maxActive)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("single-concurrency starts=%d", starts)
		}
		time.Sleep(time.Millisecond)
	}
	close(b.release)
	final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.starts) != 2 || b.starts[0] != "a" || b.starts[1] != "b" {
		t.Fatalf("launch order=%v", b.starts)
	}
}
