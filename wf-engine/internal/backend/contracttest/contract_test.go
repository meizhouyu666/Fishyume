package contracttest

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"wf.local/wf-engine/internal/backend"
)

type fakeBackend struct {
	scenario Scenario
}

func (*fakeBackend) Name() string { return "contract-fixture" }
func (*fakeBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true, SupportsWaitingInput: true, MaxConcurrentAgents: 2, SupportsConcurrentCancel: true}
}
func (b *fakeBackend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: b.Name(), Ready: true}
}
func (b *fakeBackend) Start(_ context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	data, _ := json.Marshal(map[string]any{"scenario": b.scenario, "attempt": spec.Attempt})
	return &backend.ExecutionHandle{Backend: b.Name(), SchemaVersion: 1, ID: fmt.Sprintf("handle-%s", b.scenario), Data: data}, nil
}
func (b *fakeBackend) Observe(context.Context, backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	switch b.scenario {
	case ScenarioActive:
		return &backend.ExecutionObservation{State: backend.ObservationActive}, nil
	case ScenarioWaitingInput:
		return &backend.ExecutionObservation{State: backend.ObservationWaitingInput}, nil
	case ScenarioResultPending:
		return &backend.ExecutionObservation{State: backend.ObservationResultPending}, nil
	case ScenarioTerminalSucceeded:
		return terminal("succeeded"), nil
	case ScenarioTerminalFailed:
		return terminal("failed"), nil
	case ScenarioTerminalIndeterminate:
		return terminal("indeterminate"), nil
	case ScenarioLost:
		return &backend.ExecutionObservation{State: backend.ObservationLost}, nil
	default:
		return &backend.ExecutionObservation{State: backend.ObservationActive}, nil
	}
}
func (*fakeBackend) Output(context.Context, backend.ExecutionHandle, int) (string, error) {
	return "fixture output", nil
}
func (b *fakeBackend) Cancel(context.Context, backend.ExecutionHandle) (*backend.CancelResult, error) {
	if b.scenario == ScenarioCancelNotConfirmed {
		return &backend.CancelResult{State: backend.CancelNotConfirmed, Diagnostic: "fixture did not confirm"}, nil
	}
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}

func terminal(status string) *backend.ExecutionObservation {
	return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{Status: status, Summary: status + " fixture"}}
}

func TestRunContract(t *testing.T) {
	Run(t, func(t *testing.T, scenario Scenario) Fixture {
		t.Helper()
		return Fixture{
			Backend: &fakeBackend{scenario: scenario},
			Spec: backend.AgentExecutionSpec{
				RunID: "run-1", NodeID: "agent-1", Attempt: 1, Workspace: t.TempDir(), Tool: "codex", Runtime: "local", Instructions: "do work",
			},
		}
	})
}
