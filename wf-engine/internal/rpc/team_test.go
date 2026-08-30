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
	"wf.local/wf-engine/internal/backend"
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
	runs := run.NewService(&fakeBackend{result: backend.AgentResult{Status: "succeeded", Summary: "done"}}, state)
	applications := application.NewService(runs, "codex", state)
	teams := team.NewService(state)
	if err := teams.SetRunLookup(func(runID string) (string, error) {
		snapshot, err := runs.Get(runID)
		return snapshot.Project, err
	}); err != nil {
		t.Fatal(err)
	}
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
	if !capabilities.Features.Panel || !capabilities.Features.Handoff || len(capabilities.ParticipantTemplates) != 2 {
		t.Fatalf("capabilities=%+v", capabilities)
	}

	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	startedResponse := callTeamRPC(t, runs, applications, teams, "team.start", teamcontract.TeamStartRequestV1{SchemaVersion: teamcontract.SchemaVersion, ClientRequestID: "rpc-start", Project: project, Topic: "Compare RPC approaches"})
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

	messages, err := teams.Messages(teamcontract.TeamMessagesRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: started.Team.TeamID, Limit: 100})
	if err != nil || len(messages.Messages) != 2 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	createRequest := teamcontract.HandoffCreateRequestV1{
		SchemaVersion: teamcontract.SchemaVersion, HandoffID: "handoff-1", TeamID: started.Team.TeamID,
		ExpectedStateVersion: currentTeamVersion(t, teams, started.Team.TeamID), Goal: "Promote the accepted RPC design",
		Decisions: []string{"Use the bounded implementation"}, SelectedMessageIDs: []string{messages.Messages[0].MessageID, messages.Messages[1].MessageID},
	}
	createdRPC := callTeamRPC(t, runs, applications, teams, "team.handoff.create", createRequest)
	if createdRPC.Error != nil {
		t.Fatal(createdRPC.Error)
	}
	encoded, _ = json.Marshal(createdRPC.Result)
	var created teamcontract.HandoffCreateResponseV1
	if err := json.Unmarshal(encoded, &created); err != nil || created.Handoff.ContentHash == "" || created.Replayed {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	if ids, err := state.ListRunIDs(); err != nil || len(ids) != 0 {
		t.Fatalf("Handoff RPC created Runs=%v err=%v", ids, err)
	}
	listed := callTeamRPC(t, runs, applications, teams, "team.handoff.list", teamcontract.HandoffListRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: started.Team.TeamID, Limit: 1})
	if listed.Error != nil {
		t.Fatal(listed.Error)
	}
	encoded, _ = json.Marshal(listed.Result)
	var page teamcontract.HandoffListResponseV1
	if err := json.Unmarshal(encoded, &page); err != nil || len(page.Items) != 1 || page.Items[0].HandoffID != createRequest.HandoffID {
		t.Fatalf("page=%+v err=%v", page, err)
	}

	teamSnapshot, err := teams.Get(started.Team.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	sameRun, err := runs.Start(context.Background(), run.StartRequest{Project: teamSnapshot.Project, Task: "execute accepted design"})
	if err != nil {
		t.Fatal(err)
	}
	otherRun, err := runs.Start(context.Background(), run.StartRequest{Project: t.TempDir(), Task: "unrelated work"})
	if err != nil {
		t.Fatal(err)
	}
	controllersDone, cancelControllers := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelControllers()
	if err := applications.CompatibilityWaitControllers(controllersDone); err != nil {
		t.Fatal(err)
	}
	unknown := callTeamRPC(t, runs, applications, teams, "team.handoff.bindRun", teamcontract.HandoffBindRunRequestV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "bind-missing", TeamID: started.Team.TeamID, HandoffID: createRequest.HandoffID, RunID: "run-missing", ExpectedStateVersion: createRequest.ExpectedStateVersion})
	if unknown.Error == nil || unknown.Error.Code != -32004 {
		t.Fatalf("unknown Run response=%+v", unknown)
	}
	crossProject := callTeamRPC(t, runs, applications, teams, "team.handoff.bindRun", teamcontract.HandoffBindRunRequestV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "bind-cross-project", TeamID: started.Team.TeamID, HandoffID: createRequest.HandoffID, RunID: otherRun.ID, ExpectedStateVersion: createRequest.ExpectedStateVersion})
	if crossProject.Error == nil || crossProject.Error.Code != -32009 {
		t.Fatalf("cross-project response=%+v", crossProject)
	}
	bindRequest := teamcontract.HandoffBindRunRequestV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "bind-valid", TeamID: started.Team.TeamID, HandoffID: createRequest.HandoffID, RunID: sameRun.ID, ExpectedStateVersion: createRequest.ExpectedStateVersion}
	bound := callTeamRPC(t, runs, applications, teams, "team.handoff.bindRun", bindRequest)
	if bound.Error != nil {
		t.Fatal(bound.Error)
	}
	shown := callTeamRPC(t, runs, applications, teams, "team.handoff.get", teamcontract.HandoffGetRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: started.Team.TeamID, HandoffID: createRequest.HandoffID})
	if shown.Error != nil {
		t.Fatal(shown.Error)
	}
	encoded, _ = json.Marshal(shown.Result)
	var view teamcontract.HandoffGetResponseV1
	if err := json.Unmarshal(encoded, &view); err != nil || view.Binding == nil || view.Binding.RunID != sameRun.ID {
		t.Fatalf("view=%+v err=%v", view, err)
	}
}

func currentTeamVersion(t *testing.T, teams *team.Service, teamID string) uint64 {
	t.Helper()
	snapshot, err := teams.Get(teamID)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.StateVersion
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
	unknownHandoffField := callTeamRPC(t, runs, applications, teams, "team.handoff.create", map[string]any{"schemaVersion": teamcontract.SchemaVersion, "teamId": "team-1", "handoffId": "handoff-1", "expectedStateVersion": 1, "goal": "goal", "selectedMessageIds": []string{"message-1"}, "unknown": true})
	if unknownHandoffField.Error == nil || unknownHandoffField.Error.Code != -32602 {
		t.Fatalf("unknown Handoff field response=%+v", unknownHandoffField)
	}
	unknownModel := callTeamRPC(t, runs, applications, teams, "team.start", teamcontract.TeamStartRequestV1{SchemaVersion: teamcontract.SchemaVersion, ClientRequestID: "unknown-model", Project: t.TempDir(), Topic: "Compare", Participants: []teamcontract.ParticipantSpecV1{{Label: "one", Role: "first", ModelID: "codex/local/unknown"}, {Label: "two", Role: "second", ModelID: "codex/local/gpt-5.6"}}})
	if unknownModel.Error == nil || unknownModel.Error.Code != -32602 {
		t.Fatalf("unknown model error=%+v", unknownModel.Error)
	}
}
