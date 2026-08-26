package team

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/sessiondriver"
	"wf.local/wf-engine/internal/teamcontract"
)

func (s *Service) sessionAction(ctx context.Context, action teamcontract.TeamActionV1) (teamcontract.TeamActionResponseV1, error) {
	hash, _, err := teamcontract.CanonicalHash(action)
	if err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	s.mu.Lock()
	var intent actionIntentV1
	readErr := s.state.ReadTeamActionIntent(action.TeamID, action.ActionID, &intent)
	if readErr == nil {
		if intent.RequestHash != hash {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, ErrConflict
		}
		if intent.Response != nil {
			response := *intent.Response
			response.Replayed = true
			s.mu.Unlock()
			if action.Type == teamcontract.ActionFollowUp {
				s.dispatchAsync(action.TeamID)
			}
			return response, nil
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, readErr
	}
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(action.TeamID, &snapshot); err != nil {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, err
	}
	if snapshot.Mode != teamcontract.ModeSession {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, ErrCapabilityUnavailable
	}
	if readErr == nil && snapshot.State == teamcontract.LifecycleClosed {
		response, recoverErr := s.completeClosedSessionActionLocked(snapshot, intent)
		s.mu.Unlock()
		return response, recoverErr
	}
	if readErr != nil {
		if snapshot.StateVersion != action.ExpectedStateVersion || snapshot.State == teamcontract.LifecycleClosed {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, ErrConflict
		}
		if s.now().UTC().Sub(snapshot.CreatedAt) >= teamSessionLifetime && action.Type == teamcontract.ActionFollowUp {
			snapshot.State, snapshot.StateVersion, snapshot.UpdatedAt = teamcontract.LifecycleClosing, snapshot.StateVersion+1, s.now().UTC()
			_ = s.state.WriteTeamSnapshot(snapshot)
			s.mu.Unlock()
			if currentParticipantsTerminal(s.state, snapshot) {
				_ = s.finalizeGracefulClose(context.Background(), snapshot.TeamID)
			}
			return teamcontract.TeamActionResponseV1{}, ErrConflict
		}
		if err := s.preflightSessionActionLocked(snapshot, action); err != nil {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, err
		}
		count, err := s.state.TeamMutationIntentCount(action.TeamID)
		if err != nil {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, err
		}
		if count >= teamcontract.MaxMutationReceipts {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, ErrQuotaExceeded
		}
		intent = actionIntentV1{SchemaVersion: teamcontract.SchemaVersion, RequestHash: hash, Action: action}
		if err := s.state.WriteTeamActionIntent(action.TeamID, action.ActionID, intent); err != nil {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	s.mu.Unlock()

	switch action.Type {
	case teamcontract.ActionFollowUp:
		return s.commitFollowUp(action, intent)
	case teamcontract.ActionCancelTurn:
		return s.cancelExactSessionTurn(ctx, action, intent)
	case teamcontract.ActionClose:
		return s.closeTeamSession(ctx, action, intent)
	case teamcontract.ActionCancel:
		return s.cancelTeamSession(ctx, action, intent)
	default:
		return teamcontract.TeamActionResponseV1{}, ErrCapabilityUnavailable
	}
}

func (s *Service) completeClosedSessionActionLocked(snapshot teamcontract.TeamSessionV1, intent actionIntentV1) (teamcontract.TeamActionResponseV1, error) {
	action := intent.Action
	applied := false
	switch action.Type {
	case teamcontract.ActionFollowUp:
		messages, err := s.state.ReadTeamMessages(action.TeamID)
		if err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		for _, message := range messages {
			if message.MessageID == stableActionIdentity("message", action.ActionID) {
				applied = true
				break
			}
		}
		if applied {
			for _, participantID := range action.FollowUp.ParticipantIDs {
				if _, err := readStoredTurn(s.state, action.TeamID, stableActionIdentity("turn-"+participantID, action.ActionID)); err != nil {
					applied = false
					break
				}
			}
		}
	case teamcontract.ActionCancelTurn:
		turn, err := readStoredTurn(s.state, action.TeamID, action.CancelTurn.TurnID)
		if err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		applied = turn.State == teamcontract.TurnCancelled
	case teamcontract.ActionClose:
		applied = snapshot.CloseReason == teamcontract.CloseHostClosed
	case teamcontract.ActionCancel:
		applied = snapshot.CloseReason == teamcontract.CloseCancelled
	}
	if !applied {
		return teamcontract.TeamActionResponseV1{}, ErrConflict
	}
	response := teamcontract.TeamActionResponseV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: action.ActionID, TeamID: action.TeamID, Type: action.Type, StateVersion: snapshot.StateVersion, State: snapshot.State}
	intent.Response = &response
	if err := s.state.WriteTeamActionIntent(action.TeamID, action.ActionID, intent); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	return response, nil
}

func (s *Service) preflightSessionActionLocked(snapshot teamcontract.TeamSessionV1, action teamcontract.TeamActionV1) error {
	switch action.Type {
	case teamcontract.ActionFollowUp:
		if snapshot.State != teamcontract.LifecycleOpen {
			return ErrConflict
		}
		return s.preflightFollowUpLocked(snapshot, *action.FollowUp)
	case teamcontract.ActionCancelTurn:
		if snapshot.State != teamcontract.LifecycleRunning && snapshot.State != teamcontract.LifecycleOpen && snapshot.State != teamcontract.LifecycleClosing {
			return ErrConflict
		}
		turn, err := readStoredTurn(s.state, snapshot.TeamID, action.CancelTurn.TurnID)
		if err != nil {
			return err
		}
		if turn.State != teamcontract.TurnActive && turn.State != teamcontract.TurnCancelling {
			return ErrConflict
		}
		participant := participantByID(snapshot.Participants, turn.ParticipantID)
		if participant == nil || participant.CurrentTurnID != turn.TurnID {
			return ErrConflict
		}
		_, err = s.state.ReadTeamSessionExecution(snapshot.TeamID, turn.TurnID)
		return err
	case teamcontract.ActionClose:
		if action.Close.Reason != teamcontract.CloseHostClosed || (snapshot.State != teamcontract.LifecycleOpen && snapshot.State != teamcontract.LifecycleRunning && snapshot.State != teamcontract.LifecycleClosing) {
			return ErrConflict
		}
		return nil
	case teamcontract.ActionCancel:
		if snapshot.State == teamcontract.LifecycleClosed {
			return ErrConflict
		}
		return nil
	default:
		return ErrCapabilityUnavailable
	}
}

func (s *Service) preflightFollowUpLocked(snapshot teamcontract.TeamSessionV1, followUp teamcontract.FollowUpActionV1) error {
	turnIDs, err := s.state.ListTeamTurnIDs(snapshot.TeamID)
	if err != nil {
		return err
	}
	if len(turnIDs)+len(followUp.ParticipantIDs) > teamcontract.MaxParticipantTurns {
		return ErrQuotaExceeded
	}
	messages, err := s.state.ReadTeamMessages(snapshot.TeamID)
	if err != nil {
		return err
	}
	if len(messages)+1 > teamcontract.MaxRetainedMessages {
		return ErrQuotaExceeded
	}
	referenced := make(map[string]teamcontract.TeamMessageV1, len(followUp.ReferencedMessageIDs))
	for _, id := range followUp.ReferencedMessageIDs {
		found := false
		for _, message := range messages {
			if message.MessageID == id {
				referenced[id], found = message, true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: referenced Team message %q does not exist", ErrInvalidArgument, id)
		}
	}
	promptBytes := len([]byte(followUp.Content))
	for _, id := range followUp.ReferencedMessageIDs {
		promptBytes += len([]byte(referenced[id].Content)) + len(id) + 32
	}
	if promptBytes > sessiondriver.MaxPromptBytes {
		return ErrQuotaExceeded
	}
	selected := make([]teamcontract.ParticipantV1, 0, len(followUp.ParticipantIDs))
	for _, participantID := range followUp.ParticipantIDs {
		participant := participantByID(snapshot.Participants, participantID)
		if participant == nil {
			return fmt.Errorf("%w: participant %q does not exist", ErrInvalidArgument, participantID)
		}
		if participant.CurrentTurnID != "" {
			turn, err := readStoredTurn(s.state, snapshot.TeamID, participant.CurrentTurnID)
			if err != nil {
				return err
			}
			if !terminalTeamTurn(turn.State) {
				return ErrConflict
			}
		}
		raw, err := s.state.ReadTeamParticipantSession(snapshot.TeamID, participantID)
		if err != nil {
			return err
		}
		record, err := decodeParticipantSession(raw, snapshot.TeamID, participantID)
		if err != nil {
			return err
		}
		if record.State == privateSessionLost || record.State == privateSessionClosed {
			return ErrSessionLost
		}
		selected = append(selected, *participant)
	}
	additionalCost := s.participantCost(selected, snapshot.CatalogHash)
	if additionalCost > teamcontract.MaxCostGrant || snapshot.CostUsed+additionalCost > snapshot.CostGrant {
		return ErrQuotaExceeded
	}
	message := hostMessageForAction(snapshot, teamcontract.TeamActionV1{ActionID: "preflight", FollowUp: &followUp}, uint64(len(messages)+1), s.now().UTC())
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	total := len(encoded) + 1
	for _, existing := range messages {
		data, _ := json.Marshal(existing)
		total += len(data) + 1
	}
	if total > teamcontract.MaxRetainedMessageBytes {
		return ErrQuotaExceeded
	}
	return nil
}

func (s *Service) commitFollowUp(action teamcontract.TeamActionV1, intent actionIntentV1) (teamcontract.TeamActionResponseV1, error) {
	s.mu.Lock()
	dispatch := false
	defer func() {
		s.mu.Unlock()
		if dispatch {
			s.dispatchAsync(action.TeamID)
		}
	}()
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(action.TeamID, &snapshot); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	messages, err := s.state.ReadTeamMessages(action.TeamID)
	if err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	messageID := stableActionIdentity("message", action.ActionID)
	messageExists := false
	for _, message := range messages {
		if message.MessageID == messageID {
			messageExists = true
			break
		}
	}
	if !messageExists {
		message := hostMessageForAction(snapshot, action, uint64(len(messages)+1), s.now().UTC())
		if err := s.state.AppendTeamMessage(message); err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	messageEventExists, err := teamEventExists(s.state, action.TeamID, teamcontract.EventMessageCommitted, messageID, "")
	if err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	if !messageEventExists {
		if err := s.appendTeamEventLocked(snapshot, teamcontract.EventMessageCommitted, "Host follow-up committed", messageID, ""); err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	catalog := s.catalog
	cumulative, err := persistedTeamCost(s.state, action.TeamID)
	if err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	for _, participantID := range action.FollowUp.ParticipantIDs {
		participant := participantByID(snapshot.Participants, participantID)
		if participant == nil {
			return teamcontract.TeamActionResponseV1{}, ErrInvalidArgument
		}
		turnID := stableActionIdentity("turn-"+participantID, action.ActionID)
		if existing, err := readStoredTurn(s.state, action.TeamID, turnID); err == nil {
			participant.CurrentTurnID = existing.TurnID
			participant.State = participantStateForTurn(existing.State)
			if err := s.state.WriteTeamParticipant(*participant, action.TeamID); err != nil {
				return teamcontract.TeamActionResponseV1{}, err
			}
			preparedEventExists, err := teamEventExists(s.state, action.TeamID, teamcontract.EventParticipantPrepared, messageID, turnID)
			if err != nil {
				return teamcontract.TeamActionResponseV1{}, err
			}
			if !preparedEventExists {
				if err := s.appendTeamEventLocked(snapshot, teamcontract.EventParticipantPrepared, "follow-up Turn prepared", messageID, turnID); err != nil {
					return teamcontract.TeamActionResponseV1{}, err
				}
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return teamcontract.TeamActionResponseV1{}, err
		}
		cost, err := modelCost(catalog, participant.ModelID)
		if err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		cumulative += cost
		number, err := nextParticipantTurnNumber(s.state, action.TeamID, participantID)
		if err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		now := s.now().UTC()
		turn := teamcontract.ParticipantTurnV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: action.TeamID, ParticipantID: participantID, TurnID: turnID, Number: number, State: teamcontract.TurnPrepared, Driver: participant.Driver, Target: participant.Target, ModelID: participant.ModelID, Usage: teamcontract.TeamTurnUsageV1{Target: participant.Target, CatalogHash: snapshot.CatalogHash, CostUnits: cost, CumulativeCostUnits: cumulative}, CreatedAt: now, UpdatedAt: now}
		if err := s.state.WriteTeamTurn(turn); err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		participant.CurrentTurnID, participant.State = turnID, teamcontract.ParticipantRunning
		if err := s.state.WriteTeamParticipant(*participant, action.TeamID); err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		if err := s.appendTeamEventLocked(snapshot, teamcontract.EventParticipantPrepared, "follow-up Turn prepared", messageID, turnID); err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	snapshot.CostUsed = cumulative
	snapshot.Participants = reloadParticipants(s.state, snapshot)
	if snapshot.StateVersion == action.ExpectedStateVersion {
		snapshot.StateVersion++
	}
	if currentParticipantsTerminal(s.state, snapshot) {
		snapshot.State = teamcontract.LifecycleOpen
	} else {
		snapshot.State = teamcontract.LifecycleRunning
	}
	snapshot.UpdatedAt = s.now().UTC()
	if err := s.state.WriteTeamSnapshot(snapshot); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	response := teamcontract.TeamActionResponseV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: action.ActionID, TeamID: action.TeamID, Type: action.Type, StateVersion: snapshot.StateVersion, State: snapshot.State}
	intent.Response = &response
	if err := s.state.WriteTeamActionIntent(action.TeamID, action.ActionID, intent); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	dispatch = true
	return response, nil
}

func (s *Service) cancelExactSessionTurn(ctx context.Context, action teamcontract.TeamActionV1, intent actionIntentV1) (teamcontract.TeamActionResponseV1, error) {
	s.mu.Lock()
	turn, err := readStoredTurn(s.state, action.TeamID, action.CancelTurn.TurnID)
	if err != nil {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, err
	}
	raw, err := s.state.ReadTeamSessionExecution(action.TeamID, turn.TurnID)
	if err != nil {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, err
	}
	execution, err := decodeSessionTurnExecution(raw, action.TeamID, turn)
	if err != nil {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, err
	}
	driver := s.sessionDrivers[turn.Driver]
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
		var snapshot teamcontract.TeamSessionV1
		if err := s.state.ReadTeamSnapshot(action.TeamID, &snapshot); err != nil {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, err
		}
		snapshot.StateVersion++
		snapshot.UpdatedAt = s.now().UTC()
		if err := s.state.WriteTeamSnapshot(snapshot); err != nil {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	s.mu.Unlock()

	operation := s.sessionLock(action.TeamID, turn.ParticipantID)
	operation.Lock()
	defer operation.Unlock()
	s.mu.Lock()
	turn, err = readStoredTurn(s.state, action.TeamID, action.CancelTurn.TurnID)
	if err != nil {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, err
	}
	if turn.State == teamcontract.TurnCancelled {
		response, completeErr := s.completeCancelledTurnActionLocked(action, intent, turn, false)
		s.mu.Unlock()
		return response, completeErr
	}
	raw, err = s.state.ReadTeamSessionExecution(action.TeamID, turn.TurnID)
	if err != nil {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, err
	}
	execution, err = decodeSessionTurnExecution(raw, action.TeamID, turn)
	s.mu.Unlock()
	if err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}

	observed, observeErr := driver.ObserveTurn(ctx, execution.Session, execution.Turn)
	if observeErr == nil && observed != nil {
		execution.Session, execution.Turn = observed.Session, observed.Turn
	}
	if observeErr == nil && observed != nil && observed.State == sessiondriver.TurnResponded {
		if err := s.commitContribution(action.TeamID, turn, observed.Output); err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		_ = s.parkParticipantSession(ctx, driver, action.TeamID, turn.ParticipantID, observed.Session)
		return teamcontract.TeamActionResponseV1{}, ErrConflict
	}
	if observeErr != nil || observed == nil || (observed.State != sessiondriver.TurnInterrupted && observed.State != sessiondriver.TurnActive && observed.State != sessiondriver.TurnDispatching) {
		if observeErr != nil {
			return teamcontract.TeamActionResponseV1{}, observeErr
		}
		return teamcontract.TeamActionResponseV1{}, ErrConflict
	}
	if observed.State != sessiondriver.TurnInterrupted {
		cancelled, err := driver.CancelTurn(ctx, observed.Session, observed.Turn)
		if err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		if cancelled == nil || cancelled.State != sessiondriver.CancelConfirmed {
			return teamcontract.TeamActionResponseV1{}, fmt.Errorf("Session Turn cancellation was not confirmed")
		}
		execution.Session, execution.Turn = cancelled.Session, cancelled.Turn
	}
	parked, err := driver.ParkSession(ctx, execution.Session)
	if err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	execution.Session = *parked

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeSessionTurnExecutionLocked(execution); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	record := participantSessionRecord{SchemaVersion: teamSessionPrivateSchema, TeamID: action.TeamID, ParticipantID: turn.ParticipantID, Generation: parked.Generation, State: privateSessionParked, Handle: *parked, UpdatedAt: s.now().UTC()}
	if err := s.writeParticipantSessionLocked(record); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	return s.completeCancelledTurnActionLocked(action, intent, turn, true)
}

func (s *Service) completeCancelledTurnActionLocked(action teamcontract.TeamActionV1, intent actionIntentV1, turn teamcontract.ParticipantTurnV1, transition bool) (teamcontract.TeamActionResponseV1, error) {
	now := s.now().UTC()
	if transition {
		turn.State, turn.Diagnostic, turn.UpdatedAt, turn.CompletedAt = teamcontract.TurnCancelled, "cancelled by Host action", now, &now
		if err := s.state.WriteTeamTurn(turn); err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(action.TeamID, &snapshot); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	changed := transition
	for index := range snapshot.Participants {
		if snapshot.Participants[index].ParticipantID == turn.ParticipantID && snapshot.Participants[index].State != teamcontract.ParticipantCancelled {
			snapshot.Participants[index].State = teamcontract.ParticipantCancelled
			if err := s.state.WriteTeamParticipant(snapshot.Participants[index], action.TeamID); err != nil {
				return teamcontract.TeamActionResponseV1{}, err
			}
			changed = true
		}
	}
	if changed {
		snapshot.StateVersion++
		allTerminal := currentParticipantsTerminal(s.state, snapshot)
		_, _ = settleTerminalRound(&snapshot, allTerminal)
		snapshot.UpdatedAt = now
		if err := s.state.WriteTeamSnapshot(snapshot); err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	if transition {
		if err := s.appendTeamEventLockedFromTurn(action.TeamID, turn, teamcontract.EventParticipantEvent, "Session Turn cancelled"); err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	response := teamcontract.TeamActionResponseV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: action.ActionID, TeamID: action.TeamID, Type: action.Type, StateVersion: snapshot.StateVersion, State: snapshot.State}
	if intent.RequestHash != "" {
		intent.Response = &response
		if err := s.state.WriteTeamActionIntent(action.TeamID, action.ActionID, intent); err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	return response, nil
}

func (s *Service) closeTeamSession(ctx context.Context, action teamcontract.TeamActionV1, intent actionIntentV1) (teamcontract.TeamActionResponseV1, error) {
	s.mu.Lock()
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(action.TeamID, &snapshot); err != nil {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, err
	}
	if snapshot.State != teamcontract.LifecycleClosing {
		snapshot.State, snapshot.StateVersion, snapshot.UpdatedAt = teamcontract.LifecycleClosing, snapshot.StateVersion+1, s.now().UTC()
		if err := s.state.WriteTeamSnapshot(snapshot); err != nil {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	response := teamcontract.TeamActionResponseV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: action.ActionID, TeamID: action.TeamID, Type: action.Type, StateVersion: snapshot.StateVersion, State: snapshot.State}
	intent.Response = &response
	if err := s.state.WriteTeamActionIntent(action.TeamID, action.ActionID, intent); err != nil {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, err
	}
	terminal := currentParticipantsTerminal(s.state, snapshot)
	s.mu.Unlock()
	if terminal {
		if err := s.finalizeGracefulClose(ctx, action.TeamID); err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		closed, err := s.Get(action.TeamID)
		if err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		response.State, response.StateVersion = closed.State, closed.StateVersion
		s.mu.Lock()
		intent.Response = &response
		err = s.state.WriteTeamActionIntent(action.TeamID, action.ActionID, intent)
		s.mu.Unlock()
		return response, err
	}
	return response, nil
}

func (s *Service) cancelTeamSession(ctx context.Context, action teamcontract.TeamActionV1, intent actionIntentV1) (teamcontract.TeamActionResponseV1, error) {
	s.mu.Lock()
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(action.TeamID, &snapshot); err != nil {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, err
	}
	if snapshot.State == teamcontract.LifecycleClosed && snapshot.CloseReason == teamcontract.CloseCancelled {
		response, err := s.completeClosedSessionActionLocked(snapshot, intent)
		s.mu.Unlock()
		return response, err
	}
	if snapshot.State != teamcontract.LifecycleCancelling {
		snapshot.State, snapshot.StateVersion, snapshot.UpdatedAt = teamcontract.LifecycleCancelling, snapshot.StateVersion+1, s.now().UTC()
		if err := s.state.WriteTeamSnapshot(snapshot); err != nil {
			s.mu.Unlock()
			return teamcontract.TeamActionResponseV1{}, err
		}
	}
	turnIDs, err := s.state.ListTeamTurnIDs(action.TeamID)
	if err != nil {
		s.mu.Unlock()
		return teamcontract.TeamActionResponseV1{}, err
	}
	s.mu.Unlock()
	for _, turnID := range turnIDs {
		turn, err := readStoredTurn(s.state, action.TeamID, turnID)
		if err != nil {
			return teamcontract.TeamActionResponseV1{}, err
		}
		if turn.State == teamcontract.TurnActive || turn.State == teamcontract.TurnCancelling {
			cancelAction := teamcontract.TeamActionV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: stableActionIdentity("cancel-turn", action.ActionID+turnID), TeamID: action.TeamID, ExpectedStateVersion: snapshot.StateVersion, Type: teamcontract.ActionCancelTurn, CancelTurn: &teamcontract.CancelTurnActionV1{TurnID: turnID}}
			cancelIntent := actionIntentV1{SchemaVersion: teamcontract.SchemaVersion, Action: cancelAction}
			if _, err := s.cancelExactSessionTurn(ctx, cancelAction, cancelIntent); err != nil {
				return teamcontract.TeamActionResponseV1{}, err
			}
		} else if turn.State == teamcontract.TurnPrepared || turn.State == teamcontract.TurnDispatching {
			s.mu.Lock()
			if err := s.markTurnFailureLocked(action.TeamID, turn, teamcontract.TurnCancelled, "cancelled before active execution"); err != nil {
				s.mu.Unlock()
				return teamcontract.TeamActionResponseV1{}, err
			}
			s.mu.Unlock()
		}
	}
	if err := s.closeParticipantSessions(ctx, action.TeamID); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.state.ReadTeamSnapshot(action.TeamID, &snapshot); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	if snapshot.State == teamcontract.LifecycleClosed && snapshot.CloseReason == teamcontract.CloseCancelled {
		return s.completeClosedSessionActionLocked(snapshot, intent)
	}
	snapshot.State, snapshot.CloseReason, snapshot.StateVersion, snapshot.UpdatedAt = teamcontract.LifecycleClosed, teamcontract.CloseCancelled, snapshot.StateVersion+1, s.now().UTC()
	if err := s.state.WriteTeamSnapshot(snapshot); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	if err := s.appendTeamEventLocked(snapshot, teamcontract.EventTeamCancelled, "Team Session cancelled", "", ""); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	response := teamcontract.TeamActionResponseV1{SchemaVersion: teamcontract.SchemaVersion, ActionID: action.ActionID, TeamID: action.TeamID, Type: action.Type, StateVersion: snapshot.StateVersion, State: snapshot.State}
	intent.Response = &response
	if err := s.state.WriteTeamActionIntent(action.TeamID, action.ActionID, intent); err != nil {
		return teamcontract.TeamActionResponseV1{}, err
	}
	return response, nil
}

func (s *Service) finalizeGracefulClose(ctx context.Context, teamID string) error {
	if err := s.closeParticipantSessions(ctx, teamID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(teamID, &snapshot); err != nil {
		return err
	}
	if snapshot.State == teamcontract.LifecycleClosed {
		return nil
	}
	if snapshot.State != teamcontract.LifecycleClosing || !currentParticipantsTerminal(s.state, snapshot) {
		return nil
	}
	snapshot.State, snapshot.CloseReason, snapshot.StateVersion, snapshot.UpdatedAt = teamcontract.LifecycleClosed, teamcontract.CloseHostClosed, snapshot.StateVersion+1, s.now().UTC()
	if err := s.state.WriteTeamSnapshot(snapshot); err != nil {
		return err
	}
	return s.appendTeamEventLocked(snapshot, teamcontract.EventTeamClosed, "Team Session closed", "", "")
}

func (s *Service) closeParticipantSessions(ctx context.Context, teamID string) error {
	s.mu.Lock()
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(teamID, &snapshot); err != nil {
		s.mu.Unlock()
		return err
	}
	type closingSession struct {
		participantID string
		driver        sessiondriver.Driver
	}
	items := make([]closingSession, 0, len(snapshot.Participants))
	for _, participant := range snapshot.Participants {
		raw, err := s.state.ReadTeamParticipantSession(teamID, participant.ParticipantID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			s.mu.Unlock()
			return err
		}
		record, err := decodeParticipantSession(raw, teamID, participant.ParticipantID)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		if record.State == privateSessionClosed || record.State == privateSessionLost {
			continue
		}
		driver := s.sessionDrivers[participant.Driver]
		if driver == nil {
			s.mu.Unlock()
			return ErrCapabilityUnavailable
		}
		items = append(items, closingSession{participantID: participant.ParticipantID, driver: driver})
	}
	s.mu.Unlock()
	for _, item := range items {
		operation := s.sessionLock(teamID, item.participantID)
		operation.Lock()
		s.mu.Lock()
		raw, err := s.state.ReadTeamParticipantSession(teamID, item.participantID)
		if err != nil {
			s.mu.Unlock()
			operation.Unlock()
			return err
		}
		record, err := decodeParticipantSession(raw, teamID, item.participantID)
		s.mu.Unlock()
		if err != nil {
			operation.Unlock()
			return err
		}
		if record.State == privateSessionClosed || record.State == privateSessionLost {
			operation.Unlock()
			continue
		}
		closed, err := item.driver.CloseSession(ctx, record.Handle)
		if err != nil {
			operation.Unlock()
			return err
		}
		if closed == nil {
			operation.Unlock()
			return fmt.Errorf("Session Driver returned no closed Session handle")
		}
		record.Handle, record.State, record.UpdatedAt = *closed, privateSessionClosed, s.now().UTC()
		s.mu.Lock()
		err = s.writeParticipantSessionLocked(record)
		s.mu.Unlock()
		operation.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func hostMessageForAction(snapshot teamcontract.TeamSessionV1, action teamcontract.TeamActionV1, sequence uint64, createdAt time.Time) teamcontract.TeamMessageV1 {
	digest := sha256.Sum256([]byte(action.FollowUp.Content))
	return teamcontract.TeamMessageV1{SchemaVersion: teamcontract.SchemaVersion, MessageID: stableActionIdentity("message", action.ActionID), TeamID: snapshot.TeamID, Sequence: sequence, Kind: teamcontract.MessageHost, Actor: "host", Recipients: append([]string(nil), action.FollowUp.ParticipantIDs...), Content: action.FollowUp.Content, ReferencedMessageIDs: append([]string(nil), action.FollowUp.ReferencedMessageIDs...), CreatedAt: createdAt, ContentHash: hex.EncodeToString(digest[:])}
}

func stableActionIdentity(prefix, value string) string {
	digest := sha256.Sum256([]byte(value))
	return prefix + "-" + hex.EncodeToString(digest[:12])
}

func participantByID(participants []teamcontract.ParticipantV1, participantID string) *teamcontract.ParticipantV1 {
	for index := range participants {
		if participants[index].ParticipantID == participantID {
			return &participants[index]
		}
	}
	return nil
}

func terminalTeamTurn(state teamcontract.TurnState) bool {
	return state == teamcontract.TurnResponded || state == teamcontract.TurnFailed || state == teamcontract.TurnIndeterminate || state == teamcontract.TurnCancelled
}

func currentParticipantsTerminal(state interface {
	ReadTeamTurn(string, string, *teamcontract.ParticipantTurnV1) error
}, snapshot teamcontract.TeamSessionV1) bool {
	for _, participant := range snapshot.Participants {
		if participant.CurrentTurnID == "" {
			continue
		}
		var turn teamcontract.ParticipantTurnV1
		if err := state.ReadTeamTurn(snapshot.TeamID, participant.CurrentTurnID, &turn); err != nil || !terminalTeamTurn(turn.State) {
			return false
		}
	}
	return true
}

func reloadParticipants(state interface {
	ReadTeamParticipant(string, string, *teamcontract.ParticipantV1) error
}, snapshot teamcontract.TeamSessionV1) []teamcontract.ParticipantV1 {
	result := append([]teamcontract.ParticipantV1(nil), snapshot.Participants...)
	for index := range result {
		_ = state.ReadTeamParticipant(snapshot.TeamID, result[index].ParticipantID, &result[index])
	}
	return result
}

func nextParticipantTurnNumber(state interface {
	ListTeamTurnIDs(string) ([]string, error)
	ReadTeamTurn(string, string, *teamcontract.ParticipantTurnV1) error
}, teamID, participantID string) (int, error) {
	ids, err := state.ListTeamTurnIDs(teamID)
	if err != nil {
		return 0, err
	}
	maximum := 0
	for _, id := range ids {
		var turn teamcontract.ParticipantTurnV1
		if err := state.ReadTeamTurn(teamID, id, &turn); err != nil {
			return 0, err
		}
		if turn.ParticipantID == participantID && turn.Number > maximum {
			maximum = turn.Number
		}
	}
	return maximum + 1, nil
}

func modelCost(catalog routing.CapabilityCatalogV1, modelID string) (int, error) {
	for _, model := range catalog.Models {
		if model.ID == modelID {
			return routing.CostUnitsForTarget(catalog, model.Target)
		}
	}
	return 0, fmt.Errorf("trusted model %q is unavailable", modelID)
}

func persistedTeamCost(state interface {
	ListTeamTurnIDs(string) ([]string, error)
	ReadTeamTurn(string, string, *teamcontract.ParticipantTurnV1) error
}, teamID string) (int, error) {
	ids, err := state.ListTeamTurnIDs(teamID)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, id := range ids {
		var turn teamcontract.ParticipantTurnV1
		if err := state.ReadTeamTurn(teamID, id, &turn); err != nil {
			return 0, err
		}
		total += turn.Usage.CostUnits
	}
	return total, nil
}

func participantStateForTurn(state teamcontract.TurnState) teamcontract.ParticipantState {
	switch state {
	case teamcontract.TurnResponded:
		return teamcontract.ParticipantResponded
	case teamcontract.TurnFailed:
		return teamcontract.ParticipantFailed
	case teamcontract.TurnIndeterminate:
		return teamcontract.ParticipantIndeterminate
	case teamcontract.TurnCancelled:
		return teamcontract.ParticipantCancelled
	default:
		return teamcontract.ParticipantRunning
	}
}

func teamEventExists(state interface {
	ReadTeamEvents(string) ([]teamcontract.TeamEventV1, error)
}, teamID string, eventType teamcontract.EventType, messageID, turnID string) (bool, error) {
	events, err := state.ReadTeamEvents(teamID)
	if err != nil {
		return false, err
	}
	for _, event := range events {
		if event.Type == eventType && event.MessageID == messageID && event.TurnID == turnID {
			return true, nil
		}
	}
	return false, nil
}

func readStoredTurn(state interface {
	ReadTeamTurn(string, string, *teamcontract.ParticipantTurnV1) error
}, teamID, turnID string) (teamcontract.ParticipantTurnV1, error) {
	var turn teamcontract.ParticipantTurnV1
	err := state.ReadTeamTurn(teamID, turnID, &turn)
	return turn, err
}
