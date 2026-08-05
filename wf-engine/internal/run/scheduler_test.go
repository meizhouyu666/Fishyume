package run

import (
	"reflect"
	"testing"

	"wf.local/wf-engine/internal/workflow"
)

func TestPlanScheduleIsDeterministicAndBounded(t *testing.T) {
	doc := workflow.Document{
		APIVersion: workflow.APIVersion, Name: "parallel", Execution: workflow.Execution{MaxConcurrency: 2},
		Nodes: map[string]workflow.Node{
			"z":       {Type: "agent", Task: "z"},
			"a":       {Type: "agent", Task: "a"},
			"approve": {Type: "approval", Prompt: "approve"},
		},
	}
	order, err := workflow.Validate(doc)
	if err != nil {
		t.Fatal(err)
	}
	normalized := workflow.Normalized{Document: doc, TopologicalOrder: order}
	nodes := []NodeSnapshot{
		{ID: "a", Type: "agent", Phase: NodePhasePending},
		{ID: "approve", Type: "approval", Phase: NodePhasePending},
		{ID: "z", Type: "agent", Phase: NodePhasePending},
	}
	decision, err := PlanSchedule(normalized, WorkflowSnapshot{}, nodes, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decision.ReadyAgents, []string{"a", "z"}) || !reflect.DeepEqual(decision.ReadyApprovals, []string{"approve"}) {
		t.Fatalf("decision=%+v", decision)
	}
	if decision.EffectiveLimit != 2 || decision.AvailableAgents != 0 {
		t.Fatalf("capacity=%+v", decision)
	}
	limited, err := PlanSchedule(normalized, WorkflowSnapshot{}, nodes, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(limited.ReadyAgents, []string{"a"}) || limited.EffectiveLimit != 1 {
		t.Fatalf("Backend-limited decision=%+v", limited)
	}
}

func TestPlanSchedulePreservesSingleConcurrencyOrder(t *testing.T) {
	doc := workflow.Document{APIVersion: workflow.APIVersion, Name: "single", Execution: workflow.Execution{MaxConcurrency: 1}, Nodes: map[string]workflow.Node{
		"z": {Type: "agent", Task: "z"}, "a": {Type: "agent", Task: "a"},
	}}
	order, err := workflow.Validate(doc)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := PlanSchedule(workflow.Normalized{Document: doc, TopologicalOrder: order}, WorkflowSnapshot{}, []NodeSnapshot{
		{ID: "a", Type: "agent", Phase: NodePhasePending}, {ID: "z", Type: "agent", Phase: NodePhasePending},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decision.ReadyAgents, []string{"a"}) {
		t.Fatalf("ready=%v", decision.ReadyAgents)
	}
}

func TestPlanScheduleStopsAfterFailureAndMarksDescendants(t *testing.T) {
	doc := workflow.Document{APIVersion: workflow.APIVersion, Name: "failure", Execution: workflow.Execution{MaxConcurrency: 3}, Nodes: map[string]workflow.Node{
		"failed":     {Type: "agent", Task: "fail"},
		"sibling":    {Type: "agent", Task: "sibling"},
		"descendant": {Type: "agent", DependsOn: []string{"failed"}, Task: "descendant"},
	}}
	order, err := workflow.Validate(doc)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := PlanSchedule(workflow.Normalized{Document: doc, TopologicalOrder: order}, WorkflowSnapshot{}, []NodeSnapshot{
		{ID: "failed", Type: "agent", Phase: NodePhaseCompleted, Conclusion: ConclusionFailed},
		{ID: "sibling", Type: "agent", Phase: NodePhasePending},
		{ID: "descendant", Type: "agent", Phase: NodePhasePending},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.StopScheduling || len(decision.ReadyAgents) != 0 || !reflect.DeepEqual(decision.SkipUpstream, []string{"descendant"}) {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestPlanScheduleAllowsExplicitEligibleFailureBranch(t *testing.T) {
	doc := workflow.Document{APIVersion: workflow.APIVersion, Name: "failure-branch", Execution: workflow.Execution{MaxConcurrency: 3}, Nodes: map[string]workflow.Node{
		"failed":     {Type: "agent", Task: "fail"},
		"handler":    {Type: "agent", DependsOn: []string{"failed"}, When: &workflow.Condition{Node: "failed", Field: "result.reason", Equals: "failed"}, Task: "handle"},
		"descendant": {Type: "agent", DependsOn: []string{"failed"}, Task: "descendant"},
		"sibling":    {Type: "agent", Task: "sibling"},
	}}
	order, err := workflow.Validate(doc)
	if err != nil {
		t.Fatal(err)
	}
	failedResult := workflow.Result{Reason: "failed"}
	decision, err := PlanSchedule(workflow.Normalized{Document: doc, TopologicalOrder: order}, WorkflowSnapshot{}, []NodeSnapshot{
		{ID: "failed", Type: "agent", Phase: NodePhaseCompleted, Conclusion: ConclusionFailed, Result: &failedResult},
		{ID: "handler", Type: "agent", Phase: NodePhasePending},
		{ID: "descendant", Type: "agent", Phase: NodePhasePending},
		{ID: "sibling", Type: "agent", Phase: NodePhasePending},
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !decision.StopScheduling || !reflect.DeepEqual(decision.ReadyAgents, []string{"handler"}) || !reflect.DeepEqual(decision.SkipUpstream, []string{"descendant"}) || !reflect.DeepEqual(decision.SkipFailurePolicy, []string{"sibling"}) {
		t.Fatalf("decision=%+v", decision)
	}
}

func TestPlanScheduleRejectsInvalidBackendLimit(t *testing.T) {
	doc := workflow.Document{APIVersion: workflow.APIVersion, Name: "invalid", Execution: workflow.Execution{MaxConcurrency: 1}, Nodes: map[string]workflow.Node{"a": {Type: "agent", Task: "a"}}}
	if _, err := PlanSchedule(workflow.Normalized{Document: doc, TopologicalOrder: []string{"a"}}, WorkflowSnapshot{}, []NodeSnapshot{{ID: "a", Type: "agent", Phase: NodePhasePending}}, -1); err == nil {
		t.Fatal("accepted negative Backend limit")
	}
}

func TestEffectiveConcurrencyUsesPlatformNeutralLimits(t *testing.T) {
	for _, test := range []struct {
		workflow, backend, want int
	}{
		{workflow: 1, backend: 0, want: 1},
		{workflow: 2, backend: 0, want: 2},
		{workflow: 10, backend: 3, want: 3},
		{workflow: 32, backend: 0, want: FishyumeSafetyConcurrencyCeiling},
	} {
		got, err := EffectiveConcurrency(test.workflow, test.backend)
		if err != nil || got != test.want {
			t.Fatalf("effective(%d,%d)=%d err=%v want=%d", test.workflow, test.backend, got, err, test.want)
		}
	}
	if _, err := EffectiveConcurrency(2, -1); err == nil {
		t.Fatal("accepted negative Backend limit")
	}
}
