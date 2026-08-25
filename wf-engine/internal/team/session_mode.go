package team

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"wf.local/wf-engine/internal/sessiondriver"
	"wf.local/wf-engine/internal/teamcontract"
)

const (
	teamSessionPrivateSchema = "fishyume.team-session/v1"
	teamSessionLifetime      = 24 * time.Hour
)

type privateSessionState string

const (
	privateSessionActive privateSessionState = "active"
	privateSessionParked privateSessionState = "parked"
	privateSessionLost   privateSessionState = "lost"
	privateSessionClosed privateSessionState = "closed"
)

type participantSessionRecord struct {
	SchemaVersion string                      `json:"schemaVersion"`
	TeamID        string                      `json:"teamId"`
	ParticipantID string                      `json:"participantId"`
	Generation    uint64                      `json:"generation"`
	State         privateSessionState         `json:"state"`
	Handle        sessiondriver.SessionHandle `json:"handle"`
	UpdatedAt     time.Time                   `json:"updatedAt"`
}

type sessionTurnExecution struct {
	SchemaVersion string                      `json:"schemaVersion"`
	TeamID        string                      `json:"teamId"`
	ParticipantID string                      `json:"participantId"`
	TurnID        string                      `json:"turnId"`
	Session       sessiondriver.SessionHandle `json:"session"`
	Turn          sessiondriver.TurnHandle    `json:"turn"`
}

func (s *Service) dispatchSessionRound(ctx context.Context, teamID string) (teamcontract.TeamSessionV1, error) {
	s.mu.Lock()
	if _, err := s.prepareInitialTurnsLocked(teamID); err != nil {
		s.mu.Unlock()
		return teamcontract.TeamSessionV1{}, err
	}
	turnIDs, err := s.state.ListTeamTurnIDs(teamID)
	if err != nil {
		s.mu.Unlock()
		return teamcontract.TeamSessionV1{}, err
	}
	drivers := make(map[string]sessiondriver.Driver, len(s.sessionDrivers))
	for name, driver := range s.sessionDrivers {
		drivers[name] = driver
	}
	s.mu.Unlock()

	limit := make(chan struct{}, teamcontract.MaxActiveTurns)
	var firstErr error
	var errorMu sync.Mutex
	var wait sync.WaitGroup
	for _, turnID := range turnIDs {
		turnID := turnID
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case limit <- struct{}{}:
				defer func() { <-limit }()
			case <-ctx.Done():
				errorMu.Lock()
				if firstErr == nil {
					firstErr = ctx.Err()
				}
				errorMu.Unlock()
				return
			}
			if err := s.dispatchSessionTurn(ctx, teamID, turnID, drivers); err != nil {
				errorMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errorMu.Unlock()
			}
		}()
	}
	wait.Wait()
	current, err := s.Get(teamID)
	if err != nil {
		return teamcontract.TeamSessionV1{}, err
	}
	if current.State == teamcontract.LifecycleClosing {
		if closeErr := s.finalizeGracefulClose(ctx, teamID); closeErr != nil && firstErr == nil {
			firstErr = closeErr
		}
		current, err = s.Get(teamID)
		if err != nil {
			return teamcontract.TeamSessionV1{}, err
		}
	}
	return current, firstErr
}

func (s *Service) dispatchSessionTurn(ctx context.Context, teamID, turnID string, drivers map[string]sessiondriver.Driver) error {
	s.mu.Lock()
	var turn teamcontract.ParticipantTurnV1
	if err := s.state.ReadTeamTurn(teamID, turnID, &turn); err != nil {
		s.mu.Unlock()
		return err
	}
	if turn.State != teamcontract.TurnPrepared && turn.State != teamcontract.TurnDispatching && turn.State != teamcontract.TurnActive {
		s.mu.Unlock()
		return nil
	}
	driver := drivers[turn.Driver]
	if driver == nil {
		_ = s.markTurnFailureLocked(teamID, turn, teamcontract.TurnFailed, "Session Driver is unavailable")
		s.mu.Unlock()
		return ErrCapabilityUnavailable
	}
	if raw, err := s.state.ReadTeamSessionExecution(teamID, turnID); err == nil {
		execution, decodeErr := decodeSessionTurnExecution(raw, teamID, turn)
		if decodeErr != nil {
			_ = s.markTurnFailureLocked(teamID, turn, teamcontract.TurnIndeterminate, decodeErr.Error())
			s.mu.Unlock()
			return decodeErr
		}
		s.mu.Unlock()
		return s.observeSessionTurn(ctx, driver, teamID, turn, execution)
	} else if !errors.Is(err, os.ErrNotExist) {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	operation := s.sessionLock(teamID, turn.ParticipantID)
	operation.Lock()
	operationLocked := true
	defer func() {
		if operationLocked {
			operation.Unlock()
		}
	}()
	record, err := s.ensureParticipantSession(ctx, driver, teamID, turn)
	if err != nil {
		s.mu.Lock()
		_ = s.markTurnFailureLocked(teamID, turn, teamcontract.TurnIndeterminate, boundedDiagnostic(err.Error()))
		s.mu.Unlock()
		return err
	}

	s.mu.Lock()
	var current teamcontract.ParticipantTurnV1
	if err := s.state.ReadTeamTurn(teamID, turnID, &current); err != nil {
		s.mu.Unlock()
		return err
	}
	if current.State == teamcontract.TurnPrepared {
		current.State, current.UpdatedAt = teamcontract.TurnDispatching, s.now().UTC()
		if err := s.state.WriteTeamTurn(current); err != nil {
			s.mu.Unlock()
			return err
		}
		if err := s.appendTeamEventLockedFromTurn(teamID, current, teamcontract.EventParticipantEvent, "Session turn dispatching"); err != nil {
			s.mu.Unlock()
			return err
		}
	} else if current.State != teamcontract.TurnDispatching {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	prompt, err := s.sessionTurnPrompt(teamID, current)
	if err != nil {
		return err
	}
	started, err := driver.StartTurn(ctx, record.Handle, sessiondriver.StartTurnRequest{
		ProtocolVersion: sessiondriver.ProtocolVersion,
		Identity:        sessiondriver.TurnIdentity{TurnID: current.TurnID, ExpectedSessionGeneration: record.Generation},
		Prompt:          prompt, MaxOutputBytes: teamcontract.MaxMessageBytes,
	})
	if err != nil {
		s.mu.Lock()
		_ = s.markTurnFailureLocked(teamID, current, teamcontract.TurnIndeterminate, boundedDiagnostic(err.Error()))
		s.mu.Unlock()
		return err
	}
	if started == nil {
		return fmt.Errorf("Session Driver returned no started Turn")
	}
	execution := sessionTurnExecution{SchemaVersion: teamSessionPrivateSchema, TeamID: teamID, ParticipantID: current.ParticipantID, TurnID: current.TurnID, Session: started.Session, Turn: started.Turn}
	if err := validateSessionTurnExecution(execution, teamID, current); err != nil {
		return err
	}

	s.mu.Lock()
	if err := s.writeSessionTurnExecutionLocked(execution); err != nil {
		s.mu.Unlock()
		return err
	}
	record.Handle, record.State, record.UpdatedAt = started.Session, privateSessionActive, s.now().UTC()
	if err := s.writeParticipantSessionLocked(record); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.state.ReadTeamTurn(teamID, turnID, &current); err != nil {
		s.mu.Unlock()
		return err
	}
	if current.State != teamcontract.TurnDispatching {
		s.mu.Unlock()
		return nil
	}
	current.State, current.UpdatedAt = teamcontract.TurnActive, s.now().UTC()
	if err := s.state.WriteTeamTurn(current); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.appendTeamEventLockedFromTurn(teamID, current, teamcontract.EventParticipantActive, "Session turn active"); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	operationLocked = false
	operation.Unlock()
	return s.observeSessionTurn(ctx, driver, teamID, current, execution)
}

func (s *Service) ensureParticipantSession(ctx context.Context, driver sessiondriver.Driver, teamID string, turn teamcontract.ParticipantTurnV1) (participantSessionRecord, error) {
	s.mu.Lock()
	raw, err := s.state.ReadTeamParticipantSession(teamID, turn.ParticipantID)
	if err == nil {
		record, decodeErr := decodeParticipantSession(raw, teamID, turn.ParticipantID)
		s.mu.Unlock()
		if decodeErr != nil {
			return participantSessionRecord{}, decodeErr
		}
		if record.State == privateSessionLost || record.State == privateSessionClosed {
			return participantSessionRecord{}, fmt.Errorf("%w: participant Session is %s", ErrSessionLost, record.State)
		}
		if record.State == privateSessionParked {
			resumed, resumeErr := driver.ResumeSession(ctx, record.Handle)
			if resumeErr != nil {
				if errors.Is(resumeErr, sessiondriver.ErrLost) {
					record.State, record.UpdatedAt = privateSessionLost, s.now().UTC()
					s.mu.Lock()
					writeErr := s.writeParticipantSessionLocked(record)
					s.mu.Unlock()
					if writeErr != nil {
						return participantSessionRecord{}, writeErr
					}
					return participantSessionRecord{}, fmt.Errorf("%w: %v", ErrSessionLost, resumeErr)
				}
				return participantSessionRecord{}, resumeErr
			}
			if resumed == nil {
				return participantSessionRecord{}, fmt.Errorf("Session Driver returned no resumed Session handle")
			}
			record.Handle, record.State, record.UpdatedAt = *resumed, privateSessionActive, s.now().UTC()
			s.mu.Lock()
			writeErr := s.writeParticipantSessionLocked(record)
			s.mu.Unlock()
			if writeErr != nil {
				return participantSessionRecord{}, writeErr
			}
		}
		return record, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		s.mu.Unlock()
		return participantSessionRecord{}, err
	}
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(teamID, &snapshot); err != nil {
		s.mu.Unlock()
		return participantSessionRecord{}, err
	}
	s.mu.Unlock()
	started, err := driver.StartSession(ctx, sessiondriver.StartSessionRequest{
		ProtocolVersion: sessiondriver.ProtocolVersion,
		Identity:        sessiondriver.SessionIdentity{TeamID: teamID, ParticipantID: turn.ParticipantID, Generation: 1},
		Workspace:       snapshot.Project, Target: turn.Target, ModelID: turn.ModelID, Sandbox: sessiondriver.SandboxReadOnly,
	})
	if err != nil {
		return participantSessionRecord{}, err
	}
	if started == nil {
		return participantSessionRecord{}, fmt.Errorf("Session Driver returned no Session handle")
	}
	record := participantSessionRecord{SchemaVersion: teamSessionPrivateSchema, TeamID: teamID, ParticipantID: turn.ParticipantID, Generation: 1, State: privateSessionActive, Handle: *started, UpdatedAt: s.now().UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existingRaw, readErr := s.state.ReadTeamParticipantSession(teamID, turn.ParticipantID); readErr == nil {
		return decodeParticipantSession(existingRaw, teamID, turn.ParticipantID)
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return participantSessionRecord{}, readErr
	}
	if err := s.writeParticipantSessionLocked(record); err != nil {
		return participantSessionRecord{}, err
	}
	return record, nil
}

func (s *Service) observeSessionTurn(ctx context.Context, driver sessiondriver.Driver, teamID string, turn teamcontract.ParticipantTurnV1, execution sessionTurnExecution) error {
	operation := s.sessionLock(teamID, turn.ParticipantID)
	for {
		operation.Lock()
		s.mu.Lock()
		var current teamcontract.ParticipantTurnV1
		readErr := s.state.ReadTeamTurn(teamID, turn.TurnID, &current)
		s.mu.Unlock()
		if readErr != nil {
			operation.Unlock()
			return readErr
		}
		if current.State != teamcontract.TurnActive && current.State != teamcontract.TurnDispatching {
			operation.Unlock()
			return nil
		}
		observed, err := driver.ObserveTurn(ctx, execution.Session, execution.Turn)
		if err != nil {
			s.markSessionLost(teamID, turn, execution.Session, err)
			operation.Unlock()
			return err
		}
		if observed == nil {
			err := fmt.Errorf("Session Driver returned no Turn observation")
			s.markSessionLost(teamID, turn, execution.Session, err)
			operation.Unlock()
			return err
		}
		execution.Session, execution.Turn = observed.Session, observed.Turn
		s.mu.Lock()
		if err := s.writeSessionTurnExecutionLocked(execution); err != nil {
			s.mu.Unlock()
			operation.Unlock()
			return err
		}
		record := participantSessionRecord{SchemaVersion: teamSessionPrivateSchema, TeamID: teamID, ParticipantID: turn.ParticipantID, Generation: observed.Session.Generation, State: privateSessionActive, Handle: observed.Session, UpdatedAt: s.now().UTC()}
		if err := s.writeParticipantSessionLocked(record); err != nil {
			s.mu.Unlock()
			operation.Unlock()
			return err
		}
		s.mu.Unlock()
		switch observed.State {
		case sessiondriver.TurnDispatching, sessiondriver.TurnActive:
			operation.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(25 * time.Millisecond):
			}
		case sessiondriver.TurnResponded:
			if err := s.commitContribution(teamID, turn, observed.Output); err != nil {
				operation.Unlock()
				return err
			}
			err := s.parkParticipantSession(ctx, driver, teamID, turn.ParticipantID, observed.Session)
			operation.Unlock()
			return err
		case sessiondriver.TurnInterrupted:
			s.mu.Lock()
			err := s.markTurnFailureLocked(teamID, turn, teamcontract.TurnCancelled, "Session Turn was interrupted")
			s.mu.Unlock()
			if err != nil {
				operation.Unlock()
				return err
			}
			err = s.parkParticipantSession(ctx, driver, teamID, turn.ParticipantID, observed.Session)
			operation.Unlock()
			return err
		case sessiondriver.TurnFailed:
			s.mu.Lock()
			err := s.markTurnFailureLocked(teamID, turn, teamcontract.TurnFailed, observed.Diagnostic)
			s.mu.Unlock()
			if err != nil {
				operation.Unlock()
				return err
			}
			err = s.parkParticipantSession(ctx, driver, teamID, turn.ParticipantID, observed.Session)
			operation.Unlock()
			return err
		case sessiondriver.TurnLost:
			err := fmt.Errorf("%w: %s", ErrSessionLost, observed.Diagnostic)
			s.markSessionLost(teamID, turn, observed.Session, err)
			operation.Unlock()
			return err
		default:
			operation.Unlock()
			return fmt.Errorf("Session Driver returned unsupported Turn state %q", observed.State)
		}
	}
}

func (s *Service) parkParticipantSession(ctx context.Context, driver sessiondriver.Driver, teamID, participantID string, handle sessiondriver.SessionHandle) error {
	parked, err := driver.ParkSession(ctx, handle)
	if err != nil {
		return err
	}
	if parked == nil {
		return fmt.Errorf("Session Driver returned no parked Session handle")
	}
	record := participantSessionRecord{SchemaVersion: teamSessionPrivateSchema, TeamID: teamID, ParticipantID: participantID, Generation: parked.Generation, State: privateSessionParked, Handle: *parked, UpdatedAt: s.now().UTC()}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeParticipantSessionLocked(record)
}

func (s *Service) markSessionLost(teamID string, turn teamcontract.ParticipantTurnV1, handle sessiondriver.SessionHandle, cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := participantSessionRecord{SchemaVersion: teamSessionPrivateSchema, TeamID: teamID, ParticipantID: turn.ParticipantID, Generation: handle.Generation, State: privateSessionLost, Handle: handle, UpdatedAt: s.now().UTC()}
	_ = s.writeParticipantSessionLocked(record)
	_ = s.markTurnFailureLocked(teamID, turn, teamcontract.TurnIndeterminate, boundedDiagnostic(cause.Error()))
}

func (s *Service) sessionTurnPrompt(teamID string, turn teamcontract.ParticipantTurnV1) (string, error) {
	if turn.Number == 1 {
		return s.turnPrompt(teamID, turn.ParticipantID), nil
	}
	messages, err := s.state.ReadTeamMessages(teamID)
	if err != nil {
		return "", err
	}
	for index := len(messages) - 1; index >= 0; index-- {
		message := messages[index]
		if message.Kind != teamcontract.MessageHost || !containsString(message.Recipients, turn.ParticipantID) {
			continue
		}
		prompt := message.Content
		for _, referencedID := range message.ReferencedMessageIDs {
			for _, candidate := range messages {
				if candidate.MessageID == referencedID {
					prompt += "\n\nReferenced public message " + referencedID + ":\n" + candidate.Content
					break
				}
			}
		}
		if len([]byte(prompt)) > sessiondriver.MaxPromptBytes {
			return "", ErrQuotaExceeded
		}
		return prompt, nil
	}
	return "", fmt.Errorf("Host message for Session Turn %q is unavailable", turn.TurnID)
}

func (s *Service) writeParticipantSessionLocked(record participantSessionRecord) error {
	if err := validateParticipantSession(record); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return s.state.WriteTeamParticipantSession(record.TeamID, record.ParticipantID, data)
}

func (s *Service) writeSessionTurnExecutionLocked(execution sessionTurnExecution) error {
	var turn teamcontract.ParticipantTurnV1
	if err := s.state.ReadTeamTurn(execution.TeamID, execution.TurnID, &turn); err != nil {
		return err
	}
	if err := validateSessionTurnExecution(execution, execution.TeamID, turn); err != nil {
		return err
	}
	data, err := json.Marshal(execution)
	if err != nil {
		return err
	}
	return s.state.WriteTeamSessionExecution(execution.TeamID, execution.TurnID, data)
}

func decodeParticipantSession(data json.RawMessage, teamID, participantID string) (participantSessionRecord, error) {
	var record participantSessionRecord
	if err := teamcontract.DecodeStrict(data, &record); err != nil {
		return record, err
	}
	if record.TeamID != teamID || record.ParticipantID != participantID {
		return record, fmt.Errorf("private participant Session identity does not match its path")
	}
	return record, validateParticipantSession(record)
}

func validateParticipantSession(record participantSessionRecord) error {
	if record.SchemaVersion != teamSessionPrivateSchema || record.TeamID == "" || record.ParticipantID == "" || record.Generation == 0 || record.UpdatedAt.IsZero() {
		return fmt.Errorf("private participant Session record is incomplete")
	}
	if record.State != privateSessionActive && record.State != privateSessionParked && record.State != privateSessionLost && record.State != privateSessionClosed {
		return fmt.Errorf("private participant Session state %q is invalid", record.State)
	}
	if err := sessiondriver.ValidateSessionHandle(record.Handle); err != nil {
		return err
	}
	if record.Handle.Generation != record.Generation {
		return fmt.Errorf("private participant Session generation is stale")
	}
	return nil
}

func decodeSessionTurnExecution(data json.RawMessage, teamID string, turn teamcontract.ParticipantTurnV1) (sessionTurnExecution, error) {
	var execution sessionTurnExecution
	if err := teamcontract.DecodeStrict(data, &execution); err != nil {
		return execution, err
	}
	return execution, validateSessionTurnExecution(execution, teamID, turn)
}

func validateSessionTurnExecution(execution sessionTurnExecution, teamID string, turn teamcontract.ParticipantTurnV1) error {
	if execution.SchemaVersion != teamSessionPrivateSchema || execution.TeamID != teamID || execution.ParticipantID != turn.ParticipantID || execution.TurnID != turn.TurnID {
		return fmt.Errorf("private Session Turn execution identity is stale")
	}
	if err := sessiondriver.ValidateSessionHandle(execution.Session); err != nil {
		return err
	}
	if err := sessiondriver.ValidateTurnHandle(execution.Turn); err != nil {
		return err
	}
	if execution.Turn.ID != turn.TurnID || execution.Turn.SessionID != execution.Session.ID || execution.Turn.SessionGeneration != execution.Session.Generation {
		return fmt.Errorf("private Session Turn handle binding is stale")
	}
	return nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
