package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"wf.local/wf-engine/internal/teamcontract"
)

func TestTeamPrivateSessionRecordsAreOpaqueBoundedJSON(t *testing.T) {
	state := New(t.TempDir())
	value := json.RawMessage(`{"schemaVersion":"private/v1","opaque":{"revision":1}}`)
	if err := state.WriteTeamParticipantSession("team-private", "participant-1", value); err != nil {
		t.Fatal(err)
	}
	read, err := state.ReadTeamParticipantSession("team-private", "participant-1")
	if err != nil || !sameJSON(read, value) {
		t.Fatalf("participant Session=%s err=%v", read, err)
	}
	if err := state.WriteTeamSessionExecution("team-private", "turn-1", value); err != nil {
		t.Fatal(err)
	}
	read, err = state.ReadTeamSessionExecution("team-private", "turn-1")
	if err != nil || !sameJSON(read, value) {
		t.Fatalf("Session execution=%s err=%v", read, err)
	}
	if err := state.WriteTeamParticipantSession("team-private", "participant-1", json.RawMessage(`{"broken":`)); err == nil {
		t.Fatal("invalid private JSON was accepted")
	}
	oversized := json.RawMessage(`"` + strings.Repeat("x", 2*teamcontract.MaxExecutionHandleBytes) + `"`)
	if err := state.WriteTeamSessionExecution("team-private", "turn-1", oversized); err == nil {
		t.Fatal("oversized private Session execution was accepted")
	}
}

func sameJSON(left, right []byte) bool {
	var leftValue, rightValue any
	return json.Unmarshal(left, &leftValue) == nil && json.Unmarshal(right, &rightValue) == nil && reflect.DeepEqual(leftValue, rightValue)
}

func testTeamSnapshot() teamcontract.TeamSessionV1 {
	now := time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC)
	return teamcontract.TeamSessionV1{
		SchemaVersion: teamcontract.SchemaVersion, TeamID: "team-store-1", ClientRequestID: "request-1", RequestHash: strings.Repeat("c", 64), Project: `C:\project`,
		Topic: "compare", CatalogHash: strings.Repeat("a", 64), State: teamcontract.LifecycleRunning, StateVersion: 1, CostGrant: teamcontract.DefaultCostGrant, CreatedAt: now, UpdatedAt: now,
		Participants: []teamcontract.ParticipantV1{
			{ParticipantID: "participant-1", Label: "architect", Role: "design", ModelID: "codex/local/gpt-5.6", Driver: "codex", Target: "local", State: teamcontract.ParticipantPending},
			{ParticipantID: "participant-2", Label: "reviewer", Role: "review", ModelID: "codex/local/gpt-5.6-luna", Driver: "codex", Target: "local", State: teamcontract.ParticipantPending},
		},
	}
}

func TestTeamStorePersistsIndependentAggregateAndStrictLogs(t *testing.T) {
	state := New(t.TempDir())
	snapshot := testTeamSnapshot()
	if err := state.InitTeam(snapshot.TeamID); err != nil {
		t.Fatal(err)
	}
	if err := state.EnsureTeamSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	var loaded teamcontract.TeamSessionV1
	if err := state.ReadTeamSnapshot(snapshot.TeamID, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.TeamID != snapshot.TeamID || loaded.Project != snapshot.Project {
		t.Fatalf("loaded=%+v", loaded)
	}
	if _, err := os.Stat(state.RunDir(snapshot.TeamID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Team state unexpectedly used run path: %v", err)
	}

	hash := strings.Repeat("b", 64)
	for sequence := uint64(1); sequence <= 2; sequence++ {
		if err := state.AppendTeamEvent(teamcontract.TeamEventV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: snapshot.TeamID, Sequence: sequence, Type: teamcontract.EventParticipantEvent, StateVersion: sequence, TurnID: "turn-1", Summary: "bounded", CreatedAt: snapshot.CreatedAt}); err != nil {
			t.Fatal(err)
		}
		if err := state.AppendTeamMessage(teamcontract.TeamMessageV1{SchemaVersion: teamcontract.SchemaVersion, MessageID: fmt.Sprintf("message-%d", sequence), TeamID: snapshot.TeamID, Sequence: sequence, Kind: teamcontract.MessageHost, Actor: "host", Content: "message", ContentHash: hash, CreatedAt: snapshot.CreatedAt}); err != nil {
			t.Fatal(err)
		}
	}
	events, err := state.ReadTeamEvents(snapshot.TeamID)
	if err != nil || len(events) != 2 || events[1].Sequence != 2 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	messages, err := state.ReadTeamMessages(snapshot.TeamID)
	if err != nil || len(messages) != 2 || messages[1].Sequence != 2 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
}

func TestTeamStoreRejectsSequenceGapsAndMessageQuota(t *testing.T) {
	state := New(t.TempDir())
	snapshot := testTeamSnapshot()
	if err := state.InitTeam(snapshot.TeamID); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendTeamEvent(teamcontract.TeamEventV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: snapshot.TeamID, Sequence: 2, Type: teamcontract.EventTeamCreated, StateVersion: 1, CreatedAt: snapshot.CreatedAt}); err == nil {
		t.Fatal("event sequence gap was accepted")
	}
	for sequence := uint64(1); sequence <= teamcontract.MaxRetainedMessages; sequence++ {
		if err := state.AppendTeamMessage(teamcontract.TeamMessageV1{SchemaVersion: teamcontract.SchemaVersion, MessageID: fmt.Sprintf("message-%d", sequence), TeamID: snapshot.TeamID, Sequence: sequence, Kind: teamcontract.MessageHost, Actor: "host", Content: "x", ContentHash: strings.Repeat("a", 64), CreatedAt: snapshot.CreatedAt}); err != nil {
			t.Fatalf("message %d: %v", sequence, err)
		}
	}
	if err := state.AppendTeamMessage(teamcontract.TeamMessageV1{SchemaVersion: teamcontract.SchemaVersion, MessageID: "message-over", TeamID: snapshot.TeamID, Sequence: teamcontract.MaxRetainedMessages + 1, Kind: teamcontract.MessageHost, Actor: "host", Content: "x", ContentHash: strings.Repeat("a", 64), CreatedAt: snapshot.CreatedAt}); err == nil {
		t.Fatal("message quota was not enforced")
	}
}

func TestTeamStoreFaultBoundariesDoNotCommitPartialRecords(t *testing.T) {
	state := New(t.TempDir())
	snapshot := testTeamSnapshot()
	if err := state.InitTeam(snapshot.TeamID); err != nil {
		t.Fatal(err)
	}
	state.SetFaultInjectorForTest(func(operation, _ string) error {
		if operation == "append_team_message" {
			return fmt.Errorf("injected")
		}
		return nil
	})
	err := state.AppendTeamMessage(teamcontract.TeamMessageV1{SchemaVersion: teamcontract.SchemaVersion, MessageID: "message-1", TeamID: snapshot.TeamID, Sequence: 1, Kind: teamcontract.MessageHost, Actor: "host", Content: "x", ContentHash: strings.Repeat("a", 64), CreatedAt: snapshot.CreatedAt})
	if err == nil {
		t.Fatal("faulted message append succeeded")
	}
	state.SetFaultInjectorForTest(nil)
	messages, err := state.ReadTeamMessages(snapshot.TeamID)
	if err != nil || len(messages) != 0 {
		t.Fatalf("faulted append left records=%+v err=%v", messages, err)
	}

	state.SetFaultInjectorForTest(func(operation, path string) error {
		if operation == "write_json" && path == state.TeamSnapshotPath(snapshot.TeamID) {
			return fmt.Errorf("injected")
		}
		return nil
	})
	if err := state.WriteTeamSnapshot(snapshot); err == nil {
		t.Fatal("faulted snapshot write succeeded")
	}
	state.SetFaultInjectorForTest(nil)
	if _, err := os.Stat(state.TeamSnapshotPath(snapshot.TeamID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("faulted snapshot exists: %v", err)
	}
}

func TestTeamStoreRejectsFutureSchemaOnRead(t *testing.T) {
	state := New(t.TempDir())
	snapshot := testTeamSnapshot()
	if err := state.InitTeam(snapshot.TeamID); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), teamcontract.SchemaVersion, "fishyume.team/v99", 1))
	if err := os.WriteFile(state.TeamSnapshotPath(snapshot.TeamID), data, 0o600); err != nil {
		t.Fatal(err)
	}
	var loaded teamcontract.TeamSessionV1
	if err := state.ReadTeamSnapshot(snapshot.TeamID, &loaded); err == nil {
		t.Fatal("future Team schema was accepted")
	}
}

func TestTeamStoreRejectsUnknownSnapshotFields(t *testing.T) {
	state := New(t.TempDir())
	snapshot := testTeamSnapshot()
	if err := state.InitTeam(snapshot.TeamID); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data[:len(data)-1], []byte(`,"unexpected":true}`)...)
	if err := os.WriteFile(state.TeamSnapshotPath(snapshot.TeamID), data, 0o600); err != nil {
		t.Fatal(err)
	}
	var loaded teamcontract.TeamSessionV1
	if err := state.ReadTeamSnapshot(snapshot.TeamID, &loaded); err == nil {
		t.Fatal("unknown Team snapshot field was accepted")
	}
}

func TestTeamStoreKeepsOneBindingPerHandoff(t *testing.T) {
	state := New(t.TempDir())
	snapshot := testTeamSnapshot()
	if err := state.InitTeam(snapshot.TeamID); err != nil {
		t.Fatal(err)
	}
	first := teamcontract.HandoffBindingV1{TeamID: snapshot.TeamID, HandoffID: "handoff-a", RunID: "run-a", Project: snapshot.Project, BoundAt: snapshot.CreatedAt}
	second := teamcontract.HandoffBindingV1{TeamID: snapshot.TeamID, HandoffID: "handoff-b", RunID: "run-b", Project: snapshot.Project, BoundAt: snapshot.CreatedAt.Add(time.Second)}
	if err := state.WriteTeamBinding(first); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTeamBinding(second); err != nil {
		t.Fatal(err)
	}
	bindings, err := state.ReadTeamBindings(snapshot.TeamID)
	if err != nil || len(bindings) != 2 || bindings[0] != first || bindings[1] != second {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	var loaded teamcontract.HandoffBindingV1
	if err := state.ReadTeamBinding(snapshot.TeamID, first.HandoffID, &loaded); err != nil || loaded != first {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	conflict := first
	conflict.RunID = "run-other"
	if err := state.WriteTeamBinding(conflict); err == nil {
		t.Fatal("conflicting binding was accepted")
	}
}

func TestTeamStoreRejectsOversizedBindingCollection(t *testing.T) {
	state := New(t.TempDir())
	snapshot := testTeamSnapshot()
	if err := state.InitTeam(snapshot.TeamID); err != nil {
		t.Fatal(err)
	}
	items := make([]teamcontract.HandoffBindingV1, 0, teamcontract.MaxMutationReceipts+1)
	for index := 0; index <= teamcontract.MaxMutationReceipts; index++ {
		items = append(items, teamcontract.HandoffBindingV1{TeamID: snapshot.TeamID, HandoffID: fmt.Sprintf("handoff-%d", index), RunID: fmt.Sprintf("run-%d", index), Project: snapshot.Project, BoundAt: snapshot.CreatedAt})
	}
	if err := state.writeJSON(state.TeamBindingsPath(snapshot.TeamID), teamHandoffBindingsV1{SchemaVersion: teamcontract.SchemaVersion, Items: items}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReadTeamBindings(snapshot.TeamID); err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("oversized binding collection error=%v", err)
	}
}
