package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wf.local/wf-engine/internal/application"
	"wf.local/wf-engine/internal/explorationdriver"
	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/team"
	"wf.local/wf-engine/internal/teamcontract"
)

type rpcTeamDriver struct{}

func (*rpcTeamDriver) Name() string { return "codex" }
func (*rpcTeamDriver) Capabilities() explorationdriver.DriverCapabilities {
	return explorationdriver.DriverCapabilities{Targets: []string{"local"}, SupportsOutput: true, SupportsRecovery: true, SupportsConfirmedCancel: true, SupportsConcurrentCancel: true}
}
func (*rpcTeamDriver) Doctor(context.Context, explorationdriver.DoctorRequest) explorationdriver.DoctorReport {
	return explorationdriver.DoctorReport{Driver: "codex", Ready: true}
}
func (*rpcTeamDriver) Start(_ context.Context, request explorationdriver.StartRequest) (*explorationdriver.ExecutionHandle, error) {
	return &explorationdriver.ExecutionHandle{Driver: "codex", Target: request.Target, SchemaVersion: 1, ID: request.Identity.TurnID, Data: json.RawMessage(`{"fixture":true}`)}, nil
}
func (*rpcTeamDriver) Observe(context.Context, explorationdriver.ExecutionHandle) (*explorationdriver.Observation, error) {
	return &explorationdriver.Observation{State: explorationdriver.ObservationTerminal}, nil
}
func (*rpcTeamDriver) Output(context.Context, explorationdriver.ExecutionHandle, int) (string, error) {
	return `{"schemaVersion":"fishyume.team/v1","status":"completed","contentMarkdown":"RPC contribution"}`, nil
}
func (*rpcTeamDriver) Cancel(context.Context, explorationdriver.ExecutionHandle) (*explorationdriver.CancelResult, error) {
	return &explorationdriver.CancelResult{State: explorationdriver.CancelConfirmed}, nil
}

func newTeamRPCFixture(t *testing.T) (*run.Service, *application.Service, *team.Service, *store.Store) {
	t.Helper()
	state := store.New(t.TempDir())
	runs := run.NewService(&fakeBackend{}, state)
	applications := application.NewService(runs, "codex", state)
	teams := team.NewService(state)
	if err := teams.SetDriver(&rpcTeamDriver{}); err != nil {
		t.Fatal(err)
	}
	return runs, applications, teams, state
}

func callTeamRPC(t *testing.T, runs *run.Service, applications *application.Service, teams *team.Service, method string, params any) Response {
	t.Helper()
	input := request(1, method, params)
	var output bytes.Buffer
	server := NewServerWithTeam(strings.NewReader(input), &output, runs, applications, teams)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &response); err != nil {
		t.Fatalf("response=%q err=%v", output.String(), err)
	}
	return response
}

func TestTeamRPCStartAndCapabilityGates(t *testing.T) {
	runs, applications, teams, state := newTeamRPCFixture(t)
	capability := callTeamRPC(t, runs, applications, teams, "team.capabilities", teamcontract.TeamCapabilitiesRequestV1{SchemaVersion: teamcontract.SchemaVersion})
	if capability.Error != nil {
		t.Fatal(capability.Error)
	}
	encoded, _ := json.Marshal(capability.Result)
	var capabilities teamcontract.TeamCapabilitiesV1
	if err := json.Unmarshal(encoded, &capabilities); err != nil {
		t.Fatal(err)
	}
	if !capabilities.Features.Panel || capabilities.Features.Handoff || len(capabilities.ParticipantTemplates) != 2 {
		t.Fatalf("capabilities=%+v", capabilities)
	}

	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	startedResponse := callTeamRPC(t, runs, applications, teams, "team.start", teamcontract.TeamStartRequestV1{SchemaVersion: teamcontract.SchemaVersion, ClientRequestID: "rpc-start", Project: project, Mode: teamcontract.ModePanel, Topic: "Compare RPC approaches"})
	if startedResponse.Error != nil {
		t.Fatal(startedResponse.Error)
	}
	encoded, _ = json.Marshal(startedResponse.Result)
	var started teamcontract.TeamStartResponseV1
	if err := json.Unmarshal(encoded, &started); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		current, err := teams.Get(started.Team.TeamID)
		if err != nil {
			t.Fatal(err)
		}
		if current.State == teamcontract.LifecycleClosed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Team did not close: %+v", current)
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWait()
	if err := teams.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	if ids, err := state.ListRunIDs(); err != nil || len(ids) != 0 {
		t.Fatalf("Team RPC created Runs=%v err=%v", ids, err)
	}

	handoff := callTeamRPC(t, runs, applications, teams, "team.handoff.get", teamcontract.HandoffGetRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: started.Team.TeamID, HandoffID: "handoff-1"})
	if handoff.Error == nil || handoff.Error.Code != -32010 {
		t.Fatalf("handoff response=%+v", handoff)
	}
}

func TestTeamRPCRejectsUnknownAndUnsupportedInvalidRequests(t *testing.T) {
	runs, applications, teams, _ := newTeamRPCFixture(t)
	unknown := callTeamRPC(t, runs, applications, teams, "team.get", map[string]any{"schemaVersion": teamcontract.SchemaVersion, "teamId": "team-1", "unknown": true})
	if unknown.Error == nil || unknown.Error.Code != -32602 {
		t.Fatalf("unknown response=%+v", unknown)
	}
	invalidHandoff := callTeamRPC(t, runs, applications, teams, "team.handoff.get", map[string]any{"schemaVersion": teamcontract.SchemaVersion, "teamId": "bad id", "handoffId": "handoff-1"})
	if invalidHandoff.Error == nil || invalidHandoff.Error.Code != -32602 {
		t.Fatalf("invalid handoff error=%+v", invalidHandoff.Error)
	}
	unknownModel := callTeamRPC(t, runs, applications, teams, "team.start", teamcontract.TeamStartRequestV1{SchemaVersion: teamcontract.SchemaVersion, ClientRequestID: "unknown-model", Project: t.TempDir(), Mode: teamcontract.ModePanel, Topic: "Compare", Participants: []teamcontract.ParticipantSpecV1{{Label: "one", Role: "first", ModelID: "codex/local/unknown"}, {Label: "two", Role: "second", ModelID: "codex/local/gpt-5.6"}}})
	if unknownModel.Error == nil || unknownModel.Error.Code != -32602 {
		t.Fatalf("unknown model error=%+v", unknownModel.Error)
	}
}
