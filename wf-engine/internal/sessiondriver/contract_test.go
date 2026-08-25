package sessiondriver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeDriver struct{}

func (fakeDriver) Name() string { return "fake-session" }
func (fakeDriver) Capabilities() DriverCapabilities {
	return DriverCapabilities{Targets: []string{"local"}, SupportsResume: true, SupportsPark: true, SupportsRecovery: true, SupportsDirectedInput: true, SupportsConfirmedCancel: true, MaxConcurrentTurns: 1}
}
func (fakeDriver) StartSession(context.Context, StartSessionRequest) (*SessionHandle, error) {
	return &SessionHandle{Driver: "fake-session", Target: "local", SchemaVersion: 1, ID: "session-1", Generation: 1, Revision: 1, Data: json.RawMessage(`{}`)}, nil
}
func (fakeDriver) StartTurn(context.Context, SessionHandle, StartTurnRequest) (*StartTurnResult, error) {
	return nil, nil
}
func (fakeDriver) ObserveTurn(context.Context, SessionHandle, TurnHandle) (*TurnObservation, error) {
	return nil, nil
}
func (fakeDriver) ParkSession(context.Context, SessionHandle) (*SessionHandle, error) {
	return nil, nil
}
func (fakeDriver) ResumeSession(context.Context, SessionHandle) (*SessionHandle, error) {
	return nil, nil
}
func (fakeDriver) CancelTurn(context.Context, SessionHandle, TurnHandle) (*CancelTurnResult, error) {
	return nil, nil
}
func (fakeDriver) CloseSession(context.Context, SessionHandle) (*SessionHandle, error) {
	return nil, nil
}

var _ Driver = fakeDriver{}

func validSessionRequest() StartSessionRequest {
	return StartSessionRequest{ProtocolVersion: ProtocolVersion, Identity: SessionIdentity{TeamID: "team-1", ParticipantID: "participant-1", Generation: 1}, Workspace: `C:\project`, Target: "local", ModelID: "codex/local/gpt-5.6-luna", Sandbox: SandboxReadOnly}
}

func validSessionHandle() SessionHandle {
	return SessionHandle{Driver: "fake", Target: "local", SchemaVersion: 1, ID: "session-1", Generation: 1, Revision: 1, Data: json.RawMessage(`{}`)}
}

func validTurnHandle() TurnHandle {
	return TurnHandle{Driver: "fake", Target: "local", SchemaVersion: 1, ID: "turn-1", SessionID: "session-1", SessionGeneration: 1, Data: json.RawMessage(`{}`)}
}

func TestSessionContractRequiresReadOnlyBoundIdentity(t *testing.T) {
	if err := ValidateStartSessionRequest(validSessionRequest()); err != nil {
		t.Fatal(err)
	}
	for name, update := range map[string]func(*StartSessionRequest){
		"wrong protocol":   func(value *StartSessionRequest) { value.ProtocolVersion++ },
		"missing team":     func(value *StartSessionRequest) { value.Identity.TeamID = "" },
		"zero generation":  func(value *StartSessionRequest) { value.Identity.Generation = 0 },
		"writable sandbox": func(value *StartSessionRequest) { value.Sandbox = "workspace-write" },
	} {
		t.Run(name, func(t *testing.T) {
			request := validSessionRequest()
			update(&request)
			if err := ValidateStartSessionRequest(request); err == nil {
				t.Fatalf("request unexpectedly accepted: %+v", request)
			}
		})
	}
}

func TestTurnPromptDoesNotSerializeAndBoundsOutput(t *testing.T) {
	request := StartTurnRequest{ProtocolVersion: ProtocolVersion, Identity: TurnIdentity{TurnID: "turn-1", ExpectedSessionGeneration: 1}, Prompt: "DO-NOT-PERSIST", MaxOutputBytes: 1024}
	if err := ValidateStartTurnRequest(request); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), request.Prompt) {
		t.Fatalf("serialized turn request contains prompt: %s", encoded)
	}
	request.Prompt = strings.Repeat("x", MaxPromptBytes+1)
	if err := ValidateStartTurnRequest(request); err == nil {
		t.Fatal("oversized prompt was accepted")
	}
}

func TestSessionCapabilitiesRequireCoherentResumeSemantics(t *testing.T) {
	capabilities := fakeDriver{}.Capabilities()
	if err := ValidateCapabilities(capabilities); err != nil {
		t.Fatal(err)
	}
	capabilities.SupportsResume = false
	if err := ValidateCapabilities(capabilities); err == nil {
		t.Fatal("recovery and park without resume were accepted")
	}
}

func TestSessionHandlesAndResultsAreBounded(t *testing.T) {
	if err := ValidateSessionHandle(validSessionHandle()); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTurnHandle(validTurnHandle()); err != nil {
		t.Fatal(err)
	}
	observation := TurnObservation{Session: validSessionHandle(), Turn: validTurnHandle(), State: TurnResponded, Output: "answer"}
	if err := ValidateTurnObservation(observation); err != nil {
		t.Fatal(err)
	}
	observation.State = TurnActive
	if err := ValidateTurnObservation(observation); err == nil {
		t.Fatal("active turn carried terminal output")
	}
	result := CancelTurnResult{Session: validSessionHandle(), Turn: validTurnHandle(), State: CancelConfirmed}
	if err := ValidateCancelTurnResult(result); err != nil {
		t.Fatal(err)
	}
}

func TestSessionErrorsRemainClassifiable(t *testing.T) {
	if err := Conflict("revision %d is stale", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict is not classifiable: %v", err)
	}
	if err := Lost("thread disappeared"); !errors.Is(err, ErrLost) {
		t.Fatalf("lost error is not classifiable: %v", err)
	}
}
