package team

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"wf.local/wf-engine/internal/explorationdriver"
	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/teamcontract"
)

type actionIntentV1 struct {
	SchemaVersion string                             `json:"schemaVersion"`
	RequestHash   string                             `json:"requestHash"`
	Action        teamcontract.TeamActionV1          `json:"action"`
	Response      *teamcontract.TeamActionResponseV1 `json:"response,omitempty"`
}

func (s *Service) Capabilities() (teamcontract.TeamCapabilitiesV1, error) {
	s.mu.Lock()
	catalog := routing.CanonicalCatalogV1(s.catalog)
	s.mu.Unlock()
	hash, err := routing.CatalogHash(catalog)
	if err != nil {
		return teamcontract.TeamCapabilitiesV1{}, err
	}
	presets := []struct{ label, role string }{
		{"architect", "propose a coherent architecture and tradeoffs"},
		{"reviewer", "challenge assumptions and identify failure modes"},
		{"researcher", "investigate evidence and alternative approaches"},
		{"verifier", "check claims, constraints, and unresolved risks"},
	}
	count := len(catalog.Models)
	if count > teamcontract.MaxParticipantTemplates {
		count = teamcontract.MaxParticipantTemplates
	}
	templates := make([]teamcontract.ParticipantTemplateV1, 0, count)
	for index, model := range catalog.Models[:count] {
		templates = append(templates, teamcontract.ParticipantTemplateV1{Label: presets[index].label, Role: presets[index].role, ModelID: model.ID, Driver: model.Target.Driver, Target: model.Target.Provider})
	}
	harnessModels := make(map[string][]teamcontract.HarnessModelOptionV1)
	for _, model := range catalog.Models {
		driver := model.Target.Driver
		harnessModels[driver] = append(harnessModels[driver], teamcontract.HarnessModelOptionV1{
			ModelID: model.ID, Provider: model.Target.Provider, Model: model.Target.Model,
		})
	}
	drivers := make([]string, 0, len(harnessModels))
	for driver := range harnessModels {
		drivers = append(drivers, driver)
	}
	sort.Strings(drivers)
	harnesses := make([]teamcontract.HarnessCapabilityV1, 0, len(drivers))
	for _, driver := range drivers {
		models := harnessModels[driver]
		sort.SliceStable(models, func(i, j int) bool { return models[i].ModelID < models[j].ModelID })
		harnesses = append(harnesses, teamcontract.HarnessCapabilityV1{Driver: driver, Models: models})
	}
	features := teamcontract.TeamFeatureFlagsV1{Panel: true, Handoff: true, Cancel: true}
	value := teamcontract.TeamCapabilitiesV1{SchemaVersion: teamcontract.SchemaVersion, Features: features, Limits: teamcontract.DefaultLimits(), ParticipantTemplates: templates, Harnesses: harnesses, CatalogHash: hash}
	return value, teamcontract.ValidateCapabilities(value)
}

// Begin creates or replays a Team and asynchronously dispatches its durable
// initial turns. Callers observe completion through get/events/messages.
func (s *Service) Begin(ctx context.Context, request teamcontract.TeamStartRequestV1) (StartResult, error) {
	result, err := s.Start(ctx, request)
	if err != nil {
		return result, err
	}
	if result.Team.State != teamcontract.LifecycleClosed {
		s.dispatchAsync(result.Team.TeamID)
	}
	return result, nil
}

func (s *Service) Recover(ctx context.Context) error {
	ids, err := s.state.ListTeamIDs()
	if err != nil {
		return err
	}
	for _, teamID := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		snapshot, err := s.Get(teamID)
		if err != nil {
			return fmt.Errorf("recover Team %q: %w", teamID, err)
		}
		handoffIntents, readErr := s.state.ListTeamHandoffIntents(teamID)
		if readErr != nil {
			return readErr
		}
		for _, raw := range handoffIntents {
			var intent handoffCreateIntentV1
			if err := teamcontract.DecodeStrict(raw, &intent); err != nil {
				return fmt.Errorf("recover Handoff creation: %w", err)
			}
			if intent.Response == nil {
				if _, err := s.HandoffCreate(intent.Request); err != nil {
					return fmt.Errorf("recover Handoff %q: %w", intent.Request.HandoffID, err)
				}
			}
		}
		bindingIntents, readErr := s.state.ListTeamBindingIntents(teamID)
		if readErr != nil {
			return readErr
		}
		for _, raw := range bindingIntents {
			var intent handoffBindingIntentV1
			if err := teamcontract.DecodeStrict(raw, &intent); err != nil {
				return fmt.Errorf("recover Handoff binding: %w", err)
			}
			if intent.Response == nil {
				if _, err := s.HandoffBindRun(intent.Request); err != nil {
					return fmt.Errorf("recover Handoff binding %q: %w", intent.Request.ActionID, err)
				}
			}
		}
		intents, readErr := s.state.ListTeamActionIntents(teamID)
		if readErr != nil {
			return readErr
		}
		recoveringAction := false
		for _, raw := range intents {
			var intent actionIntentV1
			if err := teamcontract.DecodeStrict(raw, &intent); err != nil {
				return fmt.Errorf("recover Team action: %w", err)
			}
			if intent.Response == nil && intent.Action.Type == teamcontract.ActionCancel {
				recoveringAction = true
				s.actionAsync(intent.Action)
			}
		}
		if recoveringAction {
			continue
		}
		if snapshot.State == teamcontract.LifecycleClosed {
			continue
		}
		switch snapshot.State {
		case teamcontract.LifecycleCreated, teamcontract.LifecycleRunning, teamcontract.LifecycleClosing:
			s.dispatchAsync(teamID)
		}
	}
	return nil
}

func (s *Service) dispatchAsync(teamID string) {
	s.mu.Lock()
	if _, exists := s.activeControllers[teamID]; exists {
		s.mu.Unlock()
		return
	}
	s.activeControllers[teamID] = struct{}{}
	s.controllerWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.controllerWG.Done()
		defer func() {
			s.mu.Lock()
			delete(s.activeControllers, teamID)
			s.mu.Unlock()
		}()
		_, _ = s.DispatchInitial(context.Background(), teamID)
	}()
}

func (s *Service) actionAsync(action teamcontract.TeamActionV1) {
	s.mu.Lock()
	s.controllerWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.controllerWG.Done()
		_, _ = s.Action(context.Background(), action)
	}()
}

func (s *Service) Wait(ctx context.Context) error {
	s.mu.Lock()
	done := make(chan struct{})
	go func() { s.controllerWG.Wait(); close(done) }()
	s.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) List(request teamcontract.TeamListRequestV1) (teamcontract.TeamListResponseV1, error) {
	if err := teamcontract.ValidateListRequest(request); err != nil {
		return teamcontract.TeamListResponseV1{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.state.ListTeamIDs()
	if err != nil {
		return teamcontract.TeamListResponseV1{}, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = teamcontract.DefaultListLimit
	}
	items := make([]teamcontract.TeamSummaryV1, 0, limit)
	next := ""
	for _, id := range ids {
		if request.Cursor != "" && id <= request.Cursor {
			continue
		}
		snapshot, err := s.Get(id)
		if err != nil {
			return teamcontract.TeamListResponseV1{}, err
		}
		if request.Project != "" && snapshot.Project != request.Project {
			continue
		}
		if request.State != "" && snapshot.State != request.State {
			continue
		}
		if len(items) == limit {
			next = items[len(items)-1].TeamID
			break
		}
		items = append(items, teamcontract.TeamSummaryV1{TeamID: snapshot.TeamID, Project: snapshot.Project, Topic: snapshot.Topic, State: snapshot.State, StateVersion: snapshot.StateVersion, CloseReason: snapshot.CloseReason, Participants: len(snapshot.Participants), CostGrant: snapshot.CostGrant, CostUsed: snapshot.CostUsed, CreatedAt: snapshot.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: snapshot.UpdatedAt.Format(time.RFC3339Nano)})
	}
	return teamcontract.TeamListResponseV1{SchemaVersion: teamcontract.SchemaVersion, Items: items, NextCursor: next}, nil
}

func (s *Service) GetView(request teamcontract.TeamGetRequestV1) (teamcontract.TeamGetResponseV1, error) {
	if err := teamcontract.ValidateGetRequest(request); err != nil {
		return teamcontract.TeamGetResponseV1{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err := s.Get(request.TeamID)
	if err != nil {
		return teamcontract.TeamGetResponseV1{}, err
	}
	ids, err := s.state.ListTeamTurnIDs(request.TeamID)
	if err != nil {
		return teamcontract.TeamGetResponseV1{}, err
	}
	turns := make([]teamcontract.ParticipantTurnV1, 0, len(ids))
	for _, id := range ids {
		var turn teamcontract.ParticipantTurnV1
		if err := s.state.ReadTeamTurn(request.TeamID, id, &turn); err != nil {
			return teamcontract.TeamGetResponseV1{}, err
		}
		turns = append(turns, turn)
	}
	return teamcontract.TeamGetResponseV1{SchemaVersion: teamcontract.SchemaVersion, Team: snapshot, Turns: turns}, nil
}

func (s *Service) Events(ctx context.Context, request teamcontract.TeamEventsRequestV1) (teamcontract.TeamEventsResponseV1, error) {
	if err := teamcontract.ValidateEventsRequest(request); err != nil {
		return teamcontract.TeamEventsResponseV1{}, err
	}
	if _, err := s.Get(request.TeamID); err != nil {
		return teamcontract.TeamEventsResponseV1{}, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = teamcontract.DefaultListLimit
	}
	deadline := time.Now().Add(time.Duration(request.WaitMS) * time.Millisecond)
	for {
		values, err := s.state.ReadTeamEvents(request.TeamID)
		if err != nil {
			return teamcontract.TeamEventsResponseV1{}, err
		}
		page, next, more := pageEvents(values, request.AfterSequence, limit)
		if len(page) > 0 || request.WaitMS == 0 || !time.Now().Before(deadline) {
			return teamcontract.TeamEventsResponseV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: request.TeamID, Events: page, NextAfterSequence: next, More: more}, nil
		}
		select {
		case <-ctx.Done():
			return teamcontract.TeamEventsResponseV1{}, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (s *Service) Messages(request teamcontract.TeamMessagesRequestV1) (teamcontract.TeamMessagesResponseV1, error) {
	if err := teamcontract.ValidateMessagesRequest(request); err != nil {
		return teamcontract.TeamMessagesResponseV1{}, err
	}
	if _, err := s.Get(request.TeamID); err != nil {
		return teamcontract.TeamMessagesResponseV1{}, err
	}
	limit := request.Limit
	if limit == 0 {
		limit = teamcontract.DefaultListLimit
	}
	values, err := s.state.ReadTeamMessages(request.TeamID)
	if err != nil {
		return teamcontract.TeamMessagesResponseV1{}, err
	}
	page, next, more := pageMessages(values, request.AfterSequence, limit)
	return teamcontract.TeamMessagesResponseV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: request.TeamID, Messages: page, NextAfterSequence: next, More: more}, nil
}

func (s *Service) Action(ctx context.Context, action teamcontract.TeamActionV1) (teamcontract.TeamActionResponseV1, error) {
	if err := teamcontract.ValidateActionRequest(action); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	if action.Type != teamcontract.ActionCancel {
		return teamcontract.TeamActionResponseV1{}, ErrCapabilityUnavailable
	}
	hash, _, err := teamcontract.CanonicalHash(action)
	if err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}

	s.mu.Lock()
	var existing actionIntentV1
	readErr := s.state.ReadTeamActionIntent(action.TeamID, action.ActionID, &existing)
	if readErr == nil {
		if existing.RequestHash != hash {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, ErrConflict
		}
		if existing.Response != nil {
			response := *existing.Response
			response.Replayed = true
			s.mu.Unlock()
			return response, nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, readErr
	}
	snapshot, err := s.Get(action.TeamID)
	if err != nil {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, err
	}
	if readErr != nil {
		if snapshot.StateVersion != action.ExpectedStateVersion {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, ErrConflict
		}
		if snapshot.State == teamcontract.LifecycleClosed && snapshot.CloseReason != teamcontract.CloseCancelled {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, ErrConflict
		}
		intentCount, err := s.state.TeamMutationIntentCount(action.TeamID)
		if err != nil {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, err
		}
		if intentCount >= teamcontract.MaxMutationReceipts {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, ErrQuotaExceeded
		}
		existing = actionIntentV1{SchemaVersion: teamcontract.SchemaVersion, RequestHash: hash, Action: action}
		if err := s.state.WriteTeamActionIntent(action.TeamID, action.ActionID, existing); err != nil {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	if snapshot.State == teamcontract.LifecycleClosed {
		if snapshot.CloseReason != teamcontract.CloseCancelled {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, ErrConflict
		}
		response := teamcontract.TeamActionResponseV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: action.ActionID, TeamID: action.TeamID, Type: action.Type, StateVersion: snapshot.StateVersion, State: snapshot.State}
		existing.Response = &response
		err := s.state.WriteTeamActionIntent(action.TeamID, action.ActionID, existing)
		s.mu.Unlock()
		return response, err
	}
	turnIDs, err := s.state.ListTeamTurnIDs(action.TeamID)
	if err != nil {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, err
	}
	type activeTurn struct {
		turn   teamcontract.ParticipantTurnV1
		handle explorationdriver.ExecutionHandle
		driver explorationdriver.Driver
	}
	active := make([]activeTurn, 0)
	for _, id := range turnIDs {
		var turn teamcontract.ParticipantTurnV1
		if err := s.state.ReadTeamTurn(action.TeamID, id, &turn); err != nil {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, err
		}
		switch turn.State {
		case teamcontract.TurnActive, teamcontract.TurnCancelling:
			raw, err := s.state.ReadTeamExecution(action.TeamID, id)
			if err != nil {
				if turn.State == teamcontract.TurnCancelling && errors.Is(err, os.ErrNotExist) {
					break
				}
				s.mu.Unlock()
				return teamcontract.TeamActionResponseV1{}, err
			}
			var handle explorationdriver.ExecutionHandle
			if err := teamcontract.DecodeStrict(raw, &handle); err != nil {
				s.mu.Unlock()
				return teamcontract.TeamActionResponseV1{}, err
			}
			driver := s.drivers[turn.Driver]
			if driver == nil {
				s.mu.Unlock()
				return teamcontract.TeamActionResponseV1{}, ErrCapabilityUnavailable
			}
			if turn.State == teamcontract.TurnActive {
				turn.State, turn.UpdatedAt = teamcontract.TurnCancelling, s.now().UTC()
				if err := s.state.WriteTeamTurn(turn); err != nil {
					s.mu.Unlock()
					return teamcontract.TeamActionResponseV1{}, err
				}
			}
			active = append(active, activeTurn{turn: turn, handle: handle, driver: driver})
		case teamcontract.TurnPrepared:
			turn.State, turn.UpdatedAt = teamcontract.TurnCancelling, s.now().UTC()
			if err := s.state.WriteTeamTurn(turn); err != nil {
				s.mu.Unlock()
				return teamcontract.TeamActionResponseV1{}, err
			}
		case teamcontract.TurnDispatching:
			turn.State, turn.Diagnostic, turn.UpdatedAt = teamcontract.TurnIndeterminate, "dispatching turn has no confirmed execution handle", s.now().UTC()
			if err := s.state.WriteTeamTurn(turn); err != nil {
				s.mu.Unlock()
				return teamcontract.TeamActionResponseV1{}, err
			}
			for index := range snapshot.Participants {
				if snapshot.Participants[index].ParticipantID == turn.ParticipantID {
					snapshot.Participants[index].State = teamcontract.ParticipantIndeterminate
					if err := s.state.WriteTeamParticipant(snapshot.Participants[index], action.TeamID); err != nil {
						s.mu.Unlock()
						return teamcontract.TeamActionResponseV1{}, err
					}
				}
			}
		}
	}
	if snapshot.State != teamcontract.LifecycleCancelling {
		snapshot.State, snapshot.StateVersion, snapshot.UpdatedAt = teamcontract.LifecycleCancelling, snapshot.StateVersion+1, s.now().UTC()
		if err := s.state.WriteTeamSnapshot(snapshot); err != nil {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	s.mu.Unlock()

	for _, item := range active {
		result, err := item.driver.Cancel(ctx, item.handle)
		if err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		if result == nil || result.State != explorationdriver.CancelConfirmed {
			return teamcontract.TeamActionResponseV1{}, fmt.Errorf("Team turn cancellation was not confirmed")
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, err = s.Get(action.TeamID)
	if err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	for _, id := range turnIDs {
		var turn teamcontract.ParticipantTurnV1
		if err := s.state.ReadTeamTurn(action.TeamID, id, &turn); err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		if turn.State == teamcontract.TurnCancelling {
			now := s.now().UTC()
			turn.State, turn.Diagnostic, turn.CompletedAt, turn.UpdatedAt = teamcontract.TurnCancelled, "cancelled by Team action", &now, now
			if err := s.state.WriteTeamTurn(turn); err != nil {
				return teamcontract.TeamActionResponseV1{}, err
			}
		}
	}
	for index := range snapshot.Participants {
		if snapshot.Participants[index].State == teamcontract.ParticipantPending || snapshot.Participants[index].State == teamcontract.ParticipantRunning {
			snapshot.Participants[index].State = teamcontract.ParticipantCancelled
			if err := s.state.WriteTeamParticipant(snapshot.Participants[index], action.TeamID); err != nil {
				return teamcontract.TeamActionResponseV1{}, err
			}
		}
	}
	snapshot.State, snapshot.CloseReason, snapshot.StateVersion, snapshot.UpdatedAt = teamcontract.LifecycleClosed, teamcontract.CloseCancelled, snapshot.StateVersion+1, s.now().UTC()
	if err := s.state.WriteTeamSnapshot(snapshot); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	if err := s.appendTeamEventLocked(snapshot, teamcontract.EventTeamCancelled, "Team cancelled", "", ""); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	response := teamcontract.TeamActionResponseV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: action.ActionID, TeamID: action.TeamID, Type: action.Type, StateVersion: snapshot.StateVersion, State: snapshot.State}
	existing.Response = &response
	if err := s.state.WriteTeamActionIntent(action.TeamID, action.ActionID, existing); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	return response, nil
}

func pageEvents(values []teamcontract.TeamEventV1, after uint64, limit int) ([]teamcontract.TeamEventV1, uint64, bool) {
	page := make([]teamcontract.TeamEventV1, 0, limit)
	more := false
	for _, value := range values {
		if value.Sequence <= after {
			continue
		}
		if len(page) == limit {
			more = true
			break
		}
		page = append(page, value)
	}
	next := after
	if len(page) > 0 {
		next = page[len(page)-1].Sequence
	}
	return page, next, more
}

func pageMessages(values []teamcontract.TeamMessageV1, after uint64, limit int) ([]teamcontract.TeamMessageV1, uint64, bool) {
	page := make([]teamcontract.TeamMessageV1, 0, limit)
	more := false
	for _, value := range values {
		if value.Sequence <= after {
			continue
		}
		if len(page) == limit {
			more = true
			break
		}
		page = append(page, value)
	}
	next := after
	if len(page) > 0 {
		next = page[len(page)-1].Sequence
	}
	return page, next, more
}
