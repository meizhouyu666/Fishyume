package harnesssession

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"wf.local/wf-engine/internal/explorationdriver"
	"wf.local/wf-engine/internal/sessiondriver"
)

type ExplorationAdapter struct{ session *Driver }

type explorationHandleData struct {
	Session sessiondriver.SessionHandle `json:"session"`
	Turn    sessiondriver.TurnHandle    `json:"turn"`
}

func (d *Driver) Exploration() *ExplorationAdapter { return &ExplorationAdapter{session: d} }
func (a *ExplorationAdapter) Name() string         { return a.session.Name() }

func (a *ExplorationAdapter) Capabilities() explorationdriver.DriverCapabilities {
	capabilities := a.session.Capabilities()
	return explorationdriver.DriverCapabilities{Targets: capabilities.Targets, SupportsOutput: true, SupportsRecovery: true, SupportsConfirmedCancel: true, SupportsConcurrentCancel: true, MaxConcurrentTurns: len(capabilities.Targets) * 2}
}

func (a *ExplorationAdapter) Doctor(_ context.Context, request explorationdriver.DoctorRequest) explorationdriver.DoctorReport {
	ready := false
	for _, target := range a.session.targets {
		if target == request.Target {
			ready = true
			break
		}
	}
	message := a.session.name + " route is ready"
	if !ready {
		message = a.session.name + " target is not configured"
	}
	return explorationdriver.DoctorReport{Driver: a.Name(), Ready: ready, Diagnostics: []explorationdriver.Diagnostic{{Name: "trusted_route", Status: map[bool]string{true: "pass", false: "fail"}[ready], Message: message}}}
}

func (a *ExplorationAdapter) Start(ctx context.Context, request explorationdriver.StartRequest) (*explorationdriver.ExecutionHandle, error) {
	if err := explorationdriver.ValidateStartRequest(request); err != nil {
		return nil, err
	}
	session, err := a.session.StartSession(ctx, sessiondriver.StartSessionRequest{ProtocolVersion: sessiondriver.ProtocolVersion, Identity: sessiondriver.SessionIdentity{TeamID: request.Identity.TeamID, ParticipantID: request.Identity.ParticipantID, Generation: 1}, Workspace: request.Workspace, Target: request.Target, ModelID: request.ModelID, Sandbox: sessiondriver.SandboxReadOnly})
	if err != nil {
		return nil, err
	}
	started, err := a.session.StartTurn(ctx, *session, sessiondriver.StartTurnRequest{ProtocolVersion: sessiondriver.ProtocolVersion, Identity: sessiondriver.TurnIdentity{TurnID: request.Identity.TurnID, ExpectedSessionGeneration: 1}, Prompt: request.Prompt, MaxOutputBytes: request.ResultContract.MaxBytes})
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(explorationHandleData{Session: started.Session, Turn: started.Turn})
	if err != nil {
		return nil, err
	}
	handle := &explorationdriver.ExecutionHandle{Driver: a.Name(), Target: request.Target, SchemaVersion: 1, ID: started.Session.ID, Data: data}
	return handle, explorationdriver.ValidateExecutionHandle(*handle)
}

func (a *ExplorationAdapter) Observe(ctx context.Context, handle explorationdriver.ExecutionHandle) (*explorationdriver.Observation, error) {
	data, err := a.decode(handle)
	if err != nil {
		return nil, err
	}
	observed, err := a.session.ObserveTurn(ctx, data.Session, data.Turn)
	if err != nil {
		return nil, err
	}
	state := explorationdriver.ObservationActive
	diagnostic := observed.Diagnostic
	switch observed.State {
	case sessiondriver.TurnResponded, sessiondriver.TurnFailed, sessiondriver.TurnInterrupted:
		state = explorationdriver.ObservationTerminal
	case sessiondriver.TurnLost:
		state = explorationdriver.ObservationLost
	}
	result := &explorationdriver.Observation{State: state, Diagnostic: bounded(diagnostic, explorationdriver.MaxDiagnosticBytes)}
	return result, explorationdriver.ValidateObservation(*result)
}

func (a *ExplorationAdapter) Output(_ context.Context, handle explorationdriver.ExecutionHandle, _ int) (string, error) {
	data, err := a.decode(handle)
	if err != nil {
		return "", err
	}
	record, err := a.session.read(data.Session.ID)
	if err != nil {
		return "", err
	}
	if record.LastTurnID != data.Turn.ID || record.LastTurnState != sessiondriver.TurnResponded || strings.TrimSpace(record.LastOutput) == "" {
		return "", fmt.Errorf("%s exploration turn did not produce a contribution: %s", a.Name(), record.LastDiagnostic)
	}
	return contribution(record.LastOutput)
}

func (a *ExplorationAdapter) Cancel(ctx context.Context, handle explorationdriver.ExecutionHandle) (*explorationdriver.CancelResult, error) {
	data, err := a.decode(handle)
	if err != nil {
		return nil, err
	}
	result, err := a.session.CancelTurn(ctx, data.Session, data.Turn)
	if err != nil {
		return nil, err
	}
	state := explorationdriver.CancelNotConfirmed
	if result.State == sessiondriver.CancelConfirmed {
		state = explorationdriver.CancelConfirmed
	}
	value := &explorationdriver.CancelResult{State: state, Diagnostic: result.Diagnostic}
	return value, explorationdriver.ValidateCancelResult(*value)
}

func (a *ExplorationAdapter) decode(handle explorationdriver.ExecutionHandle) (explorationHandleData, error) {
	if err := explorationdriver.ValidateExecutionHandle(handle); err != nil {
		return explorationHandleData{}, err
	}
	if handle.Driver != a.Name() {
		return explorationHandleData{}, fmt.Errorf("exploration Driver binding changed")
	}
	var data explorationHandleData
	decoder := json.NewDecoder(strings.NewReader(string(handle.Data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return explorationHandleData{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return explorationHandleData{}, fmt.Errorf("exploration handle contains trailing data")
	}
	if data.Session.ID != handle.ID || data.Session.Target != handle.Target || data.Turn.SessionID != handle.ID {
		return explorationHandleData{}, fmt.Errorf("exploration handle identity changed")
	}
	return data, nil
}

var _ explorationdriver.Driver = (*ExplorationAdapter)(nil)
