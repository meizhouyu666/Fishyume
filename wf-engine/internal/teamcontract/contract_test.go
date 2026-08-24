package teamcontract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var contractHash = strings.Repeat("a", 64)

func validTeam() TeamSessionV1 {
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	return TeamSessionV1{
		SchemaVersion: SchemaVersion, TeamID: "team-1", ClientRequestID: "request-1", RequestHash: contractHash, Project: `C:\project`, Mode: ModePanel,
		Topic: "比较两个实现方案", CatalogHash: contractHash, State: LifecycleRunning, StateVersion: 1, CostGrant: DefaultCostGrant, CreatedAt: now, UpdatedAt: now,
		Participants: []ParticipantV1{
			{ParticipantID: "participant-1", Label: "architect", Role: "propose a coherent architecture", ModelID: "codex/local/gpt-5.6", Driver: "codex", Target: "local", State: ParticipantRunning, CurrentTurnID: "turn-1"},
			{ParticipantID: "participant-2", Label: "reviewer", Role: "challenge failure modes", ModelID: "codex/local/gpt-5.6-luna", Driver: "codex", Target: "local", State: ParticipantPending},
		},
	}
}

func validContribution() ContributionV1 {
	return ContributionV1{SchemaVersion: SchemaVersion, Status: ContributionCompleted, ContentMarkdown: "## 结论\n保留边界。", Warnings: []string{"需要验证"}, OpenQuestions: []string{"是否需要迁移？"}, UsageEstimates: &UsageEstimateV1{InputTokens: 10, OutputTokens: 20, TotalTokens: 30}}
}

func TestTeamContractGoldenCanonicalJSONAndHash(t *testing.T) {
	team := validTeam()
	if err := ValidateTeamSession(team); err != nil {
		t.Fatal(err)
	}
	firstHash, firstJSON, err := CanonicalHash(team)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, secondJSON, err := CanonicalHash(team)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash || !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("canonical team encoding is not deterministic")
	}
	if !json.Valid(firstJSON) || !strings.Contains(string(firstJSON), "比较两个实现方案") {
		t.Fatalf("canonical JSON lost CJK content: %s", firstJSON)
	}
	team.Topic += "!"
	changedHash, _, err := CanonicalHash(team)
	if err != nil {
		t.Fatal(err)
	}
	if changedHash == firstHash {
		t.Fatal("canonical hash did not change after mutation")
	}
}

func TestDecodeStrictRejectsUnknownAndTrailingFields(t *testing.T) {
	data := []byte(`{"schemaVersion":"fishyume.team/v1","teamId":"team-1","clientRequestId":"request-1","project":"C:\\project","mode":"panel","topic":"topic","catalogHash":"` + contractHash + `","participants":[],"state":"running","stateVersion":1,"costGrant":1000,"createdAt":"2026-08-24T00:00:00Z","updatedAt":"2026-08-24T00:00:00Z","unexpected":true}`)
	var team TeamSessionV1
	if err := DecodeStrict(data, &team); err == nil {
		t.Fatal("unknown public field was accepted")
	}
	valid, err := CanonicalJSON(validTeam())
	if err != nil {
		t.Fatal(err)
	}
	if err := DecodeStrict(append(valid, []byte(` {}`)...), &team); err == nil {
		t.Fatal("trailing JSON value was accepted")
	}
}

func TestByteBoundariesCountUTF8Bytes(t *testing.T) {
	team := validTeam()
	base := strings.Repeat("界", MaxTopicBytes/len([]byte("界")))
	team.Topic = base + strings.Repeat("x", MaxTopicBytes-len([]byte(base)))
	if len([]byte(team.Topic)) != MaxTopicBytes {
		t.Fatal("test topic did not reach exact byte boundary")
	}
	if err := ValidateTeamSession(team); err != nil {
		t.Fatalf("exact UTF-8 byte boundary rejected: %v", err)
	}
	team.Topic += "x"
	if err := ValidateTeamSession(team); err == nil {
		t.Fatal("topic beyond byte boundary was accepted")
	}
}

func TestValidateContributionAndMessageOwnershipShape(t *testing.T) {
	contribution := validContribution()
	if err := ValidateContribution(contribution); err != nil {
		t.Fatal(err)
	}
	message := TeamMessageV1{SchemaVersion: SchemaVersion, MessageID: "message-1", TeamID: "team-1", Sequence: 1, Kind: MessageContribution, Actor: "participant-1", TurnID: "turn-1", Content: "canonical contribution", ContentHash: contractHash}
	if err := ValidateMessage(message); err != nil {
		t.Fatal(err)
	}
	message.TurnID = ""
	if err := ValidateMessage(message); err == nil {
		t.Fatal("contribution without turn ownership was accepted")
	}
	message.Kind, message.TurnID, message.ContentHash = MessageHost, "", contractHash
	if err := ValidateMessage(message); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAllFrozenActionShapes(t *testing.T) {
	base := TeamActionV1{SchemaVersion: SchemaVersion, ActionID: "action-1", TeamID: "team-1", ExpectedStateVersion: 2}
	valid := []TeamActionV1{
		{SchemaVersion: base.SchemaVersion, ActionID: base.ActionID, TeamID: base.TeamID, ExpectedStateVersion: base.ExpectedStateVersion, Type: ActionFollowUp, FollowUp: &FollowUpActionV1{Content: "next", ParticipantIDs: []string{"participant-1"}}},
		{SchemaVersion: base.SchemaVersion, ActionID: base.ActionID, TeamID: base.TeamID, ExpectedStateVersion: base.ExpectedStateVersion, Type: ActionCancelTurn, CancelTurn: &CancelTurnActionV1{TurnID: "turn-1"}},
		{SchemaVersion: base.SchemaVersion, ActionID: base.ActionID, TeamID: base.TeamID, ExpectedStateVersion: base.ExpectedStateVersion, Type: ActionClose, Close: &CloseActionV1{Reason: CloseHostClosed}},
		{SchemaVersion: base.SchemaVersion, ActionID: base.ActionID, TeamID: base.TeamID, ExpectedStateVersion: base.ExpectedStateVersion, Type: ActionCancel},
	}
	for _, action := range valid {
		if err := ValidateAction(action); err != nil {
			t.Fatalf("valid action rejected: %+v: %v", action, err)
		}
	}
	invalid := valid[0]
	invalid.CancelTurn = &CancelTurnActionV1{TurnID: "turn-1"}
	if err := ValidateAction(invalid); err == nil {
		t.Fatal("action with mismatched payload was accepted")
	}
}

func TestValidateHandoffRequiresBoundedMessageHashes(t *testing.T) {
	handoff := HandoffArtifactV1{SchemaVersion: SchemaVersion, HandoffID: "handoff-1", TeamID: "team-1", SourceTeamVersion: 3, Goal: "formalize the accepted plan", Decisions: []string{"keep the contract"}, SelectedMessageIDs: []string{"message-1"}, SourceMessageHashes: []string{contractHash}, ContentHash: contractHash}
	if err := ValidateHandoff(handoff); err != nil {
		t.Fatal(err)
	}
	handoff.SourceMessageHashes = nil
	if err := ValidateHandoff(handoff); err == nil {
		t.Fatal("handoff with mismatched selected message hashes was accepted")
	}
}

func TestDefaultLimitsMatchFrozenM71Values(t *testing.T) {
	limits := DefaultLimits()
	if limits.MinParticipants != 2 || limits.MaxParticipants != 4 || limits.DefaultCostGrant != 1000 || limits.MaxCostGrant != 6400 || limits.MaxEventPageSize != 100 || limits.BoundedWaitSeconds != 30 {
		t.Fatalf("unexpected default limits: %+v", limits)
	}
}
