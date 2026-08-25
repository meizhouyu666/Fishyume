package team

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"wf.local/wf-engine/internal/teamcontract"
)

type handoffCreateIntentV1 struct {
	SchemaVersion string                                `json:"schemaVersion"`
	RequestHash   string                                `json:"requestHash"`
	Request       teamcontract.HandoffCreateRequestV1   `json:"request"`
	Artifact      teamcontract.HandoffArtifactV1        `json:"artifact"`
	Response      *teamcontract.HandoffCreateResponseV1 `json:"response,omitempty"`
}

type handoffBindingIntentV1 struct {
	SchemaVersion string                                 `json:"schemaVersion"`
	RequestHash   string                                 `json:"requestHash"`
	Request       teamcontract.HandoffBindRunRequestV1   `json:"request"`
	Binding       teamcontract.HandoffBindingV1          `json:"binding"`
	Response      *teamcontract.HandoffBindRunResponseV1 `json:"response,omitempty"`
}

type handoffHashInputV1 struct {
	SchemaVersion          string   `json:"schemaVersion"`
	HandoffID              string   `json:"handoffId"`
	TeamID                 string   `json:"teamId"`
	SourceTeamVersion      uint64   `json:"sourceTeamVersion"`
	Goal                   string   `json:"goal"`
	Decisions              []string `json:"decisions,omitempty"`
	Constraints            []string `json:"constraints,omitempty"`
	OpenQuestions          []string `json:"openQuestions,omitempty"`
	AcceptanceExpectations []string `json:"acceptanceExpectations,omitempty"`
	SelectedMessageIDs     []string `json:"selectedMessageIds"`
	SourceMessageHashes    []string `json:"sourceMessageHashes"`
}

func (s *Service) HandoffCreate(request teamcontract.HandoffCreateRequestV1) (teamcontract.HandoffCreateResponseV1, error) {
	if err := teamcontract.ValidateHandoffCreateRequest(request); err != nil {
		return teamcontract.HandoffCreateResponseV1{}, err
	}
	requestHash, _, err := teamcontract.CanonicalHash(request)
	if err != nil {
		return teamcontract.HandoffCreateResponseV1{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var intent handoffCreateIntentV1
	readErr := s.state.ReadTeamHandoffIntent(request.TeamID, request.HandoffID, &intent)
	existingIntent := readErr == nil
	if existingIntent {
		if intent.SchemaVersion != teamcontract.SchemaVersion || intent.RequestHash != requestHash {
			return teamcontract.HandoffCreateResponseV1{}, ErrConflict
		}
		if intent.Response != nil {
			response := *intent.Response
			response.Replayed = true
			return response, nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return teamcontract.HandoffCreateResponseV1{}, readErr
	}

	if !existingIntent {
		snapshot, err := s.Get(request.TeamID)
		if err != nil {
			return teamcontract.HandoffCreateResponseV1{}, err
		}
		if snapshot.StateVersion != request.ExpectedStateVersion {
			return teamcontract.HandoffCreateResponseV1{}, ErrConflict
		}
		var existing teamcontract.HandoffArtifactV1
		if err := s.state.ReadTeamHandoff(request.TeamID, request.HandoffID, &existing); err == nil {
			return teamcontract.HandoffCreateResponseV1{}, ErrConflict
		} else if !errors.Is(err, os.ErrNotExist) {
			return teamcontract.HandoffCreateResponseV1{}, err
		}
		hashes, err := s.selectedMessageHashesLocked(request.TeamID, request.SelectedMessageIDs)
		if err != nil {
			return teamcontract.HandoffCreateResponseV1{}, err
		}
		artifact := teamcontract.HandoffArtifactV1{
			SchemaVersion: teamcontract.SchemaVersion, HandoffID: request.HandoffID, TeamID: request.TeamID,
			SourceTeamVersion: snapshot.StateVersion, Goal: request.Goal,
			Decisions: append([]string(nil), request.Decisions...), Constraints: append([]string(nil), request.Constraints...),
			OpenQuestions: append([]string(nil), request.OpenQuestions...), AcceptanceExpectations: append([]string(nil), request.AcceptanceExpectations...),
			SelectedMessageIDs: append([]string(nil), request.SelectedMessageIDs...), SourceMessageHashes: hashes, CreatedAt: s.now().UTC(),
		}
		artifact.ContentHash, err = handoffContentHash(artifact)
		if err != nil {
			return teamcontract.HandoffCreateResponseV1{}, err
		}
		if err := teamcontract.ValidateHandoff(artifact); err != nil {
			return teamcontract.HandoffCreateResponseV1{}, err
		}
		count, err := s.state.TeamMutationIntentCount(request.TeamID)
		if err != nil {
			return teamcontract.HandoffCreateResponseV1{}, err
		}
		if count >= teamcontract.MaxMutationReceipts {
			return teamcontract.HandoffCreateResponseV1{}, ErrQuotaExceeded
		}
		intent = handoffCreateIntentV1{SchemaVersion: teamcontract.SchemaVersion, RequestHash: requestHash, Request: request, Artifact: artifact}
		if err := s.state.WriteTeamHandoffIntent(request.TeamID, request.HandoffID, intent); err != nil {
			return teamcontract.HandoffCreateResponseV1{}, err
		}
	}

	if err := s.validateHandoffSourcesLocked(intent.Artifact); err != nil {
		return teamcontract.HandoffCreateResponseV1{}, err
	}
	if err := s.state.WriteTeamHandoff(intent.Artifact); err != nil {
		return teamcontract.HandoffCreateResponseV1{}, err
	}
	if err := s.appendHandoffEventLocked(intent.Artifact.TeamID, intent.Artifact.SourceTeamVersion, teamcontract.EventHandoffCreated, intent.Artifact.HandoffID, ""); err != nil {
		return teamcontract.HandoffCreateResponseV1{}, err
	}
	response := teamcontract.HandoffCreateResponseV1{SchemaVersion: teamcontract.SchemaVersion, Handoff: intent.Artifact, Replayed: existingIntent}
	storedResponse := response
	storedResponse.Replayed = false
	intent.Response = &storedResponse
	if err := s.state.WriteTeamHandoffIntent(request.TeamID, request.HandoffID, intent); err != nil {
		return teamcontract.HandoffCreateResponseV1{}, err
	}
	return response, nil
}

func (s *Service) HandoffGet(request teamcontract.HandoffGetRequestV1) (teamcontract.HandoffGetResponseV1, error) {
	if err := teamcontract.ValidateHandoffGetRequest(request); err != nil {
		return teamcontract.HandoffGetResponseV1{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.Get(request.TeamID); err != nil {
		return teamcontract.HandoffGetResponseV1{}, err
	}
	var artifact teamcontract.HandoffArtifactV1
	if err := s.state.ReadTeamHandoff(request.TeamID, request.HandoffID, &artifact); err != nil {
		return teamcontract.HandoffGetResponseV1{}, err
	}
	if err := validateHandoffContentHash(artifact); err != nil {
		return teamcontract.HandoffGetResponseV1{}, err
	}
	response := teamcontract.HandoffGetResponseV1{SchemaVersion: teamcontract.SchemaVersion, Handoff: artifact}
	var binding teamcontract.HandoffBindingV1
	if err := s.state.ReadTeamBinding(request.TeamID, request.HandoffID, &binding); err == nil {
		response.Binding = &binding
	} else if !errors.Is(err, os.ErrNotExist) {
		return teamcontract.HandoffGetResponseV1{}, err
	}
	return response, nil
}

func (s *Service) HandoffList(request teamcontract.HandoffListRequestV1) (teamcontract.HandoffListResponseV1, error) {
	if err := teamcontract.ValidateHandoffListRequest(request); err != nil {
		return teamcontract.HandoffListResponseV1{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.Get(request.TeamID); err != nil {
		return teamcontract.HandoffListResponseV1{}, err
	}
	ids, err := s.state.ListTeamHandoffIDs(request.TeamID)
	if err != nil {
		return teamcontract.HandoffListResponseV1{}, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = teamcontract.DefaultListLimit
	}
	items := make([]teamcontract.HandoffArtifactV1, 0, limit)
	next := ""
	for _, id := range ids {
		if request.Cursor != "" && id <= request.Cursor {
			continue
		}
		if len(items) == limit {
			next = items[len(items)-1].HandoffID
			break
		}
		var artifact teamcontract.HandoffArtifactV1
		if err := s.state.ReadTeamHandoff(request.TeamID, id, &artifact); err != nil {
			return teamcontract.HandoffListResponseV1{}, err
		}
		if err := validateHandoffContentHash(artifact); err != nil {
			return teamcontract.HandoffListResponseV1{}, err
		}
		items = append(items, artifact)
	}
	return teamcontract.HandoffListResponseV1{SchemaVersion: teamcontract.SchemaVersion, Items: items, NextCursor: next}, nil
}

func (s *Service) HandoffBindRun(request teamcontract.HandoffBindRunRequestV1) (teamcontract.HandoffBindRunResponseV1, error) {
	if err := teamcontract.ValidateHandoffBindRunRequest(request); err != nil {
		return teamcontract.HandoffBindRunResponseV1{}, err
	}
	requestHash, _, err := teamcontract.CanonicalHash(request)
	if err != nil {
		return teamcontract.HandoffBindRunResponseV1{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var intent handoffBindingIntentV1
	readErr := s.state.ReadTeamBindingIntent(request.TeamID, request.ActionID, &intent)
	existingIntent := readErr == nil
	if existingIntent {
		if intent.SchemaVersion != teamcontract.SchemaVersion || intent.RequestHash != requestHash {
			return teamcontract.HandoffBindRunResponseV1{}, ErrConflict
		}
		if intent.Response != nil {
			response := *intent.Response
			response.Replayed = true
			return response, nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return teamcontract.HandoffBindRunResponseV1{}, readErr
	}

	if !existingIntent {
		snapshot, err := s.Get(request.TeamID)
		if err != nil {
			return teamcontract.HandoffBindRunResponseV1{}, err
		}
		if snapshot.StateVersion != request.ExpectedStateVersion {
			return teamcontract.HandoffBindRunResponseV1{}, ErrConflict
		}
		var artifact teamcontract.HandoffArtifactV1
		if err := s.state.ReadTeamHandoff(request.TeamID, request.HandoffID, &artifact); err != nil {
			return teamcontract.HandoffBindRunResponseV1{}, err
		}
		if err := validateHandoffContentHash(artifact); err != nil {
			return teamcontract.HandoffBindRunResponseV1{}, err
		}
		if s.runLookup == nil {
			return teamcontract.HandoffBindRunResponseV1{}, ErrCapabilityUnavailable
		}
		runProject, err := s.runLookup(request.RunID)
		if err != nil {
			return teamcontract.HandoffBindRunResponseV1{}, err
		}
		if !sameProject(snapshot.Project, runProject) {
			return teamcontract.HandoffBindRunResponseV1{}, ErrConflict
		}
		var existing teamcontract.HandoffBindingV1
		bindingAlreadyExists := false
		if err := s.state.ReadTeamBinding(request.TeamID, request.HandoffID, &existing); err == nil {
			if existing.RunID != request.RunID || !sameProject(existing.Project, snapshot.Project) {
				return teamcontract.HandoffBindRunResponseV1{}, ErrConflict
			}
			bindingAlreadyExists = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return teamcontract.HandoffBindRunResponseV1{}, err
		}
		count, err := s.state.TeamMutationIntentCount(request.TeamID)
		if err != nil {
			return teamcontract.HandoffBindRunResponseV1{}, err
		}
		if count >= teamcontract.MaxMutationReceipts {
			return teamcontract.HandoffBindRunResponseV1{}, ErrQuotaExceeded
		}
		binding := existing
		if !bindingAlreadyExists {
			binding = teamcontract.HandoffBindingV1{TeamID: request.TeamID, HandoffID: request.HandoffID, RunID: request.RunID, Project: snapshot.Project, BoundAt: s.now().UTC()}
		}
		intent = handoffBindingIntentV1{SchemaVersion: teamcontract.SchemaVersion, RequestHash: requestHash, Request: request, Binding: binding}
		if err := s.state.WriteTeamBindingIntent(request.TeamID, request.ActionID, intent); err != nil {
			return teamcontract.HandoffBindRunResponseV1{}, err
		}
	}

	if err := s.state.WriteTeamBinding(intent.Binding); err != nil {
		if strings.Contains(err.Error(), "different binding") {
			return teamcontract.HandoffBindRunResponseV1{}, ErrConflict
		}
		return teamcontract.HandoffBindRunResponseV1{}, err
	}
	if err := s.appendHandoffEventLocked(request.TeamID, request.ExpectedStateVersion, teamcontract.EventHandoffBound, request.HandoffID, request.RunID); err != nil {
		return teamcontract.HandoffBindRunResponseV1{}, err
	}
	response := teamcontract.HandoffBindRunResponseV1{SchemaVersion: teamcontract.SchemaVersion, Binding: intent.Binding, Replayed: existingIntent}
	storedResponse := response
	storedResponse.Replayed = false
	intent.Response = &storedResponse
	if err := s.state.WriteTeamBindingIntent(request.TeamID, request.ActionID, intent); err != nil {
		return teamcontract.HandoffBindRunResponseV1{}, err
	}
	return response, nil
}

func (s *Service) selectedMessageHashesLocked(teamID string, selected []string) ([]string, error) {
	messages, err := s.state.ReadTeamMessages(teamID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]teamcontract.TeamMessageV1, len(messages))
	for _, message := range messages {
		byID[message.MessageID] = message
	}
	hashes := make([]string, 0, len(selected))
	for _, id := range selected {
		message, exists := byID[id]
		if !exists {
			return nil, os.ErrNotExist
		}
		actual := sha256.Sum256([]byte(message.Content))
		if message.ContentHash != hex.EncodeToString(actual[:]) {
			return nil, fmt.Errorf("%w: source message %q content hash does not match retained content", ErrConflict, id)
		}
		hashes = append(hashes, message.ContentHash)
	}
	return hashes, nil
}

func (s *Service) validateHandoffSourcesLocked(artifact teamcontract.HandoffArtifactV1) error {
	if err := validateHandoffContentHash(artifact); err != nil {
		return err
	}
	hashes, err := s.selectedMessageHashesLocked(artifact.TeamID, artifact.SelectedMessageIDs)
	if err != nil {
		return err
	}
	for index := range hashes {
		if hashes[index] != artifact.SourceMessageHashes[index] {
			return fmt.Errorf("%w: source message hash changed", ErrConflict)
		}
	}
	return nil
}

func handoffContentHash(artifact teamcontract.HandoffArtifactV1) (string, error) {
	value := handoffHashInputV1{
		SchemaVersion: artifact.SchemaVersion, HandoffID: artifact.HandoffID, TeamID: artifact.TeamID,
		SourceTeamVersion: artifact.SourceTeamVersion, Goal: artifact.Goal,
		Decisions: artifact.Decisions, Constraints: artifact.Constraints, OpenQuestions: artifact.OpenQuestions,
		AcceptanceExpectations: artifact.AcceptanceExpectations, SelectedMessageIDs: artifact.SelectedMessageIDs,
		SourceMessageHashes: artifact.SourceMessageHashes,
	}
	hash, _, err := teamcontract.CanonicalHash(value)
	return hash, err
}

func validateHandoffContentHash(artifact teamcontract.HandoffArtifactV1) error {
	want, err := handoffContentHash(artifact)
	if err != nil {
		return err
	}
	if artifact.ContentHash != want {
		return fmt.Errorf("%w: Handoff content hash is invalid", ErrConflict)
	}
	return nil
}

func (s *Service) appendHandoffEventLocked(teamID string, stateVersion uint64, eventType teamcontract.EventType, handoffID, runID string) error {
	events, err := s.state.ReadTeamEvents(teamID)
	if err != nil {
		return err
	}
	summary := "handoff " + handoffID + " created"
	if eventType == teamcontract.EventHandoffBound {
		summary = "handoff " + handoffID + " bound to Run " + runID
	}
	for _, event := range events {
		if event.Type == eventType && event.Summary == summary {
			return nil
		}
	}
	return s.state.AppendTeamEvent(teamcontract.TeamEventV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: teamID, Sequence: uint64(len(events) + 1), Type: eventType, StateVersion: stateVersion, Summary: summary, CreatedAt: s.now().UTC()})
}

func sameProject(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
