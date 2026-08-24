package explorationdriver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type fakeDriver struct{}

func (fakeDriver) Name() string { return "fake-exploration" }
func (fakeDriver) Capabilities() DriverCapabilities {
	return DriverCapabilities{Targets: []string{"local"}, SupportsOutput: true, SupportsRecovery: true, SupportsConfirmedCancel: true, SupportsConcurrentCancel: true, MaxConcurrentTurns: 2}
}
func (fakeDriver) Doctor(context.Context, DoctorRequest) DoctorReport {
	return DoctorReport{Driver: "fake-exploration", Ready: true}
}
func (fakeDriver) Start(context.Context, StartRequest) (*ExecutionHandle, error) {
	return &ExecutionHandle{Driver: "fake-exploration", Target: "local", SchemaVersion: 1, ID: "turn-exec-1", Data: json.RawMessage(`{"turnId":"turn-1"}`)}, nil
}
func (fakeDriver) Observe(context.Context, ExecutionHandle) (*Observation, error) {
	return &Observation{State: ObservationTerminal}, nil
}
func (fakeDriver) Output(context.Context, ExecutionHandle, int) (string, error) {
	return "bounded output", nil
}
func (fakeDriver) Cancel(context.Context, ExecutionHandle) (*CancelResult, error) {
	return &CancelResult{State: CancelConfirmed}, nil
}

var _ Driver = fakeDriver{}

func validStartRequest() StartRequest {
	return StartRequest{
		ProtocolVersion: ProtocolVersion,
		Identity:        ExecutionIdentity{TeamID: "team-1", ParticipantID: "participant-1", TurnID: "turn-1"},
		Workspace:       `C:\project`,
		Target:          "local",
		ModelID:         "codex/local/gpt-5.6",
		Prompt:          "Compare two approaches.",
		Sandbox:         SandboxReadOnly,
		ResultContract:  ResultContract{MaxBytes: 32 * 1024},
	}
}

func TestValidateStartRequestRequiresTeamTurnIdentityAndReadOnly(t *testing.T) {
	if err := ValidateStartRequest(validStartRequest()); err != nil {
		t.Fatal(err)
	}
	for name, update := range map[string]func(*StartRequest){
		"wrong protocol":   func(value *StartRequest) { value.ProtocolVersion++ },
		"missing team":     func(value *StartRequest) { value.Identity.TeamID = "" },
		"missing turn":     func(value *StartRequest) { value.Identity.TurnID = "" },
		"writable sandbox": func(value *StartRequest) { value.Sandbox = "workspace-write" },
		"oversized result": func(value *StartRequest) { value.ResultContract.MaxBytes = MaxOutputBytes + 1 },
		"oversized prompt": func(value *StartRequest) { value.Prompt = strings.Repeat("x", MaxPromptBytes+1) },
	} {
		t.Run(name, func(t *testing.T) {
			request := validStartRequest()
			update(&request)
			if err := ValidateStartRequest(request); err == nil {
				t.Fatalf("request unexpectedly accepted: %+v", request)
			}
		})
	}
}

func TestStartRequestDoesNotSerializePrompt(t *testing.T) {
	request := validStartRequest()
	request.Prompt = "DO-NOT-PERSIST"
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), request.Prompt) {
		t.Fatalf("serialized exploration request contains prompt: %s", encoded)
	}
}

func TestValidateCapabilitiesRejectsUnsafeCancellationClaims(t *testing.T) {
	valid := fakeDriver{}.Capabilities()
	if err := ValidateCapabilities(valid); err != nil {
		t.Fatal(err)
	}
	valid.SupportsConfirmedCancel = false
	if err := ValidateCapabilities(valid); err == nil {
		t.Fatal("concurrent cancellation without confirmed cancellation was accepted")
	}
}

func TestValidationBoundsHandleObservationAndCancellation(t *testing.T) {
	if err := ValidateExecutionHandle(ExecutionHandle{Driver: "fake", Target: "local", SchemaVersion: 1, ID: "exec-1", Data: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateExecutionHandle(ExecutionHandle{Driver: "fake", Target: "local", SchemaVersion: 1, ID: "exec-1", Data: json.RawMessage("not-json")}); err == nil {
		t.Fatal("invalid handle data was accepted")
	}
	if err := ValidateObservation(Observation{State: ObservationTerminal, Diagnostic: strings.Repeat("x", MaxDiagnosticBytes+1)}); err == nil {
		t.Fatal("oversized diagnostic was accepted")
	}
	if err := ValidateCancelResult(CancelResult{State: CancelConfirmed, Diagnostic: strings.Repeat("x", MaxDiagnosticBytes+1)}); err == nil {
		t.Fatal("oversized cancellation diagnostic was accepted")
	}
}
