// Package team owns the Team exploration aggregate without importing the
// Workflow Run service. This increment stops at durable preparation: no
// external Agent launch is implied by Start.
package team

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/teamcontract"
)

var (
	ErrConflict              = errors.New("team request conflicts with an existing request")
	ErrCapabilityUnavailable = errors.New("team capability is unavailable")
	ErrQuotaExceeded         = errors.New("team quota exceeded")
)

type StartResult struct {
	Team     teamcontract.TeamSessionV1
	Replayed bool
}

type Service struct {
	state *store.Store
	now   func() time.Time
	mu    sync.Mutex
}

func NewService(state *store.Store) *Service {
	return &Service{state: state, now: time.Now}
}

func (s *Service) Start(ctx context.Context, request teamcontract.TeamStartRequestV1) (StartResult, error) {
	if s == nil || s.state == nil {
		return StartResult{}, fmt.Errorf("team state store is unavailable")
	}
	if err := teamcontract.ValidateStartRequest(request); err != nil {
		return StartResult{}, err
	}
	if request.Mode != teamcontract.ModePanel {
		return StartResult{}, ErrCapabilityUnavailable
	}
	project, err := canonicalProject(request.Project)
	if err != nil {
		return StartResult{}, err
	}
	normalized, participants, catalogHash, err := normalizeStart(request, project)
	if err != nil {
		return StartResult{}, err
	}
	requestHash, _, err := teamcontract.CanonicalHash(normalized)
	if err != nil {
		return StartResult{}, fmt.Errorf("hash normalized team request: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return StartResult{}, err
	}
	ids, err := s.state.ListTeamIDs()
	if err != nil {
		return StartResult{}, err
	}
	for _, teamID := range ids {
		var existing teamcontract.TeamSessionV1
		if err := s.state.ReadTeamSnapshot(teamID, &existing); err != nil {
			return StartResult{}, fmt.Errorf("inspect existing Team %q: %w", teamID, err)
		}
		if existing.ClientRequestID != request.ClientRequestID {
			continue
		}
		if existing.RequestHash != requestHash {
			return StartResult{}, ErrConflict
		}
		return StartResult{Team: existing, Replayed: true}, nil
	}
	teamID, err := newID("team")
	if err != nil {
		return StartResult{}, err
	}
	now := s.now().UTC()
	teamSnapshot := teamcontract.TeamSessionV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: teamID, ClientRequestID: request.ClientRequestID, RequestHash: requestHash, Project: project, Mode: request.Mode, Topic: request.Topic, Instructions: request.Instructions, CatalogHash: catalogHash, Participants: participants, State: teamcontract.LifecycleCreated, StateVersion: 1, CostGrant: normalized.CostGrant, CostUsed: participantCost(participants, catalogHash), CreatedAt: now, UpdatedAt: now}
	if teamSnapshot.CostUsed > teamSnapshot.CostGrant {
		return StartResult{}, ErrQuotaExceeded
	}
	if err := s.state.InitTeam(teamID); err != nil {
		return StartResult{}, err
	}
	if err := s.state.EnsureTeamSnapshot(teamSnapshot); err != nil {
		return StartResult{}, err
	}
	for _, participant := range participants {
		if err := s.state.WriteTeamParticipant(participant, teamID); err != nil {
			return StartResult{}, err
		}
	}
	events, err := s.state.ReadTeamEvents(teamID)
	if err != nil {
		return StartResult{}, err
	}
	if len(events) == 0 {
		event := teamcontract.TeamEventV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: teamID, Sequence: 1, Type: teamcontract.EventTeamCreated, StateVersion: teamSnapshot.StateVersion, Summary: "team prepared", CreatedAt: now}
		if err := s.state.AppendTeamEvent(event); err != nil {
			return StartResult{}, err
		}
	} else if events[0].Type != teamcontract.EventTeamCreated {
		return StartResult{}, fmt.Errorf("Team %q has an invalid creation journal", teamID)
	}
	return StartResult{Team: teamSnapshot}, nil
}

func (s *Service) Get(teamID string) (teamcontract.TeamSessionV1, error) {
	if s == nil || s.state == nil {
		return teamcontract.TeamSessionV1{}, fmt.Errorf("team state store is unavailable")
	}
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(teamID, &snapshot); err != nil {
		return teamcontract.TeamSessionV1{}, err
	}
	return snapshot, nil
}

func normalizeStart(request teamcontract.TeamStartRequestV1, project string) (teamcontract.TeamStartRequestV1, []teamcontract.ParticipantV1, string, error) {
	catalog := routing.BuiltinCatalogV1()
	if err := routing.ValidateCatalog(catalog); err != nil {
		return teamcontract.TeamStartRequestV1{}, nil, "", err
	}
	catalogHash, err := routing.CatalogHash(catalog)
	if err != nil {
		return teamcontract.TeamStartRequestV1{}, nil, "", err
	}
	normalized := request
	normalized.Project = project
	normalized.CostGrant = request.CostGrant
	if normalized.CostGrant == 0 {
		normalized.CostGrant = teamcontract.DefaultCostGrant
	}
	specs := append([]teamcontract.ParticipantSpecV1(nil), request.Participants...)
	if len(specs) == 0 {
		specs = []teamcontract.ParticipantSpecV1{
			{Label: "architect", Role: "propose a coherent architecture and tradeoffs", ModelID: catalog.Models[0].ID},
			{Label: "reviewer", Role: "challenge assumptions and identify failure modes", ModelID: catalog.Models[1].ID},
		}
	}
	participants := make([]teamcontract.ParticipantV1, 0, len(specs))
	seenModels := make(map[string]struct{}, len(specs))
	for index, spec := range specs {
		var model routing.ModelCapabilityV1
		found := false
		for _, candidate := range catalog.Models {
			if candidate.ID == spec.ModelID {
				model, found = candidate, true
				break
			}
		}
		if !found {
			return teamcontract.TeamStartRequestV1{}, nil, "", fmt.Errorf("participant model %q is absent from trusted catalog", spec.ModelID)
		}
		if _, exists := seenModels[model.ID]; exists {
			return teamcontract.TeamStartRequestV1{}, nil, "", fmt.Errorf("participant model %q is duplicated", model.ID)
		}
		seenModels[model.ID] = struct{}{}
		participantID := fmt.Sprintf("participant-%d", index+1)
		participants = append(participants, teamcontract.ParticipantV1{ParticipantID: participantID, Label: spec.Label, Role: spec.Role, ModelID: model.ID, Driver: model.Target.Driver, Target: model.Target.Provider, State: teamcontract.ParticipantPending})
		specs[index] = spec
	}
	normalized.Participants = specs
	return normalized, participants, catalogHash, nil
}

func participantCost(participants []teamcontract.ParticipantV1, catalogHash string) int {
	catalog := routing.BuiltinCatalogV1()
	if hash, err := routing.CatalogHash(catalog); err != nil || hash != catalogHash {
		return teamcontract.MaxCostGrant + 1
	}
	total := 0
	for _, participant := range participants {
		for _, model := range catalog.Models {
			if model.ID == participant.ModelID {
				cost, err := routing.CostUnitsForTarget(catalog, model.Target)
				if err != nil {
					return teamcontract.MaxCostGrant + 1
				}
				total += cost
			}
		}
	}
	return total
}

func canonicalProject(value string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(value))
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve project path: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect project path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("project path is not a directory")
	}
	if len([]byte(canonical)) > teamcontract.MaxProjectBytes {
		return "", fmt.Errorf("project path exceeds %d bytes", teamcontract.MaxProjectBytes)
	}
	return filepath.Clean(canonical), nil
}

func newID(prefix string) (string, error) {
	bytesValue := make([]byte, 12)
	if _, err := rand.Read(bytesValue); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(bytesValue), nil
}
