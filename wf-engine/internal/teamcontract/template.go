package teamcontract

import (
	"fmt"
	"strings"
	"time"
)

const (
	TemplateSchemaVersion       = "fishyume.team-template/v1"
	MaxTemplateNameBytes        = 128
	MaxTemplateDescriptionBytes = 4 * 1024
	MaxTemplateRoleHintBytes    = 512
	MaxTemplateColorBytes       = 16
	MaxTemplateModelIDBytes     = 256
)

// TeamTemplateV1 is a reusable Team shape. It contains configuration only;
// running state and task-specific instructions live on TeamSessionV1.
type TeamTemplateV1 struct {
	SchemaVersion string                 `json:"schemaVersion"`
	TemplateID    string                 `json:"templateId"`
	Name          string                 `json:"name"`
	Description   string                 `json:"description,omitempty"`
	Color         string                 `json:"color,omitempty"`
	Members       []TeamTemplateMemberV1 `json:"members"`
	CreatedAt     time.Time              `json:"createdAt"`
	UpdatedAt     time.Time              `json:"updatedAt"`
}

type TeamTemplateMemberV1 struct {
	Label    string `json:"label"`
	RoleHint string `json:"roleHint,omitempty"`
	Driver   string `json:"driver"`
	ModelID  string `json:"modelId"`
}

func ValidateTeamTemplate(value TeamTemplateV1) error {
	if value.SchemaVersion != TemplateSchemaVersion {
		return fmt.Errorf("unsupported team template schema version %q", value.SchemaVersion)
	}
	if err := validateTemplateID(value.TemplateID, "templateId"); err != nil {
		return err
	}
	if err := validateTemplateText(value.Name, MaxTemplateNameBytes, "template name", true); err != nil {
		return err
	}
	if err := validateTemplateText(value.Description, MaxTemplateDescriptionBytes, "template description", false); err != nil {
		return err
	}
	if err := validateTemplateText(value.Color, MaxTemplateColorBytes, "template color", false); err != nil {
		return err
	}
	if value.Color != "" && !validTemplateColor(value.Color) {
		return fmt.Errorf("unsupported template color %q", value.Color)
	}
	if len(value.Members) < MinParticipants || len(value.Members) > MaxParticipants {
		return fmt.Errorf("template members must contain %d-%d entries", MinParticipants, MaxParticipants)
	}
	seen := make(map[string]struct{}, len(value.Members))
	for _, member := range value.Members {
		if err := validateTemplateText(member.Label, MaxParticipantLabelBytes, "template member label", true); err != nil {
			return err
		}
		if err := validateTemplateText(member.RoleHint, MaxTemplateRoleHintBytes, "template member role hint", false); err != nil {
			return err
		}
		if err := validateTemplateID(member.Driver, "template member driver"); err != nil {
			return err
		}
		if err := validateTemplateText(member.ModelID, MaxTemplateModelIDBytes, "template member modelId", true); err != nil {
			return err
		}
		if _, exists := seen[member.Label]; exists {
			return fmt.Errorf("duplicate template member label %q", member.Label)
		}
		seen[member.Label] = struct{}{}
	}
	return nil
}

func validateTemplateID(value, name string) error {
	if !safeAPIID.MatchString(value) {
		return fmt.Errorf("%s must be a bounded safe identifier", name)
	}
	return nil
}

func validateTemplateText(value string, limit int, name string, required bool) error {
	if required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s contains surrounding whitespace", name)
	}
	if err := validateBounded(value, limit, name); err != nil {
		return err
	}
	return nil
}

func validTemplateColor(value string) bool {
	switch value {
	case "cyan", "violet", "blue", "green", "orange", "red", "gray":
		return true
	default:
		return false
	}
}
