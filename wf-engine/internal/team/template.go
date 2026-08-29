package team

import (
	"errors"
	"os"
	"time"

	"wf.local/wf-engine/internal/teamcontract"
)

func (s *Service) TemplateList(request teamcontract.TeamTemplateListRequestV1) (teamcontract.TeamTemplateListResponseV1, error) {
	if err := teamcontract.ValidateTemplateListRequest(request); err != nil {
		return teamcontract.TeamTemplateListResponseV1{}, err
	}
	ids, err := s.state.ListTeamTemplateIDs()
	if err != nil {
		return teamcontract.TeamTemplateListResponseV1{}, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = teamcontract.DefaultListLimit
	}
	items := make([]teamcontract.TeamTemplateV1, 0, limit)
	next := ""
	for _, id := range ids {
		if request.Cursor != "" && id <= request.Cursor {
			continue
		}
		var item teamcontract.TeamTemplateV1
		if err := s.state.ReadTeamTemplate(id, &item); err != nil {
			return teamcontract.TeamTemplateListResponseV1{}, err
		}
		if len(items) == limit {
			next = item.TemplateID
			break
		}
		items = append(items, item)
	}
	return teamcontract.TeamTemplateListResponseV1{SchemaVersion: teamcontract.TemplateSchemaVersion, Items: items, NextCursor: next}, nil
}

func (s *Service) TemplateGet(request teamcontract.TeamTemplateGetRequestV1) (teamcontract.TeamTemplateV1, error) {
	if err := teamcontract.ValidateTemplateGetRequest(request); err != nil {
		return teamcontract.TeamTemplateV1{}, err
	}
	var item teamcontract.TeamTemplateV1
	if err := s.state.ReadTeamTemplate(request.TemplateID, &item); err != nil {
		return teamcontract.TeamTemplateV1{}, err
	}
	return item, nil
}

func (s *Service) TemplateUpsert(request teamcontract.TeamTemplateUpsertRequestV1) (teamcontract.TeamTemplateV1, error) {
	if err := teamcontract.ValidateTemplateUpsertRequest(request); err != nil {
		return teamcontract.TeamTemplateV1{}, err
	}
	now := time.Now().UTC()
	item := teamcontract.TeamTemplateV1{SchemaVersion: teamcontract.TemplateSchemaVersion, TemplateID: request.TemplateID, Name: request.Name, Description: request.Description, Color: request.Color, Members: append([]teamcontract.TeamTemplateMemberV1(nil), request.Members...), CreatedAt: now, UpdatedAt: now}
	var existing teamcontract.TeamTemplateV1
	if err := s.state.ReadTeamTemplate(request.TemplateID, &existing); err == nil {
		item.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, os.ErrNotExist) {
		return teamcontract.TeamTemplateV1{}, err
	}
	if item.Color == "" {
		item.Color = "cyan"
	}
	if err := s.state.WriteTeamTemplate(item); err != nil {
		return teamcontract.TeamTemplateV1{}, err
	}
	return item, nil
}

func (s *Service) TemplateDelete(request teamcontract.TeamTemplateDeleteRequestV1) error {
	if err := teamcontract.ValidateTemplateDeleteRequest(request); err != nil {
		return err
	}
	return s.state.DeleteTeamTemplate(request.TemplateID)
}

func templateParticipantSpecs(value teamcontract.TeamTemplateV1) []teamcontract.ParticipantSpecV1 {
	specs := make([]teamcontract.ParticipantSpecV1, 0, len(value.Members))
	for _, member := range value.Members {
		role := member.RoleHint
		if role == "" {
			role = member.Label
		}
		specs = append(specs, teamcontract.ParticipantSpecV1{Label: member.Label, Role: role, ModelID: member.ModelID})
	}
	return specs
}
