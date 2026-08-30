package store

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"wf.local/wf-engine/internal/teamcontract"
)

func (s *Store) TeamDir(teamID string) string { return filepath.Join(s.root, "teams", teamID) }
func (s *Store) TeamTemplatePath(templateID string) string {
	return filepath.Join(s.root, "team-templates", templateID+".json")
}

func (s *Store) WriteTeamTemplate(value teamcontract.TeamTemplateV1) error {
	if err := teamcontract.ValidateTeamTemplate(value); err != nil {
		return err
	}
	return s.writeJSON(s.TeamTemplatePath(value.TemplateID), value)
}

func (s *Store) ReadTeamTemplate(templateID string, target *teamcontract.TeamTemplateV1) error {
	if err := validateID("template", templateID); err != nil {
		return err
	}
	if err := readTeamContractJSON(s.TeamTemplatePath(templateID), target); err != nil {
		return err
	}
	if target.TemplateID != templateID {
		return fmt.Errorf("template ID %q does not match %q", target.TemplateID, templateID)
	}
	return teamcontract.ValidateTeamTemplate(*target)
}

func (s *Store) ListTeamTemplateIDs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "team-templates"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if safeID.MatchString(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Store) DeleteTeamTemplate(templateID string) error {
	if err := validateID("template", templateID); err != nil {
		return err
	}
	if err := os.Remove(s.TeamTemplatePath(templateID)); err != nil {
		return err
	}
	return nil
}

func (s *Store) TeamSnapshotPath(teamID string) string {
	return filepath.Join(s.TeamDir(teamID), "team.json")
}
func (s *Store) TeamEventsPath(teamID string) string {
	return filepath.Join(s.TeamDir(teamID), "events.jsonl")
}
func (s *Store) TeamMessagesPath(teamID string) string {
	return filepath.Join(s.TeamDir(teamID), "messages.jsonl")
}
func (s *Store) TeamParticipantPath(teamID, participantID string) string {
	return filepath.Join(s.TeamDir(teamID), "participants", participantID+".json")
}
func (s *Store) TeamTurnDir(teamID, turnID string) string {
	return filepath.Join(s.TeamDir(teamID), "turns", turnID)
}
func (s *Store) TeamTurnPath(teamID, turnID string) string {
	return filepath.Join(s.TeamTurnDir(teamID, turnID), "turn.json")
}
func (s *Store) TeamExecutionPath(teamID, turnID string) string {
	return filepath.Join(s.TeamTurnDir(teamID, turnID), "execution.json")
}
func (s *Store) TeamParticipantSessionPath(teamID, participantID string) string {
	return filepath.Join(s.TeamDir(teamID), "sessions", participantID+".json")
}
func (s *Store) TeamSessionExecutionPath(teamID, turnID string) string {
	return filepath.Join(s.TeamTurnDir(teamID, turnID), "session-execution.json")
}
func (s *Store) TeamHandoffPath(teamID, handoffID string) string {
	return filepath.Join(s.TeamDir(teamID), "handoffs", handoffID+".json")
}
func (s *Store) TeamBindingsPath(teamID string) string {
	return filepath.Join(s.TeamDir(teamID), "handoff-bindings.json")
}

func (s *Store) TeamHandoffIntentPath(teamID, handoffID string) string {
	return filepath.Join(s.TeamDir(teamID), "handoff-intents", digestID(handoffID)+".json")
}

func (s *Store) TeamBindingIntentPath(teamID, actionID string) string {
	return filepath.Join(s.TeamDir(teamID), "handoff-binding-intents", digestID(actionID)+".json")
}

func (s *Store) TeamActionIntentPath(teamID, actionID string) string {
	return filepath.Join(s.TeamDir(teamID), "action-intents", digestID(actionID)+".json")
}

func digestID(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func (s *Store) InitTeam(teamID string) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	for _, dir := range []string{filepath.Join(s.TeamDir(teamID), "participants"), filepath.Join(s.TeamDir(teamID), "turns"), filepath.Join(s.TeamDir(teamID), "sessions"), filepath.Join(s.TeamDir(teamID), "handoffs"), filepath.Join(s.TeamDir(teamID), "action-intents"), filepath.Join(s.TeamDir(teamID), "handoff-intents"), filepath.Join(s.TeamDir(teamID), "handoff-binding-intents")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create team directory %q: %w", dir, err)
		}
	}
	return nil
}

func (s *Store) WriteTeamSnapshot(snapshot teamcontract.TeamSessionV1) error {
	if err := teamcontract.ValidateTeamSession(snapshot); err != nil {
		return err
	}
	return s.writeJSON(s.TeamSnapshotPath(snapshot.TeamID), snapshot)
}

func (s *Store) EnsureTeamSnapshot(snapshot teamcontract.TeamSessionV1) error {
	if err := teamcontract.ValidateTeamSession(snapshot); err != nil {
		return err
	}
	return s.ensureJSON(s.TeamSnapshotPath(snapshot.TeamID), snapshot, fmt.Sprintf("initial Team snapshot for %q", snapshot.TeamID))
}

func (s *Store) ReadTeamSnapshot(teamID string, target *teamcontract.TeamSessionV1) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := readTeamSessionCompatJSON(s.TeamSnapshotPath(teamID), target); err != nil {
		return err
	}
	if target.TeamID != teamID {
		return fmt.Errorf("Team snapshot ID %q does not match %q", target.TeamID, teamID)
	}
	return teamcontract.ValidateTeamSession(*target)
}

func readTeamSessionCompatJSON(path string, target *teamcontract.TeamSessionV1) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Team contract %q: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(teamcontract.MaxHandoffBytes)+1))
	if err != nil {
		return fmt.Errorf("read Team contract %q: %w", path, err)
	}
	if len(data) > teamcontract.MaxHandoffBytes {
		return fmt.Errorf("Team contract %q exceeds %d bytes", path, teamcontract.MaxHandoffBytes)
	}
	if err := teamcontract.DecodeTeamSessionCompat(data, target); err != nil {
		return fmt.Errorf("decode Team contract %q: %w", path, err)
	}
	return nil
}

func (s *Store) WriteTeamParticipant(value teamcontract.ParticipantV1, teamID string) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := teamcontract.ValidateParticipant(value); err != nil {
		return err
	}
	return s.writeJSON(s.TeamParticipantPath(teamID, value.ParticipantID), value)
}

func (s *Store) ReadTeamParticipant(teamID, participantID string, target *teamcontract.ParticipantV1) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("participant", participantID); err != nil {
		return err
	}
	if err := readTeamContractJSON(s.TeamParticipantPath(teamID, participantID), target); err != nil {
		return err
	}
	if target.ParticipantID != participantID {
		return fmt.Errorf("participant ID %q does not match %q", target.ParticipantID, participantID)
	}
	return teamcontract.ValidateParticipant(*target)
}

func (s *Store) ListTeamParticipantIDs(teamID string) ([]string, error) {
	if err := validateID("team", teamID); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(s.TeamDir(teamID), "participants"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(filepath.Ext(entry.Name()))]
		if safeID.MatchString(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Store) WriteTeamTurn(value teamcontract.ParticipantTurnV1) error {
	if err := teamcontract.ValidateParticipantTurn(value); err != nil {
		return err
	}
	return s.writeJSON(s.TeamTurnPath(value.TeamID, value.TurnID), value)
}

func (s *Store) ReadTeamTurn(teamID, turnID string, target *teamcontract.ParticipantTurnV1) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("turn", turnID); err != nil {
		return err
	}
	if err := readTeamContractJSON(s.TeamTurnPath(teamID, turnID), target); err != nil {
		return err
	}
	if target.TeamID != teamID || target.TurnID != turnID {
		return fmt.Errorf("turn identity does not match requested path")
	}
	return teamcontract.ValidateParticipantTurn(*target)
}

func (s *Store) ListTeamTurnIDs(teamID string) ([]string, error) {
	if err := validateID("team", teamID); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(s.TeamDir(teamID), "turns"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && safeID.MatchString(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Store) WriteTeamExecution(teamID, turnID string, handle json.RawMessage) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("turn", turnID); err != nil {
		return err
	}
	if len(handle) == 0 || len(handle) > teamcontract.MaxExecutionHandleBytes || !json.Valid(handle) {
		return fmt.Errorf("team execution handle is invalid or exceeds its bound")
	}
	return s.writeJSON(s.TeamExecutionPath(teamID, turnID), handle)
}

func (s *Store) ReadTeamExecution(teamID, turnID string) (json.RawMessage, error) {
	if err := validateID("team", teamID); err != nil {
		return nil, err
	}
	if err := validateID("turn", turnID); err != nil {
		return nil, err
	}
	var handle json.RawMessage
	if err := readJSON(s.TeamExecutionPath(teamID, turnID), &handle); err != nil {
		return nil, err
	}
	if len(handle) == 0 || len(handle) > teamcontract.MaxExecutionHandleBytes || !json.Valid(handle) {
		return nil, fmt.Errorf("persisted team execution handle is invalid")
	}
	return handle, nil
}

func (s *Store) WriteTeamParticipantSession(teamID, participantID string, value json.RawMessage) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("participant", participantID); err != nil {
		return err
	}
	return s.writeBoundedTeamPrivateJSON(s.TeamParticipantSessionPath(teamID, participantID), value)
}

func (s *Store) ReadTeamParticipantSession(teamID, participantID string) (json.RawMessage, error) {
	if err := validateID("team", teamID); err != nil {
		return nil, err
	}
	if err := validateID("participant", participantID); err != nil {
		return nil, err
	}
	return readBoundedTeamPrivateJSON(s.TeamParticipantSessionPath(teamID, participantID))
}

func (s *Store) WriteTeamSessionExecution(teamID, turnID string, value json.RawMessage) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("turn", turnID); err != nil {
		return err
	}
	return s.writeBoundedTeamPrivateJSON(s.TeamSessionExecutionPath(teamID, turnID), value)
}

func (s *Store) ReadTeamSessionExecution(teamID, turnID string) (json.RawMessage, error) {
	if err := validateID("team", teamID); err != nil {
		return nil, err
	}
	if err := validateID("turn", turnID); err != nil {
		return nil, err
	}
	return readBoundedTeamPrivateJSON(s.TeamSessionExecutionPath(teamID, turnID))
}

func (s *Store) writeBoundedTeamPrivateJSON(path string, value json.RawMessage) error {
	if len(value) == 0 || len(value) > 2*teamcontract.MaxExecutionHandleBytes || !json.Valid(value) {
		return fmt.Errorf("private Team Session record is invalid or exceeds its bound")
	}
	return s.writeJSON(path, value)
}

func readBoundedTeamPrivateJSON(path string) (json.RawMessage, error) {
	var value json.RawMessage
	if err := readJSON(path, &value); err != nil {
		return nil, err
	}
	if len(value) == 0 || len(value) > 2*teamcontract.MaxExecutionHandleBytes || !json.Valid(value) {
		return nil, fmt.Errorf("persisted private Team Session record is invalid")
	}
	return value, nil
}

func (s *Store) AppendTeamEvent(value teamcontract.TeamEventV1) error {
	if err := teamcontract.ValidateEvent(value); err != nil {
		return err
	}
	path := s.TeamEventsPath(value.TeamID)
	return s.appendTeamRecord(path, "append_team_event", value.Sequence, func(raw []byte) (uint64, error) {
		var event teamcontract.TeamEventV1
		if err := json.Unmarshal(raw, &event); err != nil {
			return 0, err
		}
		if err := teamcontract.ValidateEvent(event); err != nil {
			return 0, err
		}
		return event.Sequence, nil
	}, value)
}

func (s *Store) AppendTeamMessage(value teamcontract.TeamMessageV1) error {
	if err := teamcontract.ValidateMessage(value); err != nil {
		return err
	}
	path := s.TeamMessagesPath(value.TeamID)
	return s.appendTeamRecordWithQuota(path, "append_team_message", value.Sequence, teamcontract.MaxRetainedMessages, teamcontract.MaxRetainedMessageBytes, func(raw []byte) (uint64, error) {
		var message teamcontract.TeamMessageV1
		if err := json.Unmarshal(raw, &message); err != nil {
			return 0, err
		}
		if err := teamcontract.ValidateMessage(message); err != nil {
			return 0, err
		}
		return message.Sequence, nil
	}, value)
}

func (s *Store) ReadTeamEvents(teamID string) ([]teamcontract.TeamEventV1, error) {
	if err := validateID("team", teamID); err != nil {
		return nil, err
	}
	var events []teamcontract.TeamEventV1
	err := readTeamLog(s.TeamEventsPath(teamID), func(raw []byte, expected uint64) error {
		var event teamcontract.TeamEventV1
		if err := json.Unmarshal(raw, &event); err != nil {
			return err
		}
		if event.TeamID != teamID || event.Sequence != expected {
			return fmt.Errorf("team event sequence is not strictly increasing")
		}
		if err := teamcontract.ValidateEvent(event); err != nil {
			return err
		}
		events = append(events, event)
		return nil
	})
	return events, err
}

func (s *Store) ReadTeamMessages(teamID string) ([]teamcontract.TeamMessageV1, error) {
	if err := validateID("team", teamID); err != nil {
		return nil, err
	}
	var messages []teamcontract.TeamMessageV1
	err := readTeamLog(s.TeamMessagesPath(teamID), func(raw []byte, expected uint64) error {
		var message teamcontract.TeamMessageV1
		if err := json.Unmarshal(raw, &message); err != nil {
			return err
		}
		if message.TeamID != teamID || message.Sequence != expected {
			return fmt.Errorf("team message sequence is not strictly increasing")
		}
		if err := teamcontract.ValidateMessage(message); err != nil {
			return err
		}
		messages = append(messages, message)
		return nil
	})
	return messages, err
}

func (s *Store) WriteTeamHandoff(value teamcontract.HandoffArtifactV1) error {
	if err := teamcontract.ValidateHandoff(value); err != nil {
		return err
	}
	return s.ensureJSON(s.TeamHandoffPath(value.TeamID, value.HandoffID), value, fmt.Sprintf("handoff %q", value.HandoffID))
}

func (s *Store) ReadTeamHandoff(teamID, handoffID string, target *teamcontract.HandoffArtifactV1) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("handoff", handoffID); err != nil {
		return err
	}
	if err := readTeamContractJSON(s.TeamHandoffPath(teamID, handoffID), target); err != nil {
		return err
	}
	if target.TeamID != teamID || target.HandoffID != handoffID {
		return fmt.Errorf("handoff identity does not match requested path")
	}
	return teamcontract.ValidateHandoff(*target)
}

type teamHandoffBindingsV1 struct {
	SchemaVersion string                          `json:"schemaVersion"`
	Items         []teamcontract.HandoffBindingV1 `json:"items"`
}

func (s *Store) WriteTeamBinding(value teamcontract.HandoffBindingV1) error {
	if err := teamcontract.ValidateHandoffBinding(value); err != nil {
		return err
	}
	bindings, err := s.ReadTeamBindings(value.TeamID)
	if err != nil {
		return err
	}
	for _, existing := range bindings {
		if existing.HandoffID == value.HandoffID {
			if existing == value {
				return nil
			}
			return fmt.Errorf("handoff %q already has a different binding", value.HandoffID)
		}
	}
	bindings = append(bindings, value)
	if len(bindings) > teamcontract.MaxMutationReceipts {
		return fmt.Errorf("team Handoff binding quota exceeded")
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].HandoffID < bindings[j].HandoffID })
	collection := teamHandoffBindingsV1{SchemaVersion: teamcontract.SchemaVersion, Items: bindings}
	encoded, err := json.Marshal(collection)
	if err != nil {
		return err
	}
	if len(encoded) > teamcontract.MaxRetainedMessageBytes {
		return fmt.Errorf("team Handoff bindings exceed %d bytes", teamcontract.MaxRetainedMessageBytes)
	}
	return s.writeJSON(s.TeamBindingsPath(value.TeamID), collection)
}

func (s *Store) ReadTeamBinding(teamID, handoffID string, target *teamcontract.HandoffBindingV1) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("handoff", handoffID); err != nil {
		return err
	}
	bindings, err := s.ReadTeamBindings(teamID)
	if err != nil {
		return err
	}
	for _, binding := range bindings {
		if binding.HandoffID == handoffID {
			*target = binding
			return nil
		}
	}
	return os.ErrNotExist
}

func (s *Store) ReadTeamBindings(teamID string) ([]teamcontract.HandoffBindingV1, error) {
	if err := validateID("team", teamID); err != nil {
		return nil, err
	}
	var collection teamHandoffBindingsV1
	if err := readTeamContractJSONLimit(s.TeamBindingsPath(teamID), &collection, teamcontract.MaxRetainedMessageBytes); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []teamcontract.HandoffBindingV1{}, nil
		}
		return nil, err
	}
	if collection.SchemaVersion != teamcontract.SchemaVersion {
		return nil, fmt.Errorf("unsupported Team binding schema version %q", collection.SchemaVersion)
	}
	if len(collection.Items) > teamcontract.MaxMutationReceipts {
		return nil, fmt.Errorf("team Handoff binding quota exceeded")
	}
	seen := make(map[string]struct{}, len(collection.Items))
	for _, binding := range collection.Items {
		if err := teamcontract.ValidateHandoffBinding(binding); err != nil {
			return nil, err
		}
		if binding.TeamID != teamID {
			return nil, fmt.Errorf("binding team ID %q does not match %q", binding.TeamID, teamID)
		}
		if _, exists := seen[binding.HandoffID]; exists {
			return nil, fmt.Errorf("duplicate binding for handoff %q", binding.HandoffID)
		}
		seen[binding.HandoffID] = struct{}{}
	}
	return collection.Items, nil
}

func (s *Store) WriteTeamHandoffIntent(teamID, handoffID string, intent any) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("handoff", handoffID); err != nil {
		return err
	}
	return s.writeJSON(s.TeamHandoffIntentPath(teamID, handoffID), intent)
}

func (s *Store) ReadTeamHandoffIntent(teamID, handoffID string, target any) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("handoff", handoffID); err != nil {
		return err
	}
	return readTeamContractJSON(s.TeamHandoffIntentPath(teamID, handoffID), target)
}

func (s *Store) WriteTeamBindingIntent(teamID, actionID string, intent any) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("action", actionID); err != nil {
		return err
	}
	return s.writeJSON(s.TeamBindingIntentPath(teamID, actionID), intent)
}

func (s *Store) ReadTeamBindingIntent(teamID, actionID string, target any) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("action", actionID); err != nil {
		return err
	}
	return readTeamContractJSON(s.TeamBindingIntentPath(teamID, actionID), target)
}

func (s *Store) WriteTeamActionIntent(teamID, actionID string, intent any) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("action", actionID); err != nil {
		return err
	}
	return s.writeJSON(s.TeamActionIntentPath(teamID, actionID), intent)
}

func (s *Store) ReadTeamActionIntent(teamID, actionID string, target any) error {
	if err := validateID("team", teamID); err != nil {
		return err
	}
	if err := validateID("action", actionID); err != nil {
		return err
	}
	return readTeamContractJSON(s.TeamActionIntentPath(teamID, actionID), target)
}

func (s *Store) ListTeamActionIntents(teamID string) ([]json.RawMessage, error) {
	return s.listTeamIntentDirectory(teamID, "action-intents")
}

func (s *Store) ListTeamHandoffIntents(teamID string) ([]json.RawMessage, error) {
	return s.listTeamIntentDirectory(teamID, "handoff-intents")
}

func (s *Store) ListTeamBindingIntents(teamID string) ([]json.RawMessage, error) {
	return s.listTeamIntentDirectory(teamID, "handoff-binding-intents")
}

func (s *Store) TeamMutationIntentCount(teamID string) (int, error) {
	total := 0
	for _, directory := range []string{"action-intents", "handoff-intents", "handoff-binding-intents"} {
		values, err := s.listTeamIntentDirectory(teamID, directory)
		if err != nil {
			return 0, err
		}
		total += len(values)
	}
	return total, nil
}

func (s *Store) listTeamIntentDirectory(teamID, directory string) ([]json.RawMessage, error) {
	if err := validateID("team", teamID); err != nil {
		return nil, err
	}
	path := filepath.Join(s.TeamDir(teamID), directory)
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return []json.RawMessage{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) > teamcontract.MaxMutationReceipts {
		return nil, fmt.Errorf("team action intent quota exceeded")
	}
	values := make([]json.RawMessage, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var raw json.RawMessage
		if err := readJSON(filepath.Join(path, entry.Name()), &raw); err != nil {
			return nil, err
		}
		values = append(values, raw)
	}
	return values, nil
}

func (s *Store) ListTeamHandoffIDs(teamID string) ([]string, error) {
	if err := validateID("team", teamID); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(s.TeamDir(teamID), "handoffs"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if safeID.MatchString(id) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Store) ListTeamIDs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "teams"))
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && safeID.MatchString(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Store) appendTeamRecord(path, operation string, sequence uint64, parse func([]byte) (uint64, error), value any) error {
	return s.appendTeamRecordWithQuota(path, operation, sequence, 0, 0, parse, value)
}

func (s *Store) appendTeamRecordWithQuota(path, operation string, sequence uint64, maxRecords, maxBytes int, parse func([]byte) (uint64, error), value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := s.injectFault(operation, path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return withLeaseGuard(path, func() error {
		last, count, bytesUsed, err := scanTeamLog(path, parse)
		if err != nil {
			return err
		}
		if sequence != last+1 {
			return fmt.Errorf("team record sequence %d does not follow %d", sequence, last)
		}
		if maxRecords > 0 && count >= maxRecords {
			return fmt.Errorf("team record quota exceeded: %d records", maxRecords)
		}
		if maxBytes > 0 && bytesUsed+len(data)+1 > maxBytes {
			return fmt.Errorf("team record byte quota exceeded: %d bytes", maxBytes)
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer file.Close()
		if _, err := file.Write(append(data, '\n')); err != nil {
			return err
		}
		return file.Sync()
	})
}

func scanTeamLog(path string, parse func([]byte) (uint64, error)) (uint64, int, int, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, 0, nil
	}
	if err != nil {
		return 0, 0, 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), teamcontract.MaxHandoffBytes)
	var last uint64
	count, bytesUsed := 0, 0
	for scanner.Scan() {
		raw := append([]byte(nil), scanner.Bytes()...)
		sequence, err := parse(raw)
		if err != nil {
			return 0, 0, 0, err
		}
		if sequence != last+1 {
			return 0, 0, 0, fmt.Errorf("team log sequence is not strictly increasing")
		}
		last, count, bytesUsed = sequence, count+1, bytesUsed+len(raw)+1
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, 0, err
	}
	return last, count, bytesUsed, nil
}

func readTeamLog(path string, visit func([]byte, uint64) error) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), teamcontract.MaxHandoffBytes)
	var expected uint64 = 1
	for scanner.Scan() {
		if err := visit(append([]byte(nil), scanner.Bytes()...), expected); err != nil {
			return err
		}
		expected++
	}
	return scanner.Err()
}

func readTeamContractJSON(path string, target any) error {
	return readTeamContractJSONLimit(path, target, teamcontract.MaxHandoffBytes)
}

func readTeamContractJSONLimit(path string, target any, limit int) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open Team contract %q: %w", path, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return fmt.Errorf("read Team contract %q: %w", path, err)
	}
	if len(data) > limit {
		return fmt.Errorf("Team contract %q exceeds %d bytes", path, limit)
	}
	if err := teamcontract.DecodeStrict(data, target); err != nil {
		return fmt.Errorf("decode Team contract %q: %w", path, err)
	}
	return nil
}
