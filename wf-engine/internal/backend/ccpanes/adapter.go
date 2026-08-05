package ccpanes

import (
	"context"
	"encoding/json"
	"fmt"

	"wf.local/wf-engine/internal/backend"
)

const adapterHandleSchemaVersion = 1

type Adapter struct {
	legacy *Backend
}

type adapterHandleData struct {
	SessionID string            `json:"sessionId"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

func NewAdapter() (*Adapter, error) {
	legacy, err := New()
	if err != nil {
		return nil, err
	}
	return &Adapter{legacy: legacy}, nil
}

func NewUnavailableAdapter(err error) *Adapter {
	return &Adapter{legacy: NewUnavailable(err)}
}

func NewAdapterWithBackend(legacy *Backend) *Adapter {
	return &Adapter{legacy: legacy}
}

func (a *Adapter) Name() string { return "ccpanes" }

func (*Adapter) Capabilities() backend.Capabilities {
	return backend.Capabilities{
		Tools: []string{"codex", "claude", "opencode"}, Runtimes: []string{"local", "wsl", "ssh"},
		SupportsOutput: true, SupportsWaitingInput: true, MaxConcurrentAgents: 0, SupportsConcurrentCancel: true,
	}
}

func (a *Adapter) Doctor(ctx context.Context, request backend.DoctorRequest) backend.DoctorReport {
	report := backend.DoctorReport{Backend: a.Name()}
	if err := a.legacy.Doctor(ctx); err != nil {
		report.Diagnostics = append(report.Diagnostics, backend.Diagnostic{Name: "control-plane", Status: backend.DiagnosticError, Message: err.Error()})
		return report
	}
	report.Diagnostics = append(report.Diagnostics, backend.Diagnostic{Name: "control-plane", Status: backend.DiagnosticOK, Message: "CC-Panes control plane is ready"})
	if request.Workspace != "" {
		if err := a.legacy.DoctorProject(ctx, request.Workspace); err != nil {
			report.Diagnostics = append(report.Diagnostics, backend.Diagnostic{Name: "workspace", Status: backend.DiagnosticError, Message: err.Error()})
			return report
		}
		report.Diagnostics = append(report.Diagnostics, backend.Diagnostic{Name: "workspace", Status: backend.DiagnosticOK, Message: "workspace is registered in CC-Panes"})
	}
	report.Ready = true
	return report
}

func (a *Adapter) Start(ctx context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	if err := backend.ValidateAgentExecutionSpec(spec); err != nil {
		return nil, err
	}
	session, err := a.legacy.Launch(ctx, backend.LaunchSpec{
		RunID: spec.RunID, Project: spec.Workspace, Tool: spec.Tool, Runtime: spec.Runtime, Prompt: spec.Instructions,
	})
	if session == nil || session.ID == "" {
		return nil, err
	}
	handle, encodeErr := a.handleFromSession(*session)
	if encodeErr != nil {
		return nil, encodeErr
	}
	return handle, err
}

func (a *Adapter) Observe(ctx context.Context, handle backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	session, err := a.sessionFromHandle(handle)
	if err != nil {
		return nil, err
	}
	observation, err := a.legacy.Reconcile(ctx, session)
	if err != nil || observation == nil {
		return observation, err
	}
	switch observation.State {
	case backend.ObservationExited:
		return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{
			Status: "indeterminate", Summary: "Agent execution exited without a terminal CC-Panes result",
		}}, nil
	case backend.ObservationError:
		return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{
			Status: "failed", Summary: "Agent execution reported an error before a terminal CC-Panes result",
		}}, nil
	default:
		return observation, nil
	}
}

func (a *Adapter) Output(ctx context.Context, handle backend.ExecutionHandle, lines int) (string, error) {
	session, err := a.sessionFromHandle(handle)
	if err != nil {
		return "", err
	}
	return a.legacy.Output(ctx, session, lines)
}

func (a *Adapter) Cancel(ctx context.Context, handle backend.ExecutionHandle) (*backend.CancelResult, error) {
	session, err := a.sessionFromHandle(handle)
	if err != nil {
		return nil, err
	}
	if err := a.legacy.Cancel(ctx, session); err != nil {
		return &backend.CancelResult{State: backend.CancelNotConfirmed, Diagnostic: err.Error()}, nil
	}
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}

func (a *Adapter) DecodeLegacySession(session backend.Session) (*backend.ExecutionHandle, error) {
	return a.handleFromSession(session)
}

func (a *Adapter) handleFromSession(session backend.Session) (*backend.ExecutionHandle, error) {
	if session.ID == "" {
		return nil, fmt.Errorf("CC-Panes session ID is required")
	}
	data, err := json.Marshal(adapterHandleData{SessionID: session.ID, Metadata: session.Metadata})
	if err != nil {
		return nil, err
	}
	handle := &backend.ExecutionHandle{Backend: a.Name(), SchemaVersion: adapterHandleSchemaVersion, ID: session.ID, Data: data}
	if err := backend.ValidateExecutionHandle(*handle); err != nil {
		return nil, err
	}
	return handle, nil
}

func (a *Adapter) sessionFromHandle(handle backend.ExecutionHandle) (backend.Session, error) {
	if err := backend.ValidateExecutionHandle(handle); err != nil {
		return backend.Session{}, err
	}
	if handle.Backend != a.Name() {
		return backend.Session{}, fmt.Errorf("CC-Panes Adapter cannot decode handle for Backend %q", handle.Backend)
	}
	if handle.SchemaVersion != adapterHandleSchemaVersion {
		return backend.Session{}, fmt.Errorf("unsupported CC-Panes handle schema version %d", handle.SchemaVersion)
	}
	var data adapterHandleData
	if err := json.Unmarshal(handle.Data, &data); err != nil {
		return backend.Session{}, fmt.Errorf("decode CC-Panes handle: %w", err)
	}
	if data.SessionID == "" || data.SessionID != handle.ID {
		return backend.Session{}, fmt.Errorf("CC-Panes handle session ID does not match handle ID")
	}
	return backend.Session{ID: data.SessionID, Metadata: data.Metadata}, nil
}
