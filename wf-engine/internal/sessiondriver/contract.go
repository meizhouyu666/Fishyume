// Package sessiondriver defines the resumable conversation port used by M7.
// It is deliberately separate from Workflow and one-shot exploration Drivers.
package sessiondriver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	ProtocolVersion        = 1
	MaxHandleDataBytes     = 64 * 1024
	MaxPromptBytes         = 64 * 1024
	MaxOutputBytes         = 64 * 1024
	MaxDiagnosticBytes     = 16 * 1024
	MaxDriverIdentityBytes = 256
)

var (
	ErrConflict = errors.New("AgentSession identity conflict")
	ErrLost     = errors.New("AgentSession is lost")
)

type Sandbox string

const SandboxReadOnly Sandbox = "read-only"

type SessionIdentity struct {
	TeamID        string `json:"teamId"`
	ParticipantID string `json:"participantId"`
	Generation    uint64 `json:"generation"`
}

type StartSessionRequest struct {
	ProtocolVersion int             `json:"protocolVersion"`
	Identity        SessionIdentity `json:"identity"`
	Workspace       string          `json:"workspace"`
	Target          string          `json:"target"`
	ModelID         string          `json:"modelId"`
	Sandbox         Sandbox         `json:"sandbox"`
}

type SessionHandle struct {
	Driver        string          `json:"driver"`
	Target        string          `json:"target"`
	SchemaVersion int             `json:"schemaVersion"`
	ID            string          `json:"id"`
	Generation    uint64          `json:"generation"`
	Revision      uint64          `json:"revision"`
	Data          json.RawMessage `json:"data,omitempty"`
}

type TurnIdentity struct {
	TurnID                    string `json:"turnId"`
	ExpectedSessionGeneration uint64 `json:"expectedSessionGeneration"`
}

type StartTurnRequest struct {
	ProtocolVersion int          `json:"protocolVersion"`
	Identity        TurnIdentity `json:"identity"`
	Prompt          string       `json:"-"`
	MaxOutputBytes  int          `json:"maxOutputBytes"`
}

type TurnHandle struct {
	Driver            string          `json:"driver"`
	Target            string          `json:"target"`
	SchemaVersion     int             `json:"schemaVersion"`
	ID                string          `json:"id"`
	SessionID         string          `json:"sessionId"`
	SessionGeneration uint64          `json:"sessionGeneration"`
	Data              json.RawMessage `json:"data,omitempty"`
}

type StartTurnResult struct {
	Session SessionHandle `json:"session"`
	Turn    TurnHandle    `json:"turn"`
}

type DriverCapabilities struct {
	Targets                 []string `json:"targets"`
	SupportsResume          bool     `json:"supportsResume"`
	SupportsPark            bool     `json:"supportsPark"`
	SupportsRecovery        bool     `json:"supportsRecovery"`
	SupportsDirectedInput   bool     `json:"supportsDirectedInput"`
	SupportsConfirmedCancel bool     `json:"supportsConfirmedCancel"`
	MaxConcurrentTurns      int      `json:"maxConcurrentTurns"`
}

type TurnState string

const (
	TurnDispatching TurnState = "dispatching"
	TurnActive      TurnState = "active"
	TurnResponded   TurnState = "responded"
	TurnInterrupted TurnState = "interrupted"
	TurnFailed      TurnState = "failed"
	TurnLost        TurnState = "lost"
)

type TurnObservation struct {
	Session    SessionHandle `json:"session"`
	Turn       TurnHandle    `json:"turn"`
	State      TurnState     `json:"state"`
	Output     string        `json:"output,omitempty"`
	Diagnostic string        `json:"diagnostic,omitempty"`
}

type CancelState string

const (
	CancelConfirmed    CancelState = "confirmed"
	CancelNotConfirmed CancelState = "not_confirmed"
)

type CancelTurnResult struct {
	Session    SessionHandle `json:"session"`
	Turn       TurnHandle    `json:"turn"`
	State      CancelState   `json:"state"`
	Diagnostic string        `json:"diagnostic,omitempty"`
}

type Driver interface {
	Name() string
	Capabilities() DriverCapabilities
	StartSession(context.Context, StartSessionRequest) (*SessionHandle, error)
	StartTurn(context.Context, SessionHandle, StartTurnRequest) (*StartTurnResult, error)
	ObserveTurn(context.Context, SessionHandle, TurnHandle) (*TurnObservation, error)
	ParkSession(context.Context, SessionHandle) (*SessionHandle, error)
	ResumeSession(context.Context, SessionHandle) (*SessionHandle, error)
	CancelTurn(context.Context, SessionHandle, TurnHandle) (*CancelTurnResult, error)
	CloseSession(context.Context, SessionHandle) (*SessionHandle, error)
}

func Conflict(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrConflict, fmt.Sprintf(format, args...))
}

func Lost(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrLost, fmt.Sprintf(format, args...))
}

func ValidateStartSessionRequest(request StartSessionRequest) error {
	if request.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported AgentSession protocol version %d", request.ProtocolVersion)
	}
	if err := ValidateSessionIdentity(request.Identity); err != nil {
		return err
	}
	if err := validateRequiredIdentity(map[string]string{
		"workspace": request.Workspace, "target": request.Target, "modelId": request.ModelID,
	}); err != nil {
		return err
	}
	if request.Sandbox != SandboxReadOnly {
		return fmt.Errorf("AgentSession sandbox must be %q", SandboxReadOnly)
	}
	return nil
}

func ValidateSessionIdentity(identity SessionIdentity) error {
	if err := validateRequiredIdentity(map[string]string{
		"teamId": identity.TeamID, "participantId": identity.ParticipantID,
	}); err != nil {
		return err
	}
	if identity.Generation == 0 {
		return fmt.Errorf("AgentSession generation must be positive")
	}
	return nil
}

func ValidateStartTurnRequest(request StartTurnRequest) error {
	if request.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported AgentSession protocol version %d", request.ProtocolVersion)
	}
	if err := validateRequiredIdentity(map[string]string{"turnId": request.Identity.TurnID}); err != nil {
		return err
	}
	if request.Identity.ExpectedSessionGeneration == 0 {
		return fmt.Errorf("expected AgentSession generation must be positive")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	if len([]byte(request.Prompt)) > MaxPromptBytes {
		return fmt.Errorf("AgentSession prompt exceeds %d bytes", MaxPromptBytes)
	}
	if request.MaxOutputBytes < 1 || request.MaxOutputBytes > MaxOutputBytes {
		return fmt.Errorf("AgentSession output limit must be between 1 and %d", MaxOutputBytes)
	}
	return nil
}

func ValidateCapabilities(capabilities DriverCapabilities) error {
	if len(capabilities.Targets) == 0 {
		return fmt.Errorf("Session Driver must declare at least one target")
	}
	seen := make(map[string]struct{}, len(capabilities.Targets))
	for _, target := range capabilities.Targets {
		if err := validateIdentityValue("target", target); err != nil {
			return err
		}
		if _, exists := seen[target]; exists {
			return fmt.Errorf("Session Driver target %q is duplicated", target)
		}
		seen[target] = struct{}{}
	}
	if capabilities.MaxConcurrentTurns != 1 {
		return fmt.Errorf("Session Driver v1 requires exactly one concurrent turn")
	}
	if capabilities.SupportsRecovery && !capabilities.SupportsResume {
		return fmt.Errorf("Session recovery requires resume support")
	}
	if capabilities.SupportsPark && !capabilities.SupportsResume {
		return fmt.Errorf("Session parking requires resume support")
	}
	return nil
}

func ValidateSessionHandle(handle SessionHandle) error {
	if err := validateRequiredIdentity(map[string]string{
		"driver": handle.Driver, "target": handle.Target, "sessionId": handle.ID,
	}); err != nil {
		return err
	}
	if handle.SchemaVersion < 1 || handle.Generation == 0 || handle.Revision == 0 {
		return fmt.Errorf("AgentSession handle version, generation, and revision must be positive")
	}
	return validateHandleData(handle.Data)
}

func ValidateTurnHandle(handle TurnHandle) error {
	if err := validateRequiredIdentity(map[string]string{
		"driver": handle.Driver, "target": handle.Target, "turnId": handle.ID, "sessionId": handle.SessionID,
	}); err != nil {
		return err
	}
	if handle.SchemaVersion < 1 || handle.SessionGeneration == 0 {
		return fmt.Errorf("AgentSession turn handle version and generation must be positive")
	}
	return validateHandleData(handle.Data)
}

func ValidateTurnObservation(observation TurnObservation) error {
	if err := ValidateSessionHandle(observation.Session); err != nil {
		return err
	}
	if err := ValidateTurnHandle(observation.Turn); err != nil {
		return err
	}
	switch observation.State {
	case TurnDispatching, TurnActive, TurnResponded, TurnInterrupted, TurnFailed, TurnLost:
	default:
		return fmt.Errorf("unsupported AgentSession turn state %q", observation.State)
	}
	if len([]byte(observation.Output)) > MaxOutputBytes {
		return fmt.Errorf("AgentSession output exceeds %d bytes", MaxOutputBytes)
	}
	if len([]byte(observation.Diagnostic)) > MaxDiagnosticBytes {
		return fmt.Errorf("AgentSession diagnostic exceeds %d bytes", MaxDiagnosticBytes)
	}
	if observation.State != TurnResponded && observation.Output != "" {
		return fmt.Errorf("only a responded AgentSession turn can carry output")
	}
	return nil
}

func ValidateCancelTurnResult(result CancelTurnResult) error {
	if err := ValidateSessionHandle(result.Session); err != nil {
		return err
	}
	if err := ValidateTurnHandle(result.Turn); err != nil {
		return err
	}
	if result.State != CancelConfirmed && result.State != CancelNotConfirmed {
		return fmt.Errorf("unsupported AgentSession cancellation state %q", result.State)
	}
	if len([]byte(result.Diagnostic)) > MaxDiagnosticBytes {
		return fmt.Errorf("AgentSession cancellation diagnostic exceeds %d bytes", MaxDiagnosticBytes)
	}
	return nil
}

func validateRequiredIdentity(values map[string]string) error {
	for name, value := range values {
		if err := validateIdentityValue(name, value); err != nil {
			return err
		}
	}
	return nil
}

func validateIdentityValue(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s contains surrounding whitespace", name)
	}
	if len([]byte(value)) > MaxDriverIdentityBytes {
		return fmt.Errorf("%s exceeds %d bytes", name, MaxDriverIdentityBytes)
	}
	return nil
}

func validateHandleData(data json.RawMessage) error {
	if len(data) > MaxHandleDataBytes {
		return fmt.Errorf("AgentSession handle data exceeds %d bytes", MaxHandleDataBytes)
	}
	if len(data) > 0 && !json.Valid(data) {
		return fmt.Errorf("AgentSession handle data is not valid JSON")
	}
	return nil
}
