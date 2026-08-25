package team

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/teamcontract"
)

func completedPanel(t *testing.T, state *store.Store, service *Service, requestID string) (teamcontract.TeamSessionV1, []teamcontract.TeamMessageV1) {
	t.Helper()
	if err := service.SetDriver(&immediateDriver{output: `{"schemaVersion":"fishyume.team/v1","status":"completed","contentMarkdown":"public answer"}`}); err != nil {
		t.Fatal(err)
	}
	started, err := service.Start(context.Background(), startRequest(t.TempDir(), requestID))
	if err != nil {
		t.Fatal(err)
	}
	finished, err := service.DispatchInitial(context.Background(), started.Team.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := state.ReadTeamMessages(finished.TeamID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	return finished, messages
}

func handoffRequest(team teamcontract.TeamSessionV1, messages []teamcontract.TeamMessageV1, id string) teamcontract.HandoffCreateRequestV1 {
	return teamcontract.HandoffCreateRequestV1{
		SchemaVersion: teamcontract.SchemaVersion, HandoffID: id, TeamID: team.TeamID, ExpectedStateVersion: team.StateVersion,
		Goal: "Implement the selected design", Decisions: []string{"Use the smaller design"}, Constraints: []string{"Keep the public Run contract unchanged"},
		OpenQuestions: []string{"Which rollout window?"}, AcceptanceExpectations: []string{"All repository gates pass"},
		SelectedMessageIDs: []string{messages[0].MessageID, messages[1].MessageID},
	}
}

func TestHandoffCreateGetListAreImmutableAndIdempotent(t *testing.T) {
	state := store.New(t.TempDir())
	service := NewService(state)
	panel, messages := completedPanel(t, state, service, "handoff-create")
	request := handoffRequest(panel, messages, "handoff-a")
	created, err := service.HandoffCreate(request)
	if err != nil || created.Replayed {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	if created.Handoff.SourceTeamVersion != panel.StateVersion || len(created.Handoff.SourceMessageHashes) != 2 || created.Handoff.ContentHash == "" {
		t.Fatalf("artifact=%+v", created.Handoff)
	}
	if ids, err := state.ListRunIDs(); err != nil || len(ids) != 0 {
		t.Fatalf("Handoff created Runs=%v err=%v", ids, err)
	}
	replayed, err := service.HandoffCreate(request)
	if err != nil || !replayed.Replayed || !reflect.DeepEqual(replayed.Handoff, created.Handoff) {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	changed := request
	changed.Decisions = []string{"Use a different design"}
	if _, err := service.HandoffCreate(changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed request error=%v", err)
	}

	second := handoffRequest(panel, messages, "handoff-b")
	second.Decisions = []string{"Use a second design"}
	secondResult, err := service.HandoffCreate(second)
	if err != nil || secondResult.Handoff.ContentHash == created.Handoff.ContentHash {
		t.Fatalf("second=%+v err=%v", secondResult, err)
	}
	page, err := service.HandoffList(teamcontract.HandoffListRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: panel.TeamID, Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	next, err := service.HandoffList(teamcontract.HandoffListRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: panel.TeamID, Cursor: page.NextCursor, Limit: 1})
	if err != nil || len(next.Items) != 1 || next.Items[0].HandoffID == page.Items[0].HandoffID {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	view, err := service.HandoffGet(teamcontract.HandoffGetRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: panel.TeamID, HandoffID: request.HandoffID})
	if err != nil || view.Binding != nil || !reflect.DeepEqual(view.Handoff, created.Handoff) {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	events, err := state.ReadTeamEvents(panel.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	createdEvents := 0
	for _, event := range events {
		if event.Type == teamcontract.EventHandoffCreated {
			createdEvents++
		}
	}
	if createdEvents != 2 {
		t.Fatalf("handoff creation events=%d", createdEvents)
	}
}

func TestHandoffHashIgnoresTimestampButCoversDecisions(t *testing.T) {
	base := teamcontract.HandoffArtifactV1{SchemaVersion: teamcontract.SchemaVersion, HandoffID: "handoff-hash", TeamID: "team-hash", SourceTeamVersion: 4, Goal: "goal", Decisions: []string{"one"}, SelectedMessageIDs: []string{"message-1"}, SourceMessageHashes: []string{strings.Repeat("a", 64)}, CreatedAt: time.Unix(1, 0).UTC()}
	first, err := handoffContentHash(base)
	if err != nil {
		t.Fatal(err)
	}
	base.CreatedAt = time.Unix(2, 0).UTC()
	second, _ := handoffContentHash(base)
	if first != second {
		t.Fatalf("timestamp changed content hash: %s != %s", first, second)
	}
	base.Decisions = []string{"two"}
	changed, _ := handoffContentHash(base)
	if changed == first {
		t.Fatal("decision did not change content hash")
	}
}

func TestHandoffRejectsMissingAndAlteredSourceMessages(t *testing.T) {
	state := store.New(t.TempDir())
	service := NewService(state)
	panel, messages := completedPanel(t, state, service, "handoff-sources")
	missing := handoffRequest(panel, messages, "handoff-missing")
	missing.SelectedMessageIDs[0] = "message-missing"
	if _, err := service.HandoffCreate(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing message error=%v", err)
	}

	data, err := os.ReadFile(state.TeamMessagesPath(panel.TeamID))
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(data), "public answer", "altered answer", 1)
	if tampered == string(data) {
		t.Fatal("message fixture did not contain expected content")
	}
	if err := os.WriteFile(state.TeamMessagesPath(panel.TeamID), []byte(tampered), 0o600); err != nil {
		t.Fatal(err)
	}
	request := handoffRequest(panel, messages, "handoff-altered")
	if _, err := service.HandoffCreate(request); !errors.Is(err, ErrConflict) {
		t.Fatalf("altered message error=%v", err)
	}
}

func TestHandoffBindRunValidatesLookupProjectAndConflicts(t *testing.T) {
	state := store.New(t.TempDir())
	service := NewService(state)
	panel, messages := completedPanel(t, state, service, "handoff-bind")
	projects := map[string]string{"run-a": panel.Project, "run-b": panel.Project, "run-other": t.TempDir()}
	if err := service.SetRunLookup(func(runID string) (string, error) {
		project, ok := projects[runID]
		if !ok {
			return "", os.ErrNotExist
		}
		return project, nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"handoff-a", "handoff-b"} {
		if _, err := service.HandoffCreate(handoffRequest(panel, messages, id)); err != nil {
			t.Fatal(err)
		}
	}
	bind := teamcontract.HandoffBindRunRequestV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "bind-a", TeamID: panel.TeamID, HandoffID: "handoff-a", RunID: "run-a", ExpectedStateVersion: panel.StateVersion}
	bound, err := service.HandoffBindRun(bind)
	if err != nil || bound.Replayed || bound.Binding.RunID != "run-a" {
		t.Fatalf("bound=%+v err=%v", bound, err)
	}
	replayed, err := service.HandoffBindRun(bind)
	if err != nil || !replayed.Replayed || replayed.Binding != bound.Binding {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	conflict := bind
	conflict.ActionID = "bind-conflict"
	conflict.RunID = "run-b"
	if _, err := service.HandoffBindRun(conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("rebind error=%v", err)
	}
	unknown := bind
	unknown.ActionID, unknown.HandoffID, unknown.RunID = "bind-unknown", "handoff-b", "run-missing"
	if _, err := service.HandoffBindRun(unknown); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unknown Run error=%v", err)
	}
	other := unknown
	other.ActionID, other.RunID = "bind-other", "run-other"
	if _, err := service.HandoffBindRun(other); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-project error=%v", err)
	}
	second := unknown
	second.ActionID, second.RunID = "bind-b", "run-b"
	if _, err := service.HandoffBindRun(second); err != nil {
		t.Fatal(err)
	}
	bindings, err := state.ReadTeamBindings(panel.TeamID)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	view, err := service.HandoffGet(teamcontract.HandoffGetRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: panel.TeamID, HandoffID: "handoff-a"})
	if err != nil || view.Binding == nil || view.Binding.RunID != "run-a" {
		t.Fatalf("bound view=%+v err=%v", view, err)
	}
}

func TestRecoverCompletesInterruptedHandoffAndBinding(t *testing.T) {
	state := store.New(t.TempDir())
	service := NewService(state)
	panel, messages := completedPanel(t, state, service, "handoff-recover")
	request := handoffRequest(panel, messages, "handoff-recover")
	state.SetFaultInjectorForTest(func(operation, path string) error {
		if operation == "write_json" && path == state.TeamHandoffPath(panel.TeamID, request.HandoffID) {
			return errors.New("injected Handoff write failure")
		}
		return nil
	})
	if _, err := service.HandoffCreate(request); err == nil {
		t.Fatal("faulted Handoff creation succeeded")
	}
	state.SetFaultInjectorForTest(nil)
	if _, err := os.Stat(state.TeamHandoffIntentPath(panel.TeamID, request.HandoffID)); err != nil {
		t.Fatal("creation intent was not durable")
	}

	recovered := NewService(state)
	if err := recovered.SetRunLookup(func(runID string) (string, error) {
		if runID == "run-recover" {
			return panel.Project, nil
		}
		return "", os.ErrNotExist
	}); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := recovered.HandoffGet(teamcontract.HandoffGetRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: panel.TeamID, HandoffID: request.HandoffID}); err != nil {
		t.Fatal(err)
	}

	bind := teamcontract.HandoffBindRunRequestV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "bind-recover", TeamID: panel.TeamID, HandoffID: request.HandoffID, RunID: "run-recover", ExpectedStateVersion: panel.StateVersion}
	state.SetFaultInjectorForTest(func(operation, path string) error {
		if operation == "write_json" && path == state.TeamBindingsPath(panel.TeamID) {
			return errors.New("injected binding write failure")
		}
		return nil
	})
	if _, err := recovered.HandoffBindRun(bind); err == nil {
		t.Fatal("faulted binding succeeded")
	}
	state.SetFaultInjectorForTest(nil)
	secondRecovery := NewService(state)
	if err := secondRecovery.SetRunLookup(func(runID string) (string, error) { return panel.Project, nil }); err != nil {
		t.Fatal(err)
	}
	if err := secondRecovery.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := secondRecovery.HandoffGet(teamcontract.HandoffGetRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: panel.TeamID, HandoffID: request.HandoffID})
	if err != nil || view.Binding == nil || view.Binding.RunID != bind.RunID {
		t.Fatalf("recovered view=%+v err=%v", view, err)
	}
}

func TestHandoffRetryAfterReceiptFailurePreservesArtifactAndDeduplicatesEvents(t *testing.T) {
	state := store.New(t.TempDir())
	service := NewService(state)
	panel, messages := completedPanel(t, state, service, "handoff-receipt-retry")
	request := handoffRequest(panel, messages, "handoff-receipt-retry")
	intentWrites := 0
	state.SetFaultInjectorForTest(func(operation, path string) error {
		if operation == "write_json" && path == state.TeamHandoffIntentPath(panel.TeamID, request.HandoffID) {
			intentWrites++
			if intentWrites == 2 {
				return errors.New("injected Handoff receipt failure")
			}
		}
		return nil
	})
	if _, err := service.HandoffCreate(request); err == nil {
		t.Fatal("Handoff creation survived receipt fault")
	}
	var persisted teamcontract.HandoffArtifactV1
	if err := state.ReadTeamHandoff(panel.TeamID, request.HandoffID, &persisted); err != nil {
		t.Fatal(err)
	}
	state.SetFaultInjectorForTest(nil)
	replayed, err := service.HandoffCreate(request)
	if err != nil || !replayed.Replayed || !replayed.Handoff.CreatedAt.Equal(persisted.CreatedAt) {
		t.Fatalf("replayed=%+v persisted=%+v err=%v", replayed, persisted, err)
	}

	if err := service.SetRunLookup(func(runID string) (string, error) { return panel.Project, nil }); err != nil {
		t.Fatal(err)
	}
	bind := teamcontract.HandoffBindRunRequestV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "bind-receipt-retry", TeamID: panel.TeamID, HandoffID: request.HandoffID, RunID: "run-receipt-retry", ExpectedStateVersion: panel.StateVersion}
	bindingIntentWrites := 0
	state.SetFaultInjectorForTest(func(operation, path string) error {
		if operation == "write_json" && path == state.TeamBindingIntentPath(panel.TeamID, bind.ActionID) {
			bindingIntentWrites++
			if bindingIntentWrites == 2 {
				return errors.New("injected binding receipt failure")
			}
		}
		return nil
	})
	if _, err := service.HandoffBindRun(bind); err == nil {
		t.Fatal("Handoff binding survived receipt fault")
	}
	var persistedBinding teamcontract.HandoffBindingV1
	if err := state.ReadTeamBinding(panel.TeamID, request.HandoffID, &persistedBinding); err != nil {
		t.Fatal(err)
	}
	state.SetFaultInjectorForTest(nil)
	replayedBinding, err := service.HandoffBindRun(bind)
	if err != nil || !replayedBinding.Replayed || !replayedBinding.Binding.BoundAt.Equal(persistedBinding.BoundAt) {
		t.Fatalf("replayed binding=%+v persisted=%+v err=%v", replayedBinding, persistedBinding, err)
	}
	events, err := state.ReadTeamEvents(panel.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	createdEvents, boundEvents := 0, 0
	for _, event := range events {
		switch event.Type {
		case teamcontract.EventHandoffCreated:
			createdEvents++
		case teamcontract.EventHandoffBound:
			boundEvents++
		}
	}
	if createdEvents != 1 || boundEvents != 1 {
		t.Fatalf("Handoff events created=%d bound=%d", createdEvents, boundEvents)
	}
}
