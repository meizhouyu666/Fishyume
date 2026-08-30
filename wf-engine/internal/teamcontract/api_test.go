package teamcontract

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestTeamContractFreezeMatchesImplementation(t *testing.T) {
	data, err := os.ReadFile("../../../contracts/fishyume-team-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var freeze struct {
		SchemaVersion string       `json:"schemaVersion"`
		Status        string       `json:"status"`
		APIVersion    string       `json:"apiVersion"`
		Methods       []string     `json:"methods"`
		Actions       []ActionType `json:"actions"`
		ErrorCodes    []ErrorCode  `json:"errorCodes"`
		Limits        TeamLimitsV1 `json:"limits"`
	}
	if err := json.Unmarshal(data, &freeze); err != nil {
		t.Fatal(err)
	}
	if freeze.SchemaVersion != "fishyume.team-contract-freeze/v1" || freeze.Status != "frozen" || freeze.APIVersion != SchemaVersion {
		t.Fatalf("invalid Team freeze identity: %+v", freeze)
	}
	if !reflect.DeepEqual(freeze.Methods, StableMethods) {
		t.Fatalf("methods=%v want %v", freeze.Methods, StableMethods)
	}
	wantActions := []ActionType{ActionCancel}
	wantErrors := []ErrorCode{ErrorInvalidArgument, ErrorNotFound, ErrorConflict, ErrorCapabilityUnavailable, ErrorQuotaExceeded, ErrorNotReady, ErrorProtocolMismatch, ErrorInternal}
	if !reflect.DeepEqual(freeze.Actions, wantActions) || !reflect.DeepEqual(freeze.ErrorCodes, wantErrors) || !reflect.DeepEqual(freeze.Limits, DefaultLimits()) {
		t.Fatalf("Team freeze values do not match implementation")
	}
}

func TestHandoffCreateRequestRejectsDuplicateMessagesAndAggregateOverflow(t *testing.T) {
	request := HandoffCreateRequestV1{SchemaVersion: SchemaVersion, HandoffID: "handoff-1", TeamID: "team-1", ExpectedStateVersion: 1, Goal: "goal", SelectedMessageIDs: []string{"message-1", "message-1"}}
	if err := ValidateHandoffCreateRequest(request); err == nil {
		t.Fatal("duplicate selected message IDs were accepted")
	}
	request.SelectedMessageIDs = []string{"message-1"}
	request.Decisions = []string{strings.Repeat("a", MaxMessageBytes), strings.Repeat("b", MaxMessageBytes), strings.Repeat("c", MaxMessageBytes), strings.Repeat("d", MaxMessageBytes)}
	if err := ValidateHandoffCreateRequest(request); err == nil || !strings.Contains(err.Error(), "request exceeds") {
		t.Fatalf("aggregate overflow error=%v", err)
	}
}

func TestTeamAPIRequestsRejectUnknownFieldsAndBounds(t *testing.T) {
	var request TeamEventsRequestV1
	if err := DecodeStrict([]byte(`{"schemaVersion":"fishyume.team/v1","teamId":"team-1","unknown":true}`), &request); err == nil {
		t.Fatal("unknown Team API field was accepted")
	}
	request = TeamEventsRequestV1{SchemaVersion: SchemaVersion, TeamID: "team-1", Limit: MaxEventPageSize + 1}
	if err := ValidateEventsRequest(request); err == nil {
		t.Fatal("oversized event page was accepted")
	}
	request = TeamEventsRequestV1{SchemaVersion: SchemaVersion, TeamID: "team-1", WaitMS: MaxEventWaitMS + 1}
	if err := ValidateEventsRequest(request); err == nil {
		t.Fatal("oversized event wait was accepted")
	}
}
