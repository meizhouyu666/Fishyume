package run

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/store"
)

type m65Backend struct {
	mu      sync.Mutex
	starts  []backend.AgentExecutionSpec
	results []backend.AgentResult
}

func (*m65Backend) Name() string { return "codex" }
func (*m65Backend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true, SupportsWaitingInput: true, SupportsConcurrentCancel: true}
}
func (*m65Backend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: "codex", Ready: true}
}
func (b *m65Backend) Start(_ context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.starts = append(b.starts, spec)
	return &backend.ExecutionHandle{Backend: "codex", Target: spec.Runtime, SchemaVersion: 1, ID: fmt.Sprintf("%s-%d", spec.NodeID, spec.Attempt)}, nil
}
func (b *m65Backend) Observe(_ context.Context, handle backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	attempt, _ := strconv.Atoi(handle.ID[strings.LastIndex(handle.ID, "-")+1:])
	index := attempt - 1
	result := b.results[index]
	return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &result}, nil
}
func (*m65Backend) Output(context.Context, backend.ExecutionHandle, int) (string, error) {
	return "", nil
}
func (*m65Backend) Cancel(context.Context, backend.ExecutionHandle) (*backend.CancelResult, error) {
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}
func (b *m65Backend) launchSpecs() []backend.AgentExecutionSpec {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]backend.AgentExecutionSpec(nil), b.starts...)
}

func m65Workflow(maxCost int) string {
	return fmt.Sprintf(`apiVersion: fishyume/v1
name: m6-5
defaults:
  agent: {driver: codex, target: local}
execution: {maxConcurrency: 2}
nodes:
  work:
    type: agent
    task: route with accounting
    agent:
      routing:
        schemaVersion: fishyume.routing-requirement/v1
        capabilities: [repo_edit, repo_read, structured_output, tool_use]
        complexity: standard
        quality: balanced
        latency: balanced
        maxCostUnits: %d
        maxContextBytes: 131072
        maxOutputBytes: 32768
        allowModelFallback: true
`, maxCost)
}

func TestM65ApprovedFallbackPersistsRouteAndCumulativeCost(t *testing.T) {
	candidate := &m65Backend{results: []backend.AgentResult{
		{Status: "failed", Summary: "provider unavailable", SideEffectStatus: agent.SideEffectNone},
		{Status: "succeeded", Summary: "fallback succeeded"},
	}}
	state := store.New(t.TempDir())
	service := NewService(candidate, state)
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Content: m65Workflow(101)})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	waitForControllers(t, service)
	if failed.Conclusion != ConclusionFailed {
		t.Fatalf("first conclusion = %q", failed.Conclusion)
	}
	expectedAttempt := 1
	action := &ResumeAction{ActionID: "m6-5-fallback-1", ActionRequestHash: "m6-5-fallback-hash", Type: "retry", NodeID: "work", ExpectedAttempt: &expectedAttempt}
	if _, err := service.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: action}); err != nil {
		t.Fatal(err)
	}
	completed := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool {
		return snapshot.Phase == PhaseCompleted && snapshot.Nodes["work"].CurrentAttempt == 2
	})
	waitForControllers(t, service)
	if completed.Conclusion != ConclusionSucceeded {
		t.Fatalf("fallback conclusion = %q", completed.Conclusion)
	}

	var first, second AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "work", 1, &first); err != nil {
		t.Fatal(err)
	}
	if err := state.ReadAttempt(started.ID, "work", 2, &second); err != nil {
		t.Fatal(err)
	}
	if first.SideEffectStatus != agent.SideEffectNone || first.RoutingUsage == nil || first.RoutingUsage.CumulativeCostUnits != 1 {
		t.Fatalf("first Attempt evidence = %+v", first)
	}
	if second.RoutingDecision == nil || second.RoutingDecision.Selected.Model != "gpt-5.6" || second.RoutingUsage == nil || second.RoutingUsage.RouteIndex != 1 || second.RoutingUsage.CumulativeCostUnits != 101 {
		t.Fatalf("fallback Attempt = %+v", second)
	}
	starts := candidate.launchSpecs()
	if len(starts) != 2 || starts[0].Model != "gpt-5.6-luna" || starts[1].Model != "gpt-5.6" {
		t.Fatalf("launch routes = %+v", starts)
	}
	if _, err := service.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: action}); err != nil {
		t.Fatal(err)
	}
	if len(candidate.launchSpecs()) != 2 {
		t.Fatalf("action replay duplicated fallback launch: %+v", candidate.launchSpecs())
	}
}

func TestM65BudgetRejectsFallbackBeforeActionMutation(t *testing.T) {
	candidate := &m65Backend{results: []backend.AgentResult{{Status: "failed", Summary: "provider unavailable", SideEffectStatus: agent.SideEffectNone}}}
	state := store.New(t.TempDir())
	service := NewService(candidate, state)
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Content: m65Workflow(100)})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	waitForControllers(t, service)
	expectedAttempt := 1
	_, err = service.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: &ResumeAction{Type: "retry", NodeID: "work", ExpectedAttempt: &expectedAttempt}})
	if err == nil || !strings.Contains(err.Error(), "routing_invalid_budget") {
		t.Fatalf("fallback budget error = %v", err)
	}
	current, err := service.Status(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Run.StateVersion != failed.StateVersion || current.Run.Conclusion != ConclusionFailed || len(candidate.launchSpecs()) != 1 {
		t.Fatalf("rejected action mutated state: before=%+v after=%+v starts=%d", failed, current.Run, len(candidate.launchSpecs()))
	}
}

func TestM65UnknownSideEffectRetriesCurrentRouteWithoutFallback(t *testing.T) {
	candidate := &m65Backend{results: []backend.AgentResult{
		{Status: "failed", Summary: "failed after unknown activity", SideEffectStatus: agent.SideEffectUnknown},
		{Status: "succeeded", Summary: "same route retry succeeded"},
	}}
	state := store.New(t.TempDir())
	service := NewService(candidate, state)
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Content: m65Workflow(101)})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	waitForControllers(t, service)
	expectedAttempt := 1
	if _, err := service.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: &ResumeAction{Type: "retry", NodeID: "work", ExpectedAttempt: &expectedAttempt}}); err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool {
		return snapshot.Phase == PhaseCompleted && snapshot.Nodes["work"].CurrentAttempt == 2
	})
	waitForControllers(t, service)
	var second AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "work", 2, &second); err != nil {
		t.Fatal(err)
	}
	if second.RoutingDecision.Selected.Model != "gpt-5.6-luna" || second.RoutingUsage.RouteIndex != 0 || second.RoutingUsage.CumulativeCostUnits != 2 {
		t.Fatalf("unknown-side-effect retry route = %+v", second)
	}
}

func TestM65ReadsM64AttemptWithoutRoutingUsage(t *testing.T) {
	decision, err := resolveAttemptRouting("codex", routing.DefaultRequirementV1())
	if err != nil {
		t.Fatal(err)
	}
	attempt := AttemptSnapshot{Number: 1, Phase: NodePhaseRunning, ResolvedDriver: "codex", ResolvedTarget: "local", LaunchState: LaunchPrepared, RoutingDecision: decision}
	if err := ValidateAttemptSnapshot(attempt); err != nil {
		t.Fatal(err)
	}
	if attempt.RoutingUsage != nil || attempt.SideEffectStatus != "" {
		t.Fatalf("M6.4 Attempt unexpectedly gained M6.5 evidence: %+v", attempt)
	}
}

func TestM65RejectsInconsistentPersistedCumulativeCost(t *testing.T) {
	candidate := &m65Backend{results: []backend.AgentResult{{Status: "succeeded", Summary: "done"}}}
	state := store.New(t.TempDir())
	service := NewService(candidate, state)
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Content: m65Workflow(101)})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	waitForControllers(t, service)
	var attempt AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "work", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	attempt.RoutingUsage.CumulativeCostUnits++
	if err := state.UpdateAttempt(started.ID, "work", 1, attempt); err != nil {
		t.Fatal(err)
	}
	if _, err := service.routingCostBeforeAttempt(started.ID, "work", 2); err == nil || !strings.Contains(err.Error(), "does not match expected cost") {
		t.Fatalf("inconsistent cumulative cost error = %v", err)
	}
}
