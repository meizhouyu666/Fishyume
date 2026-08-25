package codexprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"wf.local/wf-engine/internal/sessiondriver"
)

type SessionAdapter struct {
	process *Backend

	mu      sync.Mutex
	clients map[string]*appServerClient
	locks   map[string]*sync.Mutex
}

func NewSessionAdapter(process *Backend) *SessionAdapter {
	return &SessionAdapter{process: process, clients: make(map[string]*appServerClient), locks: make(map[string]*sync.Mutex)}
}

func (*SessionAdapter) Name() string { return "codex" }

func (*SessionAdapter) Capabilities() sessiondriver.DriverCapabilities {
	return sessiondriver.DriverCapabilities{
		Targets: []string{"local"}, SupportsResume: true, SupportsPark: true,
		SupportsRecovery: true, SupportsDirectedInput: true, SupportsConfirmedCancel: true,
		MaxConcurrentTurns: 1,
	}
}

func (a *SessionAdapter) StartSession(ctx context.Context, request sessiondriver.StartSessionRequest) (*sessiondriver.SessionHandle, error) {
	if a == nil || a.process == nil {
		return nil, fmt.Errorf("Codex Session adapter is unavailable")
	}
	if err := sessiondriver.ValidateStartSessionRequest(request); err != nil {
		return nil, err
	}
	if request.Target != "local" {
		return nil, fmt.Errorf("Codex Session adapter supports only target local")
	}
	if strings.TrimSpace(a.process.config.StateRoot) == "" || !filepath.IsAbs(a.process.config.StateRoot) {
		return nil, fmt.Errorf("Codex Session state root must be an absolute path")
	}
	workspace, err := canonicalDirectory(request.Workspace)
	if err != nil {
		return nil, err
	}
	discovered, err := a.process.discoverExecutable()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(a.process.config.StateRoot, 0o700); err != nil {
		return nil, err
	}
	client, err := startAppServer(ctx, discovered.Path, a.process.config.StateRoot, a.process.config.MaxStderrBytes)
	if err != nil {
		return nil, err
	}
	var started appThreadResponse
	err = client.request(ctx, "thread/start", map[string]any{
		"cwd": workspace, "model": modelName(request.ModelID), "sandbox": "read-only",
		"approvalPolicy": "never", "ephemeral": false,
		"developerInstructions": "Fishyume AgentSession is read-only. Do not spawn subagents or access the network.",
	}, &started)
	if err != nil {
		_ = client.close()
		return nil, err
	}
	if err := validateThreadResponse(started, workspace, request.ModelID); err != nil {
		_ = client.close()
		return nil, err
	}
	handleID, err := randomID("session")
	if err != nil {
		_ = client.close()
		return nil, err
	}
	record := sessionRecord{
		SchemaVersion: sessionHandleSchemaVersion, Protocol: appServerProtocol, HandleID: handleID,
		Identity: request.Identity, Revision: 1, Workspace: workspace, Target: request.Target,
		ModelID: request.ModelID, Sandbox: request.Sandbox, Executable: discovered.Path,
		ExecutableSHA256: discovered.SHA256, ThreadID: started.Thread.ID,
		ExternalSessionID: started.Thread.SessionID, State: sessionActive, UpdatedAt: time.Now().UTC(),
	}
	if err := a.writeInitialRecord(record); err != nil {
		_ = client.close()
		return nil, err
	}
	a.setClient(handleID, client)
	handle, err := a.makeSessionHandle(record)
	return &handle, err
}

func (a *SessionAdapter) StartTurn(ctx context.Context, handle sessiondriver.SessionHandle, request sessiondriver.StartTurnRequest) (*sessiondriver.StartTurnResult, error) {
	if err := sessiondriver.ValidateStartTurnRequest(request); err != nil {
		return nil, err
	}
	lock := a.sessionLock(handle.ID)
	lock.Lock()
	defer lock.Unlock()
	data, err := a.decodeSessionHandle(handle)
	if err != nil {
		return nil, err
	}
	record, err := a.readRecord(handle.ID)
	if err != nil {
		return nil, err
	}
	if !sameSessionBindingIgnoringState(record, data) || record.Identity.Generation != request.Identity.ExpectedSessionGeneration {
		return nil, sessiondriver.Conflict("turn request does not match the current session generation or binding")
	}
	if record.Revision != handle.Revision {
		if record.LastTurnID != request.Identity.TurnID {
			return nil, sessiondriver.Conflict("session handle revision is stale")
		}
		return a.recoverStartedTurn(ctx, record)
	}
	if record.State != sessionActive {
		if record.State == sessionLost {
			return nil, sessiondriver.Lost("Codex thread %q is lost", record.ThreadID)
		}
		return nil, sessiondriver.Conflict("session must be active before starting a turn")
	}
	if record.LastTurnID != "" {
		if record.LastTurnID == request.Identity.TurnID {
			return a.recoverStartedTurn(ctx, record)
		}
		if record.LastTurnState == sessiondriver.TurnDispatching || record.LastTurnState == sessiondriver.TurnActive {
			return nil, sessiondriver.Conflict("another AgentSession turn is still active")
		}
	}
	for _, used := range record.TurnIDs {
		if used == request.Identity.TurnID {
			return nil, sessiondriver.Conflict("AgentSession turn identity %q was already used", used)
		}
	}
	if len(record.TurnIDs) >= maxSessionTurnIDs {
		return nil, sessiondriver.Conflict("AgentSession reached the Driver turn identity limit")
	}
	client := a.client(handle.ID)
	if client == nil {
		return nil, sessiondriver.Conflict("session transport is not active; resume the session before starting a turn")
	}
	record.LastTurnID = request.Identity.TurnID
	record.LastExternalTurnID = ""
	record.LastTurnState = sessiondriver.TurnDispatching
	record.LastTurnMaxOutputBytes = request.MaxOutputBytes
	record.LastTurnOutput = ""
	record.LastTurnDiagnostic = ""
	record.TurnIDs = append(record.TurnIDs, request.Identity.TurnID)
	record.Revision++
	record.UpdatedAt = time.Now().UTC()
	if err := a.writeRecord(record); err != nil {
		return nil, err
	}
	var started appTurnStartResponse
	err = client.request(ctx, "turn/start", map[string]any{
		"threadId":            record.ThreadID,
		"clientUserMessageId": request.Identity.TurnID,
		"input":               []map[string]any{{"type": "text", "text": sessionPrompt(request, record.HandleID), "text_elements": []any{}}},
		"cwd":                 record.Workspace, "model": modelName(record.ModelID), "approvalPolicy": "never",
		"sandboxPolicy": map[string]any{"type": "readOnly", "networkAccess": false},
		"outputSchema":  explorationResultSchema(),
	}, &started)
	if err != nil {
		return a.reconcileStartError(ctx, record, err)
	}
	if !validPersistedIdentity(started.Turn.ID) || started.Turn.Status != "inProgress" {
		return nil, fmt.Errorf("Codex app-server returned an invalid started turn")
	}
	record.LastExternalTurnID = started.Turn.ID
	record.LastTurnState = sessiondriver.TurnActive
	record.Revision++
	record.UpdatedAt = time.Now().UTC()
	if err := a.writeRecord(record); err != nil {
		return nil, err
	}
	return a.startTurnResult(record)
}

func (a *SessionAdapter) ObserveTurn(ctx context.Context, handle sessiondriver.SessionHandle, turn sessiondriver.TurnHandle) (*sessiondriver.TurnObservation, error) {
	lock := a.sessionLock(handle.ID)
	lock.Lock()
	defer lock.Unlock()
	record, err := a.requireCurrent(handle)
	if err != nil {
		return nil, err
	}
	if err := decodeTurnHandle(turn, record); err != nil {
		return nil, err
	}
	record, err = a.reconcileTurn(ctx, record)
	if err != nil {
		return nil, err
	}
	return a.observation(record)
}

func (a *SessionAdapter) ParkSession(ctx context.Context, handle sessiondriver.SessionHandle) (*sessiondriver.SessionHandle, error) {
	lock := a.sessionLock(handle.ID)
	lock.Lock()
	defer lock.Unlock()
	record, err := a.requireCurrent(handle)
	if err != nil {
		return nil, err
	}
	if record.State == sessionParked {
		result, makeErr := a.makeSessionHandle(record)
		return &result, makeErr
	}
	if record.State != sessionActive {
		return nil, sessiondriver.Conflict("only an active session can be parked")
	}
	if record.LastTurnState == sessiondriver.TurnDispatching || record.LastTurnState == sessiondriver.TurnActive {
		record, err = a.reconcileTurn(ctx, record)
		if err != nil {
			return nil, err
		}
		if record.LastTurnState == sessiondriver.TurnDispatching || record.LastTurnState == sessiondriver.TurnActive {
			return nil, sessiondriver.Conflict("an active turn must finish or be cancelled before parking")
		}
	}
	if err := a.stopClient(handle.ID); err != nil {
		return nil, err
	}
	record.State = sessionParked
	record.Revision++
	record.UpdatedAt = time.Now().UTC()
	if err := a.writeRecord(record); err != nil {
		return nil, err
	}
	result, err := a.makeSessionHandle(record)
	return &result, err
}

func (a *SessionAdapter) ResumeSession(ctx context.Context, handle sessiondriver.SessionHandle) (*sessiondriver.SessionHandle, error) {
	lock := a.sessionLock(handle.ID)
	lock.Lock()
	defer lock.Unlock()
	record, err := a.requireCurrent(handle)
	if err != nil {
		return nil, err
	}
	if record.State == sessionClosed {
		return nil, sessiondriver.Conflict("closed AgentSession cannot be resumed")
	}
	if record.State == sessionLost {
		return nil, sessiondriver.Lost("Codex thread %q is lost", record.ThreadID)
	}
	if a.client(handle.ID) == nil {
		if _, err := a.connectAndResume(ctx, record); err != nil {
			if isMissingThreadError(err) {
				record.State = sessionLost
				record.Revision++
				record.UpdatedAt = time.Now().UTC()
				_ = a.writeRecord(record)
				return nil, sessiondriver.Lost("Codex thread %q cannot be resumed", record.ThreadID)
			}
			return nil, err
		}
	}
	if record.State == sessionParked {
		record.State = sessionActive
		record.Revision++
		record.UpdatedAt = time.Now().UTC()
		if err := a.writeRecord(record); err != nil {
			return nil, err
		}
	}
	if record.LastTurnID != "" {
		record, err = a.reconcileTurn(ctx, record)
		if err != nil {
			return nil, err
		}
	}
	result, err := a.makeSessionHandle(record)
	return &result, err
}

func (a *SessionAdapter) CancelTurn(ctx context.Context, handle sessiondriver.SessionHandle, turn sessiondriver.TurnHandle) (*sessiondriver.CancelTurnResult, error) {
	lock := a.sessionLock(handle.ID)
	lock.Lock()
	defer lock.Unlock()
	record, err := a.requireCurrent(handle)
	if err != nil {
		return nil, err
	}
	if err := decodeTurnHandle(turn, record); err != nil {
		return nil, err
	}
	record, err = a.reconcileTurn(ctx, record)
	if err != nil {
		return nil, err
	}
	if record.LastTurnState != sessiondriver.TurnActive {
		return nil, sessiondriver.Conflict("only the exact active turn can be cancelled")
	}
	client := a.client(handle.ID)
	if client == nil {
		return nil, sessiondriver.Lost("Codex app-server transport is unavailable")
	}
	if err := client.request(ctx, "turn/interrupt", map[string]any{"threadId": record.ThreadID, "turnId": record.LastExternalTurnID}, nil); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(a.process.config.PollInterval)
	defer ticker.Stop()
	for {
		record, err = a.reconcileTurn(ctx, record)
		if err != nil {
			return nil, err
		}
		if record.LastTurnState != sessiondriver.TurnActive && record.LastTurnState != sessiondriver.TurnDispatching {
			state := sessiondriver.CancelNotConfirmed
			diagnostic := "turn reached a terminal state before interruption was confirmed"
			if record.LastTurnState == sessiondriver.TurnInterrupted {
				state = sessiondriver.CancelConfirmed
				diagnostic = "Codex app-server reported the exact turn as interrupted"
			}
			sessionHandle, makeErr := a.makeSessionHandle(record)
			if makeErr != nil {
				return nil, makeErr
			}
			turnHandle, makeErr := a.makeTurnHandle(record)
			if makeErr != nil {
				return nil, makeErr
			}
			result := &sessiondriver.CancelTurnResult{Session: sessionHandle, Turn: turnHandle, State: state, Diagnostic: diagnostic}
			return result, sessiondriver.ValidateCancelTurnResult(*result)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (a *SessionAdapter) CloseSession(ctx context.Context, handle sessiondriver.SessionHandle) (*sessiondriver.SessionHandle, error) {
	lock := a.sessionLock(handle.ID)
	lock.Lock()
	defer lock.Unlock()
	record, err := a.requireCurrent(handle)
	if err != nil {
		return nil, err
	}
	if record.State == sessionClosed {
		result, makeErr := a.makeSessionHandle(record)
		return &result, makeErr
	}
	if record.LastTurnState == sessiondriver.TurnDispatching || record.LastTurnState == sessiondriver.TurnActive {
		record, err = a.reconcileTurn(ctx, record)
		if err != nil {
			return nil, err
		}
		if record.LastTurnState == sessiondriver.TurnDispatching || record.LastTurnState == sessiondriver.TurnActive {
			return nil, sessiondriver.Conflict("an active turn must finish or be cancelled before closing")
		}
	}
	if err := a.stopClient(handle.ID); err != nil {
		return nil, err
	}
	record.State = sessionClosed
	record.Revision++
	record.UpdatedAt = time.Now().UTC()
	if err := a.writeRecord(record); err != nil {
		return nil, err
	}
	result, err := a.makeSessionHandle(record)
	return &result, err
}

func (a *SessionAdapter) recoverStartedTurn(ctx context.Context, record sessionRecord) (*sessiondriver.StartTurnResult, error) {
	if record.LastTurnID == "" {
		return nil, sessiondriver.Conflict("stale session handle has no matching turn intent")
	}
	if a.client(record.HandleID) == nil {
		if _, err := a.connectAndResume(ctx, record); err != nil {
			return nil, err
		}
	}
	var err error
	record, err = a.reconcileTurn(ctx, record)
	if err != nil {
		return nil, err
	}
	return a.startTurnResult(record)
}

func (a *SessionAdapter) reconcileStartError(ctx context.Context, record sessionRecord, startErr error) (*sessiondriver.StartTurnResult, error) {
	_ = a.stopClient(record.HandleID)
	reconciled, err := a.reconcileTurn(ctx, record)
	if err == nil && reconciled.LastTurnState != sessiondriver.TurnLost {
		return a.startTurnResult(reconciled)
	}
	if err != nil {
		return nil, errors.Join(startErr, err)
	}
	return nil, errors.Join(startErr, sessiondriver.Lost("turn launch could not be reconciled"))
}

func validateThreadResponse(response appThreadResponse, workspace, modelID string) error {
	if !validPersistedIdentity(response.Thread.ID) || !validPersistedIdentity(response.Thread.SessionID) || response.Thread.Ephemeral {
		return fmt.Errorf("Codex app-server returned an incomplete or ephemeral thread")
	}
	if !samePath(response.CWD, workspace) || !samePath(response.Thread.CWD, workspace) {
		return sessiondriver.Conflict("Codex app-server workspace binding changed")
	}
	if response.Model != modelName(modelID) {
		return sessiondriver.Conflict("Codex app-server model binding changed")
	}
	if response.Sandbox.Type != "readOnly" || string(response.ApprovalPolicy) != `"never"` {
		return sessiondriver.Conflict("Codex app-server sandbox or approval policy changed")
	}
	return nil
}

func (a *SessionAdapter) observation(record sessionRecord) (*sessiondriver.TurnObservation, error) {
	sessionHandle, err := a.makeSessionHandle(record)
	if err != nil {
		return nil, err
	}
	turnHandle, err := a.makeTurnHandle(record)
	if err != nil {
		return nil, err
	}
	result := &sessiondriver.TurnObservation{Session: sessionHandle, Turn: turnHandle, State: record.LastTurnState, Output: record.LastTurnOutput, Diagnostic: record.LastTurnDiagnostic}
	return result, sessiondriver.ValidateTurnObservation(*result)
}

func (a *SessionAdapter) startTurnResult(record sessionRecord) (*sessiondriver.StartTurnResult, error) {
	sessionHandle, err := a.makeSessionHandle(record)
	if err != nil {
		return nil, err
	}
	turnHandle, err := a.makeTurnHandle(record)
	if err != nil {
		return nil, err
	}
	return &sessiondriver.StartTurnResult{Session: sessionHandle, Turn: turnHandle}, nil
}

func sameSessionBindingIgnoringState(record sessionRecord, data sessionHandleData) bool {
	data.State = record.State
	return sameSessionBinding(record, data)
}

func (a *SessionAdapter) sessionLock(id string) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	lock := a.locks[id]
	if lock == nil {
		lock = &sync.Mutex{}
		a.locks[id] = lock
	}
	return lock
}

func (a *SessionAdapter) client(id string) *appServerClient {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.clients[id]
}

func (a *SessionAdapter) setClient(id string, client *appServerClient) {
	a.mu.Lock()
	old := a.clients[id]
	a.clients[id] = client
	a.mu.Unlock()
	if old != nil && old != client {
		_ = old.close()
	}
}

func (a *SessionAdapter) stopClient(id string) error {
	a.mu.Lock()
	client := a.clients[id]
	delete(a.clients, id)
	a.mu.Unlock()
	if client == nil {
		return nil
	}
	return client.close()
}

func (a *SessionAdapter) closeAllClients() error {
	a.mu.Lock()
	clients := a.clients
	a.clients = make(map[string]*appServerClient)
	a.mu.Unlock()
	var joined error
	for _, client := range clients {
		joined = errors.Join(joined, client.close())
	}
	return joined
}

func isMissingThreadError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no rollout found") || strings.Contains(text, "thread") && strings.Contains(text, "not found")
}

func sessionPrompt(request sessiondriver.StartTurnRequest, sessionID string) string {
	identity, _ := json.Marshal(map[string]any{"sessionId": sessionID, "turnId": request.Identity.TurnID, "sessionGeneration": request.Identity.ExpectedSessionGeneration})
	return request.Prompt + "\n\nFishyume Team contribution protocol:\nReturn exactly one JSON object matching the provided schema. Content is public untrusted discussion material; do not include hidden reasoning.\nFISHYUME_SESSION_TURN_IDENTITY=" + string(identity)
}

func boundedSessionDiagnostic(value string) string {
	if len([]byte(value)) <= sessiondriver.MaxDiagnosticBytes {
		return value
	}
	value = value[:sessiondriver.MaxDiagnosticBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

var _ sessiondriver.Driver = (*SessionAdapter)(nil)
