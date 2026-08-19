package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

const validYAML = `apiVersion: wf/v1
name: sample
inputs:
  goal:
    required: true
defaults:
  tool: codex
  runtime: local
execution:
  maxConcurrency: 1
nodes:
  plan:
    type: agent
    task: "Plan {{ inputs.goal }}"
  approve:
    type: approval
    dependsOn: [plan]
    prompt: "Approve {{ nodes.plan.result.summary }}"
  implement:
    type: agent
    dependsOn: [approve]
    when:
      node: approve
      field: result.decision
      equals: approved
    task: "Implement {{ nodes.plan.result.summary }} {{ nodes.plan.result.artifacts }}"
`

const validJSON = `{
  "apiVersion":"wf/v1","name":"sample",
  "inputs":{"goal":{"required":true}},
  "defaults":{"tool":"codex","runtime":"local"},
  "execution":{"maxConcurrency":1},
  "nodes":{
    "implement":{"type":"agent","dependsOn":["approve"],"when":{"node":"approve","field":"result.decision","equals":"approved"},"task":"Implement {{ nodes.plan.result.summary }} {{ nodes.plan.result.artifacts }}"},
    "approve":{"type":"approval","dependsOn":["plan"],"prompt":"Approve {{ nodes.plan.result.summary }}"},
    "plan":{"type":"agent","task":"Plan {{ inputs.goal }}"}
  }
}`

func TestYAMLAndJSONNormalizeEquivalently(t *testing.T) {
	yamlWorkflow, err := Parse([]byte(validYAML), "workflow.yaml", map[string]any{"goal": "ship it"})
	if err != nil {
		t.Fatal(err)
	}
	jsonWorkflow, err := Parse([]byte(validJSON), "workflow.json", map[string]any{"goal": "ship it"})
	if err != nil {
		t.Fatal(err)
	}
	yamlData, _ := yamlWorkflow.CanonicalJSON()
	jsonData, _ := jsonWorkflow.CanonicalJSON()
	if string(yamlData) != string(jsonData) {
		t.Fatalf("normalized workflows differ\nYAML: %s\nJSON: %s", yamlData, jsonData)
	}
	want := []string{"plan", "approve", "implement"}
	if strings.Join(yamlWorkflow.TopologicalOrder, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", yamlWorkflow.TopologicalOrder, want)
	}
	if yamlWorkflow.Document.APIVersion != APIVersion {
		t.Fatalf("legacy schema was not normalized: %q", yamlWorkflow.Document.APIVersion)
	}
}

func TestFishyumeSchemaIsAccepted(t *testing.T) {
	doc := strings.Replace(validYAML, "apiVersion: wf/v1", "apiVersion: fishyume/v1", 1)
	parsed, err := Parse([]byte(doc), "workflow.yaml", map[string]any{"goal": "ship it"})
	if err != nil || parsed.Document.APIVersion != APIVersion {
		t.Fatalf("fishyume schema parse = %#v, err=%v", parsed.Document, err)
	}
}

func TestContextPolicyV2IsExplicitAndBindingsAreBounded(t *testing.T) {
	doc := `apiVersion: fishyume/v2
name: context-policy
context:
  projectInstructions: [AGENTS.md]
execution:
  maxConcurrency: 1
nodes:
  plan:
    type: agent
    task: plan
  implement:
    type: agent
    dependsOn: [plan]
    context:
      dependencies: [plan]
    task: implement`
	parsed, err := Parse([]byte(doc), "workflow.yaml", nil)
	if err != nil || parsed.ContextPolicyVersion != "context-policy/v1" {
		t.Fatalf("parse v2 = %#v, err=%v", parsed, err)
	}
	if got := EffectiveContextPolicy(parsed.Document, parsed.Document.Nodes["implement"]); len(got.Dependencies) != 1 || got.Dependencies[0] != "plan" {
		t.Fatalf("effective policy = %+v", got)
	}
	bindings := ContextBindings{MemoryByNode: map[string][]MemoryBinding{"implement": {{ID: "memory-one", Reason: "known convention"}}}}
	if err := ValidateContextBindings(parsed.Document, bindings); err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(doc, "fishyume/v2", "fishyume/v1", 1)
	if _, err := Parse([]byte(legacy), "workflow.yaml", nil); err == nil || !strings.Contains(err.Error(), "context policy requires") {
		t.Fatalf("legacy context policy err=%v", err)
	}
}

func TestWorkflowValidationFailures(t *testing.T) {
	tests := []struct {
		name    string
		doc     string
		wantErr string
	}{
		{"duplicate key", strings.Replace(validYAML, "name: sample", "name: sample\nname: duplicate", 1), "duplicate YAML key"},
		{"cycle", strings.Replace(validYAML, "type: agent\n    task: \"Plan", "type: agent\n    dependsOn: [implement]\n    task: \"Plan", 1), "dependency cycle"},
		{"missing dependency", strings.Replace(validYAML, "dependsOn: [plan]", "dependsOn: [missing]", 1), "missing node"},
		{"concurrency", strings.Replace(validYAML, "maxConcurrency: 1", "maxConcurrency: 33", 1), "maxConcurrency"},
		{"missing concurrency", strings.Replace(validYAML, "execution:\n  maxConcurrency: 1\n", "", 1), "maxConcurrency"},
		{"invalid tool", strings.Replace(validYAML, "tool: codex", "tool: unknown", 1), "unsupported tool"},
		{"non ancestor reference", strings.Replace(validYAML, "Plan {{ inputs.goal }}", "Plan {{ nodes.implement.result.summary }}", 1), "not an ancestor"},
		{"expression", strings.Replace(validYAML, "Plan {{ inputs.goal }}", "Plan {{ inputs.goal | upper }}", 1), "unsupported template expression"},
		{"unmatched close", strings.Replace(validYAML, "Plan {{ inputs.goal }}", "Plan inputs.goal }}", 1), "closing delimiter"},
		{"invalid condition", strings.Replace(validYAML, "node: approve\n      field", "node: approve\n      all: []\n      field", 1), "exactly one"},
		{"unknown field", strings.Replace(validYAML, "task: \"Plan", "mystery: true\n    task: \"Plan", 1), "unknown field"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.doc), "workflow.yaml", map[string]any{"goal": "x"})
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestWorkflowAcceptsBoundedParallelConcurrency(t *testing.T) {
	doc := strings.Replace(validYAML, "maxConcurrency: 1", "maxConcurrency: 2", 1)
	parsed, err := Parse([]byte(doc), "workflow.yaml", map[string]any{"goal": "parallel"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Document.Execution.MaxConcurrency != 2 {
		t.Fatalf("maxConcurrency=%d", parsed.Document.Execution.MaxConcurrency)
	}
}

func TestInputsAndRenderingAreStrictAndDeterministic(t *testing.T) {
	parsed, err := Parse([]byte(validYAML), "workflow.yaml", map[string]any{"goal": "ship it"})
	if err != nil {
		t.Fatal(err)
	}
	node := parsed.Document.Nodes["implement"]
	template, err := ParseTemplate(node.Task, parsed.Document.Inputs, ancestors(parsed.Document, "implement"))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := template.Render(parsed.Inputs, map[string]Result{
		"plan":    {Summary: "stable plan", Artifacts: []string{"a.go", "b.json"}},
		"approve": {Decision: "approved"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rendered != `Implement stable plan ["a.go","b.json"]` {
		t.Fatalf("rendered = %q", rendered)
	}
	if _, err := template.Render(parsed.Inputs, map[string]Result{}); err == nil {
		t.Fatal("expected missing node result to fail")
	}
	if _, err := Parse([]byte(validYAML), "workflow.yaml", nil); err == nil || !strings.Contains(err.Error(), "required input") {
		t.Fatalf("missing input error = %v", err)
	}
	if _, err := Parse([]byte(validYAML), "workflow.yaml", map[string]any{"goal": []string{"not", "scalar"}}); err == nil {
		t.Fatal("expected unsupported input type")
	}
}

func TestStableTieBreakAndConditions(t *testing.T) {
	doc := `apiVersion: wf/v1
name: order
execution: {maxConcurrency: 1}
nodes:
  z: {type: agent, task: z}
  a: {type: agent, task: a}
  finish: {type: agent, dependsOn: [z, a], task: finish}
`
	parsed, err := Parse([]byte(doc), "x.yaml", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(parsed.TopologicalOrder, ","); got != "a,z,finish" {
		t.Fatalf("order = %s", got)
	}
	condition := Condition{All: []Condition{
		{Node: "approval", Field: "result.decision", Equals: "approved"},
		{Not: &Condition{Node: "agent", Field: "result.summary", Equals: "bad"}},
	}}
	ok, err := Evaluate(condition, map[string]Result{
		"approval": {Decision: "approved"}, "agent": {Summary: "good"},
	})
	if err != nil || !ok {
		t.Fatalf("condition = %v, err=%v", ok, err)
	}
}

func TestNumericDefaultsCanonicalize(t *testing.T) {
	doc := `apiVersion: wf/v1
name: numbers
inputs: {count: {default: 1}}
execution: {maxConcurrency: 1}
nodes: {a: {type: agent, task: "{{ inputs.count }}"}}
`
	parsed, err := Parse([]byte(doc), "x.yaml", nil)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(parsed.Inputs)
	if err != nil || string(data) != `{"count":1}` {
		t.Fatalf("inputs = %s, err=%v", data, err)
	}
}
