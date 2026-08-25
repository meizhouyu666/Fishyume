package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"wf.local/wf-engine/internal/sessiondriver"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/teamcontract"
)

type immediateSessionDriver struct {
	mu        sync.Mutex
	sessions  int
	turns     []sessiondriver.StartTurnRequest
	cancels   []string
	closed    int
	active    map[string]bool
	stopped   map[string]bool
	resumeErr error
}

func (*immediateSessionDriver) Name() string { return "codex" }

func (*immediateSessionDriver) Capabilities() sessiondriver.DriverCapabilities {
	return sessiondriver.DriverCapabilities{
		Targets: []string{"local"}, SupportsResume: true, SupportsPark: true,
		SupportsRecovery: true, SupportsDirectedInput: true, SupportsConfirmedCancel: true,
		MaxConcurrentTurns: 1,
	}
}

func (d *immediateSessionDriver) StartSession(_ context.Context, request sessiondriver.StartSessionRequest) (*sessiondriver.SessionHandle, error) {
	d.mu.Lock()
	d.sessions++
	d.mu.Unlock()
	return &sessiondriver.SessionHandle{Driver: d.Name(), Target: request.Target, SchemaVersion: 1, ID: "session-" + request.Identity.ParticipantID, Generation: request.Identity.Generation, Revision: 1, Data: json.RawMessage(`{}`)}, nil
}

func (d *immediateSessionDriver) StartTurn(_ context.Context, session sessiondriver.SessionHandle, request sessiondriver.StartTurnRequest) (*sessiondriver.StartTurnResult, error) {
	d.mu.Lock()
	d.turns = append(d.turns, request)
	if d.active == nil {
		d.active = make(map[string]bool)
	}
	if d.stopped == nil {
		d.stopped = make(map[string]bool)
	}
	if strings.Contains(request.Prompt, "scenario:active") {
		d.active[request.Identity.TurnID] = true
	}
	d.mu.Unlock()
	session.Revision++
	turn := sessiondriver.TurnHandle{Driver: d.Name(), Target: session.Target, SchemaVersion: 1, ID: request.Identity.TurnID, SessionID: session.ID, SessionGeneration: session.Generation, Data: json.RawMessage(`{}`)}
	return &sessiondriver.StartTurnResult{Session: session, Turn: turn}, nil
}

func (d *immediateSessionDriver) ObserveTurn(_ context.Context, session sessiondriver.SessionHandle, turn sessiondriver.TurnHandle) (*sessiondriver.TurnObservation, error) {
	d.mu.Lock()
	active, stopped := d.active[turn.ID], d.stopped[turn.ID]
	d.mu.Unlock()
	if stopped {
		return &sessiondriver.TurnObservation{Session: session, Turn: turn, State: sessiondriver.TurnInterrupted}, nil
	}
	if active {
		return &sessiondriver.TurnObservation{Session: session, Turn: turn, State: sessiondriver.TurnActive}, nil
	}
	content := fmt.Sprintf("response from %s", turn.ID)
	output, _ := json.Marshal(teamcontract.ContributionV1{SchemaVersion: teamcontract.SchemaVersion, Status: teamcontract.ContributionCompleted, ContentMarkdown: content})
	return &sessiondriver.TurnObservation{Session: session, Turn: turn, State: sessiondriver.TurnResponded, Output: string(output)}, nil
}

func (*immediateSessionDriver) ParkSession(_ context.Context, session sessiondriver.SessionHandle) (*sessiondriver.SessionHandle, error) {
	session.Revision++
	return &session, nil
}

func (d *immediateSessionDriver) ResumeSession(_ context.Context, session sessiondriver.SessionHandle) (*sessiondriver.SessionHandle, error) {
	d.mu.Lock()
	err := d.resumeErr
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	session.Revision++
	return &session, nil
}

func (d *immediateSessionDriver) CancelTurn(_ context.Context, session sessiondriver.SessionHandle, turn sessiondriver.TurnHandle) (*sessiondriver.CancelTurnResult, error) {
	d.mu.Lock()
	d.cancels = append(d.cancels, turn.ID)
	d.active[turn.ID] = false
	d.stopped[turn.ID] = true
	d.mu.Unlock()
	session.Revision++
	return &sessiondriver.CancelTurnResult{Session: session, Turn: turn, State: sessiondriver.CancelConfirmed}, nil
}

func (d *immediateSessionDriver) CloseSession(_ context.Context, session sessiondriver.SessionHandle) (*sessiondriver.SessionHandle, error) {
	d.mu.Lock()
	d.closed++
	d.mu.Unlock()
	session.Revision++
	return &session, nil
}

func (d *immediateSessionDriver) release(turnID string) {
	d.mu.Lock()
	d.active[turnID] = false
	d.mu.Unlock()
}

func TestSessionModeInitialRoundOpensAndPersistsParticipantSessions(t *testing.T) {
	driver := &immediateSessionDriver{}
	state := store.New(t.TempDir())
	service := NewService(state)
	if err := service.SetSessionDriver(driver); err != nil {
		t.Fatal(err)
	}
	capabilities, err := service.Capabilities()
	if err != nil || !capabilities.Features.Session || !capabilities.Features.FollowUp || len(capabilities.SupportedModes) != 2 {
		t.Fatalf("capabilities=%+v err=%v", capabilities, err)
	}
	request := startRequest(t.TempDir(), "session-initial")
	request.Mode = teamcontract.ModeSession
	started, err := service.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := service.DispatchInitial(context.Background(), started.Team.TeamID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.State != teamcontract.LifecycleOpen || opened.CloseReason != "" {
		t.Fatalf("opened Team=%+v", opened)
	}
	messages, err := state.ReadTeamMessages(opened.TeamID)
	if err != nil || len(messages) != 2 {
		t.Fatalf("messages=%+v err=%v", messages, err)
	}
	for _, participant := range opened.Participants {
		raw, err := state.ReadTeamParticipantSession(opened.TeamID, participant.ParticipantID)
		if err != nil {
			t.Fatal(err)
		}
		record, err := decodeParticipantSession(raw, opened.TeamID, participant.ParticipantID)
		if err != nil || record.State != privateSessionParked {
			t.Fatalf("Session record=%+v err=%v", record, err)
		}
	}
	driver.mu.Lock()
	sessions, turns := driver.sessions, len(driver.turns)
	driver.mu.Unlock()
	if sessions != 2 || turns != 2 {
		t.Fatalf("external sessions=%d turns=%d", sessions, turns)
	}
}

func TestSessionModeRequiresEligibleResumeDriver(t *testing.T) {
	request := startRequest(t.TempDir(), "session-no-driver")
	request.Mode = teamcontract.ModeSession
	service := NewService(store.New(t.TempDir()))
	if _, err := service.Start(context.Background(), request); err != ErrCapabilityUnavailable {
		t.Fatalf("Session mode error=%v", err)
	}
}

func TestSessionFollowUpAddressesOnlySelectedParticipantAndCommitsCanonicalMessages(t *testing.T) {
	service, state, driver, opened := openTestSession(t, teamcontract.DefaultCostGrant)
	initialMessages, err := state.ReadTeamMessages(opened.TeamID)
	if err != nil || len(initialMessages) != 2 {
		t.Fatalf("initial messages=%+v err=%v", initialMessages, err)
	}
	action := teamcontract.TeamActionV1{
		SchemaVersion: teamcontract.SchemaVersion, ActionID: "follow-selected", TeamID: opened.TeamID,
		ExpectedStateVersion: opened.StateVersion, Type: teamcontract.ActionFollowUp,
		FollowUp: &teamcontract.FollowUpActionV1{Content: "review the peer", ParticipantIDs: []string{"participant-1"}, ReferencedMessageIDs: []string{initialMessages[1].MessageID}},
	}
	response, err := service.Action(context.Background(), action)
	if err != nil || response.State != teamcontract.LifecycleRunning {
		t.Fatalf("follow-up response=%+v err=%v", response, err)
	}
	awaitTeamState(t, service, opened.TeamID, teamcontract.LifecycleOpen)
	messages, err := state.ReadTeamMessages(opened.TeamID)
	if err != nil || len(messages) != 4 || messages[2].Kind != teamcontract.MessageHost || messages[2].Recipients[0] != "participant-1" || messages[3].Actor != "participant-1" {
		t.Fatalf("follow-up messages=%+v err=%v", messages, err)
	}
	turnIDs, err := state.ListTeamTurnIDs(opened.TeamID)
	if err != nil || len(turnIDs) != 3 {
		t.Fatalf("turn IDs=%v err=%v", turnIDs, err)
	}
	current, err := service.Get(opened.TeamID)
	if err != nil || current.CostUsed != 201 {
		t.Fatalf("Team cost=%+v err=%v", current, err)
	}
	driver.mu.Lock()
	turns := append([]sessiondriver.StartTurnRequest(nil), driver.turns...)
	driver.mu.Unlock()
	if len(turns) != 3 || !strings.Contains(turns[2].Prompt, initialMessages[1].MessageID) {
		t.Fatalf("directed external Turns=%+v", turns)
	}
}

func TestSessionFollowUpQuotaRejectsBeforePublicOrExternalMutation(t *testing.T) {
	service, state, driver, opened := openTestSession(t, 101)
	action := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "follow-over-cost", TeamID: opened.TeamID, ExpectedStateVersion: opened.StateVersion, Type: teamcontract.ActionFollowUp, FollowUp: &teamcontract.FollowUpActionV1{Content: "more", ParticipantIDs: []string{"participant-1"}}}
	if _, err := service.Action(context.Background(), action); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("quota error=%v", err)
	}
	messages, _ := state.ReadTeamMessages(opened.TeamID)
	turnIDs, _ := state.ListTeamTurnIDs(opened.TeamID)
	driver.mu.Lock()
	externalTurns := len(driver.turns)
	driver.mu.Unlock()
	if len(messages) != 2 || len(turnIDs) != 2 || externalTurns != 2 {
		t.Fatalf("quota rejection mutated messages=%d turns=%d external=%d", len(messages), len(turnIDs), externalTurns)
	}
}

func TestSessionCloseClosesParkedSessionsWithoutCancellation(t *testing.T) {
	service, _, driver, opened := openTestSession(t, teamcontract.DefaultCostGrant)
	action := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "close-session", TeamID: opened.TeamID, ExpectedStateVersion: opened.StateVersion, Type: teamcontract.ActionClose, Close: &teamcontract.CloseActionV1{Reason: teamcontract.CloseHostClosed}}
	response, err := service.Action(context.Background(), action)
	if err != nil || response.State != teamcontract.LifecycleClosed {
		t.Fatalf("close response=%+v err=%v", response, err)
	}
	closed, err := service.Get(opened.TeamID)
	if err != nil || closed.CloseReason != teamcontract.CloseHostClosed {
		t.Fatalf("closed Team=%+v err=%v", closed, err)
	}
	driver.mu.Lock()
	closedSessions, cancels := driver.closed, len(driver.cancels)
	driver.mu.Unlock()
	if closedSessions != 2 || cancels != 0 {
		t.Fatalf("closed Sessions=%d cancellations=%d", closedSessions, cancels)
	}
}

func TestSessionCancelTurnTargetsExactActiveTurn(t *testing.T) {
	service, state, driver, opened := openTestSession(t, teamcontract.DefaultCostGrant)
	follow := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "follow-active", TeamID: opened.TeamID, ExpectedStateVersion: opened.StateVersion, Type: teamcontract.ActionFollowUp, FollowUp: &teamcontract.FollowUpActionV1{Content: "scenario:active", ParticipantIDs: []string{"participant-1"}}}
	if _, err := service.Action(context.Background(), follow); err != nil {
		t.Fatal(err)
	}
	active := awaitParticipantTurnState(t, service, opened.TeamID, "participant-1", teamcontract.TurnActive)
	current, _ := service.Get(opened.TeamID)
	cancel := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "cancel-active", TeamID: opened.TeamID, ExpectedStateVersion: current.StateVersion, Type: teamcontract.ActionCancelTurn, CancelTurn: &teamcontract.CancelTurnActionV1{TurnID: active.TurnID}}
	response, err := service.Action(context.Background(), cancel)
	if err != nil {
		t.Fatalf("cancel response=%+v err=%v", response, err)
	}
	awaitParticipantTurnState(t, service, opened.TeamID, "participant-1", teamcontract.TurnCancelled)
	driver.mu.Lock()
	cancelled := append([]string(nil), driver.cancels...)
	driver.mu.Unlock()
	if len(cancelled) != 1 || cancelled[0] != active.TurnID {
		t.Fatalf("cancelled Turns=%v", cancelled)
	}
	turnIDs, _ := state.ListTeamTurnIDs(opened.TeamID)
	if len(turnIDs) != 3 {
		t.Fatalf("unexpected Turn count=%d", len(turnIDs))
	}
}

func TestSessionCloseWaitsForActiveTurnWithoutCancellingIt(t *testing.T) {
	service, _, driver, opened := openTestSession(t, teamcontract.DefaultCostGrant)
	follow := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "follow-close-active", TeamID: opened.TeamID, ExpectedStateVersion: opened.StateVersion, Type: teamcontract.ActionFollowUp, FollowUp: &teamcontract.FollowUpActionV1{Content: "scenario:active", ParticipantIDs: []string{"participant-1"}}}
	if _, err := service.Action(context.Background(), follow); err != nil {
		t.Fatal(err)
	}
	active := awaitParticipantTurnState(t, service, opened.TeamID, "participant-1", teamcontract.TurnActive)
	current, _ := service.Get(opened.TeamID)
	closeAction := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "close-with-active", TeamID: opened.TeamID, ExpectedStateVersion: current.StateVersion, Type: teamcontract.ActionClose, Close: &teamcontract.CloseActionV1{Reason: teamcontract.CloseHostClosed}}
	response, err := service.Action(context.Background(), closeAction)
	if err != nil || response.State != teamcontract.LifecycleClosing {
		t.Fatalf("close response=%+v err=%v", response, err)
	}
	driver.mu.Lock()
	cancelsBeforeRelease := len(driver.cancels)
	driver.mu.Unlock()
	if cancelsBeforeRelease != 0 {
		t.Fatalf("graceful close cancelled active Turns: %d", cancelsBeforeRelease)
	}
	driver.release(active.TurnID)
	closed := awaitTeamState(t, service, opened.TeamID, teamcontract.LifecycleClosed)
	if closed.CloseReason != teamcontract.CloseHostClosed {
		t.Fatalf("closed Team=%+v", closed)
	}
	driver.mu.Lock()
	cancels, closedSessions := len(driver.cancels), driver.closed
	driver.mu.Unlock()
	if cancels != 0 || closedSessions != 2 {
		t.Fatalf("graceful close cancellations=%d closed Sessions=%d", cancels, closedSessions)
	}
}

func TestSessionResumeLossIsDurableAndNeverCreatesReplacement(t *testing.T) {
	service, state, driver, opened := openTestSession(t, teamcontract.DefaultCostGrant)
	driver.mu.Lock()
	driver.resumeErr = sessiondriver.Lost("fixture thread is gone")
	driver.mu.Unlock()
	action := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "follow-lost", TeamID: opened.TeamID, ExpectedStateVersion: opened.StateVersion, Type: teamcontract.ActionFollowUp, FollowUp: &teamcontract.FollowUpActionV1{Content: "continue", ParticipantIDs: []string{"participant-1"}}}
	if _, err := service.Action(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	awaitParticipantTurnState(t, service, opened.TeamID, "participant-1", teamcontract.TurnIndeterminate)
	raw, err := state.ReadTeamParticipantSession(opened.TeamID, "participant-1")
	if err != nil {
		t.Fatal(err)
	}
	record, err := decodeParticipantSession(raw, opened.TeamID, "participant-1")
	if err != nil || record.State != privateSessionLost {
		t.Fatalf("lost record=%+v err=%v", record, err)
	}
	current, _ := service.Get(opened.TeamID)
	second := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "follow-after-loss", TeamID: opened.TeamID, ExpectedStateVersion: current.StateVersion, Type: teamcontract.ActionFollowUp, FollowUp: &teamcontract.FollowUpActionV1{Content: "retry", ParticipantIDs: []string{"participant-1"}}}
	if _, err := service.Action(context.Background(), second); !errors.Is(err, ErrSessionLost) {
		t.Fatalf("follow-up after loss error=%v", err)
	}
	driver.mu.Lock()
	sessions, externalTurns := driver.sessions, len(driver.turns)
	driver.mu.Unlock()
	if sessions != 2 || externalTurns != 2 {
		t.Fatalf("lost Session was replaced: sessions=%d external turns=%d", sessions, externalTurns)
	}
}

func TestRecoverExpiresOpenSessionAfterLifetime(t *testing.T) {
	_, state, driver, opened := openTestSession(t, teamcontract.DefaultCostGrant)
	recovered := NewService(state)
	recovered.now = func() time.Time { return opened.CreatedAt.Add(teamSessionLifetime + time.Second) }
	if err := recovered.SetSessionDriver(driver); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	closed, err := recovered.Get(opened.TeamID)
	if err != nil || closed.State != teamcontract.LifecycleClosed || closed.CloseReason != teamcontract.CloseHostClosed {
		t.Fatalf("expired Team=%+v err=%v", closed, err)
	}
	driver.mu.Lock()
	closedSessions := driver.closed
	driver.mu.Unlock()
	if closedSessions != 2 {
		t.Fatalf("expired Session closes=%d", closedSessions)
	}
}

func TestRecoverObservesPersistedActiveSessionTurnsWithoutRestart(t *testing.T) {
	driver := &immediateSessionDriver{}
	state := store.New(t.TempDir())
	first := NewService(state)
	if err := first.SetSessionDriver(driver); err != nil {
		t.Fatal(err)
	}
	request := startRequest(t.TempDir(), "session-recover-active")
	request.Mode, request.Topic = teamcontract.ModeSession, "scenario:active"
	started, err := first.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = first.DispatchInitial(ctx, started.Team.TeamID)
	}()
	turnIDs := awaitAllTurnsState(t, first, started.Team.TeamID, teamcontract.TurnActive)
	stop()
	<-done
	driver.mu.Lock()
	startsBefore := len(driver.turns)
	driver.mu.Unlock()

	recovered := NewService(state)
	if err := recovered.SetSessionDriver(driver); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, turnID := range turnIDs {
		driver.release(turnID)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := recovered.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	current, err := recovered.Get(started.Team.TeamID)
	if err != nil || current.State != teamcontract.LifecycleOpen {
		t.Fatalf("recovered Team=%+v err=%v", current, err)
	}
	driver.mu.Lock()
	startsAfter := len(driver.turns)
	driver.mu.Unlock()
	if startsAfter != startsBefore {
		t.Fatalf("active Turns restarted: before=%d after=%d", startsBefore, startsAfter)
	}
}

func TestRecoverCompletesPartialFollowUpWithoutDuplicatePublicRecords(t *testing.T) {
	_, state, driver, opened := openTestSession(t, teamcontract.DefaultCostGrant)
	action := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "follow-crash", TeamID: opened.TeamID, ExpectedStateVersion: opened.StateVersion, Type: teamcontract.ActionFollowUp, FollowUp: &teamcontract.FollowUpActionV1{Content: "recover this", ParticipantIDs: []string{"participant-1"}}}
	hash, _, err := teamcontract.CanonicalHash(action)
	if err != nil {
		t.Fatal(err)
	}
	intent := actionIntentV1{SchemaVersion: teamcontract.SchemaVersion, RequestHash: hash, Action: action}
	if err := state.WriteTeamActionIntent(opened.TeamID, action.ActionID, intent); err != nil {
		t.Fatal(err)
	}
	message := hostMessageForAction(opened, action, 3, time.Now().UTC())
	if err := state.AppendTeamMessage(message); err != nil {
		t.Fatal(err)
	}

	recovered := NewService(state)
	if err := recovered.SetSessionDriver(driver); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := recovered.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	messages, _ := state.ReadTeamMessages(opened.TeamID)
	turnIDs, _ := state.ListTeamTurnIDs(opened.TeamID)
	events, _ := state.ReadTeamEvents(opened.TeamID)
	messageEvents := 0
	for _, event := range events {
		if event.Type == teamcontract.EventMessageCommitted && event.MessageID == message.MessageID {
			messageEvents++
		}
	}
	if len(messages) != 4 || len(turnIDs) != 3 || messageEvents != 1 {
		t.Fatalf("recovered messages=%d turns=%d message events=%d", len(messages), len(turnIDs), messageEvents)
	}
	var receipt actionIntentV1
	if err := state.ReadTeamActionIntent(opened.TeamID, action.ActionID, &receipt); err != nil || receipt.Response == nil {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	driver.mu.Lock()
	externalTurns := len(driver.turns)
	driver.mu.Unlock()
	if externalTurns != 3 {
		t.Fatalf("external Turns=%d", externalTurns)
	}

	second := NewService(state)
	if err := second.SetSessionDriver(driver); err != nil {
		t.Fatal(err)
	}
	if err := second.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	driver.mu.Lock()
	externalAfterReplay := len(driver.turns)
	driver.mu.Unlock()
	if externalAfterReplay != externalTurns {
		t.Fatalf("receipt replay launched a Turn: before=%d after=%d", externalTurns, externalAfterReplay)
	}
}

func TestRecoverCompletesClosedCancelReceiptWithoutReopeningTeam(t *testing.T) {
	service, state, driver, opened := openTestSession(t, teamcontract.DefaultCostGrant)
	action := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: "cancel-receipt-crash", TeamID: opened.TeamID, ExpectedStateVersion: opened.StateVersion, Type: teamcontract.ActionCancel}
	if _, err := service.Action(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	var intent actionIntentV1
	if err := state.ReadTeamActionIntent(opened.TeamID, action.ActionID, &intent); err != nil {
		t.Fatal(err)
	}
	intent.Response = nil
	if err := state.WriteTeamActionIntent(opened.TeamID, action.ActionID, intent); err != nil {
		t.Fatal(err)
	}
	recovered := NewService(state)
	if err := recovered.SetSessionDriver(driver); err != nil {
		t.Fatal(err)
	}
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := recovered.Wait(waitCtx); err != nil {
		t.Fatal(err)
	}
	closed, _ := recovered.Get(opened.TeamID)
	if closed.State != teamcontract.LifecycleClosed || closed.CloseReason != teamcontract.CloseCancelled {
		t.Fatalf("recovered Team=%+v", closed)
	}
	if err := state.ReadTeamActionIntent(opened.TeamID, action.ActionID, &intent); err != nil || intent.Response == nil {
		t.Fatalf("recovered receipt=%+v err=%v", intent, err)
	}
}

func TestPrivateSessionRecordsRejectUnknownFields(t *testing.T) {
	_, state, _, opened := openTestSession(t, teamcontract.DefaultCostGrant)
	raw, err := state.ReadTeamParticipantSession(opened.TeamID, "participant-1")
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	value["futureField"] = true
	changed, _ := json.Marshal(value)
	if _, err := decodeParticipantSession(changed, opened.TeamID, "participant-1"); err == nil {
		t.Fatal("private Session record with unknown field was accepted")
	}
}

func openTestSession(t *testing.T, grant int) (*Service, *store.Store, *immediateSessionDriver, teamcontract.TeamSessionV1) {
	t.Helper()
	driver := &immediateSessionDriver{}
	state := store.New(t.TempDir())
	service := NewService(state)
	if err := service.SetSessionDriver(driver); err != nil {
		t.Fatal(err)
	}
	request := startRequest(t.TempDir(), "session-"+fmt.Sprint(grant))
	request.Mode, request.CostGrant = teamcontract.ModeSession, grant
	started, err := service.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := service.DispatchInitial(context.Background(), started.Team.TeamID)
	if err != nil || opened.State != teamcontract.LifecycleOpen {
		t.Fatalf("opened=%+v err=%v", opened, err)
	}
	return service, state, driver, opened
}

func awaitTeamState(t *testing.T, service *Service, teamID string, wanted teamcontract.Lifecycle) teamcontract.TeamSessionV1 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		value, err := service.Get(teamID)
		if err == nil && value.State == wanted {
			return value
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Team %s did not reach %s", teamID, wanted)
	return teamcontract.TeamSessionV1{}
}

func awaitParticipantTurnState(t *testing.T, service *Service, teamID, participantID string, wanted teamcontract.TurnState) teamcontract.ParticipantTurnV1 {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		view, err := service.GetView(teamcontract.TeamGetRequestV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: teamID})
		if err == nil {
			for _, participant := range view.Team.Participants {
				if participant.ParticipantID != participantID {
					continue
				}
				for _, turn := range view.Turns {
					if turn.TurnID == participant.CurrentTurnID && turn.State == wanted {
						return turn
					}
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("participant %s did not reach %s", participantID, wanted)
	return teamcontract.ParticipantTurnV1{}
}

var _ sessiondriver.Driver = (*immediateSessionDriver)(nil)
