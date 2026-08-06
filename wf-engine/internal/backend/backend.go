package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"wf.local/wf-engine/internal/agent"
)

const MaxExecutionHandleDataSize = 64 * 1024

type ResultContract struct {
	Schema   json.RawMessage `json:"schema,omitempty"`
	MaxBytes int             `json:"maxBytes,omitempty"`
}

type AgentExecutionSpec struct {
	RunID          string
	NodeID         string
	Attempt        int
	Workspace      string
	Tool           string
	Runtime        string
	Instructions   string
	RequiredSkills []string
	ResultContract ResultContract
	// Envelope carries the M4 headless protocol. The flattened fields above are
	// retained for the bounded backend compatibility window.
	Envelope *agent.AttemptEnvelope
}

type ExecutionHandle struct {
	Driver        string          `json:"driver,omitempty"`
	Target        string          `json:"target,omitempty"`
	Backend       string          `json:"-"`
	SchemaVersion int             `json:"schemaVersion"`
	ID            string          `json:"id"`
	Data          json.RawMessage `json:"data,omitempty"`
}

func (handle ExecutionHandle) DriverName() string {
	if strings.TrimSpace(handle.Driver) != "" {
		return handle.Driver
	}
	return handle.Backend
}

func (handle ExecutionHandle) MarshalJSON() ([]byte, error) {
	target := handle.Target
	if target == "" {
		target = "local"
	}
	return json.Marshal(struct {
		Driver        string          `json:"driver"`
		Target        string          `json:"target"`
		SchemaVersion int             `json:"schemaVersion"`
		ID            string          `json:"id"`
		Data          json.RawMessage `json:"data,omitempty"`
	}{Driver: handle.DriverName(), Target: target, SchemaVersion: handle.SchemaVersion, ID: handle.ID, Data: handle.Data})
}

func (handle *ExecutionHandle) UnmarshalJSON(data []byte) error {
	var wire struct {
		Driver        string          `json:"driver"`
		Target        string          `json:"target"`
		Backend       string          `json:"backend"`
		SchemaVersion int             `json:"schemaVersion"`
		ID            string          `json:"id"`
		Data          json.RawMessage `json:"data,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	driver := wire.Driver
	if driver == "" {
		driver = wire.Backend
	}
	target := wire.Target
	if target == "" {
		target = "local"
	}
	*handle = ExecutionHandle{Driver: driver, Target: target, Backend: driver, SchemaVersion: wire.SchemaVersion, ID: wire.ID, Data: wire.Data}
	return nil
}

type Usage struct {
	InputTokensEstimated  int `json:"inputTokensEstimated"`
	OutputTokensEstimated int `json:"outputTokensEstimated"`
}

type AgentResult struct {
	Status    string          `json:"status"`
	Summary   string          `json:"summary,omitempty"`
	Artifacts []string        `json:"artifacts,omitempty"`
	Warnings  []string        `json:"warnings,omitempty"`
	Checks    []string        `json:"checks,omitempty"`
	Questions []InputQuestion `json:"questions,omitempty"`
	Usage     Usage           `json:"usage"`
}

type InputQuestion struct {
	ID       string   `json:"id"`
	Prompt   string   `json:"prompt"`
	Choices  []string `json:"choices,omitempty"`
	Required bool     `json:"required"`
}

type ObservationState string

const (
	ObservationActive        ObservationState = "active"
	ObservationWaitingInput  ObservationState = "waiting_input"
	ObservationResultPending ObservationState = "result_pending"
	ObservationTerminal      ObservationState = "terminal"
	ObservationLost          ObservationState = "lost"

	// Deprecated compatibility states. Concrete Backends must migrate these
	// to terminal AgentResult values during M2.1.2.
	ObservationExited ObservationState = "exited"
	ObservationError  ObservationState = "error"

	// Deprecated name retained while run.Service moves to result_pending.
	ObservationCompletionMissing = ObservationResultPending
)

type ExecutionObservation struct {
	State      ObservationState `json:"state"`
	Result     *AgentResult     `json:"result,omitempty"`
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

type Capabilities struct {
	Tools                    []string `json:"tools,omitempty"`
	Runtimes                 []string `json:"runtimes,omitempty"`
	SupportsOutput           bool     `json:"supportsOutput"`
	SupportsWaitingInput     bool     `json:"supportsWaitingInput"`
	MaxConcurrentAgents      int      `json:"maxConcurrentAgents,omitempty"`
	SupportsConcurrentCancel bool     `json:"supportsConcurrentCancel"`
}

type DoctorRequest struct {
	Workspace string
	Tool      string
	Runtime   string
}

type DiagnosticStatus string

const (
	DiagnosticOK      DiagnosticStatus = "ok"
	DiagnosticWarning DiagnosticStatus = "warning"
	DiagnosticError   DiagnosticStatus = "error"
)

type Diagnostic struct {
	Name    string           `json:"name"`
	Status  DiagnosticStatus `json:"status"`
	Message string           `json:"message"`
}

type DoctorReport struct {
	Backend     string       `json:"backend"`
	Ready       bool         `json:"ready"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type AgentBackend interface {
	Name() string
	Capabilities() Capabilities
	Doctor(context.Context, DoctorRequest) DoctorReport
	Start(context.Context, AgentExecutionSpec) (*ExecutionHandle, error)
	Observe(context.Context, ExecutionHandle) (*ExecutionObservation, error)
	Output(context.Context, ExecutionHandle, int) (string, error)
	Cancel(context.Context, ExecutionHandle) (*CancelResult, error)
}

// LegacySessionDecoder is used only to resume M2.1.1 Attempts that persisted
// the former Session shape. New Attempts persist ExecutionHandle directly.
type LegacySessionDecoder interface {
	DecodeLegacySession(Session) (*ExecutionHandle, error)
}

func ValidateAgentExecutionSpec(spec AgentExecutionSpec) error {
	if strings.TrimSpace(spec.RunID) == "" {
		return fmt.Errorf("run ID is required")
	}
	if strings.TrimSpace(spec.NodeID) == "" {
		return fmt.Errorf("node ID is required")
	}
	if spec.Attempt < 1 {
		return fmt.Errorf("attempt must be positive")
	}
	if strings.TrimSpace(spec.Workspace) == "" {
		return fmt.Errorf("workspace is required")
	}
	if strings.TrimSpace(spec.Tool) == "" {
		return fmt.Errorf("tool is required")
	}
	if strings.TrimSpace(spec.Runtime) == "" {
		return fmt.Errorf("runtime is required")
	}
	if strings.TrimSpace(spec.Instructions) == "" {
		return fmt.Errorf("instructions are required")
	}
	if spec.ResultContract.MaxBytes < 0 {
		return fmt.Errorf("result max bytes cannot be negative")
	}
	if len(spec.ResultContract.Schema) > 0 && !json.Valid(spec.ResultContract.Schema) {
		return fmt.Errorf("result schema is not valid JSON")
	}
	return nil
}

func ValidateExecutionHandle(handle ExecutionHandle) error {
	if strings.TrimSpace(handle.DriverName()) == "" {
		return fmt.Errorf("handle backend is required")
	}
	if handle.SchemaVersion < 1 {
		return fmt.Errorf("handle schema version must be positive")
	}
	if strings.TrimSpace(handle.ID) == "" {
		return fmt.Errorf("handle ID is required")
	}
	if len(handle.Data) > MaxExecutionHandleDataSize {
		return fmt.Errorf("handle data exceeds %d bytes", MaxExecutionHandleDataSize)
	}
	if len(handle.Data) == 0 {
		return nil
	}
	if !json.Valid(handle.Data) {
		return fmt.Errorf("handle data is not valid JSON")
	}
	var object map[string]any
	if err := json.Unmarshal(handle.Data, &object); err != nil || object == nil {
		return fmt.Errorf("handle data must be a JSON object")
	}
	return nil
}

func ValidateAgentResult(result AgentResult) error {
	switch result.Status {
	case "succeeded", "failed", "needs_input", "indeterminate":
	default:
		return fmt.Errorf("unsupported Agent result status %q", result.Status)
	}
	if strings.TrimSpace(result.Summary) == "" {
		return fmt.Errorf("Agent result summary is required")
	}
	if result.Status == "needs_input" && len(result.Questions) == 0 {
		return fmt.Errorf("needs_input result requires at least one question")
	}
	if result.Status != "needs_input" && len(result.Questions) > 0 {
		return fmt.Errorf("only needs_input result may carry questions")
	}
	if result.Usage.InputTokensEstimated < 0 || result.Usage.OutputTokensEstimated < 0 {
		return fmt.Errorf("Agent result usage cannot be negative")
	}
	return nil
}

func ValidateExecutionObservation(observation ExecutionObservation) error {
	switch observation.State {
	case ObservationActive, ObservationWaitingInput, ObservationResultPending, ObservationLost:
		if observation.Result != nil {
			return fmt.Errorf("observation state %q cannot carry a terminal result", observation.State)
		}
		return nil
	case ObservationTerminal:
		if observation.Result == nil {
			return fmt.Errorf("terminal observation requires an Agent result")
		}
		return ValidateAgentResult(*observation.Result)
	default:
		return fmt.Errorf("unsupported observation state %q", observation.State)
	}
}

func ValidateCancelResult(result CancelResult) error {
	switch result.State {
	case CancelConfirmed, CancelNotConfirmed:
		return nil
	default:
		return fmt.Errorf("unsupported cancel state %q", result.State)
	}
}

// The types below are retained only while the M2.1.1 CC-Panes implementation
// and run.Service migrate to AgentBackend. New Backends must implement
// AgentBackend and must not use this legacy surface.
type LaunchSpec struct {
	RunID    string
	Project  string
	Tool     string
	Runtime  string
	Prompt   string
	StateDir string
}

type Session struct {
	ID       string
	Metadata map[string]string
}

type BackendResult = AgentResult
type Observation = ExecutionObservation

type Backend interface {
	Name() string
	Doctor(context.Context) error
	Launch(context.Context, LaunchSpec) (*Session, error)
	Wait(context.Context, Session) (*BackendResult, error)
	Output(context.Context, Session, int) (string, error)
	Cancel(context.Context, Session) error
}

type ProjectDoctor interface {
	DoctorProject(context.Context, string) error
}

// Reconciler is a temporary compatibility interface. Observe is mandatory on
// AgentBackend and replaces this optional capability.
type Reconciler interface {
	Reconcile(context.Context, Session) (*Observation, error)
}
