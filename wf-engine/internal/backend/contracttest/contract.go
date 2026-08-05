package contracttest

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"wf.local/wf-engine/internal/backend"
)

type Scenario string

const (
	ScenarioActive                Scenario = "active"
	ScenarioWaitingInput          Scenario = "waiting-input"
	ScenarioResultPending         Scenario = "result-pending"
	ScenarioTerminalSucceeded     Scenario = "terminal-succeeded"
	ScenarioTerminalFailed        Scenario = "terminal-failed"
	ScenarioTerminalIndeterminate Scenario = "terminal-indeterminate"
	ScenarioLost                  Scenario = "lost"
	ScenarioCancelConfirmed       Scenario = "cancel-confirmed"
	ScenarioCancelNotConfirmed    Scenario = "cancel-not-confirmed"
)

type Fixture struct {
	Backend backend.AgentBackend
	Spec    backend.AgentExecutionSpec
}

type Factory func(*testing.T, Scenario) Fixture

func Run(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("doctor", func(t *testing.T) {
		fixture := factory(t, ScenarioActive)
		report := fixture.Backend.Doctor(context.Background(), backend.DoctorRequest{
			Workspace: fixture.Spec.Workspace, Tool: fixture.Spec.Tool, Runtime: fixture.Spec.Runtime,
		})
		if !report.Ready || report.Backend != fixture.Backend.Name() {
			t.Fatalf("doctor report=%+v", report)
		}
	})

	t.Run("start-handle-round-trip", func(t *testing.T) {
		fixture := factory(t, ScenarioActive)
		if err := backend.ValidateAgentExecutionSpec(fixture.Spec); err != nil {
			t.Fatalf("invalid fixture spec: %v", err)
		}
		handle, err := fixture.Backend.Start(context.Background(), fixture.Spec)
		if err != nil {
			t.Fatal(err)
		}
		if handle == nil {
			t.Fatal("Start returned a nil handle")
		}
		if err := backend.ValidateExecutionHandle(*handle); err != nil {
			t.Fatalf("invalid handle: %v", err)
		}
		data, err := json.Marshal(handle)
		if err != nil {
			t.Fatal(err)
		}
		var restored backend.ExecutionHandle
		if err := json.Unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		if err := backend.ValidateExecutionHandle(restored); err != nil {
			t.Fatalf("restored handle: %v", err)
		}
	})

	observations := []struct {
		name     string
		scenario Scenario
		state    backend.ObservationState
		status   string
	}{
		{"active", ScenarioActive, backend.ObservationActive, ""},
		{"waiting-input", ScenarioWaitingInput, backend.ObservationWaitingInput, ""},
		{"result-pending", ScenarioResultPending, backend.ObservationResultPending, ""},
		{"terminal-succeeded", ScenarioTerminalSucceeded, backend.ObservationTerminal, "succeeded"},
		{"terminal-failed", ScenarioTerminalFailed, backend.ObservationTerminal, "failed"},
		{"terminal-indeterminate", ScenarioTerminalIndeterminate, backend.ObservationTerminal, "indeterminate"},
		{"lost", ScenarioLost, backend.ObservationLost, ""},
	}
	for _, test := range observations {
		t.Run("observe-"+test.name, func(t *testing.T) {
			fixture := factory(t, test.scenario)
			handle, err := fixture.Backend.Start(context.Background(), fixture.Spec)
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				observation, err := fixture.Backend.Observe(context.Background(), *handle)
				if err != nil {
					t.Fatal(err)
				}
				if observation == nil {
					t.Fatal("Observe returned nil")
				}
				if observation.State == test.state && (test.status == "" || (observation.Result != nil && observation.Result.Status == test.status)) {
					if err := backend.ValidateExecutionObservation(*observation); err != nil {
						t.Fatalf("invalid observation: %v", err)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("observation=%+v, want state=%q status=%q", observation, test.state, test.status)
				}
				time.Sleep(25 * time.Millisecond)
			}
		})
	}

	for _, test := range []struct {
		name     string
		scenario Scenario
		state    backend.CancelState
	}{
		{"confirmed", ScenarioCancelConfirmed, backend.CancelConfirmed},
		{"not-confirmed", ScenarioCancelNotConfirmed, backend.CancelNotConfirmed},
	} {
		t.Run("cancel-"+test.name, func(t *testing.T) {
			fixture := factory(t, test.scenario)
			handle, err := fixture.Backend.Start(context.Background(), fixture.Spec)
			if err != nil {
				t.Fatal(err)
			}
			result, err := fixture.Backend.Cancel(context.Background(), *handle)
			if err != nil {
				t.Fatal(err)
			}
			if result == nil {
				t.Fatal("Cancel returned nil")
			}
			if err := backend.ValidateCancelResult(*result); err != nil {
				t.Fatalf("invalid cancel result: %v", err)
			}
			if result.State != test.state {
				t.Fatalf("cancel state=%q, want %q", result.State, test.state)
			}
		})
	}

	t.Run("bounded-output", func(t *testing.T) {
		fixture := factory(t, ScenarioActive)
		handle, err := fixture.Backend.Start(context.Background(), fixture.Spec)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.Backend.Output(context.Background(), *handle, 20); err != nil {
			t.Fatal(err)
		}
	})
}
