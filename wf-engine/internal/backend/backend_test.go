package backend

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateAgentExecutionSpec(t *testing.T) {
	valid := AgentExecutionSpec{
		RunID: "run-1", NodeID: "plan", Attempt: 1, Workspace: `C:\project`, Tool: "codex", Runtime: "local",
		Instructions: "plan work", ResultContract: ResultContract{Schema: json.RawMessage(`{"type":"object"}`), MaxBytes: 1024},
	}
	if err := ValidateAgentExecutionSpec(valid); err != nil {
		t.Fatal(err)
	}
	invalid := valid
	invalid.Attempt = 0
	if err := ValidateAgentExecutionSpec(invalid); err == nil {
		t.Fatal("accepted non-positive Attempt")
	}
	invalid = valid
	invalid.ResultContract.Schema = json.RawMessage(`{`)
	if err := ValidateAgentExecutionSpec(invalid); err == nil {
		t.Fatal("accepted invalid result schema")
	}
}

func TestValidateExecutionHandle(t *testing.T) {
	valid := ExecutionHandle{Backend: "direct", SchemaVersion: 1, ID: "pid-1", Data: json.RawMessage(`{"pid":123}`)}
	if err := ValidateExecutionHandle(valid); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*ExecutionHandle){
		"backend": func(handle *ExecutionHandle) { handle.Backend = "" },
		"version": func(handle *ExecutionHandle) { handle.SchemaVersion = 0 },
		"id":      func(handle *ExecutionHandle) { handle.ID = "" },
		"json":    func(handle *ExecutionHandle) { handle.Data = json.RawMessage(`[]`) },
		"size": func(handle *ExecutionHandle) {
			handle.Data = json.RawMessage(`{"value":"` + strings.Repeat("x", MaxExecutionHandleDataSize) + `"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := valid
			mutate(&candidate)
			if err := ValidateExecutionHandle(candidate); err == nil {
				t.Fatalf("accepted invalid handle: %+v", candidate)
			}
		})
	}
}

func TestValidateAgentResultAndObservation(t *testing.T) {
	result := AgentResult{Status: "succeeded", Summary: "done"}
	if err := ValidateAgentResult(result); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExecutionObservation(ExecutionObservation{State: ObservationTerminal, Result: &result}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExecutionObservation(ExecutionObservation{State: ObservationTerminal}); err == nil {
		t.Fatal("accepted terminal observation without result")
	}
	if err := ValidateExecutionObservation(ExecutionObservation{State: ObservationActive, Result: &result}); err == nil {
		t.Fatal("accepted active observation with terminal result")
	}
	result.Status = "completion_missing"
	if err := ValidateAgentResult(result); err == nil {
		t.Fatal("accepted platform waiting state as Agent result")
	}
	result = AgentResult{Status: "succeeded", Summary: "done", Questions: []InputQuestion{{ID: "approval", Prompt: "Proceed?", Required: true}}}
	if err := ValidateAgentResult(result); err == nil || !strings.Contains(err.Error(), "only needs_input") {
		t.Fatalf("accepted questions on succeeded result: %v", err)
	}
	result.Status = "needs_input"
	if err := ValidateAgentResult(result); err != nil {
		t.Fatal(err)
	}
	result.Questions[0].ID = ""
	if err := ValidateAgentResult(result); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("accepted malformed question: %v", err)
	}
	result.Questions = []InputQuestion{{ID: "approval", Prompt: "Proceed?", Required: true}, {ID: "approval", Prompt: "Really proceed?", Required: true}}
	if err := ValidateAgentResult(result); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("accepted duplicate question IDs: %v", err)
	}
}

func TestValidateCancelResult(t *testing.T) {
	for _, state := range []CancelState{CancelConfirmed, CancelNotConfirmed} {
		if err := ValidateCancelResult(CancelResult{State: state}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateCancelResult(CancelResult{State: "failed"}); err == nil {
		t.Fatal("accepted unsupported cancel state")
	}
}
