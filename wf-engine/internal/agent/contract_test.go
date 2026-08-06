package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func validEnvelope() AttemptEnvelope {
	return AttemptEnvelope{
		ProtocolVersion: ProtocolVersion,
		Identity:        AttemptIdentity{RunID: "run-1", NodeID: "implement", Attempt: 1},
		Workspace:       "workspace",
		Target:          "local",
		Task:            "implement the approved change",
		Context:         AttemptContext{UpstreamResults: []UpstreamResult{}, RequiredSkills: []string{}},
		Constraints:     map[string]string{},
		Budget:          map[string]int64{},
		ResultContract:  ResultContract{Schema: json.RawMessage(`{"type":"object"}`), MaxBytes: MaxResultBytes},
	}
}

func TestDriverCapabilitiesValidation(t *testing.T) {
	valid := DriverCapabilities{Targets: []string{"local"}, SupportsConfirmedCancel: true, SupportsConcurrentCancel: true}
	if err := ValidateCapabilities(valid); err != nil {
		t.Fatal(err)
	}
	invalid := []DriverCapabilities{
		{},
		{Targets: []string{"local", "local"}},
		{Targets: []string{"local"}, MaxConcurrentAttempts: -1},
		{Targets: []string{"local"}, SupportsConcurrentCancel: true},
	}
	for _, candidate := range invalid {
		if err := ValidateCapabilities(candidate); err == nil {
			t.Fatalf("capabilities accepted: %+v", candidate)
		}
	}
}

func TestAttemptEnvelopeValidation(t *testing.T) {
	envelope := validEnvelope()
	if err := ValidateAttemptEnvelope(envelope); err != nil {
		t.Fatal(err)
	}
	envelope.ProtocolVersion++
	if err := ValidateAttemptEnvelope(envelope); err == nil || !strings.Contains(err.Error(), "protocol version") {
		t.Fatalf("error=%v", err)
	}
}

func TestAgentResultNeedsInputContract(t *testing.T) {
	result := AgentResult{Status: "needs_input", Summary: "approval required", Questions: []InputQuestion{{ID: "risk", Prompt: "Proceed?", Required: true}}}
	if err := ValidateAgentResult(result); err != nil {
		t.Fatal(err)
	}
	result.Questions = nil
	if err := ValidateAgentResult(result); err == nil {
		t.Fatal("needs_input without questions was accepted")
	}
	result.Questions = []InputQuestion{{ID: "risk", Prompt: "Proceed?", Required: true}, {ID: "risk", Prompt: "Really proceed?", Required: true}}
	if err := ValidateAgentResult(result); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate question IDs were accepted: %v", err)
	}
	oversized := AgentResult{Status: "failed", Summary: strings.Repeat("x", 17*1024)}
	if err := ValidateAgentResult(oversized); err == nil {
		t.Fatal("oversized result was accepted")
	}
}

func TestExecutionHandleAndObservationValidation(t *testing.T) {
	handle := ExecutionHandle{Driver: "codex", Target: "local", SchemaVersion: 1, ID: "exec-1", Data: json.RawMessage(`{}`)}
	if err := ValidateExecutionHandle(handle); err != nil {
		t.Fatal(err)
	}
	result := &AgentResult{Status: "succeeded", Summary: "done"}
	observation := ExecutionObservation{State: ObservationTerminal, Result: result, Events: []DriverEvent{{Type: EventAttemptCompleted, Result: result}}}
	if err := ValidateObservation(observation); err != nil {
		t.Fatal(err)
	}
	invalidEvent := ExecutionObservation{State: ObservationActive, Events: []DriverEvent{{Type: EventAttemptCompleted}}}
	if err := ValidateObservation(invalidEvent); err == nil || !strings.Contains(err.Error(), "requires an Agent result") {
		t.Fatalf("invalid completed event error=%v", err)
	}
}
