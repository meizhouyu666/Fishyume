package workflow

import (
	"reflect"
	"strings"
	"testing"

	"wf.local/wf-engine/internal/routing"
)

func TestRoutingRequirementCompatibilityDefaultAndNodeOverride(t *testing.T) {
	nodeRequirement := routing.DefaultRequirementV1()
	nodeRequirement.Complexity = routing.ComplexityComplex
	nodeRequirement.Quality = routing.QualityPremium
	nodeRequirement.Candidates = []string{"codex/local/gpt-5.6", "codex/local/gpt-5.6-luna"}
	doc := Document{
		APIVersion: ContextPolicyAPIVersion,
		Name:       "routing",
		Defaults:   Defaults{Agent: AgentSelection{Driver: "codex", Target: "local"}},
		Execution:  Execution{MaxConcurrency: 1},
		Nodes: map[string]Node{
			"legacy":  {Type: "agent", Task: "Read"},
			"special": {Type: "agent", Task: "Edit", DependsOn: []string{"legacy"}, Agent: AgentSelection{Routing: &nodeRequirement}},
		},
	}
	if _, err := Validate(doc); err != nil {
		t.Fatal(err)
	}
	legacy := EffectiveRoutingRequirement(doc, doc.Nodes["legacy"])
	if !reflect.DeepEqual(legacy, routing.DefaultRequirementV1()) {
		t.Fatalf("legacy default = %+v", legacy)
	}
	effective := EffectiveRoutingRequirement(doc, doc.Nodes["special"])
	if effective.Complexity != routing.ComplexityComplex || effective.Quality != routing.QualityPremium || len(effective.Candidates) != 2 {
		t.Fatalf("node override = %+v", effective)
	}
	effective.Candidates[0] = "mutated"
	if doc.Nodes["special"].Agent.Routing.Candidates[0] == "mutated" {
		t.Fatal("effective routing leaked a mutable candidate slice")
	}
}

func TestRoutingRequirementWorkflowDefaultAppliesToAgentNodes(t *testing.T) {
	requirement := routing.DefaultRequirementV1()
	requirement.Latency = routing.LatencyFast
	requirement.Candidates = []string{"codex/local/gpt-5.6-luna"}
	doc := routingTestDocument()
	doc.Defaults.Agent = AgentSelection{Driver: "codex", Target: "local", Routing: &requirement}
	if _, err := Validate(doc); err != nil {
		t.Fatal(err)
	}
	effective := EffectiveRoutingRequirement(doc, doc.Nodes["agent"])
	if effective.Latency != routing.LatencyFast || !reflect.DeepEqual(effective.Candidates, requirement.Candidates) {
		t.Fatalf("workflow default routing = %+v", effective)
	}
}

func TestRoutingRequirementValidationRejectsMalformedDeclarationsAndApprovalRouting(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Document)
	}{
		{name: "missing schema version", edit: func(doc *Document) {
			requirement := routing.DefaultRequirementV1()
			requirement.SchemaVersion = ""
			node := doc.Nodes["agent"]
			node.Agent.Routing = &requirement
			doc.Nodes["agent"] = node
		}},
		{name: "unsorted capabilities", edit: func(doc *Document) {
			requirement := routing.DefaultRequirementV1()
			requirement.Capabilities = []routing.Capability{routing.CapabilityRepoRead, routing.CapabilityRepoEdit}
			node := doc.Nodes["agent"]
			node.Agent.Routing = &requirement
			doc.Nodes["agent"] = node
		}},
		{name: "approval routing", edit: func(doc *Document) {
			requirement := routing.DefaultRequirementV1()
			node := doc.Nodes["approval"]
			node.Agent.Routing = &requirement
			doc.Nodes["approval"] = node
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			doc := routingTestDocument()
			test.edit(&doc)
			if _, err := Validate(doc); err == nil {
				t.Fatal("malformed routing declaration was accepted")
			} else if !strings.Contains(err.Error(), "routing") {
				t.Fatalf("error = %v, want routing context", err)
			}
		})
	}
}

func routingTestDocument() Document {
	return Document{
		APIVersion: ContextPolicyAPIVersion,
		Name:       "routing-test",
		Execution:  Execution{MaxConcurrency: 1},
		Nodes: map[string]Node{
			"agent":    {Type: "agent", Task: "Edit"},
			"approval": {Type: "approval", DependsOn: []string{"agent"}, Prompt: "Approve?"},
		},
	}
}
