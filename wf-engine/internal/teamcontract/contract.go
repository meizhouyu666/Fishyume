// Package teamcontract defines the frozen fishyume.team/v1 domain contract.
// It is intentionally independent from Workflow, Context, and Memory types.
package teamcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	SchemaVersion = "fishyume.team/v1"

	MinParticipants            = 2
	MaxParticipants            = 4
	MaxProjectBytes            = 4 * 1024
	MaxTopicBytes              = 16 * 1024
	MaxInstructionsBytes       = 16 * 1024
	MaxParticipantLabelBytes   = 64
	MaxParticipantRoleBytes    = 2 * 1024
	MaxMessageBytes            = 32 * 1024
	MaxRetainedMessages        = 256
	MaxRetainedMessageBytes    = 2 * 1024 * 1024
	MaxParticipantTurns        = 64
	MaxActiveTurns             = 4
	MaxMutationReceipts        = 256
	DefaultCostGrant           = 1000
	MaxCostGrant               = 6400
	MaxHandoffSelectedMessages = 32
	MaxHandoffBytes            = 128 * 1024
	MaxEventPageSize           = 100
	MaxBoundedWaitSeconds      = 30
	MaxWarningBytes            = 2 * 1024
	MaxOpenQuestionBytes       = 2 * 1024
	MaxParticipantTemplates    = 4
	MaxHarnessOptions          = 16
	MaxModelsPerHarness        = 256
	MaxExecutionHandleBytes    = 64 * 1024
)

type Mode string

const (
	ModePanel Mode = "panel"
)

type Lifecycle string

const (
	LifecycleCreated    Lifecycle = "created"
	LifecycleRunning    Lifecycle = "running"
	LifecycleOpen       Lifecycle = "open"
	LifecycleClosing    Lifecycle = "closing"
	LifecycleCancelling Lifecycle = "cancelling"
	LifecycleClosed     Lifecycle = "closed"
)

type CloseReason string

const (
	ClosePanelSettled CloseReason = "panel_settled"
	CloseHostClosed   CloseReason = "host_closed"
	CloseCancelled    CloseReason = "cancelled"
)

type ParticipantState string

const (
	ParticipantPending       ParticipantState = "pending"
	ParticipantRunning       ParticipantState = "running"
	ParticipantResponded     ParticipantState = "responded"
	ParticipantFailed        ParticipantState = "failed"
	ParticipantIndeterminate ParticipantState = "indeterminate"
	ParticipantCancelled     ParticipantState = "cancelled"
)

type TurnState string

const (
	TurnPrepared      TurnState = "prepared"
	TurnDispatching   TurnState = "dispatching"
	TurnActive        TurnState = "active"
	TurnResponded     TurnState = "responded"
	TurnFailed        TurnState = "failed"
	TurnIndeterminate TurnState = "indeterminate"
	TurnCancelling    TurnState = "cancelling"
	TurnCancelled     TurnState = "cancelled"
)

type MessageKind string

const (
	MessageHost         MessageKind = "host_message"
	MessageContribution MessageKind = "participant_contribution"
)

type ContributionStatus string

const (
	ContributionCompleted ContributionStatus = "completed"
	ContributionPartial   ContributionStatus = "partial"
	ContributionUnable    ContributionStatus = "unable"
)

// ContributionType identifies the stable first-party result renderers. The
// payload remains JSON so clients can evolve their presentation independently.
type ContributionType string

const (
	ContributionReport   ContributionType = "report"
	ContributionDecision ContributionType = "decision"
	ContributionArtifact ContributionType = "artifact"
	ContributionData     ContributionType = "data"
	ContributionQuestion ContributionType = "question"
)

type ActionType string

const (
	ActionCancel ActionType = "cancel"
)

type EventType string

const (
	EventTeamCreated         EventType = "team.created"
	EventParticipantPrepared EventType = "participant.prepared"
	EventParticipantActive   EventType = "participant.active"
	EventParticipantEvent    EventType = "participant.event"
	EventMessageCommitted    EventType = "message.committed"
	EventTeamClosed          EventType = "team.closed"
	EventTeamCancelled       EventType = "team.cancelled"
	EventHandoffCreated      EventType = "handoff.created"
	EventHandoffBound        EventType = "handoff.bound"
)

type ErrorCode string

const (
	ErrorInvalidArgument       ErrorCode = "invalid_argument"
	ErrorNotFound              ErrorCode = "not_found"
	ErrorConflict              ErrorCode = "conflict"
	ErrorCapabilityUnavailable ErrorCode = "capability_unavailable"
	ErrorQuotaExceeded         ErrorCode = "quota_exceeded"
	ErrorNotReady              ErrorCode = "not_ready"
	ErrorProtocolMismatch      ErrorCode = "protocol_mismatch"
	ErrorInternal              ErrorCode = "internal"
)

type TeamSessionV1 struct {
	SchemaVersion   string          `json:"schemaVersion"`
	TeamID          string          `json:"teamId"`
	ClientRequestID string          `json:"clientRequestId"`
	RequestHash     string          `json:"requestHash"`
	Project         string          `json:"project"`
	Topic           string          `json:"topic"`
	Instructions    string          `json:"instructions,omitempty"`
	CatalogHash     string          `json:"catalogHash"`
	Participants    []ParticipantV1 `json:"participants"`
	State           Lifecycle       `json:"state"`
	StateVersion    uint64          `json:"stateVersion"`
	CostGrant       int             `json:"costGrant"`
	CostUsed        int             `json:"costUsed"`
	CloseReason     CloseReason     `json:"closeReason,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	// LegacySession is set only when a pre-panel-only snapshot contained
	// mode: session. It is process-local metadata and is never serialized.
	LegacySession bool `json:"-"`
}

type ParticipantSpecV1 struct {
	Label   string `json:"label"`
	Role    string `json:"role"`
	ModelID string `json:"modelId"`
}

type TeamStartRequestV1 struct {
	SchemaVersion   string              `json:"schemaVersion"`
	ClientRequestID string              `json:"clientRequestId"`
	Project         string              `json:"project"`
	Topic           string              `json:"topic"`
	Instructions    string              `json:"instructions,omitempty"`
	TemplateID      string              `json:"templateId,omitempty"`
	Participants    []ParticipantSpecV1 `json:"participants,omitempty"`
	CostGrant       int                 `json:"costGrant,omitempty"`
	// Mode is accepted for wire compatibility with older clients. New clients
	// omit it; session mode is no longer executable.
	Mode string `json:"mode,omitempty"`
}

type ParticipantV1 struct {
	ParticipantID string           `json:"participantId"`
	Label         string           `json:"label"`
	Role          string           `json:"role"`
	ModelID       string           `json:"modelId"`
	Driver        string           `json:"driver"`
	Target        string           `json:"target"`
	State         ParticipantState `json:"state"`
	CurrentTurnID string           `json:"currentTurnId,omitempty"`
}

type ParticipantTurnV1 struct {
	SchemaVersion       string          `json:"schemaVersion"`
	TeamID              string          `json:"teamId"`
	ParticipantID       string          `json:"participantId"`
	TurnID              string          `json:"turnId"`
	Number              int             `json:"number"`
	State               TurnState       `json:"state"`
	Driver              string          `json:"driver"`
	Target              string          `json:"target"`
	ModelID             string          `json:"modelId"`
	Usage               TeamTurnUsageV1 `json:"usage"`
	ExecutionHandle     json.RawMessage `json:"executionHandle,omitempty"`
	ContributionMessage string          `json:"contributionMessageId,omitempty"`
	Diagnostic          string          `json:"diagnostic,omitempty"`
	CreatedAt           time.Time       `json:"createdAt"`
	UpdatedAt           time.Time       `json:"updatedAt"`
	CompletedAt         *time.Time      `json:"completedAt,omitempty"`
}

type TeamMessageV1 struct {
	SchemaVersion        string      `json:"schemaVersion"`
	MessageID            string      `json:"messageId"`
	TeamID               string      `json:"teamId"`
	Sequence             uint64      `json:"sequence"`
	Kind                 MessageKind `json:"kind"`
	Actor                string      `json:"actor"`
	Recipients           []string    `json:"recipients,omitempty"`
	TurnID               string      `json:"turnId,omitempty"`
	Content              string      `json:"content"`
	ReferencedMessageIDs []string    `json:"referencedMessageIds,omitempty"`
	CreatedAt            time.Time   `json:"createdAt"`
	ContentHash          string      `json:"contentHash"`
}

type TeamEventV1 struct {
	SchemaVersion string    `json:"schemaVersion"`
	TeamID        string    `json:"teamId"`
	Sequence      uint64    `json:"sequence"`
	Type          EventType `json:"type"`
	StateVersion  uint64    `json:"stateVersion"`
	MessageID     string    `json:"messageId,omitempty"`
	TurnID        string    `json:"turnId,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	CreatedAt     time.Time `json:"createdAt"`
}

type ContributionV1 struct {
	SchemaVersion   string             `json:"schemaVersion"`
	Status          ContributionStatus `json:"status"`
	ResultType      ContributionType   `json:"resultType,omitempty"`
	Output          json.RawMessage    `json:"output,omitempty"`
	ContentMarkdown string             `json:"contentMarkdown,omitempty"`
	Warnings        []string           `json:"warnings,omitempty"`
	OpenQuestions   []string           `json:"openQuestions,omitempty"`
	UsageEstimates  *UsageEstimateV1   `json:"usageEstimates,omitempty"`
}

type UsageEstimateV1 struct {
	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
	TotalTokens  int `json:"totalTokens,omitempty"`
}

type TeamTurnUsageV1 struct {
	Target              string           `json:"target"`
	CatalogHash         string           `json:"catalogHash"`
	CostUnits           int              `json:"costUnits"`
	CumulativeCostUnits int              `json:"cumulativeCostUnits"`
	TokenEstimate       *UsageEstimateV1 `json:"tokenEstimate,omitempty"`
}

type ParticipantTemplateV1 struct {
	Label   string `json:"label"`
	Role    string `json:"role"`
	ModelID string `json:"modelId"`
	Driver  string `json:"driver"`
	Target  string `json:"target"`
}

// HarnessModelOptionV1 describes a model route selectable by a template.
// Credentials and runtime policy remain owned by the selected Harness.
type HarnessModelOptionV1 struct {
	ModelID  string `json:"modelId"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type HarnessCapabilityV1 struct {
	Driver string                 `json:"driver"`
	Models []HarnessModelOptionV1 `json:"models"`
}

type TeamLimitsV1 struct {
	MinParticipants            int `json:"minParticipants"`
	MaxParticipants            int `json:"maxParticipants"`
	MaxProjectBytes            int `json:"maxProjectBytes"`
	MaxTopicBytes              int `json:"maxTopicBytes"`
	MaxInstructionsBytes       int `json:"maxInstructionsBytes"`
	MaxMessageBytes            int `json:"maxMessageBytes"`
	MaxRetainedMessages        int `json:"maxRetainedMessages"`
	MaxRetainedMessageBytes    int `json:"maxRetainedMessageBytes"`
	MaxParticipantTurns        int `json:"maxParticipantTurns"`
	MaxActiveTurns             int `json:"maxActiveTurns"`
	MaxMutationReceipts        int `json:"maxMutationReceipts"`
	DefaultCostGrant           int `json:"defaultCostGrant"`
	MaxCostGrant               int `json:"maxCostGrant"`
	MaxHandoffSelectedMessages int `json:"maxHandoffSelectedMessages"`
	MaxHandoffBytes            int `json:"maxHandoffBytes"`
	MaxEventPageSize           int `json:"maxEventPageSize"`
	BoundedWaitSeconds         int `json:"boundedWaitSeconds"`
}

type TeamFeatureFlagsV1 struct {
	Panel   bool `json:"panel"`
	Handoff bool `json:"handoff"`
	Cancel  bool `json:"cancel"`
}

type TeamCapabilitiesV1 struct {
	SchemaVersion        string                  `json:"schemaVersion"`
	Features             TeamFeatureFlagsV1      `json:"features"`
	Limits               TeamLimitsV1            `json:"limits"`
	ParticipantTemplates []ParticipantTemplateV1 `json:"participantTemplates"`
	Harnesses            []HarnessCapabilityV1   `json:"harnesses"`
	CatalogHash          string                  `json:"catalogHash"`
}

type TeamActionV1 struct {
	SchemaVersion        string     `json:"schemaVersion"`
	ActionID             string     `json:"actionId"`
	TeamID               string     `json:"teamId"`
	ExpectedStateVersion uint64     `json:"expectedStateVersion"`
	Type                 ActionType `json:"type"`
}

type HandoffArtifactV1 struct {
	SchemaVersion          string    `json:"schemaVersion"`
	HandoffID              string    `json:"handoffId"`
	TeamID                 string    `json:"teamId"`
	SourceTeamVersion      uint64    `json:"sourceTeamVersion"`
	Goal                   string    `json:"goal"`
	Decisions              []string  `json:"decisions,omitempty"`
	Constraints            []string  `json:"constraints,omitempty"`
	OpenQuestions          []string  `json:"openQuestions,omitempty"`
	AcceptanceExpectations []string  `json:"acceptanceExpectations,omitempty"`
	SelectedMessageIDs     []string  `json:"selectedMessageIds"`
	SourceMessageHashes    []string  `json:"sourceMessageHashes"`
	ContentHash            string    `json:"contentHash"`
	CreatedAt              time.Time `json:"createdAt"`
}

type HandoffBindingV1 struct {
	TeamID    string    `json:"teamId"`
	HandoffID string    `json:"handoffId"`
	RunID     string    `json:"runId"`
	Project   string    `json:"project"`
	BoundAt   time.Time `json:"boundAt"`
}

func ValidateHandoffBinding(value HandoffBindingV1) error {
	if err := validateID(value.TeamID, "teamId"); err != nil {
		return err
	}
	if err := validateID(value.HandoffID, "handoffId"); err != nil {
		return err
	}
	if err := validateID(value.RunID, "runId"); err != nil {
		return err
	}
	if strings.TrimSpace(value.Project) == "" || value.Project != strings.TrimSpace(value.Project) {
		return fmt.Errorf("project is required without surrounding whitespace")
	}
	return validateBounded(value.Project, MaxProjectBytes, "project")
}

type TeamErrorV1 struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	Retryable bool      `json:"retryable,omitempty"`
}

func DefaultLimits() TeamLimitsV1 {
	return TeamLimitsV1{MinParticipants: MinParticipants, MaxParticipants: MaxParticipants, MaxProjectBytes: MaxProjectBytes, MaxTopicBytes: MaxTopicBytes, MaxInstructionsBytes: MaxInstructionsBytes, MaxMessageBytes: MaxMessageBytes, MaxRetainedMessages: MaxRetainedMessages, MaxRetainedMessageBytes: MaxRetainedMessageBytes, MaxParticipantTurns: MaxParticipantTurns, MaxActiveTurns: MaxActiveTurns, MaxMutationReceipts: MaxMutationReceipts, DefaultCostGrant: DefaultCostGrant, MaxCostGrant: MaxCostGrant, MaxHandoffSelectedMessages: MaxHandoffSelectedMessages, MaxHandoffBytes: MaxHandoffBytes, MaxEventPageSize: MaxEventPageSize, BoundedWaitSeconds: MaxBoundedWaitSeconds}
}

func DecodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

// DecodeTeamSessionCompat reads both current panel snapshots and snapshots
// written before TeamSession mode was removed. Legacy session snapshots are
// readable but marked so recovery will not replay them.
func DecodeTeamSessionCompat(data []byte, target *TeamSessionV1) error {
	var value struct {
		TeamSessionV1
		Mode string `json:"mode,omitempty"`
	}
	if err := DecodeStrict(data, &value); err != nil {
		return err
	}
	*target = value.TeamSessionV1
	switch value.Mode {
	case "", string(ModePanel):
		// Current snapshots and legacy panel snapshots are equivalent.
	case "session":
		target.LegacySession = true
	default:
		return fmt.Errorf("unsupported legacy team mode %q", value.Mode)
	}
	return ValidateTeamSession(*target)
}

func CanonicalJSON(value any) ([]byte, error) { return json.Marshal(value) }

func CanonicalHash(value any) (string, []byte, error) {
	canonical, err := CanonicalJSON(value)
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), canonical, nil
}

func ValidateTeamSession(value TeamSessionV1) error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported team schema version %q", value.SchemaVersion)
	}
	if err := validateID(value.TeamID, "teamId"); err != nil {
		return err
	}
	if err := validateID(value.ClientRequestID, "clientRequestId"); err != nil {
		return err
	}
	if !validHash(value.RequestHash) {
		return fmt.Errorf("requestHash must be a SHA-256 hex digest")
	}
	if err := validateBounded(value.Project, MaxProjectBytes, "project"); err != nil {
		return err
	}
	if err := validateBounded(value.Topic, MaxTopicBytes, "topic"); err != nil {
		return err
	}
	if err := validateBounded(value.Instructions, MaxInstructionsBytes, "instructions"); err != nil {
		return err
	}
	if !validHash(value.CatalogHash) {
		return fmt.Errorf("catalogHash must be a SHA-256 hex digest")
	}
	if len(value.Participants) < MinParticipants || len(value.Participants) > MaxParticipants {
		return fmt.Errorf("participants must contain %d-%d entries", MinParticipants, MaxParticipants)
	}
	if value.StateVersion == 0 {
		return fmt.Errorf("stateVersion must be positive")
	}
	if value.CostGrant < 1 || value.CostGrant > MaxCostGrant || value.CostUsed < 0 || value.CostUsed > value.CostGrant {
		return fmt.Errorf("team cost accounting is invalid")
	}
	if !validLifecycle(value.State) {
		return fmt.Errorf("unsupported team lifecycle %q", value.State)
	}
	if value.State == LifecycleClosed && !validCloseReason(value.CloseReason) {
		return fmt.Errorf("closed team requires a valid close reason")
	}
	if value.State != LifecycleClosed && value.CloseReason != "" {
		return fmt.Errorf("open team cannot have a close reason")
	}
	seenIDs, seenModels := map[string]struct{}{}, map[string]struct{}{}
	for _, participant := range value.Participants {
		if err := ValidateParticipant(participant); err != nil {
			return err
		}
		if _, exists := seenIDs[participant.ParticipantID]; exists {
			return fmt.Errorf("duplicate participantId %q", participant.ParticipantID)
		}
		if _, exists := seenModels[participant.ModelID]; exists {
			return fmt.Errorf("duplicate participant modelId %q", participant.ModelID)
		}
		seenIDs[participant.ParticipantID] = struct{}{}
		seenModels[participant.ModelID] = struct{}{}
	}
	return nil
}

func ValidateStartRequest(value TeamStartRequestV1) error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported team schema version %q", value.SchemaVersion)
	}
	if err := validateID(value.ClientRequestID, "clientRequestId"); err != nil {
		return err
	}
	if err := validateBounded(value.Project, MaxProjectBytes, "project"); err != nil {
		return err
	}
	if strings.TrimSpace(value.Project) == "" {
		return fmt.Errorf("project is required")
	}
	if strings.TrimSpace(value.Topic) == "" {
		return fmt.Errorf("topic is required")
	}
	if err := validateBounded(value.Topic, MaxTopicBytes, "topic"); err != nil {
		return err
	}
	if err := validateBounded(value.Instructions, MaxInstructionsBytes, "instructions"); err != nil {
		return err
	}
	if value.Mode != "" && value.Mode != string(ModePanel) && value.Mode != "session" {
		return fmt.Errorf("unsupported team mode %q", value.Mode)
	}
	if value.TemplateID != "" {
		if err := validateTemplateID(value.TemplateID, "templateId"); err != nil {
			return err
		}
	}
	if len(value.Participants) != 0 && (len(value.Participants) < MinParticipants || len(value.Participants) > MaxParticipants) {
		return fmt.Errorf("explicit participants must contain %d-%d entries", MinParticipants, MaxParticipants)
	}
	for _, participant := range value.Participants {
		if err := validateBounded(participant.Label, MaxParticipantLabelBytes, "participant label"); err != nil {
			return err
		}
		if err := validateBounded(participant.Role, MaxParticipantRoleBytes, "participant role"); err != nil {
			return err
		}
		if strings.TrimSpace(participant.Label) == "" || strings.TrimSpace(participant.Role) == "" || strings.TrimSpace(participant.ModelID) == "" {
			return fmt.Errorf("participant label, role, and modelId are required")
		}
		if participant.Label != strings.TrimSpace(participant.Label) || participant.Role != strings.TrimSpace(participant.Role) || participant.ModelID != strings.TrimSpace(participant.ModelID) {
			return fmt.Errorf("participant fields cannot contain surrounding whitespace")
		}
	}
	if value.CostGrant < 0 || value.CostGrant > MaxCostGrant {
		return fmt.Errorf("costGrant is out of bounds")
	}
	return nil
}

func ValidateParticipant(value ParticipantV1) error {
	if err := validateID(value.ParticipantID, "participantId"); err != nil {
		return err
	}
	if err := validateBounded(value.Label, MaxParticipantLabelBytes, "participant label"); err != nil {
		return err
	}
	if err := validateBounded(value.Role, MaxParticipantRoleBytes, "participant role"); err != nil {
		return err
	}
	for name, field := range map[string]string{"modelId": value.ModelID, "driver": value.Driver, "target": value.Target} {
		if err := validateID(field, name); err != nil {
			return err
		}
	}
	if !validParticipantState(value.State) {
		return fmt.Errorf("unsupported participant state %q", value.State)
	}
	if value.CurrentTurnID != "" {
		if err := validateID(value.CurrentTurnID, "currentTurnId"); err != nil {
			return err
		}
	}
	return nil
}

func ValidateParticipantTurn(value ParticipantTurnV1) error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported team schema version %q", value.SchemaVersion)
	}
	for name, field := range map[string]string{"teamId": value.TeamID, "participantId": value.ParticipantID, "turnId": value.TurnID, "driver": value.Driver, "target": value.Target, "modelId": value.ModelID} {
		if err := validateID(field, name); err != nil {
			return err
		}
	}
	if value.Number < 1 || value.Number > MaxParticipantTurns {
		return fmt.Errorf("turn number is out of bounds")
	}
	if !validTurnState(value.State) {
		return fmt.Errorf("unsupported turn state %q", value.State)
	}
	if err := ValidateUsage(value.Usage); err != nil {
		return err
	}
	if len(value.ExecutionHandle) > 0 && !json.Valid(value.ExecutionHandle) {
		return fmt.Errorf("executionHandle must be valid JSON")
	}
	if len(value.ExecutionHandle) > MaxExecutionHandleBytes {
		return fmt.Errorf("executionHandle exceeds %d bytes", MaxExecutionHandleBytes)
	}
	if value.ContributionMessage != "" {
		if err := validateID(value.ContributionMessage, "contributionMessageId"); err != nil {
			return err
		}
	}
	if len([]byte(value.Diagnostic)) > MaxWarningBytes {
		return fmt.Errorf("turn diagnostic exceeds %d bytes", MaxWarningBytes)
	}
	return nil
}

func ValidateMessage(value TeamMessageV1) error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported team schema version %q", value.SchemaVersion)
	}
	for name, field := range map[string]string{"messageId": value.MessageID, "teamId": value.TeamID, "actor": value.Actor} {
		if err := validateID(field, name); err != nil {
			return err
		}
	}
	if value.Sequence == 0 {
		return fmt.Errorf("message sequence must be positive")
	}
	if value.Kind != MessageHost && value.Kind != MessageContribution {
		return fmt.Errorf("unsupported message kind %q", value.Kind)
	}
	if err := validateBounded(value.Content, MaxMessageBytes, "message content"); err != nil {
		return err
	}
	if value.Kind == MessageContribution && value.TurnID == "" {
		return fmt.Errorf("contribution message requires turnId")
	}
	if value.Kind == MessageHost && value.TurnID != "" {
		return fmt.Errorf("host message cannot have turnId")
	}
	for _, id := range append(append([]string{}, value.Recipients...), value.ReferencedMessageIDs...) {
		if err := validateID(id, "message reference"); err != nil {
			return err
		}
	}
	if !validHash(value.ContentHash) {
		return fmt.Errorf("message contentHash must be a SHA-256 hex digest")
	}
	return nil
}

func ValidateEvent(value TeamEventV1) error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported team schema version %q", value.SchemaVersion)
	}
	if err := validateID(value.TeamID, "teamId"); err != nil {
		return err
	}
	if value.Sequence == 0 || value.StateVersion == 0 {
		return fmt.Errorf("event sequence and stateVersion must be positive")
	}
	switch value.Type {
	case EventTeamCreated, EventParticipantPrepared, EventParticipantActive, EventParticipantEvent, EventMessageCommitted, EventTeamClosed, EventTeamCancelled, EventHandoffCreated, EventHandoffBound:
	default:
		return fmt.Errorf("unsupported team event type %q", value.Type)
	}
	if value.MessageID != "" {
		if err := validateID(value.MessageID, "messageId"); err != nil {
			return err
		}
	}
	if value.TurnID != "" {
		if err := validateID(value.TurnID, "turnId"); err != nil {
			return err
		}
	}
	return validateBounded(value.Summary, MaxWarningBytes, "event summary")
}

func ValidateContribution(value ContributionV1) error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported team schema version %q", value.SchemaVersion)
	}
	if value.Status != ContributionCompleted && value.Status != ContributionPartial && value.Status != ContributionUnable {
		return fmt.Errorf("unsupported contribution status %q", value.Status)
	}
	if value.ContentMarkdown == "" && len(bytes.TrimSpace(value.Output)) == 0 {
		return fmt.Errorf("contribution must include contentMarkdown or output")
	}
	if value.ContentMarkdown != "" {
		if err := validateBounded(value.ContentMarkdown, MaxMessageBytes, "contribution content"); err != nil {
			return err
		}
	}
	if len(value.Output) > 0 {
		switch value.ResultType {
		case ContributionReport, ContributionDecision, ContributionArtifact, ContributionData, ContributionQuestion:
		default:
			return fmt.Errorf("unsupported contribution resultType %q", value.ResultType)
		}
		if len(value.Output) > MaxMessageBytes || !json.Valid(value.Output) || string(bytes.TrimSpace(value.Output)) == "null" {
			return fmt.Errorf("contribution output must be valid JSON within %d bytes", MaxMessageBytes)
		}
	} else if value.ResultType != "" {
		return fmt.Errorf("contribution resultType requires output")
	}
	for _, warning := range value.Warnings {
		if err := validateBounded(warning, MaxWarningBytes, "warning"); err != nil {
			return err
		}
	}
	for _, question := range value.OpenQuestions {
		if err := validateBounded(question, MaxOpenQuestionBytes, "open question"); err != nil {
			return err
		}
	}
	if value.UsageEstimates != nil {
		if err := ValidateUsageEstimate(*value.UsageEstimates); err != nil {
			return err
		}
	}
	return nil
}

func ValidateUsage(value TeamTurnUsageV1) error {
	if err := validateID(value.Target, "usage target"); err != nil {
		return err
	}
	if !validHash(value.CatalogHash) {
		return fmt.Errorf("usage catalogHash must be a SHA-256 hex digest")
	}
	if value.CostUnits < 0 || value.CumulativeCostUnits < value.CostUnits {
		return fmt.Errorf("usage cost accounting is invalid")
	}
	if value.TokenEstimate != nil {
		return ValidateUsageEstimate(*value.TokenEstimate)
	}
	return nil
}

func ValidateUsageEstimate(value UsageEstimateV1) error {
	if value.InputTokens < 0 || value.OutputTokens < 0 || value.TotalTokens < 0 {
		return fmt.Errorf("usage estimates cannot be negative")
	}
	if value.TotalTokens != 0 && value.TotalTokens < value.InputTokens+value.OutputTokens {
		return fmt.Errorf("total token estimate is inconsistent")
	}
	return nil
}

func ValidateCapabilities(value TeamCapabilitiesV1) error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported team schema version %q", value.SchemaVersion)
	}
	if err := validateHashOrEmpty(value.CatalogHash, "catalogHash"); err != nil {
		return err
	}
	if len(value.ParticipantTemplates) == 0 || len(value.ParticipantTemplates) > MaxParticipantTemplates {
		return fmt.Errorf("participantTemplates count is out of bounds")
	}
	for _, template := range value.ParticipantTemplates {
		if err := validateBounded(template.Label, MaxParticipantLabelBytes, "template label"); err != nil {
			return err
		}
		if err := validateBounded(template.Role, MaxParticipantRoleBytes, "template role"); err != nil {
			return err
		}
		for name, field := range map[string]string{"template modelId": template.ModelID, "template driver": template.Driver, "template target": template.Target} {
			if err := validateID(field, name); err != nil {
				return err
			}
		}
	}
	if len(value.Harnesses) > MaxHarnessOptions {
		return fmt.Errorf("harnesses count is out of bounds")
	}
	seenDrivers := make(map[string]struct{}, len(value.Harnesses))
	for _, harness := range value.Harnesses {
		if err := validateID(harness.Driver, "harness driver"); err != nil {
			return err
		}
		if _, exists := seenDrivers[harness.Driver]; exists {
			return fmt.Errorf("duplicate harness driver %q", harness.Driver)
		}
		seenDrivers[harness.Driver] = struct{}{}
		if len(harness.Models) == 0 || len(harness.Models) > MaxModelsPerHarness {
			return fmt.Errorf("models for harness %q are out of bounds", harness.Driver)
		}
		seenModels := make(map[string]struct{}, len(harness.Models))
		for _, model := range harness.Models {
			for name, field := range map[string]string{"harness modelId": model.ModelID, "harness provider": model.Provider, "harness model": model.Model} {
				if err := validateID(field, name); err != nil {
					return err
				}
			}
			if _, exists := seenModels[model.ModelID]; exists {
				return fmt.Errorf("duplicate model %q for harness %q", model.ModelID, harness.Driver)
			}
			seenModels[model.ModelID] = struct{}{}
		}
	}
	return nil
}

func ValidateAction(value TeamActionV1) error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported team schema version %q", value.SchemaVersion)
	}
	if err := validateID(value.ActionID, "actionId"); err != nil {
		return err
	}
	if err := validateID(value.TeamID, "teamId"); err != nil {
		return err
	}
	if value.ExpectedStateVersion == 0 {
		return fmt.Errorf("expectedStateVersion must be positive")
	}
	if value.Type != ActionCancel {
		return fmt.Errorf("unsupported action type %q", value.Type)
	}
	return nil
}

func ValidateHandoff(value HandoffArtifactV1) error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported team schema version %q", value.SchemaVersion)
	}
	if err := validateID(value.HandoffID, "handoffId"); err != nil {
		return err
	}
	if err := validateID(value.TeamID, "teamId"); err != nil {
		return err
	}
	if value.SourceTeamVersion == 0 {
		return fmt.Errorf("sourceTeamVersion must be positive")
	}
	if strings.TrimSpace(value.Goal) == "" || value.Goal != strings.TrimSpace(value.Goal) {
		return fmt.Errorf("handoff goal is required without surrounding whitespace")
	}
	if err := validateBounded(value.Goal, MaxMessageBytes, "handoff goal"); err != nil {
		return err
	}
	for name, values := range map[string][]string{"decisions": value.Decisions, "constraints": value.Constraints, "openQuestions": value.OpenQuestions, "acceptanceExpectations": value.AcceptanceExpectations} {
		for _, item := range values {
			if err := validateBounded(item, MaxMessageBytes, name); err != nil {
				return err
			}
		}
	}
	if len(value.SelectedMessageIDs) == 0 || len(value.SelectedMessageIDs) > MaxHandoffSelectedMessages {
		return fmt.Errorf("selected message count is out of bounds")
	}
	if len(value.SelectedMessageIDs) != len(value.SourceMessageHashes) {
		return fmt.Errorf("selected message IDs and hashes must have equal length")
	}
	seen := make(map[string]struct{}, len(value.SelectedMessageIDs))
	for _, id := range value.SelectedMessageIDs {
		if err := validateID(id, "selected message"); err != nil {
			return err
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("selected message IDs must be unique")
		}
		seen[id] = struct{}{}
	}
	for _, hash := range value.SourceMessageHashes {
		if !validHash(hash) {
			return fmt.Errorf("source message hash must be a SHA-256 hex digest")
		}
	}
	if !validHash(value.ContentHash) {
		return fmt.Errorf("handoff contentHash must be a SHA-256 hex digest")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > MaxHandoffBytes {
		return fmt.Errorf("handoff exceeds %d bytes", MaxHandoffBytes)
	}
	return nil
}

func ValidateError(value TeamErrorV1) error {
	if !validErrorCode(value.Code) {
		return fmt.Errorf("unsupported team error code %q", value.Code)
	}
	return validateBounded(value.Message, MaxMessageBytes, "error message")
}

func validLifecycle(value Lifecycle) bool {
	switch value {
	case LifecycleCreated, LifecycleRunning, LifecycleOpen, LifecycleClosing, LifecycleCancelling, LifecycleClosed:
		return true
	}
	return false
}
func validParticipantState(value ParticipantState) bool {
	switch value {
	case ParticipantPending, ParticipantRunning, ParticipantResponded, ParticipantFailed, ParticipantIndeterminate, ParticipantCancelled:
		return true
	}
	return false
}
func validTurnState(value TurnState) bool {
	switch value {
	case TurnPrepared, TurnDispatching, TurnActive, TurnResponded, TurnFailed, TurnIndeterminate, TurnCancelling, TurnCancelled:
		return true
	}
	return false
}
func validCloseReason(value CloseReason) bool {
	return value == ClosePanelSettled || value == CloseHostClosed || value == CloseCancelled
}
func validErrorCode(value ErrorCode) bool {
	switch value {
	case ErrorInvalidArgument, ErrorNotFound, ErrorConflict, ErrorCapabilityUnavailable, ErrorQuotaExceeded, ErrorNotReady, ErrorProtocolMismatch, ErrorInternal:
		return true
	}
	return false
}
func validateID(value, name string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s contains surrounding whitespace", name)
	}
	return nil
}
func validateBounded(value string, limit int, name string) error {
	if len([]byte(value)) > limit {
		return fmt.Errorf("%s exceeds %d bytes", name, limit)
	}
	return nil
}
func validHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func validateHashOrEmpty(value, name string) error {
	if value != "" && !validHash(value) {
		return fmt.Errorf("%s must be a SHA-256 hex digest", name)
	}
	return nil
}
