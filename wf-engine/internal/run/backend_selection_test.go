package run

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

type routingBackend struct {
	name         string
	ready        bool
	capabilities backend.Capabilities

	mu           sync.Mutex
	observations []backend.ExecutionObservation
	starts       int
	observes     int
	cancels      int
}

func (b *routingBackend) Name() string                       { return b.name }
func (b *routingBackend) Capabilities() backend.Capabilities { return b.capabilities }
func (b *routingBackend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: b.name, Ready: b.ready}
}
func (b *routingBackend) Start(context.Context, backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.starts++
	return &backend.ExecutionHandle{Backend: b.name, SchemaVersion: 1, ID: b.name + "-execution"}, nil
}
func (b *routingBackend) Observe(context.Context, backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.observes++
	if len(b.observations) == 0 {
		return &backend.ExecutionObservation{State: backend.ObservationActive}, nil
	}
	result := b.observations[0]
	b.observations = b.observations[1:]
	return &result, nil
}
func (*routingBackend) Output(context.Context, backend.ExecutionHandle, int) (string, error) {
	return "", nil
}
func (b *routingBackend) Cancel(context.Context, backend.ExecutionHandle) (*backend.CancelResult, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cancels++
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}

func newRoutingBackend(name string, observations ...backend.ExecutionObservation) *routingBackend {
	return &routingBackend{
		name:         name,
		ready:        true,
		capabilities: backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true, SupportsWaitingInput: true},
		observations: append([]backend.ExecutionObservation(nil), observations...),
	}
}

func registryWith(t *testing.T, candidates ...backend.AgentBackend) *backend.Registry {
	t.Helper()
	registry := backend.NewRegistry()
	for _, candidate := range candidates {
		if err := registry.Register(candidate); err != nil {
			t.Fatal(err)
		}
	}
	return registry
}

const approvalOnlyWorkflow = `apiVersion: fishyume/v1
name: backend-selection
execution: {maxConcurrency: 1}
nodes:
  approve: {type: approval, prompt: approve}
`

func TestDriverSelectionPriorityAndPersistence(t *testing.T) {
	registry := registryWith(t,
		newRoutingBackend("codex"), newRoutingBackend("cli"), newRoutingBackend("env"), newRoutingBackend("workflow"),
	)
	t.Setenv("FISHYUME_BACKEND", "env")
	tests := []struct {
		name           string
		requestDriver  string
		workflowDriver string
		clearEnv       bool
		want           string
	}{
		{name: "request beats workflow and env", requestDriver: "cli", workflowDriver: "workflow", want: "cli"},
		{name: "workflow beats env", workflowDriver: "workflow", want: "workflow"},
		{name: "env beats default", want: "env"},
		{name: "default is codex", clearEnv: true, want: "codex"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := store.New(t.TempDir())
			service := NewServiceWithRegistry(registry, "codex", state)
			if test.clearEnv {
				service.getenv = func(string) string { return "" }
			}
			content := approvalOnlyWorkflow
			if test.workflowDriver != "" {
				content = strings.Replace(content, "execution:", "defaults: {agent: {driver: "+test.workflowDriver+", target: local}}\nexecution:", 1)
			}
			started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "project", Driver: test.requestDriver, Filename: "workflow.yaml", Content: content})
			if err != nil {
				t.Fatal(err)
			}
			if started.ResolvedDriver != test.want {
				t.Fatalf("Driver = %q, want %q", started.ResolvedDriver, test.want)
			}
			var normalized workflow.Normalized
			if err := state.ReadWorkflow(started.ID, &normalized); err != nil {
				t.Fatal(err)
			}
			if normalized.Document.Defaults.Agent.Driver != test.want || normalized.Document.Defaults.Backend != "" || normalized.Document.Defaults.Tool != "" || normalized.Document.Defaults.Runtime != "" {
				t.Fatalf("persisted Agent selection = %+v legacy=%+v", normalized.Document.Defaults.Agent, normalized.Document.Defaults)
			}
			waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseWaiting })
			waitForControllers(t, service)
		})
	}
}

func TestUnknownAndUnsupportedBackendFailBeforeRunCreation(t *testing.T) {
	root := t.TempDir()
	state := store.New(root)
	direct := newRoutingBackend("direct")
	service := NewServiceWithRegistry(registryWith(t, direct), "direct", state)

	if _, err := service.Start(context.Background(), StartRequest{Project: "project", Driver: "missing", Task: "task"}); err == nil || !strings.Contains(err.Error(), "available Backends: direct") {
		t.Fatalf("unknown Backend error = %v", err)
	}
	if _, err := service.Start(context.Background(), StartRequest{Project: "project", Backend: "direct", Tool: "claude", Runtime: "local", Task: "task"}); err == nil || !strings.Contains(err.Error(), `conflicts with tool "claude"`) {
		t.Fatalf("capability error = %v", err)
	}
	if _, err := service.Start(context.Background(), StartRequest{Project: "project", Driver: "direct", Target: "wsl", Task: "task"}); err == nil || !strings.Contains(err.Error(), `does not support target "wsl"`) {
		t.Fatalf("runtime capability error = %v", err)
	}
	direct.ready = false
	if _, err := service.Start(context.Background(), StartRequest{Project: "project", Driver: "direct", Task: "task"}); err == nil || !strings.Contains(err.Error(), `Backend "direct" is not ready`) {
		t.Fatalf("readiness error = %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || direct.starts != 0 {
		t.Fatalf("invalid request created state or launched Backend: entries=%v starts=%d", entries, direct.starts)
	}
}

func TestPersistedBackendControlsResumeAndCancelAfterEnvironmentChanges(t *testing.T) {
	waiting := backend.ExecutionObservation{State: backend.ObservationWaitingInput}
	terminal := backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{Status: "succeeded", Summary: "done"}}

	t.Run("resume", func(t *testing.T) {
		state := store.New(t.TempDir())
		firstBackend := newRoutingBackend("first", waiting, terminal)
		secondBackend := newRoutingBackend("second")
		registry := registryWith(t, firstBackend, secondBackend)
		t.Setenv("FISHYUME_BACKEND", "first")
		firstService := NewServiceWithRegistry(registry, "second", state)
		started, err := firstService.Start(context.Background(), StartRequest{Project: "project", Task: "task"})
		if err != nil {
			t.Fatal(err)
		}
		waitForRun(t, firstService, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseWaiting })
		waitForControllers(t, firstService)

		t.Setenv("FISHYUME_BACKEND", "second")
		secondService := NewServiceWithRegistry(registry, "second", state)
		if _, err := secondService.Resume(context.Background(), ResumeRequest{RunID: started.ID}); err != nil {
			t.Fatal(err)
		}
		waitForRun(t, secondService, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
		firstBackend.mu.Lock()
		firstObserves := firstBackend.observes
		firstBackend.mu.Unlock()
		secondBackend.mu.Lock()
		secondObserves := secondBackend.observes
		secondBackend.mu.Unlock()
		if firstObserves != 2 || secondObserves != 0 {
			t.Fatalf("resume routing: first=%d second=%d", firstObserves, secondObserves)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		state := store.New(t.TempDir())
		firstBackend := newRoutingBackend("first", waiting)
		secondBackend := newRoutingBackend("second")
		registry := registryWith(t, firstBackend, secondBackend)
		t.Setenv("FISHYUME_BACKEND", "first")
		firstService := NewServiceWithRegistry(registry, "second", state)
		started, err := firstService.Start(context.Background(), StartRequest{Project: "project", Task: "task"})
		if err != nil {
			t.Fatal(err)
		}
		waitForRun(t, firstService, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseWaiting })
		waitForControllers(t, firstService)

		t.Setenv("FISHYUME_BACKEND", "second")
		secondService := NewServiceWithRegistry(registry, "second", state)
		cancelled, err := secondService.Cancel(context.Background(), started.ID)
		if err != nil {
			t.Fatal(err)
		}
		if cancelled.Conclusion != ConclusionCancelled {
			t.Fatalf("cancelled run = %+v", cancelled)
		}
		firstBackend.mu.Lock()
		firstCancels := firstBackend.cancels
		firstBackend.mu.Unlock()
		secondBackend.mu.Lock()
		secondCancels := secondBackend.cancels
		secondBackend.mu.Unlock()
		if firstCancels != 1 || secondCancels != 0 {
			t.Fatalf("cancel routing: first=%d second=%d", firstCancels, secondCancels)
		}
	})
}
