package run

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
)

type multiCancelBackend struct {
	mu          sync.Mutex
	calls       map[string]int
	active      int
	maxActive   int
	entered     chan struct{}
	release     chan struct{}
	enteredOnce sync.Once
}

func (*multiCancelBackend) Name() string { return "multi-cancel" }
func (*multiCancelBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true, MaxConcurrentAgents: 2, SupportsConcurrentCancel: true}
}
func (b *multiCancelBackend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: b.Name(), Ready: true}
}
func (b *multiCancelBackend) Start(_ context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	data, _ := json.Marshal(map[string]any{"node": spec.NodeID})
	return &backend.ExecutionHandle{Backend: b.Name(), SchemaVersion: 1, ID: "handle-" + spec.NodeID, Data: data}, nil
}
func (*multiCancelBackend) Observe(context.Context, backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	return &backend.ExecutionObservation{State: backend.ObservationActive}, nil
}
func (*multiCancelBackend) Output(context.Context, backend.ExecutionHandle, int) (string, error) {
	return "", nil
}
func (b *multiCancelBackend) Cancel(_ context.Context, handle backend.ExecutionHandle) (*backend.CancelResult, error) {
	nodeID := handle.ID[len("handle-"):]
	b.mu.Lock()
	b.calls[nodeID]++
	call := b.calls[nodeID]
	b.active++
	if b.active > b.maxActive {
		b.maxActive = b.active
	}
	if b.active == 2 {
		b.enteredOnce.Do(func() { close(b.entered) })
	}
	release := b.release
	b.mu.Unlock()
	<-release
	b.mu.Lock()
	b.active--
	b.mu.Unlock()
	if nodeID == "b" && call == 1 {
		return &backend.CancelResult{State: backend.CancelNotConfirmed, Diagnostic: "first attempt not confirmed"}, nil
	}
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}

func TestConcurrentCancellationRequiresAllConfirmationsAndRetriesOnlyUnresolved(t *testing.T) {
	b := &multiCancelBackend{calls: map[string]int{}, entered: make(chan struct{}), release: make(chan struct{})}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: parallelWorkflow(2)})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		view, viewErr := service.Status(started.ID)
		if viewErr == nil && len(view.ActiveAttempts) == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("active Attempts not ready: view=%+v err=%v", view, viewErr)
		}
		time.Sleep(time.Millisecond)
	}
	type result struct {
		run WorkflowSnapshot
		err error
	}
	firstDone := make(chan result, 1)
	go func() {
		run, cancelErr := service.Cancel(context.Background(), started.ID)
		firstDone <- result{run: run, err: cancelErr}
	}()
	select {
	case <-b.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Cancel calls did not overlap")
	}
	close(b.release)
	first := <-firstDone
	if first.err == nil || first.run.Phase != PhaseWaiting || first.run.Reason != ReasonCancelFailed || first.run.Conclusion == ConclusionCancelled {
		t.Fatalf("partial cancellation=%+v err=%v", first.run, first.err)
	}
	second, err := service.Cancel(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.Phase != PhaseCompleted || second.Conclusion != ConclusionCancelled {
		t.Fatalf("second cancellation=%+v", second)
	}
	b.mu.Lock()
	callsA, callsB, maxActive := b.calls["a"], b.calls["b"], b.maxActive
	b.mu.Unlock()
	if callsA != 1 || callsB != 2 || maxActive != 2 {
		t.Fatalf("calls a=%d b=%d maxActive=%d", callsA, callsB, maxActive)
	}
}
