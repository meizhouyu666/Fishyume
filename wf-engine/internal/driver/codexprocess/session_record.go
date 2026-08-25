package codexprocess

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"wf.local/wf-engine/internal/sessiondriver"
)

const (
	sessionHandleSchemaVersion = 1
	maxSessionTurnIDs          = 256
	maxSessionRecordBytes      = 256 * 1024
)

type sessionLifecycle string

const (
	sessionActive sessionLifecycle = "active"
	sessionParked sessionLifecycle = "parked"
	sessionLost   sessionLifecycle = "lost"
	sessionClosed sessionLifecycle = "closed"
)

type sessionRecord struct {
	SchemaVersion          int                           `json:"schemaVersion"`
	Protocol               string                        `json:"protocol"`
	HandleID               string                        `json:"handleId"`
	Identity               sessiondriver.SessionIdentity `json:"identity"`
	Revision               uint64                        `json:"revision"`
	Workspace              string                        `json:"workspace"`
	Target                 string                        `json:"target"`
	ModelID                string                        `json:"modelId"`
	Sandbox                sessiondriver.Sandbox         `json:"sandbox"`
	Executable             string                        `json:"executable"`
	ExecutableSHA256       string                        `json:"executableSha256"`
	ThreadID               string                        `json:"threadId"`
	ExternalSessionID      string                        `json:"externalSessionId"`
	State                  sessionLifecycle              `json:"state"`
	LastTurnID             string                        `json:"lastTurnId,omitempty"`
	LastExternalTurnID     string                        `json:"lastExternalTurnId,omitempty"`
	LastTurnState          sessiondriver.TurnState       `json:"lastTurnState,omitempty"`
	LastTurnMaxOutputBytes int                           `json:"lastTurnMaxOutputBytes,omitempty"`
	LastTurnOutput         string                        `json:"lastTurnOutput,omitempty"`
	LastTurnDiagnostic     string                        `json:"lastTurnDiagnostic,omitempty"`
	TurnIDs                []string                      `json:"turnIds,omitempty"`
	UpdatedAt              time.Time                     `json:"updatedAt"`
}

type sessionHandleData struct {
	Protocol          string                        `json:"protocol"`
	Identity          sessiondriver.SessionIdentity `json:"identity"`
	Workspace         string                        `json:"workspace"`
	ModelID           string                        `json:"modelId"`
	Sandbox           sessiondriver.Sandbox         `json:"sandbox"`
	Executable        string                        `json:"executable"`
	ExecutableSHA256  string                        `json:"executableSha256"`
	ThreadID          string                        `json:"threadId"`
	ExternalSessionID string                        `json:"externalSessionId"`
	State             sessionLifecycle              `json:"state"`
}

type sessionTurnHandleData struct {
	LogicalTurnID  string `json:"logicalTurnId"`
	ExternalTurnID string `json:"externalTurnId,omitempty"`
}

func (a *SessionAdapter) recordPath(id string) (string, error) {
	if !safeSessionSegment(id) {
		return "", fmt.Errorf("unsafe AgentSession handle identity")
	}
	root, err := filepath.Abs(a.process.config.StateRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Clean(root), "sessions", "codex", id, "state.json"), nil
}

func safeSessionSegment(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > sessiondriver.MaxDriverIdentityBytes {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func (a *SessionAdapter) readRecord(id string) (sessionRecord, error) {
	path, err := a.recordPath(id)
	if err != nil {
		return sessionRecord{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return sessionRecord{}, sessiondriver.Lost("session %q is not registered", id)
		}
		return sessionRecord{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return sessionRecord{}, err
	}
	if info.Size() > maxSessionRecordBytes {
		return sessionRecord{}, fmt.Errorf("AgentSession record exceeds %d bytes", maxSessionRecordBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxSessionRecordBytes+1))
	decoder.DisallowUnknownFields()
	var record sessionRecord
	if err := decoder.Decode(&record); err != nil {
		return sessionRecord{}, fmt.Errorf("decode AgentSession record: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return sessionRecord{}, fmt.Errorf("AgentSession record contains trailing data")
	}
	if err := validateSessionRecord(record); err != nil {
		return sessionRecord{}, err
	}
	return record, nil
}

func (a *SessionAdapter) writeInitialRecord(record sessionRecord) error {
	if err := validateSessionRecordForWrite(record); err != nil {
		return err
	}
	path, err := a.recordPath(record.HandleID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeJSONExclusive(path, record)
}

func (a *SessionAdapter) writeRecord(record sessionRecord) error {
	if err := validateSessionRecordForWrite(record); err != nil {
		return err
	}
	path, err := a.recordPath(record.HandleID)
	if err != nil {
		return err
	}
	return writeJSONAtomic(path, record)
}

func validateSessionRecordForWrite(record sessionRecord) error {
	if err := validateSessionRecord(record); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(data) > maxSessionRecordBytes {
		return fmt.Errorf("AgentSession record exceeds %d bytes", maxSessionRecordBytes)
	}
	return nil
}

func validateSessionRecord(record sessionRecord) error {
	if record.SchemaVersion != sessionHandleSchemaVersion || record.Protocol != appServerProtocol || !safeSessionSegment(record.HandleID) {
		return fmt.Errorf("unsupported AgentSession record")
	}
	if err := sessiondriver.ValidateSessionIdentity(record.Identity); err != nil {
		return err
	}
	if record.Revision == 0 || strings.TrimSpace(record.Workspace) == "" || record.Target != "local" || strings.TrimSpace(record.ModelID) == "" || record.Sandbox != sessiondriver.SandboxReadOnly {
		return fmt.Errorf("AgentSession record policy binding is incomplete")
	}
	if record.Executable == "" || len(record.ExecutableSHA256) != 64 || !validPersistedIdentity(record.ThreadID) || !validPersistedIdentity(record.ExternalSessionID) {
		return fmt.Errorf("AgentSession record Harness identity is incomplete")
	}
	switch record.State {
	case sessionActive, sessionParked, sessionLost, sessionClosed:
	default:
		return fmt.Errorf("unsupported AgentSession lifecycle %q", record.State)
	}
	if record.LastTurnID == "" {
		if record.LastExternalTurnID != "" || record.LastTurnState != "" || record.LastTurnMaxOutputBytes != 0 || record.LastTurnOutput != "" || record.LastTurnDiagnostic != "" || len(record.TurnIDs) != 0 {
			return fmt.Errorf("AgentSession record has turn evidence without turn identity")
		}
		return nil
	}
	if !validPersistedIdentity(record.LastTurnID) || record.LastTurnMaxOutputBytes < 1 || record.LastTurnMaxOutputBytes > sessiondriver.MaxOutputBytes {
		return fmt.Errorf("AgentSession last turn binding is invalid")
	}
	if record.LastExternalTurnID != "" && !validPersistedIdentity(record.LastExternalTurnID) {
		return fmt.Errorf("AgentSession external turn binding is invalid")
	}
	if len(record.TurnIDs) == 0 || len(record.TurnIDs) > maxSessionTurnIDs || record.TurnIDs[len(record.TurnIDs)-1] != record.LastTurnID {
		return fmt.Errorf("AgentSession turn identity history is invalid")
	}
	seen := make(map[string]struct{}, len(record.TurnIDs))
	for _, turnID := range record.TurnIDs {
		if !validPersistedIdentity(turnID) {
			return fmt.Errorf("AgentSession turn identity history is invalid")
		}
		if _, exists := seen[turnID]; exists {
			return fmt.Errorf("AgentSession turn identity %q is duplicated", turnID)
		}
		seen[turnID] = struct{}{}
	}
	switch record.LastTurnState {
	case sessiondriver.TurnDispatching, sessiondriver.TurnActive, sessiondriver.TurnResponded, sessiondriver.TurnInterrupted, sessiondriver.TurnFailed, sessiondriver.TurnLost:
	default:
		return fmt.Errorf("unsupported AgentSession last turn state %q", record.LastTurnState)
	}
	if record.State != sessionActive && (record.LastTurnState == sessiondriver.TurnDispatching || record.LastTurnState == sessiondriver.TurnActive) {
		return fmt.Errorf("inactive AgentSession record contains an active turn")
	}
	if len([]byte(record.LastTurnOutput)) > record.LastTurnMaxOutputBytes || len([]byte(record.LastTurnDiagnostic)) > sessiondriver.MaxDiagnosticBytes {
		return fmt.Errorf("AgentSession last turn evidence exceeds its bound")
	}
	if record.LastTurnState != sessiondriver.TurnResponded && record.LastTurnOutput != "" {
		return fmt.Errorf("only a responded AgentSession turn can carry output")
	}
	return nil
}

func validPersistedIdentity(value string) bool {
	return strings.TrimSpace(value) != "" && value == strings.TrimSpace(value) && len([]byte(value)) <= sessiondriver.MaxDriverIdentityBytes
}

func (a *SessionAdapter) makeSessionHandle(record sessionRecord) (sessiondriver.SessionHandle, error) {
	data, err := json.Marshal(sessionHandleData{Protocol: record.Protocol, Identity: record.Identity, Workspace: record.Workspace, ModelID: record.ModelID, Sandbox: record.Sandbox, Executable: record.Executable, ExecutableSHA256: record.ExecutableSHA256, ThreadID: record.ThreadID, ExternalSessionID: record.ExternalSessionID, State: record.State})
	if err != nil {
		return sessiondriver.SessionHandle{}, err
	}
	handle := sessiondriver.SessionHandle{Driver: a.Name(), Target: record.Target, SchemaVersion: sessionHandleSchemaVersion, ID: record.HandleID, Generation: record.Identity.Generation, Revision: record.Revision, Data: data}
	return handle, sessiondriver.ValidateSessionHandle(handle)
}

func (a *SessionAdapter) makeTurnHandle(record sessionRecord) (sessiondriver.TurnHandle, error) {
	if record.LastTurnID == "" {
		return sessiondriver.TurnHandle{}, fmt.Errorf("AgentSession has no current turn")
	}
	data, err := json.Marshal(sessionTurnHandleData{LogicalTurnID: record.LastTurnID, ExternalTurnID: record.LastExternalTurnID})
	if err != nil {
		return sessiondriver.TurnHandle{}, err
	}
	handle := sessiondriver.TurnHandle{Driver: a.Name(), Target: record.Target, SchemaVersion: sessionHandleSchemaVersion, ID: record.LastTurnID, SessionID: record.HandleID, SessionGeneration: record.Identity.Generation, Data: data}
	return handle, sessiondriver.ValidateTurnHandle(handle)
}

func (a *SessionAdapter) decodeSessionHandle(handle sessiondriver.SessionHandle) (sessionHandleData, error) {
	if err := sessiondriver.ValidateSessionHandle(handle); err != nil {
		return sessionHandleData{}, err
	}
	if handle.Driver != a.Name() || handle.Target != "local" || handle.SchemaVersion != sessionHandleSchemaVersion {
		return sessionHandleData{}, fmt.Errorf("unsupported Codex AgentSession handle")
	}
	var data sessionHandleData
	decoder := json.NewDecoder(bytes.NewReader(handle.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return data, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return data, fmt.Errorf("Codex AgentSession handle contains trailing data")
	}
	if data.Protocol != appServerProtocol || data.Identity.Generation != handle.Generation || data.ThreadID == "" || data.ExternalSessionID == "" || data.Executable == "" || len(data.ExecutableSHA256) != 64 {
		return data, fmt.Errorf("Codex AgentSession handle identity is incomplete")
	}
	return data, nil
}

func (a *SessionAdapter) requireCurrent(handle sessiondriver.SessionHandle) (sessionRecord, error) {
	data, err := a.decodeSessionHandle(handle)
	if err != nil {
		return sessionRecord{}, err
	}
	record, err := a.readRecord(handle.ID)
	if err != nil {
		return sessionRecord{}, err
	}
	if record.Revision != handle.Revision || record.Identity.Generation != handle.Generation {
		return record, sessiondriver.Conflict("session handle revision or generation is stale")
	}
	if !sameSessionBinding(record, data) {
		return record, sessiondriver.Conflict("session handle policy or Harness identity does not match durable state")
	}
	return record, nil
}

func sameSessionBinding(record sessionRecord, data sessionHandleData) bool {
	return record.Protocol == data.Protocol && record.Identity == data.Identity && samePath(record.Workspace, data.Workspace) && record.ModelID == data.ModelID && record.Sandbox == data.Sandbox && sameExecutable(record.Executable, data.Executable) && record.ExecutableSHA256 == data.ExecutableSHA256 && record.ThreadID == data.ThreadID && record.ExternalSessionID == data.ExternalSessionID && record.State == data.State
}

func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func decodeTurnHandle(handle sessiondriver.TurnHandle, record sessionRecord) error {
	if err := sessiondriver.ValidateTurnHandle(handle); err != nil {
		return err
	}
	if handle.Driver != "codex" || handle.Target != "local" || handle.SchemaVersion != sessionHandleSchemaVersion || handle.SessionID != record.HandleID || handle.SessionGeneration != record.Identity.Generation || handle.ID != record.LastTurnID {
		return sessiondriver.Conflict("turn handle does not identify the current AgentSession turn")
	}
	var data sessionTurnHandleData
	decoder := json.NewDecoder(bytes.NewReader(handle.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return err
	}
	if data.LogicalTurnID != record.LastTurnID || (data.ExternalTurnID != "" && data.ExternalTurnID != record.LastExternalTurnID) {
		return sessiondriver.Conflict("turn handle external identity is stale")
	}
	return nil
}
