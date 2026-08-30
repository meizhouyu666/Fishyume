package team

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/teamcontract"
)

func TestCapabilitiesAndPublicPages(t *testing.T) {
	contribution, _ := json.Marshal(teamcontract.ContributionV1{SchemaVersion: teamcontract.SchemaVersion, Status: teamcontract.ContributionCompleted, ContentMarkdown: "public answer"})
	state := store.New(t.TempDir())
	service := NewService(state)
	if err := service.SetDriver(&immediateDriver{output: string(contribution)}); err != nil {
		t.Fatal(err)
	}
	capabilities, err := service.Capabilities()
	if err != nil || len(capabilities.ParticipantTemplates) != 2 || len(capabilities.Harnesses) != 1 || len(capabilities.Harnesses[0].Models) != 2 || !capabilities.Features.Panel || !capabilities.Features.Cancel || !capabilities.Features.Handoff {
		t.Fatalf("capabilities=%+v err=%v", capabilities, err)
	}
	started, err := service.Start(context.Background(), startRequest(t.TempDir(), "public-pages"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.DispatchInitial(context.Background(), started.Team.TeamID); err != nil {
		t.Fatal(err)
	}
	list, err := service.List(teamcontract.TeamListRequestV1{SchemaVersion: teamcontract.SchemaVersion, Limit: 1})
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	view, err := service.GetView(teamcontract.TeamGetRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: started.Team.TeamID})
	if err != nil || len(view.Turns) != 2 {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	events, err := service.Events(context.Background(), teamcontract.TeamEventsRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: started.Team.TeamID, Limit: 1})
	if err != nil || len(events.Events) != 1 || !events.More {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	next, err := service.Events(context.Background(), teamcontract.TeamEventsRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: started.Team.TeamID, AfterSequence: events.NextAfterSequence, Limit: 100})
	if err != nil || len(next.Events) == 0 || next.Events[0].Sequence <= events.NextAfterSequence {
		t.Fatalf("next events=%+v err=%v", next, err)
	}
	messages, err := service.Messages(teamcontract.TeamMessagesRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: started.Team.TeamID, Limit: 1})
	if err != nil || len(messages.Messages) != 1 || !messages.More {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
}

func TestCancelActionIsConfirmedIdempotentAndCapabilityGated(t *testing.T) {
	driver := &immediateDriver{output: `{"schemaVersion":"fishyume.team/v1","status":"completed","contentMarkdown":"late"}`, activeFirst: true}
	state := store.New(t.TempDir())
	service := NewService(state)
	if err := service.SetDriver(driver); err != nil {
		t.Fatal(err)
	}
	started, err := service.Start(context.Background(), startRequest(t.TempDir(), "cancel-action"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		_, _ = service.DispatchInitial(ctx, started.Team.TeamID)
	}()
	awaitAllTurnsState(t, service, started.Team.TeamID, teamcontract.TurnActive)
	stop()
	<-dispatched
	current, err := service.Get(started.Team.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	action := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "action-cancel", TeamID: current.TeamID, ExpectedStateVersion: current.StateVersion, Type: teamcontract.ActionCancel}
	response, err := service.Action(context.Background(), action)
	if err != nil || response.State != teamcontract.LifecycleClosed {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	replay, err := service.Action(context.Background(), action)
	if err != nil || !replay.Replayed || replay.StateVersion != response.StateVersion {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	conflict := action
	conflict.ExpectedStateVersion++
	if _, err := service.Action(context.Background(), conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict=%v", err)
	}
	unsupported := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "action-bogus", TeamID: current.TeamID, ExpectedStateVersion: response.StateVersion, Type: teamcontract.ActionType("bogus")}
	if _, err := service.Action(context.Background(), unsupported); err == nil {
		t.Fatalf("bogus action was accepted")
	}
	intents, err := state.ListTeamActionIntents(current.TeamID)
	if err != nil || len(intents) != 1 {
		t.Fatalf("unsupported action mutated intents=%d err=%v", len(intents), err)
	}
	driver.mu.Lock()
	cancels := driver.cancels
	driver.mu.Unlock()
	if cancels != 2 {
		t.Fatalf("confirmed turn cancellations=%d want 2", cancels)
	}
}

func TestRecoverCompletesPersistedCancellingAction(t *testing.T) {
	driver := &immediateDriver{output: `{"schemaVersion":"fishyume.team/v1","status":"completed","contentMarkdown":"late"}`, activeFirst: true}
	state := store.New(t.TempDir())
	first := NewService(state)
	if err := first.SetDriver(driver); err != nil {
		t.Fatal(err)
	}
	started, err := first.Start(context.Background(), startRequest(t.TempDir(), "recover-cancel"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	dispatched := make(chan struct{})
	go func() {
		defer close(dispatched)
		_, _ = first.DispatchInitial(ctx, started.Team.TeamID)
	}()
	turnIDs := awaitAllTurnsState(t, first, started.Team.TeamID, teamcontract.TurnActive)
	stop()
	<-dispatched

	snapshot, err := first.Get(started.Team.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	action := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "action-recover", TeamID: snapshot.TeamID, ExpectedStateVersion: snapshot.StateVersion, Type: teamcontract.ActionCancel}
	hash, _, err := teamcontract.CanonicalHash(action)
	if err != nil {
		t.Fatal(err)
	}
	intent := actionIntentV1{SchemaVersion: teamcontract.SchemaVersion, RequestHash: hash, Action: action}
	if err := state.WriteTeamActionIntent(snapshot.TeamID, action.ActionID, intent); err != nil {
		t.Fatal(err)
	}
	for _, turnID := range turnIDs {
		turn, readErr := readTurn(state, snapshot.TeamID, turnID)
		if readErr != nil {
			t.Fatal(readErr)
		}
		turn.State = teamcontract.TurnCancelling
		turn.UpdatedAt = time.Now().UTC()
		if err := state.WriteTeamTurn(turn); err != nil {
			t.Fatal(err)
		}
	}
	recovered := NewService(state)
	if err := recovered.SetDriver(driver); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelWait()
	if err := recovered.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	closed, err := recovered.Get(snapshot.TeamID)
	if err != nil || closed.State != teamcontract.LifecycleClosed || closed.CloseReason != teamcontract.CloseCancelled {
		t.Fatalf("recovered Team=%+v err=%v", closed, err)
	}
	var receipt actionIntentV1
	if err := state.ReadTeamActionIntent(snapshot.TeamID, action.ActionID, &receipt); err != nil || receipt.Response == nil {
		t.Fatalf("recovered receipt=%+v err=%v", receipt, err)
	}
	driver.mu.Lock()
	cancels := driver.cancels
	driver.mu.Unlock()
	if cancels != len(turnIDs) {
		t.Fatalf("recovered cancellations=%d want %d", cancels, len(turnIDs))
	}
}

func TestRejectedCancelDoesNotConsumeMutationReceipt(t *testing.T) {
	state := store.New(t.TempDir())
	service := NewService(state)
	if err := service.SetDriver(&immediateDriver{output: `{"schemaVersion":"fishyume.team/v1","status":"completed","contentMarkdown":"done"}`}); err != nil {
		t.Fatal(err)
	}
	started, err := service.Start(context.Background(), startRequest(t.TempDir(), "closed-cancel"))
	if err != nil {
		t.Fatal(err)
	}
	closed, err := service.DispatchInitial(context.Background(), started.Team.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	action := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "action-too-late", TeamID: closed.TeamID, ExpectedStateVersion: closed.StateVersion, Type: teamcontract.ActionCancel}
	if _, err := service.Action(context.Background(), action); !errors.Is(err, ErrConflict) {
		t.Fatalf("late cancel error=%v", err)
	}
	intents, err := state.ListTeamActionIntents(closed.TeamID)
	if err != nil || len(intents) != 0 {
		t.Fatalf("rejected cancel intents=%d err=%v", len(intents), err)
	}
}
