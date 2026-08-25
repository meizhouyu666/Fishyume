package codexprocess

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wf.local/wf-engine/internal/sessiondriver"
	"wf.local/wf-engine/internal/sessiondriver/contracttest"
)

func newSessionFixture(t *testing.T) (*SessionAdapter, Config, string, string) {
	t.Helper()
	stateRoot := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "session workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	externalState := filepath.Join(t.TempDir(), "codex-thread.json")
	t.Setenv("FISHYUME_FAKE_APP_SERVER_STATE", externalState)
	config := Config{StateRoot: stateRoot, Executable: sessionFixtureBinary(t), PollInterval: 10 * time.Millisecond, MaxStderrBytes: 64 * 1024}
	adapter := NewSessionAdapter(New(config))
	t.Cleanup(func() {
		if err := adapter.closeAllClients(); err != nil {
			t.Errorf("close Session fixture transports: %v", err)
		}
	})
	return adapter, config, workspace, externalState
}

func sessionStartRequest(workspace string) sessiondriver.StartSessionRequest {
	return sessiondriver.StartSessionRequest{
		ProtocolVersion: sessiondriver.ProtocolVersion,
		Identity:        sessiondriver.SessionIdentity{TeamID: "team-session", ParticipantID: "participant-1", Generation: 1},
		Workspace:       workspace, Target: "local", ModelID: "codex/local/gpt-5.6-luna", Sandbox: sessiondriver.SandboxReadOnly,
	}
}

func sessionTurnRequest(id, prompt string) sessiondriver.StartTurnRequest {
	return sessiondriver.StartTurnRequest{ProtocolVersion: sessiondriver.ProtocolVersion, Identity: sessiondriver.TurnIdentity{TurnID: id, ExpectedSessionGeneration: 1}, Prompt: prompt, MaxOutputBytes: 32 * 1024}
}

func TestCodexSessionAdapterContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T) contracttest.Fixture {
		adapter, _, workspace, _ := newSessionFixture(t)
		return contracttest.Fixture{
			Driver: adapter, Start: sessionStartRequest(workspace),
			RespondTurn:  sessionTurnRequest("turn-respond", "fixture response"),
			FollowupTurn: sessionTurnRequest("turn-followup", "directed follow-up"),
			ActiveTurn:   sessionTurnRequest("turn-active", "scenario:active"),
		}
	})
}

func TestCodexSessionRejectsServerRequestWithoutMisroutingItsID(t *testing.T) {
	adapter, _, workspace, _ := newSessionFixture(t)
	t.Setenv("FISHYUME_FAKE_SERVER_REQUEST", "1")
	started, err := adapter.StartSession(context.Background(), sessionStartRequest(workspace))
	if err != nil || started == nil {
		t.Fatalf("server request collided with initialize response: handle=%+v err=%v", started, err)
	}
}

func TestCodexSessionRecoversActiveTurnAcrossAdapterRestart(t *testing.T) {
	adapter, config, workspace, externalState := newSessionFixture(t)
	started, err := adapter.StartSession(context.Background(), sessionStartRequest(workspace))
	if err != nil {
		t.Fatal(err)
	}
	active, err := adapter.StartTurn(context.Background(), *started, sessionTurnRequest("turn-recover", "scenario:active"))
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.stopClient(started.ID); err != nil {
		t.Fatal(err)
	}

	recovered := NewSessionAdapter(New(config))
	t.Cleanup(func() { _ = recovered.closeAllClients() })
	observed, err := recovered.ObserveTurn(context.Background(), active.Session, active.Turn)
	if err != nil || observed.State != sessiondriver.TurnActive {
		t.Fatalf("recovered observation=%+v err=%v", observed, err)
	}
	var external struct {
		Thread struct {
			Turns []any `json:"turns"`
		} `json:"thread"`
	}
	readStrictTestJSON(t, externalState, &external)
	if len(external.Thread.Turns) != 1 {
		t.Fatalf("restart recovery launched another external turn: %d", len(external.Thread.Turns))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cancelled, err := recovered.CancelTurn(ctx, observed.Session, observed.Turn)
	if err != nil || cancelled.State != sessiondriver.CancelConfirmed {
		t.Fatalf("recovered cancel=%+v err=%v", cancelled, err)
	}
}

func TestCodexSessionReconcilesLostTurnStartResponseWithoutRelaunch(t *testing.T) {
	adapter, _, workspace, externalState := newSessionFixture(t)
	started, err := adapter.StartSession(context.Background(), sessionStartRequest(workspace))
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.StartTurn(context.Background(), *started, sessionTurnRequest("turn-crash-window", "scenario:launch-then-disconnect"))
	if err != nil {
		t.Fatal(err)
	}
	observed, err := adapter.ObserveTurn(context.Background(), result.Session, result.Turn)
	if err != nil || observed.State != sessiondriver.TurnResponded {
		t.Fatalf("crash-window observation=%+v err=%v", observed, err)
	}
	var external struct {
		Thread struct {
			Turns []any `json:"turns"`
		} `json:"thread"`
	}
	readStrictTestJSON(t, externalState, &external)
	if len(external.Thread.Turns) != 1 {
		t.Fatalf("crash-window recovery duplicated external turn: %d", len(external.Thread.Turns))
	}
}

func TestCodexSessionRejectsStaleGenerationAndTurnIdentity(t *testing.T) {
	adapter, _, workspace, _ := newSessionFixture(t)
	started, err := adapter.StartSession(context.Background(), sessionStartRequest(workspace))
	if err != nil {
		t.Fatal(err)
	}
	wrongGeneration := sessionTurnRequest("turn-wrong-generation", "fixture")
	wrongGeneration.Identity.ExpectedSessionGeneration = 2
	if _, err := adapter.StartTurn(context.Background(), *started, wrongGeneration); !errors.Is(err, sessiondriver.ErrConflict) {
		t.Fatalf("wrong generation was not rejected as conflict: %v", err)
	}
	var sessionData sessionHandleData
	if err := json.Unmarshal(started.Data, &sessionData); err != nil {
		t.Fatal(err)
	}
	sessionData.ThreadID = "different-thread"
	tamperedSession := *started
	tamperedSession.Data, _ = json.Marshal(sessionData)
	if _, err := adapter.StartTurn(context.Background(), tamperedSession, sessionTurnRequest("turn-wrong-thread", "fixture")); !errors.Is(err, sessiondriver.ErrConflict) {
		t.Fatalf("wrong external session identity was not rejected as conflict: %v", err)
	}
	active, err := adapter.StartTurn(context.Background(), *started, sessionTurnRequest("turn-stale", "scenario:active"))
	if err != nil {
		t.Fatal(err)
	}
	var data sessionTurnHandleData
	if err := json.Unmarshal(active.Turn.Data, &data); err != nil {
		t.Fatal(err)
	}
	data.ExternalTurnID = "different-external-turn"
	tampered := active.Turn
	tampered.Data, _ = json.Marshal(data)
	if _, err := adapter.CancelTurn(context.Background(), active.Session, tampered); !errors.Is(err, sessiondriver.ErrConflict) {
		t.Fatalf("wrong external turn was not rejected as conflict: %v", err)
	}
}

func TestCodexSessionRejectsHistoricalTurnIdentityReuse(t *testing.T) {
	adapter, _, workspace, _ := newSessionFixture(t)
	started, err := adapter.StartSession(context.Background(), sessionStartRequest(workspace))
	if err != nil {
		t.Fatal(err)
	}
	first, err := adapter.StartTurn(context.Background(), *started, sessionTurnRequest("turn:qualified", "first response"))
	if err != nil {
		t.Fatal(err)
	}
	firstObserved, err := adapter.ObserveTurn(context.Background(), first.Session, first.Turn)
	if err != nil || firstObserved.State != sessiondriver.TurnResponded {
		t.Fatalf("first observation=%+v err=%v", firstObserved, err)
	}
	second, err := adapter.StartTurn(context.Background(), firstObserved.Session, sessionTurnRequest("turn-second", "second response"))
	if err != nil {
		t.Fatal(err)
	}
	secondObserved, err := adapter.ObserveTurn(context.Background(), second.Session, second.Turn)
	if err != nil || secondObserved.State != sessiondriver.TurnResponded {
		t.Fatalf("second observation=%+v err=%v", secondObserved, err)
	}
	if _, err := adapter.StartTurn(context.Background(), secondObserved.Session, sessionTurnRequest("turn:qualified", "must not relaunch")); !errors.Is(err, sessiondriver.ErrConflict) {
		t.Fatalf("historical turn identity was not rejected: %v", err)
	}
}

func TestCodexSessionMapsOversizedOutputToFailed(t *testing.T) {
	adapter, _, workspace, _ := newSessionFixture(t)
	started, err := adapter.StartSession(context.Background(), sessionStartRequest(workspace))
	if err != nil {
		t.Fatal(err)
	}
	request := sessionTurnRequest("turn-oversized", "scenario:oversized-output")
	request.MaxOutputBytes = 64
	result, err := adapter.StartTurn(context.Background(), *started, request)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := adapter.ObserveTurn(context.Background(), result.Session, result.Turn)
	if err != nil {
		t.Fatal(err)
	}
	if observed.State != sessiondriver.TurnFailed || observed.Output != "" || !strings.Contains(observed.Diagnostic, "exceeds 64 bytes") {
		t.Fatalf("oversized output was not mapped to bounded failure: %+v", observed)
	}
}

func TestCodexSessionRejectsUnknownHandleFields(t *testing.T) {
	adapter, _, workspace, _ := newSessionFixture(t)
	started, err := adapter.StartSession(context.Background(), sessionStartRequest(workspace))
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(started.Data, &data); err != nil {
		t.Fatal(err)
	}
	data["untrustedExtension"] = true
	tampered := *started
	tampered.Data, _ = json.Marshal(data)
	if _, err := adapter.StartTurn(context.Background(), tampered, sessionTurnRequest("turn-unknown-field", "fixture")); err == nil {
		t.Fatal("Session handle with an unknown field was accepted")
	}
}

func TestCodexSessionPersistsNoPromptAndRejectsLostThread(t *testing.T) {
	adapter, _, workspace, externalState := newSessionFixture(t)
	started, err := adapter.StartSession(context.Background(), sessionStartRequest(workspace))
	if err != nil {
		t.Fatal(err)
	}
	secret := "SECRET_PROMPT_MUST_NOT_PERSIST"
	turn, err := adapter.StartTurn(context.Background(), *started, sessionTurnRequest("turn-secret", secret))
	if err != nil {
		t.Fatal(err)
	}
	observed, err := adapter.ObserveTurn(context.Background(), turn.Session, turn.Turn)
	if err != nil {
		t.Fatal(err)
	}
	path, err := adapter.recordPath(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	recordBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(recordBytes), secret) || strings.Contains(string(observed.Session.Data), secret) || strings.Contains(string(observed.Turn.Data), secret) {
		t.Fatal("Fishyume persisted the hidden turn prompt")
	}
	parked, err := adapter.ParkSession(context.Background(), observed.Session)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(externalState, []byte(`{"nextTurn":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ResumeSession(context.Background(), *parked); !errors.Is(err, sessiondriver.ErrLost) {
		t.Fatalf("missing external thread was not classified lost: %v", err)
	}
}

func readStrictTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatal(err)
	}
}
