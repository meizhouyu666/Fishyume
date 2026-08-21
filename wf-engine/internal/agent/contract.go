package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"wf.local/wf-engine/internal/routing"
)

const (
	ProtocolVersion            = 1
	MaxResultBytes             = 64 * 1024
	MaxEventBytes              = 64 * 1024
	MaxExecutionHandleDataSize = 64 * 1024
	MaxIPCFrameBytes           = 1 * 1024 * 1024
)

const (
	ErrorInvalidEnvelope       = "INVALID_ATTEMPT_ENVELOPE"
	ErrorInvalidResult         = "INVALID_AGENT_RESULT"
	ErrorCapabilityUnavailable = "CAPABILITY_UNAVAILABLE"
	ErrorProtocolMismatch      = "PROTOCOL_MISMATCH"
	ErrorConflict              = "STATE_CONFLICT"
	ErrorNotFound              = "NOT_FOUND"
)

type IPCHandshake struct {
	ProtocolVersion int    `json:"protocolVersion"`
	StateSchema     int    `json:"stateSchema"`
	OwnerID         string `json:"ownerId"`
	StateDirHash    string `json:"stateDirHash"`
}

type APIError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type AttemptIdentity struct {
	RunID   string `json:"runId"`
	NodeID  string `json:"nodeId"`
	Attempt int    `json:"attempt"`
}

type UpstreamResult struct {
	NodeID string          `json:"nodeId"`
	Result json.RawMessage `json:"result"`
}

type AttemptContext struct {
	UpstreamResults []UpstreamResult `json:"upstreamResults"`
	RequiredSkills  []string         `json:"requiredSkills"`
	InputAnswer     json.RawMessage  `json:"inputAnswer,omitempty"`
}

type ResultContract struct {
	Schema   json.RawMessage `json:"schema,omitempty"`
	MaxBytes int             `json:"maxBytes"`
}

type AttemptEnvelope struct {
	ProtocolVersion int               `json:"protocolVersion"`
	Identity        AttemptIdentity   `json:"identity"`
	Workspace       string            `json:"workspace"`
	Target          string            `json:"target"`
	Task            string            `json:"task"`
	Context         AttemptContext    `json:"context"`
	Constraints     map[string]string `json:"constraints"`
	Budget          map[string]int64  `json:"budget"`
	ResultContract  ResultContract    `json:"resultContract"`
	// RoutingDecision is the immutable model-routing decision used to create
	// this Attempt. It is absent on historical/compatibility Attempts.
	RoutingDecision *routing.RoutingDecisionV1 `json:"routingDecision,omitempty"`

	// Prompt is compiled deterministically for the external harness but is not
	// serialized into the durable Attempt envelope or execution handle.
	Prompt string `json:"-"`
}

type DriverCapabilities struct {
	Targets                  []string `json:"targets"`
	SupportsOutput           bool     `json:"supportsOutput"`
	SupportsWaitingInput     bool     `json:"supportsWaitingInput"`
	SupportsRecovery         bool     `json:"supportsRecovery"`
	SupportsConfirmedCancel  bool     `json:"supportsConfirmedCancel"`
	SupportsConcurrentCancel bool     `json:"supportsConcurrentCancel"`
	MaxConcurrentAttempts    int      `json:"maxConcurrentAttempts,omitempty"`
}

type ExecutionHandle struct {
	Driver        string          `json:"driver"`
	Target        string          `json:"target"`
	SchemaVersion int             `json:"schemaVersion"`
	ID            string          `json:"id"`
	Data          json.RawMessage `json:"data,omitempty"`
}

type Usage struct {
	InputTokensEstimated  int `json:"inputTokensEstimated"`
	OutputTokensEstimated int `json:"outputTokensEstimated"`
}

type InputQuestion struct {
	ID       string   `json:"id"`
	Prompt   string   `json:"prompt"`
	Choices  []string `json:"choices,omitempty"`
	Required bool     `json:"required"`
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

type DriverEventType string

const (
	EventAttemptStarted       DriverEventType = "attempt.started"
	EventAttemptProgress      DriverEventType = "attempt.progress"
	EventAttemptDiagnostic    DriverEventType = "attempt.diagnostic"
	EventAttemptNeedsInput    DriverEventType = "attempt.needs_input"
	EventAttemptResultPending DriverEventType = "attempt.result_pending"
	EventAttemptCompleted     DriverEventType = "attempt.completed"
)

type DriverEvent struct {
	Type       DriverEventType `json:"type"`
	Message    string          `json:"message,omitempty"`
	Diagnostic string          `json:"diagnostic,omitempty"`
	Result     *AgentResult    `json:"result,omitempty"`
}

type ObservationState string

const (
	ObservationActive        ObservationState = "active"
	ObservationWaitingInput  ObservationState = "waiting_input"
	ObservationResultPending ObservationState = "result_pending"
	ObservationTerminal      ObservationState = "terminal"
	ObservationLost          ObservationState = "lost"
)

type ExecutionObservation struct {
	State      ObservationState `json:"state"`
	Events     []DriverEvent    `json:"events,omitempty"`
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

type DoctorRequest struct {
	Workspace string
	Target    string
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

type AgentDriver interface {
	Name() string
	Capabilities() DriverCapabilities
	Doctor(context.Context, DoctorRequest) DoctorReport
	Start(context.Context, AttemptEnvelope) (*ExecutionHandle, error)
	Observe(context.Context, ExecutionHandle) (*ExecutionObservation, error)
	Output(context.Context, ExecutionHandle, int) (string, error)
	Cancel(context.Context, ExecutionHandle) (*CancelResult, error)
}

func ValidateCapabilities(capabilities DriverCapabilities) error {
	if len(capabilities.Targets) == 0 {
		return fmt.Errorf("driver must declare at least one target")
	}
	seen := make(map[string]bool, len(capabilities.Targets))
	for _, target := range capabilities.Targets {
		if strings.TrimSpace(target) == "" || target != strings.TrimSpace(target) {
			return fmt.Errorf("driver target is empty or contains surrounding whitespace")
		}
		if seen[target] {
			return fmt.Errorf("driver target %q is duplicated", target)
		}
		seen[target] = true
	}
	if capabilities.MaxConcurrentAttempts < 0 {
		return fmt.Errorf("max concurrent attempts cannot be negative")
	}
	if !capabilities.SupportsConfirmedCancel && capabilities.SupportsConcurrentCancel {
		return fmt.Errorf("concurrent cancellation requires confirmed cancellation")
	}
	return nil
}

func ValidateAttemptEnvelope(envelope AttemptEnvelope) error {
	if envelope.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported Attempt envelope protocol version %d", envelope.ProtocolVersion)
	}
	if strings.TrimSpace(envelope.Identity.RunID) == "" || strings.TrimSpace(envelope.Identity.NodeID) == "" || envelope.Identity.Attempt < 1 {
		return fmt.Errorf("Attempt identity is incomplete")
	}
	if strings.TrimSpace(envelope.Workspace) == "" || strings.TrimSpace(envelope.Task) == "" {
		return fmt.Errorf("workspace and task are required")
	}
	if strings.TrimSpace(envelope.Target) == "" || envelope.Target != strings.TrimSpace(envelope.Target) {
		return fmt.Errorf("Attempt target is empty or contains surrounding whitespace")
	}
	if envelope.Context.UpstreamResults == nil || envelope.Context.RequiredSkills == nil || envelope.Constraints == nil || envelope.Budget == nil {
		return fmt.Errorf("Attempt envelope collections must be explicit")
	}
	if envelope.ResultContract.MaxBytes < 1 || envelope.ResultContract.MaxBytes > MaxResultBytes {
		return fmt.Errorf("result max bytes must be between 1 and %d", MaxResultBytes)
	}
	if len(envelope.ResultContract.Schema) > 0 && !json.Valid(envelope.ResultContract.Schema) {
		return fmt.Errorf("result schema is not valid JSON")
	}
	if envelope.RoutingDecision != nil {
		if err := routing.ValidateDecision(*envelope.RoutingDecision); err != nil {
			return fmt.Errorf("routing decision is invalid: %w", err)
		}
	}
	for _, upstream := range envelope.Context.UpstreamResults {
		if strings.TrimSpace(upstream.NodeID) == "" || len(upstream.Result) == 0 || !json.Valid(upstream.Result) {
			return fmt.Errorf("upstream result is invalid")
		}
	}
	return nil
}

func ValidateExecutionHandle(handle ExecutionHandle) error {
	if strings.TrimSpace(handle.Driver) == "" || strings.TrimSpace(handle.Target) == "" || strings.TrimSpace(handle.ID) == "" {
		return fmt.Errorf("execution handle identity is incomplete")
	}
	if handle.SchemaVersion < 1 {
		return fmt.Errorf("handle schema version must be positive")
	}
	if len(handle.Data) > MaxExecutionHandleDataSize {
		return fmt.Errorf("handle data exceeds %d bytes", MaxExecutionHandleDataSize)
	}
	if len(handle.Data) > 0 && !json.Valid(handle.Data) {
		return fmt.Errorf("handle data is not valid JSON")
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
	if len([]byte(result.Summary)) > 16*1024 {
		return fmt.Errorf("Agent result summary exceeds 16384 bytes")
	}
	if len(result.Artifacts) > 256 || len(result.Warnings) > 256 || len(result.Checks) > 256 || len(result.Questions) > 32 {
		return fmt.Errorf("Agent result collection exceeds its bounded limit")
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
	questionIDs := make(map[string]struct{}, len(result.Questions))
	for _, question := range result.Questions {
		if strings.TrimSpace(question.ID) == "" || strings.TrimSpace(question.Prompt) == "" {
			return fmt.Errorf("Agent input question identity is incomplete")
		}
		if _, exists := questionIDs[question.ID]; exists {
			return fmt.Errorf("Agent input question ID %q is duplicated", question.ID)
		}
		questionIDs[question.ID] = struct{}{}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode Agent result: %w", err)
	}
	if len(encoded) > MaxResultBytes {
		return fmt.Errorf("Agent result exceeds %d bytes", MaxResultBytes)
	}
	return nil
}

func ValidateObservation(observation ExecutionObservation) error {
	for _, event := range observation.Events {
		switch event.Type {
		case EventAttemptStarted, EventAttemptProgress, EventAttemptDiagnostic, EventAttemptNeedsInput, EventAttemptResultPending, EventAttemptCompleted:
		default:
			return fmt.Errorf("unsupported Driver event type %q", event.Type)
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("encode Driver event: %w", err)
		}
		if len(encoded) > MaxEventBytes {
			return fmt.Errorf("Driver event exceeds %d bytes", MaxEventBytes)
		}
		switch event.Type {
		case EventAttemptCompleted:
			if event.Result == nil {
				return fmt.Errorf("attempt.completed event requires an Agent result")
			}
			if event.Result.Status == "needs_input" {
				return fmt.Errorf("needs_input result requires an attempt.needs_input event")
			}
			if err := ValidateAgentResult(*event.Result); err != nil {
				return fmt.Errorf("attempt.completed event result: %w", err)
			}
		case EventAttemptNeedsInput:
			if event.Result != nil {
				if event.Result.Status != "needs_input" {
					return fmt.Errorf("attempt.needs_input event result must use needs_input status")
				}
				if err := ValidateAgentResult(*event.Result); err != nil {
					return fmt.Errorf("attempt.needs_input event result: %w", err)
				}
			}
		default:
			if event.Result != nil {
				return fmt.Errorf("Driver event %q cannot carry an Agent result", event.Type)
			}
		}
	}
	switch observation.State {
	case ObservationActive, ObservationWaitingInput, ObservationResultPending, ObservationLost:
		if observation.Result != nil {
			return fmt.Errorf("observation state %q cannot carry a terminal result", observation.State)
		}
	case ObservationTerminal:
		if observation.Result == nil {
			return fmt.Errorf("terminal observation requires an Agent result")
		}
		return ValidateAgentResult(*observation.Result)
	default:
		return fmt.Errorf("unsupported observation state %q", observation.State)
	}
	return nil
}

func ValidateCancelResult(result CancelResult) error {
	if result.State != CancelConfirmed && result.State != CancelNotConfirmed {
		return fmt.Errorf("unsupported cancel state %q", result.State)
	}
	return nil
}

func ValidateIPCHandshake(handshake IPCHandshake) error {
	if handshake.ProtocolVersion < 1 || handshake.StateSchema < 1 || strings.TrimSpace(handshake.OwnerID) == "" || strings.TrimSpace(handshake.StateDirHash) == "" {
		return fmt.Errorf("IPC handshake identity is incomplete")
	}
	return nil
}

func ValidateAPIError(value APIError) error {
	switch value.Code {
	case ErrorInvalidEnvelope, ErrorInvalidResult, ErrorCapabilityUnavailable, ErrorProtocolMismatch, ErrorConflict, ErrorNotFound:
	default:
		return fmt.Errorf("unsupported API error code %q", value.Code)
	}
	if strings.TrimSpace(value.Message) == "" {
		return fmt.Errorf("API error message is required")
	}
	if len(value.Data) > 0 && !json.Valid(value.Data) {
		return fmt.Errorf("API error data is not valid JSON")
	}
	return nil
}
