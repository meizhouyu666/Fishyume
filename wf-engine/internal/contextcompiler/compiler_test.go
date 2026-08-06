package contextcompiler

import (
	"encoding/json"
	"strings"
	"testing"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/workflow"
)

func TestCompileIsDeterministicAndHashesCanonicalComponents(t *testing.T) {
	input := Input{
		Identity:        agent.AttemptIdentity{RunID: "run-1", NodeID: "implement", Attempt: 2},
		Workspace:       "workspace",
		Task:            "implement",
		AncestorResults: map[string]workflow.Result{"z": {Summary: "last"}, "a": {Summary: "first"}},
		RequiredSkills:  []string{"review", "go"},
		Constraints:     map[string]string{"sandbox": "workspace-write", "interaction": "none"},
		Budget:          map[string]int64{"maxSeconds": 600},
		ResultSchema:    json.RawMessage(`{"type":"object"}`),
		ResultMaxBytes:  agent.MaxResultBytes,
	}
	first, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash != second.Hash || first.Prompt != second.Prompt {
		t.Fatalf("compiler is not deterministic: %#v %#v", first, second)
	}
	if first.Envelope.Context.UpstreamResults[0].NodeID != "a" || first.Envelope.Context.RequiredSkills[0] != "go" {
		t.Fatalf("components are not canonical: %+v", first.Envelope.Context)
	}
	if strings.Contains(first.Hash, first.Prompt) || first.Manifest.CompilerVersion != Version {
		t.Fatalf("invalid compilation metadata: %+v", first)
	}

	input.Task = "implement safely"
	changed, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	if changed.Hash == first.Hash {
		t.Fatal("context hash did not change with the task")
	}
}

func TestCompileLimitsFinalWrappedPromptAtExactBoundary(t *testing.T) {
	input := Input{
		Identity: agent.AttemptIdentity{RunID: "run-1", NodeID: "n", Attempt: 1}, Workspace: "workspace", Task: "x",
		AncestorResults: map[string]workflow.Result{}, RequiredSkills: []string{}, Constraints: map[string]string{}, Budget: map[string]int64{}, ResultMaxBytes: agent.MaxResultBytes,
	}
	base, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Task = strings.Repeat("x", 1+MaxCompiledContextBytes-len(base.Prompt))
	boundary, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(boundary.Prompt) != MaxCompiledContextBytes {
		t.Fatalf("prompt bytes=%d want=%d", len(boundary.Prompt), MaxCompiledContextBytes)
	}

	input.Task += "x"
	_, err = Compile(input)
	if err == nil || !strings.Contains(err.Error(), "compiled context exceeds") {
		t.Fatalf("error=%v", err)
	}
	wrapperBytes := len(promptPrefix) + len(promptSuffix)
	if wrapperBytes < 2 || len(boundary.Prompt)-wrapperBytes+1 > MaxCompiledContextBytes {
		t.Fatalf("overflow must be caused by wrapper bytes=%d", wrapperBytes)
	}
}

func TestCompileDoesNotSerializeCompiledPromptIntoEnvelope(t *testing.T) {
	compiled, err := Compile(Input{
		Identity: agent.AttemptIdentity{RunID: "run-1", NodeID: "n", Attempt: 1}, Workspace: "workspace", Task: "secret task",
		AncestorResults: map[string]workflow.Result{}, RequiredSkills: []string{}, Constraints: map[string]string{}, Budget: map[string]int64{}, ResultMaxBytes: agent.MaxResultBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(compiled.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "Fishyume headless Attempt envelope") {
		t.Fatalf("compiled prompt persisted in envelope: %s", encoded)
	}
}
