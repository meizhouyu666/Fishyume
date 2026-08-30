package teamcontract

import (
	"fmt"
	"regexp"
	"strings"
)

var safeAPIID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,127}$`)

const (
	DefaultListLimit = 50
	MaxListLimit     = 100
	MaxEventWaitMS   = MaxBoundedWaitSeconds * 1000
)

var StableMethods = []string{
	"team.capabilities", "team.start", "team.list", "team.get", "team.events", "team.messages", "team.action",
	"team.handoff.create", "team.handoff.get", "team.handoff.list", "team.handoff.bindRun",
	"team.template.list", "team.template.get", "team.template.upsert", "team.template.delete",
}

type TeamCapabilitiesRequestV1 struct {
	SchemaVersion string `json:"schemaVersion"`
}

type TeamStartResponseV1 struct {
	SchemaVersion string        `json:"schemaVersion"`
	Team          TeamSessionV1 `json:"team"`
	Replayed      bool          `json:"replayed"`
}

type TeamTemplateListRequestV1 struct {
	SchemaVersion string `json:"schemaVersion"`
	Cursor        string `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type TeamTemplateListResponseV1 struct {
	SchemaVersion string           `json:"schemaVersion"`
	Items         []TeamTemplateV1 `json:"items"`
	NextCursor    string           `json:"nextCursor,omitempty"`
}

type TeamTemplateGetRequestV1 struct {
	SchemaVersion string `json:"schemaVersion"`
	TemplateID    string `json:"templateId"`
}

type TeamTemplateUpsertRequestV1 struct {
	SchemaVersion string                 `json:"schemaVersion"`
	TemplateID    string                 `json:"templateId"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description,omitempty"`
	Color         string                 `json:"color,omitempty"`
	Members       []TeamTemplateMemberV1 `json:"members"`
}

type TeamTemplateUpsertResponseV1 struct {
	SchemaVersion string         `json:"schemaVersion"`
	Template      TeamTemplateV1 `json:"template"`
}

type TeamTemplateDeleteRequestV1 struct {
	SchemaVersion string `json:"schemaVersion"`
	TemplateID    string `json:"templateId"`
}

type TeamTemplateDeleteResponseV1 struct {
	SchemaVersion string `json:"schemaVersion"`
	TemplateID    string `json:"templateId"`
}

type TeamSummaryV1 struct {
	TeamID       string      `json:"teamId"`
	Project      string      `json:"project"`
	Mode         Mode        `json:"mode"`
	Topic        string      `json:"topic"`
	State        Lifecycle   `json:"state"`
	StateVersion uint64      `json:"stateVersion"`
	CloseReason  CloseReason `json:"closeReason,omitempty"`
	Participants int         `json:"participants"`
	CostGrant    int         `json:"costGrant"`
	CostUsed     int         `json:"costUsed"`
	CreatedAt    string      `json:"createdAt"`
	UpdatedAt    string      `json:"updatedAt"`
}

type TeamListRequestV1 struct {
	SchemaVersion string    `json:"schemaVersion"`
	Project       string    `json:"project,omitempty"`
	State         Lifecycle `json:"state,omitempty"`
	Cursor        string    `json:"cursor,omitempty"`
	Limit         int       `json:"limit,omitempty"`
}

type TeamListResponseV1 struct {
	SchemaVersion string          `json:"schemaVersion"`
	Items         []TeamSummaryV1 `json:"items"`
	NextCursor    string          `json:"nextCursor,omitempty"`
}

type TeamGetRequestV1 struct {
	SchemaVersion string `json:"schemaVersion"`
	TeamID        string `json:"teamId"`
}

type TeamGetResponseV1 struct {
	SchemaVersion string              `json:"schemaVersion"`
	Team          TeamSessionV1       `json:"team"`
	Turns         []ParticipantTurnV1 `json:"turns"`
}

type TeamEventsRequestV1 struct {
	SchemaVersion string `json:"schemaVersion"`
	TeamID        string `json:"teamId"`
	AfterSequence uint64 `json:"afterSequence,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	WaitMS        int    `json:"waitMs,omitempty"`
}

type TeamEventsResponseV1 struct {
	SchemaVersion     string        `json:"schemaVersion"`
	TeamID            string        `json:"teamId"`
	Events            []TeamEventV1 `json:"events"`
	NextAfterSequence uint64        `json:"nextAfterSequence"`
	More              bool          `json:"more"`
}

type TeamMessagesRequestV1 struct {
	SchemaVersion string `json:"schemaVersion"`
	TeamID        string `json:"teamId"`
	AfterSequence uint64 `json:"afterSequence,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type TeamMessagesResponseV1 struct {
	SchemaVersion     string          `json:"schemaVersion"`
	TeamID            string          `json:"teamId"`
	Messages          []TeamMessageV1 `json:"messages"`
	NextAfterSequence uint64          `json:"nextAfterSequence"`
	More              bool            `json:"more"`
}

type TeamActionResponseV1 struct {
	SchemaVersion string     `json:"schemaVersion"`
	ActionID      string     `json:"actionId"`
	TeamID        string     `json:"teamId"`
	Type          ActionType `json:"type"`
	StateVersion  uint64     `json:"stateVersion"`
	State         Lifecycle  `json:"state"`
	Replayed      bool       `json:"replayed"`
}

type HandoffCreateRequestV1 struct {
	SchemaVersion          string   `json:"schemaVersion"`
	HandoffID              string   `json:"handoffId"`
	TeamID                 string   `json:"teamId"`
	ExpectedStateVersion   uint64   `json:"expectedStateVersion"`
	Goal                   string   `json:"goal"`
	Decisions              []string `json:"decisions,omitempty"`
	Constraints            []string `json:"constraints,omitempty"`
	OpenQuestions          []string `json:"openQuestions,omitempty"`
	AcceptanceExpectations []string `json:"acceptanceExpectations,omitempty"`
	SelectedMessageIDs     []string `json:"selectedMessageIds"`
}

type HandoffCreateResponseV1 struct {
	SchemaVersion string            `json:"schemaVersion"`
	Handoff       HandoffArtifactV1 `json:"handoff"`
	Replayed      bool              `json:"replayed"`
}

type HandoffGetRequestV1 struct {
	SchemaVersion string `json:"schemaVersion"`
	TeamID        string `json:"teamId"`
	HandoffID     string `json:"handoffId"`
}

type HandoffGetResponseV1 struct {
	SchemaVersion string            `json:"schemaVersion"`
	Handoff       HandoffArtifactV1 `json:"handoff"`
	Binding       *HandoffBindingV1 `json:"binding,omitempty"`
}

type HandoffListRequestV1 struct {
	SchemaVersion string `json:"schemaVersion"`
	TeamID        string `json:"teamId"`
	Cursor        string `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type HandoffListResponseV1 struct {
	SchemaVersion string              `json:"schemaVersion"`
	Items         []HandoffArtifactV1 `json:"items"`
	NextCursor    string              `json:"nextCursor,omitempty"`
}

type HandoffBindRunRequestV1 struct {
	SchemaVersion        string `json:"schemaVersion"`
	ActionID             string `json:"actionId"`
	TeamID               string `json:"teamId"`
	HandoffID            string `json:"handoffId"`
	RunID                string `json:"runId"`
	ExpectedStateVersion uint64 `json:"expectedStateVersion"`
}

type HandoffBindRunResponseV1 struct {
	SchemaVersion string           `json:"schemaVersion"`
	Binding       HandoffBindingV1 `json:"binding"`
	Replayed      bool             `json:"replayed"`
}

func ValidateListRequest(value TeamListRequestV1) error {
	if err := validateAPIVersion(value.SchemaVersion); err != nil {
		return err
	}
	if value.Project != "" && (value.Project != strings.TrimSpace(value.Project) || len([]byte(value.Project)) > MaxProjectBytes) {
		return fmt.Errorf("project filter is invalid")
	}
	if value.State != "" && !validLifecycle(value.State) {
		return fmt.Errorf("unsupported team lifecycle %q", value.State)
	}
	return validatePage(value.Cursor, value.Limit)
}

func ValidateTemplateListRequest(value TeamTemplateListRequestV1) error {
	if value.SchemaVersion != TemplateSchemaVersion {
		return fmt.Errorf("unsupported team template schema version %q", value.SchemaVersion)
	}
	return validatePage(value.Cursor, value.Limit)
}

func ValidateTemplateGetRequest(value TeamTemplateGetRequestV1) error {
	if value.SchemaVersion != TemplateSchemaVersion {
		return fmt.Errorf("unsupported team template schema version %q", value.SchemaVersion)
	}
	return validateTemplateID(value.TemplateID, "templateId")
}

func ValidateTemplateUpsertRequest(value TeamTemplateUpsertRequestV1) error {
	template := TeamTemplateV1{SchemaVersion: TemplateSchemaVersion, TemplateID: value.TemplateID, Name: value.Name, Description: value.Description, Color: value.Color, Members: value.Members}
	return ValidateTeamTemplate(template)
}

func ValidateTemplateDeleteRequest(value TeamTemplateDeleteRequestV1) error {
	if value.SchemaVersion != TemplateSchemaVersion {
		return fmt.Errorf("unsupported team template schema version %q", value.SchemaVersion)
	}
	return validateTemplateID(value.TemplateID, "templateId")
}

func ValidateCapabilitiesRequest(value TeamCapabilitiesRequestV1) error {
	return validateAPIVersion(value.SchemaVersion)
}

func ValidateGetRequest(value TeamGetRequestV1) error {
	if err := validateAPIVersion(value.SchemaVersion); err != nil {
		return err
	}
	return validateAPIID(value.TeamID, "teamId")
}

func ValidateEventsRequest(value TeamEventsRequestV1) error {
	if err := validateAPIVersion(value.SchemaVersion); err != nil {
		return err
	}
	if err := validateAPIID(value.TeamID, "teamId"); err != nil {
		return err
	}
	if value.Limit < 0 || value.Limit > MaxEventPageSize {
		return fmt.Errorf("event limit is out of bounds")
	}
	if value.WaitMS < 0 || value.WaitMS > MaxEventWaitMS {
		return fmt.Errorf("event wait is out of bounds")
	}
	return nil
}

func ValidateMessagesRequest(value TeamMessagesRequestV1) error {
	if err := validateAPIVersion(value.SchemaVersion); err != nil {
		return err
	}
	if err := validateAPIID(value.TeamID, "teamId"); err != nil {
		return err
	}
	if value.Limit < 0 || value.Limit > MaxEventPageSize {
		return fmt.Errorf("message limit is out of bounds")
	}
	return nil
}

func ValidateActionRequest(value TeamActionV1) error {
	if err := ValidateAction(value); err != nil {
		return err
	}
	if err := validateAPIID(value.ActionID, "actionId"); err != nil {
		return err
	}
	if err := validateAPIID(value.TeamID, "teamId"); err != nil {
		return err
	}
	return nil
}

func ValidateHandoffCreateRequest(value HandoffCreateRequestV1) error {
	if err := validateAPIVersion(value.SchemaVersion); err != nil {
		return err
	}
	if err := validateAPIID(value.HandoffID, "handoffId"); err != nil {
		return err
	}
	if err := validateAPIID(value.TeamID, "teamId"); err != nil {
		return err
	}
	if value.ExpectedStateVersion == 0 {
		return fmt.Errorf("expectedStateVersion must be positive")
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
	seen := make(map[string]struct{}, len(value.SelectedMessageIDs))
	for _, id := range value.SelectedMessageIDs {
		if err := validateAPIID(id, "selected message"); err != nil {
			return err
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("selected message IDs must be unique")
		}
		seen[id] = struct{}{}
	}
	encoded, err := CanonicalJSON(value)
	if err != nil {
		return err
	}
	if len(encoded) > MaxHandoffBytes {
		return fmt.Errorf("handoff request exceeds %d bytes", MaxHandoffBytes)
	}
	return nil
}

func ValidateHandoffGetRequest(value HandoffGetRequestV1) error {
	if err := validateAPIVersion(value.SchemaVersion); err != nil {
		return err
	}
	if err := validateAPIID(value.TeamID, "teamId"); err != nil {
		return err
	}
	return validateAPIID(value.HandoffID, "handoffId")
}

func ValidateHandoffListRequest(value HandoffListRequestV1) error {
	if err := validateAPIVersion(value.SchemaVersion); err != nil {
		return err
	}
	if err := validateAPIID(value.TeamID, "teamId"); err != nil {
		return err
	}
	return validatePage(value.Cursor, value.Limit)
}

func ValidateHandoffBindRunRequest(value HandoffBindRunRequestV1) error {
	if err := validateAPIVersion(value.SchemaVersion); err != nil {
		return err
	}
	for name, id := range map[string]string{"actionId": value.ActionID, "teamId": value.TeamID, "handoffId": value.HandoffID, "runId": value.RunID} {
		if err := validateAPIID(id, name); err != nil {
			return err
		}
	}
	if value.ExpectedStateVersion == 0 {
		return fmt.Errorf("expectedStateVersion must be positive")
	}
	return nil
}

func validateAPIVersion(value string) error {
	if value != SchemaVersion {
		return fmt.Errorf("unsupported team schema version %q", value)
	}
	return nil
}

func validatePage(cursor string, limit int) error {
	if cursor != "" {
		if err := validateAPIID(cursor, "cursor"); err != nil {
			return err
		}
	}
	if limit < 0 || limit > MaxListLimit {
		return fmt.Errorf("page limit is out of bounds")
	}
	return nil
}

func validateAPIID(value, name string) error {
	if !safeAPIID.MatchString(value) {
		return fmt.Errorf("%s must be a bounded safe identifier", name)
	}
	return nil
}
