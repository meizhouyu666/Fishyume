package run

import (
	"context"
	"fmt"
	"testing"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/store"
)

type dynamicGateCatalog struct {
	*routing.CatalogRegistry
	denied map[string]bool
}

func (c *dynamicGateCatalog) EnsureTargetAvailable(_ context.Context, target routing.Target) error {
	if c.denied[target.Model] {
		return fmt.Errorf("model %s is unavailable", target.Model)
	}
	return nil
}

func newM78FallbackService(t *testing.T, candidate *m65Backend, catalogs routing.CatalogProvider) (*Service, *store.Store) {
	t.Helper()
	registry := backend.NewRegistry()
	if err := registry.Register(candidate); err != nil {
		t.Fatal(err)
	}
	state := store.New(t.TempDir())
	return NewServiceWithRegistryAndCatalogs(registry, "codex", catalogs, state), state
}

func TestM78DynamicFallbackRequiresClassifiedPreExecutionFailure(t *testing.T) {
	candidate := &m65Backend{results: []backend.AgentResult{
		{Status: "failed", Summary: "generic failure", SideEffectStatus: agent.SideEffectNone},
		{Status: "succeeded", Summary: "same route retry succeeded"},
	}}
	catalogs := &dynamicGateCatalog{CatalogRegistry: routing.BuiltinCatalogRegistry(), denied: map[string]bool{}}
	service, state := newM78FallbackService(t, candidate, catalogs)
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
	if second.RoutingDecision == nil || second.RoutingDecision.Selected.Model != "gpt-5.6-luna" || second.RoutingUsage == nil || second.RoutingUsage.RouteIndex != 0 {
		t.Fatalf("unclassified failure advanced fallback: %+v", second)
	}
}

func TestM78ClassifiedFallbackMustPassCurrentAvailabilityGate(t *testing.T) {
	candidate := &m65Backend{results: []backend.AgentResult{{
		Status: "failed", Summary: "model unavailable before execution", SideEffectStatus: agent.SideEffectNone,
		FailureClass: backend.FailureModelUnavailablePreExecution,
	}}}
	catalogs := &dynamicGateCatalog{CatalogRegistry: routing.BuiltinCatalogRegistry(), denied: map[string]bool{}}
	service, _ := newM78FallbackService(t, candidate, catalogs)
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Content: m65Workflow(101)})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	waitForControllers(t, service)
	catalogs.denied["gpt-5.6"] = true
	expectedAttempt := 1
	if _, err := service.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: &ResumeAction{Type: "retry", NodeID: "work", ExpectedAttempt: &expectedAttempt}}); err == nil {
		t.Fatal("expected unavailable fallback target to be rejected")
	}
	current, err := service.Status(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Run.StateVersion != failed.StateVersion || len(candidate.launchSpecs()) != 1 {
		t.Fatalf("rejected fallback mutated run: before=%+v after=%+v launches=%d", failed, current.Run, len(candidate.launchSpecs()))
	}
}
