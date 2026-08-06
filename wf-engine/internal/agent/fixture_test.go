package agent

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestContractsV1Fixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "contracts-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		AttemptEnvelope AttemptEnvelope `json:"attemptEnvelope"`
		DriverEvents    []DriverEvent   `json:"driverEvents"`
		AgentResults    []AgentResult   `json:"agentResults"`
		ExecutionHandle ExecutionHandle `json:"executionHandle"`
		CancelResults   []CancelResult  `json:"cancelResults"`
		IPCHandshake    IPCHandshake    `json:"ipcHandshake"`
		APIError        APIError        `json:"apiError"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAttemptEnvelope(fixture.AttemptEnvelope); err != nil {
		t.Fatal(err)
	}
	for _, event := range fixture.DriverEvents {
		if err := ValidateObservation(ExecutionObservation{State: ObservationActive, Events: []DriverEvent{event}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, result := range fixture.AgentResults {
		if err := ValidateAgentResult(result); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateExecutionHandle(fixture.ExecutionHandle); err != nil {
		t.Fatal(err)
	}
	for _, result := range fixture.CancelResults {
		if err := ValidateCancelResult(result); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateIPCHandshake(fixture.IPCHandshake); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAPIError(fixture.APIError); err != nil {
		t.Fatal(err)
	}
}
