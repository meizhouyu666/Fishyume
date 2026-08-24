// Package explorationdriver defines the one-shot execution port used by M7
// Team exploration. It deliberately has no dependency on Workflow Run,
// Node, Attempt, Context, or Result contracts.
package explorationdriver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	ProtocolVersion            = 1
	MaxExecutionHandleDataSize = 64 * 1024
	MaxPromptBytes             = 64 * 1024
	MaxOutputBytes             = 64 * 1024
	MaxDiagnosticBytes         = 16 * 1024
)

type Sandbox string

const SandboxReadOnly Sandbox = "read-only"

type ExecutionIdentity struct {
	TeamID        string `json:"teamId"`
	ParticipantID string `json:"participantId"`
	TurnID        string `json:"turnId"`
}

type ResultContract struct {
	MaxBytes int `json:"maxBytes"`
}

type StartRequest struct {
	ProtocolVersion int               `json:"protocolVersion"`
	Identity        ExecutionIdentity `json:"identity"`
	Workspace       string            `json:"workspace"`
	Target          string            `json:"target"`
	ModelID         string            `json:"modelId"`
	Prompt          string            `json:"-"`
	Sandbox         Sandbox           `json:"sandbox"`
	ResultContract  ResultContract    `json:"resultContract"`
}

type ExecutionHandle struct {
	Driver        string          `json:"driver"`
	Target        string          `json:"target"`
	SchemaVersion int             `json:"schemaVersion"`
	ID            string          `json:"id"`
	Data          json.RawMessage `json:"data,omitempty"`
}

type DriverCapabilities struct {
	Targets                  []string `json:"targets"`
	SupportsOutput           bool     `json:"supportsOutput"`
	SupportsRecovery         bool     `json:"supportsRecovery"`
	SupportsConfirmedCancel  bool     `json:"supportsConfirmedCancel"`
	SupportsConcurrentCancel bool     `json:"supportsConcurrentCancel"`
	MaxConcurrentTurns       int      `json:"maxConcurrentTurns,omitempty"`
}

type DoctorRequest struct {
	Workspace string `json:"workspace"`
	Target    string `json:"target"`
}

type Diagnostic struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type DoctorReport struct {
	Driver      string       `json:"driver"`
	Ready       bool         `json:"ready"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type ObservationState string

const (
	ObservationActive   ObservationState = "active"
	ObservationTerminal ObservationState = "terminal"
	ObservationLost     ObservationState = "lost"
)

type Observation struct {
	State      ObservationState `json:"state"`
	Diagnostic string           `json:"diagnostic,omitempty"`
}

type CancelState string

const (
	CancelConfirmed    CancelState = "confirmed"
	CancelNotConfirmed CancelState = "not_confirmed"
)

type CancelResult struct {
	State      CancelState `json:"state"`
	Diagnostic string      `json:"diagnostic,omitempty"`
}

type Driver interface {
	Name() string
	Capabilities() DriverCapabilities
	Doctor(context.Context, DoctorRequest) DoctorReport
	Start(context.Context, StartRequest) (*ExecutionHandle, error)
	Observe(context.Context, ExecutionHandle) (*Observation, error)
	Output(context.Context, ExecutionHandle, int) (string, error)
	Cancel(context.Context, ExecutionHandle) (*CancelResult, error)
}

func ValidateIdentity(identity ExecutionIdentity) error {
	if strings.TrimSpace(identity.TeamID) == "" || strings.TrimSpace(identity.ParticipantID) == "" || strings.TrimSpace(identity.TurnID) == "" {
		return fmt.Errorf("exploration execution identity is incomplete")
	}
	if identity.TeamID != strings.TrimSpace(identity.TeamID) || identity.ParticipantID != strings.TrimSpace(identity.ParticipantID) || identity.TurnID != strings.TrimSpace(identity.TurnID) {
		return fmt.Errorf("exploration execution identity contains surrounding whitespace")
	}
	return nil
}

func ValidateStartRequest(request StartRequest) error {
	if request.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported exploration protocol version %d", request.ProtocolVersion)
	}
	if err := ValidateIdentity(request.Identity); err != nil {
		return err
	}
	for name, value := range map[string]string{"workspace": request.Workspace, "target": request.Target, "modelId": request.ModelID, "prompt": request.Prompt} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if request.Target != strings.TrimSpace(request.Target) || request.ModelID != strings.TrimSpace(request.ModelID) {
		return fmt.Errorf("target and modelId cannot contain surrounding whitespace")
	}
	if request.Sandbox != SandboxReadOnly {
		return fmt.Errorf("exploration sandbox must be %q", SandboxReadOnly)
	}
	if request.ResultContract.MaxBytes < 1 || request.ResultContract.MaxBytes > MaxOutputBytes {
		return fmt.Errorf("result max bytes must be between 1 and %d", MaxOutputBytes)
	}
	if len([]byte(request.Prompt)) > MaxPromptBytes {
		return fmt.Errorf("exploration prompt exceeds %d bytes", MaxPromptBytes)
	}
	return nil
}

func ValidateCapabilities(capabilities DriverCapabilities) error {
	if len(capabilities.Targets) == 0 {
		return fmt.Errorf("exploration Driver must declare at least one target")
	}
	seen := make(map[string]struct{}, len(capabilities.Targets))
	for _, target := range capabilities.Targets {
		if strings.TrimSpace(target) == "" || target != strings.TrimSpace(target) {
			return fmt.Errorf("exploration Driver target is empty or contains surrounding whitespace")
		}
		if _, exists := seen[target]; exists {
			return fmt.Errorf("exploration Driver target %q is duplicated", target)
		}
		seen[target] = struct{}{}
	}
	if capabilities.MaxConcurrentTurns < 0 {
		return fmt.Errorf("max concurrent exploration turns cannot be negative")
	}
	if !capabilities.SupportsConfirmedCancel && capabilities.SupportsConcurrentCancel {
		return fmt.Errorf("concurrent exploration cancellation requires confirmed cancellation")
	}
	return nil
}

func ValidateExecutionHandle(handle ExecutionHandle) error {
	if strings.TrimSpace(handle.Driver) == "" || strings.TrimSpace(handle.Target) == "" || strings.TrimSpace(handle.ID) == "" {
		return fmt.Errorf("exploration execution handle identity is incomplete")
	}
	if handle.SchemaVersion < 1 {
		return fmt.Errorf("exploration handle schema version must be positive")
	}
	if len(handle.Data) > MaxExecutionHandleDataSize {
		return fmt.Errorf("exploration handle data exceeds %d bytes", MaxExecutionHandleDataSize)
	}
	if len(handle.Data) > 0 && !json.Valid(handle.Data) {
		return fmt.Errorf("exploration handle data is not valid JSON")
	}
	return nil
}

func ValidateObservation(observation Observation) error {
	switch observation.State {
	case ObservationActive, ObservationTerminal, ObservationLost:
	default:
		return fmt.Errorf("unsupported exploration observation state %q", observation.State)
	}
	if len([]byte(observation.Diagnostic)) > MaxDiagnosticBytes {
		return fmt.Errorf("exploration diagnostic exceeds %d bytes", MaxDiagnosticBytes)
	}
	return nil
}

func ValidateCancelResult(result CancelResult) error {
	if result.State != CancelConfirmed && result.State != CancelNotConfirmed {
		return fmt.Errorf("unsupported exploration cancel state %q", result.State)
	}
	if len([]byte(result.Diagnostic)) > MaxDiagnosticBytes {
		return fmt.Errorf("exploration cancellation diagnostic exceeds %d bytes", MaxDiagnosticBytes)
	}
	return nil
}
