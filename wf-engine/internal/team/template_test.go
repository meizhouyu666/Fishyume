package team

import (
	"testing"

	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/teamcontract"
)

func TestTeamTemplateLifecycleAndStartExpansion(t *testing.T) {
	service := NewService(store.New(t.TempDir()))
	capabilities, err := service.Capabilities()
	if err != nil || len(capabilities.ParticipantTemplates) < 2 {
		t.Fatalf("capabilities=%+v err=%v", capabilities, err)
	}
	template := teamcontract.TeamTemplateUpsertRequestV1{
		SchemaVersion: teamcontract.TemplateSchemaVersion,
		TemplateID:    "campus-research",
		Name:         "秋招研究团队",
		Description:  "负责秋招企业与岗位研究。",
		Color:         "cyan",
		Members: []teamcontract.TeamTemplateMemberV1{
			{Label: "research-lead", RoleHint: "研究负责人", Driver: capabilities.ParticipantTemplates[0].Driver, ModelID: capabilities.ParticipantTemplates[0].ModelID},
			{Label: "evidence-auditor", RoleHint: "证据核验", Driver: capabilities.ParticipantTemplates[1].Driver, ModelID: capabilities.ParticipantTemplates[1].ModelID},
		},
	}
	created, err := service.TemplateUpsert(template)
	if err != nil {
		t.Fatal(err)
	}
	if created.TemplateID != template.TemplateID || created.CreatedAt.IsZero() || created.Color != "cyan" {
		t.Fatalf("created template=%+v", created)
	}
	listed, err := service.TemplateList(teamcontract.TeamTemplateListRequestV1{SchemaVersion: teamcontract.TemplateSchemaVersion})
	if err != nil || len(listed.Items) != 1 {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	started, err := service.Start(t.Context(), teamcontract.TeamStartRequestV1{SchemaVersion: teamcontract.SchemaVersion, ClientRequestID: "template-start-1", Project: t.TempDir(), Mode: teamcontract.ModePanel, Topic: "研究目标", TemplateID: "campus-research"})
	if err != nil {
		t.Fatal(err)
	}
	if len(started.Team.Participants) != 2 || started.Team.Participants[0].Label != "research-lead" || started.Team.Participants[0].Role != "研究负责人" {
		t.Fatalf("template was not expanded: %+v", started.Team.Participants)
	}
	if err := service.TemplateDelete(teamcontract.TeamTemplateDeleteRequestV1{SchemaVersion: teamcontract.TemplateSchemaVersion, TemplateID: "campus-research"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.TemplateGet(teamcontract.TeamTemplateGetRequestV1{SchemaVersion: teamcontract.TemplateSchemaVersion, TemplateID: "campus-research"}); err == nil {
		t.Fatal("deleted template was still readable")
	}
}

func TestTeamTemplateAllowsUnassignedRoutingAndRejectsMismatchedModel(t *testing.T) {
	service := NewService(store.New(t.TempDir()))
	optional := teamcontract.TeamTemplateUpsertRequestV1{
		SchemaVersion: teamcontract.TemplateSchemaVersion,
		TemplateID:    "role-only",
		Name:         "Role only",
		Members: []teamcontract.TeamTemplateMemberV1{
			{Label: "researcher"},
			{Label: "reviewer"},
		},
	}
	if _, err := service.TemplateUpsert(optional); err != nil {
		t.Fatalf("optional routing template rejected: %v", err)
	}
	capabilities, err := service.Capabilities()
	if err != nil || len(capabilities.Harnesses) != 1 || len(capabilities.Harnesses[0].Models) < 2 {
		t.Fatalf("capabilities=%+v err=%v", capabilities, err)
	}
	mismatched := optional
	mismatched.TemplateID = "mismatched"
	mismatched.Members = []teamcontract.TeamTemplateMemberV1{
		{Label: "one", Driver: "codex", ModelID: "missing/model"},
		{Label: "two"},
	}
	if _, err := service.TemplateUpsert(mismatched); err == nil {
		t.Fatal("mismatched model was accepted")
	}
}
