// Package team owns the Team exploration aggregate without importing the
// Workflow Run service. This increment stops at durable preparation: no
// external Agent launch is implied by Start.
package team

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"wf.local/wf-engine/internal/explorationdriver"
	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/sessiondriver"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/teamcontract"
)

var (
	ErrInvalidArgument       = errors.New("team request contains an invalid argument")
	ErrConflict              = errors.New("team request conflicts with an existing request")
	ErrCapabilityUnavailable = errors.New("team capability is unavailable")
	ErrQuotaExceeded         = errors.New("team quota exceeded")
	ErrSessionLost           = errors.New("team participant Session is lost")
)

type StartResult struct {
	Team     teamcontract.TeamSessionV1
	Replayed bool
}

type Service struct {
	state             *store.Store
	catalog           routing.CapabilityCatalogV1
	catalogs          map[string]routing.CapabilityCatalogV1
	now               func() time.Time
	drivers           map[string]explorationdriver.Driver
	sessionDrivers    map[string]sessiondriver.Driver
	runLookup         RunLookup
	driverLimits      map[string]chan struct{}
	activeControllers map[string]struct{}
	sessionLocks      sync.Map
	mu                sync.Mutex
	controllerWG      sync.WaitGroup
}

func (s *Service) sessionLock(teamID, participantID string) *sync.Mutex {
	key := teamID + "\x00" + participantID
	value, _ := s.sessionLocks.LoadOrStore(key, &sync.Mutex{})
	return value.(*sync.Mutex)
}

type RunLookup func(runID string) (project string, err error)

func NewService(state *store.Store) *Service {
	service, err := NewServiceWithCatalog(state, routing.BuiltinCatalogV1())
	if err != nil {
		panic(err)
	}
	return service
}

func NewServiceWithCatalog(state *store.Store, catalog routing.CapabilityCatalogV1) (*Service, error) {
	catalog = routing.CanonicalCatalogV1(catalog)
	if err := routing.ValidateCatalog(catalog); err != nil {
		return nil, err
	}
	if len(catalog.Models) < teamcontract.MinParticipants {
		return nil, fmt.Errorf("Team Agent route catalog requires at least %d models", teamcontract.MinParticipants)
	}
	hash, err := routing.CatalogHash(catalog)
	if err != nil {
		return nil, err
	}
	return &Service{
		state:             state,
		catalog:           catalog,
		catalogs:          map[string]routing.CapabilityCatalogV1{hash: catalog},
		now:               time.Now,
		drivers:           make(map[string]explorationdriver.Driver),
		sessionDrivers:    make(map[string]sessiondriver.Driver),
		driverLimits:      make(map[string]chan struct{}),
		activeControllers: make(map[string]struct{}),
	}, nil
}

func (s *Service) SetRunLookup(lookup RunLookup) error {
	if lookup == nil {
		return fmt.Errorf("Run lookup is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runLookup != nil {
		return fmt.Errorf("Run lookup is already configured")
	}
	s.runLookup = lookup
	return nil
}

func (s *Service) SetDriver(driver explorationdriver.Driver) error {
	return s.setDriver(driver, false)
}

func (s *Service) ReplaceDriver(driver explorationdriver.Driver) error {
	return s.setDriver(driver, true)
}

func (s *Service) setDriver(driver explorationdriver.Driver, replace bool) error {
	if driver == nil {
		return fmt.Errorf("exploration driver is required")
	}
	if err := explorationdriver.ValidateCapabilities(driver.Capabilities()); err != nil {
		return err
	}
	name := strings.TrimSpace(driver.Name())
	if name == "" || name != driver.Name() {
		return fmt.Errorf("exploration driver name is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.drivers[name]; exists && !replace {
		return fmt.Errorf("exploration driver %q is already registered", name)
	}
	s.drivers[name] = driver
	maximum := driver.Capabilities().MaxConcurrentTurns
	if maximum <= 0 || maximum > teamcontract.MaxActiveTurns {
		maximum = teamcontract.MaxActiveTurns
	}
	s.driverLimits[name] = make(chan struct{}, maximum)
	return nil
}

func (s *Service) RemoveDriver(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.drivers, name)
	delete(s.driverLimits, name)
}

func (s *Service) SetSessionDriver(driver sessiondriver.Driver) error {
	return s.setSessionDriver(driver, false)
}

func (s *Service) ReplaceSessionDriver(driver sessiondriver.Driver) error {
	return s.setSessionDriver(driver, true)
}

func (s *Service) setSessionDriver(driver sessiondriver.Driver, replace bool) error {
	if driver == nil {
		return fmt.Errorf("Session Driver is required")
	}
	capabilities := driver.Capabilities()
	if err := sessiondriver.ValidateCapabilities(capabilities); err != nil {
		return err
	}
	if !capabilities.SupportsResume || !capabilities.SupportsRecovery || !capabilities.SupportsDirectedInput || !capabilities.SupportsConfirmedCancel {
		return fmt.Errorf("Session Driver must support resume, recovery, directed input, and confirmed cancellation")
	}
	name := strings.TrimSpace(driver.Name())
	if name == "" || name != driver.Name() {
		return fmt.Errorf("Session Driver name is invalid")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.sessionDrivers[name]; exists && !replace {
		return fmt.Errorf("Session Driver %q is already registered", name)
	}
	s.sessionDrivers[name] = driver
	return nil
}

func (s *Service) RemoveSessionDriver(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessionDrivers, name)
}

func (s *Service) SetCatalog(catalog routing.CapabilityCatalogV1) error {
	catalog = routing.CanonicalCatalogV1(catalog)
	if err := routing.ValidateCatalog(catalog); err != nil {
		return err
	}
	if len(catalog.Models) < teamcontract.MinParticipants {
		return fmt.Errorf("Team Agent route catalog requires at least %d models", teamcontract.MinParticipants)
	}
	hash, err := routing.CatalogHash(catalog)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.catalogs == nil {
		s.catalogs = make(map[string]routing.CapabilityCatalogV1)
	}
	if previousHash, hashErr := routing.CatalogHash(s.catalog); hashErr == nil {
		s.catalogs[previousHash] = s.catalog
	}
	s.catalog, s.catalogs[hash] = catalog, catalog
	return nil
}

func (s *Service) AddHistoricalCatalog(catalog routing.CapabilityCatalogV1) error {
	catalog = routing.CanonicalCatalogV1(catalog)
	hash, err := routing.CatalogHash(catalog)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.catalogs == nil {
		s.catalogs = make(map[string]routing.CapabilityCatalogV1)
	}
	s.catalogs[hash] = catalog
	return nil
}

func (s *Service) catalogByHashLocked(hash string) (routing.CapabilityCatalogV1, bool) {
	catalog, ok := s.catalogs[hash]
	return catalog, ok
}

func (s *Service) Drivers() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.drivers))
	for name := range s.drivers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *Service) Start(ctx context.Context, request teamcontract.TeamStartRequestV1) (StartResult, error) {
	if s == nil || s.state == nil {
		return StartResult{}, fmt.Errorf("team state store is unavailable")
	}
	if err := teamcontract.ValidateStartRequest(request); err != nil {
		return StartResult{}, err
	}
	if request.TemplateID != "" {
		template, err := s.TemplateGet(teamcontract.TeamTemplateGetRequestV1{SchemaVersion: teamcontract.TemplateSchemaVersion, TemplateID: request.TemplateID})
		if err != nil {
			return StartResult{}, err
		}
		if len(request.Participants) == 0 {
			for _, member := range template.Members {
				if member.ModelID == "" {
					return StartResult{}, fmt.Errorf("%w: template member %q has no Harness/Model selection; provide explicit participants for this launch", ErrInvalidArgument, member.Label)
				}
			}
			request.Participants = templateParticipantSpecs(template)
		}
	}
	if request.Mode != teamcontract.ModePanel && request.Mode != teamcontract.ModeSession {
		return StartResult{}, ErrCapabilityUnavailable
	}
	project, err := canonicalProject(request.Project)
	if err != nil {
		return StartResult{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	normalized, participants, catalogHash, err := s.normalizeStart(request, project)
	if err != nil {
		return StartResult{}, err
	}
	if request.Mode == teamcontract.ModeSession && !s.supportsSessionParticipants(participants) {
		return StartResult{}, ErrCapabilityUnavailable
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
		if err := s.repairPreparedTeamLocked(existing); err != nil {
			return StartResult{}, err
		}
		return StartResult{Team: existing, Replayed: true}, nil
	}
	teamID, err := newID("team")
	if err != nil {
		return StartResult{}, err
	}
	now := s.now().UTC()
	teamSnapshot := teamcontract.TeamSessionV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: teamID, ClientRequestID: request.ClientRequestID, RequestHash: requestHash, Project: project, Mode: request.Mode, Topic: request.Topic, Instructions: request.Instructions, CatalogHash: catalogHash, Participants: participants, State: teamcontract.LifecycleCreated, StateVersion: 1, CostGrant: normalized.CostGrant, CostUsed: s.participantCost(participants, catalogHash), CreatedAt: now, UpdatedAt: now}
	if teamSnapshot.CostUsed > teamSnapshot.CostGrant {
		return StartResult{}, ErrQuotaExceeded
	}
	if err := s.state.InitTeam(teamID); err != nil {
		return StartResult{}, err
	}
	if err := s.state.EnsureTeamSnapshot(teamSnapshot); err != nil {
		return StartResult{}, err
	}
	if err := s.repairPreparedTeamLocked(teamSnapshot); err != nil {
		return StartResult{}, err
	}
	return StartResult{Team: teamSnapshot}, nil
}

func (s *Service) supportsSessionParticipants(participants []teamcontract.ParticipantV1) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, participant := range participants {
		driver := s.sessionDrivers[participant.Driver]
		if driver == nil {
			return false
		}
		capabilities := driver.Capabilities()
		targetFound := false
		for _, target := range capabilities.Targets {
			if target == participant.Target {
				targetFound = true
				break
			}
		}
		if !targetFound {
			return false
		}
	}
	return true
}

func (s *Service) repairPreparedTeamLocked(snapshot teamcontract.TeamSessionV1) error {
	if err := s.state.InitTeam(snapshot.TeamID); err != nil {
		return err
	}
	for _, participant := range snapshot.Participants {
		if err := s.state.WriteTeamParticipant(participant, snapshot.TeamID); err != nil {
			return err
		}
	}
	events, err := s.state.ReadTeamEvents(snapshot.TeamID)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return s.state.AppendTeamEvent(teamcontract.TeamEventV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: snapshot.TeamID, Sequence: 1, Type: teamcontract.EventTeamCreated, StateVersion: snapshot.StateVersion, Summary: "team prepared", CreatedAt: snapshot.CreatedAt})
	}
	if events[0].Type != teamcontract.EventTeamCreated {
		return fmt.Errorf("Team %q has an invalid creation journal", snapshot.TeamID)
	}
	return nil
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

// DispatchInitial prepares and runs every initial participant turn. A caller
// may safely retry this method after a process restart: prepared turns have a
// stable identity and dispatching turns without a durable handle are never
// relaunched automatically.
func (s *Service) DispatchInitial(ctx context.Context, teamID string) (teamcontract.TeamSessionV1, error) {
	if s == nil || s.state == nil {
		return teamcontract.TeamSessionV1{}, fmt.Errorf("team state store is unavailable")
	}
	snapshot, err := s.Get(teamID)
	if err != nil {
		return teamcontract.TeamSessionV1{}, err
	}
	if snapshot.Mode == teamcontract.ModeSession {
		return s.dispatchSessionRound(ctx, teamID)
	}
	s.mu.Lock()
	_, err = s.prepareInitialTurnsLocked(teamID)
	if err != nil {
		s.mu.Unlock()
		return teamcontract.TeamSessionV1{}, err
	}
	turnIDs, err := s.state.ListTeamTurnIDs(teamID)
	if err != nil {
		s.mu.Unlock()
		return teamcontract.TeamSessionV1{}, err
	}
	drivers := make(map[string]explorationdriver.Driver, len(s.drivers))
	limits := make(map[string]chan struct{}, len(s.driverLimits))
	for name, driver := range s.drivers {
		drivers[name] = driver
		limits[name] = s.driverLimits[name]
	}
	s.mu.Unlock()
	var wait sync.WaitGroup
	var errorMu sync.Mutex
	var firstErr error
	for _, turnID := range turnIDs {
		turnID := turnID
		wait.Add(1)
		go func() {
			defer wait.Done()
			var turn teamcontract.ParticipantTurnV1
			if err := s.state.ReadTeamTurn(teamID, turnID, &turn); err != nil {
				errorMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errorMu.Unlock()
				return
			}
			limit := limits[turn.Driver]
			if limit != nil {
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
			}
			if err := s.dispatchTurn(ctx, teamID, turnID, drivers); err != nil {
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
	return current, firstErr
}

func (s *Service) prepareInitialTurnsLocked(teamID string) (teamcontract.TeamSessionV1, error) {
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(teamID, &snapshot); err != nil {
		return snapshot, err
	}
	if snapshot.State == teamcontract.LifecycleClosed {
		return snapshot, nil
	}
	catalog, foundCatalog := s.catalogByHashLocked(snapshot.CatalogHash)
	if !foundCatalog {
		return snapshot, fmt.Errorf("Team Catalog %q is unavailable for recovery", snapshot.CatalogHash)
	}
	now := s.now().UTC()
	changed := false
	cumulative := 0
	for index := range snapshot.Participants {
		participant := &snapshot.Participants[index]
		if participant.CurrentTurnID != "" {
			var existing teamcontract.ParticipantTurnV1
			if err := s.state.ReadTeamTurn(teamID, participant.CurrentTurnID, &existing); err != nil {
				return snapshot, err
			}
			cumulative = existing.Usage.CumulativeCostUnits
			continue
		}
		var target routing.Target
		var found bool
		for _, model := range catalog.Models {
			if model.ID == participant.ModelID {
				target, found = model.Target, true
				break
			}
		}
		if !found {
			return snapshot, fmt.Errorf("participant model %q is absent from trusted catalog", participant.ModelID)
		}
		cost, err := routing.CostUnitsForTarget(catalog, target)
		if err != nil {
			return snapshot, err
		}
		cumulative += cost
		turnID := fmt.Sprintf("turn-%s-%d", participant.ParticipantID, 1)
		turn := teamcontract.ParticipantTurnV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: teamID, ParticipantID: participant.ParticipantID, TurnID: turnID, Number: 1, State: teamcontract.TurnPrepared, Driver: participant.Driver, Target: participant.Target, ModelID: participant.ModelID, Usage: teamcontract.TeamTurnUsageV1{Target: participant.Target, CatalogHash: snapshot.CatalogHash, CostUnits: cost, CumulativeCostUnits: cumulative}, CreatedAt: now, UpdatedAt: now}
		if err := s.state.WriteTeamTurn(turn); err != nil {
			return snapshot, err
		}
		participant.CurrentTurnID, participant.State = turnID, teamcontract.ParticipantRunning
		if err := s.state.WriteTeamParticipant(*participant, teamID); err != nil {
			return snapshot, err
		}
		if err := s.appendTeamEventLocked(snapshot, teamcontract.EventParticipantPrepared, "turn prepared", "", turnID); err != nil {
			return snapshot, err
		}
		changed = true
	}
	if !changed {
		return snapshot, nil
	}
	snapshot.State, snapshot.StateVersion, snapshot.UpdatedAt = teamcontract.LifecycleRunning, snapshot.StateVersion+1, now
	if err := s.state.WriteTeamSnapshot(snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (s *Service) dispatchTurn(ctx context.Context, teamID, turnID string, drivers map[string]explorationdriver.Driver) error {
	s.mu.Lock()
	var turn teamcontract.ParticipantTurnV1
	if err := s.state.ReadTeamTurn(teamID, turnID, &turn); err != nil {
		s.mu.Unlock()
		return err
	}
	driver := drivers[turn.Driver]
	if driver == nil {
		s.markTurnFailureLocked(teamID, turn, teamcontract.TurnFailed, "exploration driver is unavailable")
		s.mu.Unlock()
		return ErrCapabilityUnavailable
	}
	if turn.State == teamcontract.TurnActive {
		handleData, err := s.state.ReadTeamExecution(teamID, turnID)
		if err != nil {
			s.markTurnFailureLocked(teamID, turn, teamcontract.TurnIndeterminate, "active turn handle is unavailable")
			s.mu.Unlock()
			return err
		}
		var handle explorationdriver.ExecutionHandle
		if err := json.Unmarshal(handleData, &handle); err != nil {
			s.markTurnFailureLocked(teamID, turn, teamcontract.TurnIndeterminate, "active turn handle is invalid")
			s.mu.Unlock()
			return err
		}
		s.mu.Unlock()
		return s.observeTurn(ctx, driver, teamID, turn, handle)
	}
	if turn.State == teamcontract.TurnDispatching {
		s.markTurnFailureLocked(teamID, turn, teamcontract.TurnIndeterminate, "dispatching turn has no confirmed execution handle")
		s.mu.Unlock()
		return nil
	}
	if turn.State != teamcontract.TurnPrepared {
		s.mu.Unlock()
		return nil
	}
	turn.State, turn.UpdatedAt = teamcontract.TurnDispatching, s.now().UTC()
	if err := s.state.WriteTeamTurn(turn); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.appendTeamEventLockedFromTurn(teamID, turn, teamcontract.EventParticipantEvent, "turn dispatching"); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	request := explorationdriver.StartRequest{ProtocolVersion: explorationdriver.ProtocolVersion, Identity: explorationdriver.ExecutionIdentity{TeamID: teamID, ParticipantID: turn.ParticipantID, TurnID: turn.TurnID}, Workspace: "", Target: turn.Target, ModelID: turn.ModelID, Prompt: s.turnPrompt(teamID, turn.ParticipantID), Sandbox: explorationdriver.SandboxReadOnly, ResultContract: explorationdriver.ResultContract{MaxBytes: teamcontract.MaxMessageBytes}}
	// Workspace is loaded from the durable Team snapshot so a retry never uses
	// caller-controlled process state.
	snapshot, err := s.Get(teamID)
	if err != nil {
		return err
	}
	request.Workspace = snapshot.Project
	if err := explorationdriver.ValidateStartRequest(request); err != nil {
		return err
	}
	handle, err := driver.Start(ctx, request)
	if err != nil {
		s.mu.Lock()
		s.markTurnFailureLocked(teamID, turn, teamcontract.TurnFailed, boundedDiagnostic(err.Error()))
		s.mu.Unlock()
		return err
	}
	if handle == nil || handle.Driver != driver.Name() {
		s.mu.Lock()
		s.markTurnFailureLocked(teamID, turn, teamcontract.TurnIndeterminate, "driver returned an invalid execution handle")
		s.mu.Unlock()
		return fmt.Errorf("invalid exploration execution handle")
	}
	if err := explorationdriver.ValidateExecutionHandle(*handle); err != nil {
		s.mu.Lock()
		s.markTurnFailureLocked(teamID, turn, teamcontract.TurnIndeterminate, boundedDiagnostic(err.Error()))
		s.mu.Unlock()
		return err
	}
	encodedHandle, err := json.Marshal(handle)
	if err != nil {
		return err
	}
	s.mu.Lock()
	var current teamcontract.ParticipantTurnV1
	if err := s.state.ReadTeamTurn(teamID, turnID, &current); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.state.WriteTeamExecution(teamID, turnID, encodedHandle); err != nil {
		s.mu.Unlock()
		return err
	}
	if current.State != teamcontract.TurnDispatching {
		s.mu.Unlock()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, cancelErr := driver.Cancel(cleanupCtx, *handle)
		if cancelErr != nil {
			return cancelErr
		}
		if result == nil || result.State != explorationdriver.CancelConfirmed {
			return fmt.Errorf("late exploration execution cancellation was not confirmed")
		}
		return nil
	}
	turn = current
	turn.State, turn.UpdatedAt = teamcontract.TurnActive, s.now().UTC()
	if err := s.state.WriteTeamTurn(turn); err != nil {
		s.mu.Unlock()
		return err
	}
	if err := s.appendTeamEventLockedFromTurn(teamID, turn, teamcontract.EventParticipantActive, "turn active"); err != nil {
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()
	return s.observeTurn(ctx, driver, teamID, turn, *handle)
}

func (s *Service) observeTurn(ctx context.Context, driver explorationdriver.Driver, teamID string, turn teamcontract.ParticipantTurnV1, handle explorationdriver.ExecutionHandle) error {
	for {
		observation, err := driver.Observe(ctx, handle)
		if err != nil {
			s.mu.Lock()
			s.markTurnFailureLocked(teamID, turn, teamcontract.TurnIndeterminate, boundedDiagnostic(err.Error()))
			s.mu.Unlock()
			return err
		}
		if observation == nil {
			s.mu.Lock()
			s.markTurnFailureLocked(teamID, turn, teamcontract.TurnIndeterminate, "driver returned no observation")
			s.mu.Unlock()
			return fmt.Errorf("driver returned no observation")
		}
		if err := explorationdriver.ValidateObservation(*observation); err != nil {
			s.mu.Lock()
			s.markTurnFailureLocked(teamID, turn, teamcontract.TurnIndeterminate, boundedDiagnostic(err.Error()))
			s.mu.Unlock()
			return err
		}
		switch observation.State {
		case explorationdriver.ObservationActive:
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(25 * time.Millisecond):
			}
		case explorationdriver.ObservationLost:
			s.mu.Lock()
			s.markTurnFailureLocked(teamID, turn, teamcontract.TurnIndeterminate, observation.Diagnostic)
			s.mu.Unlock()
			return fmt.Errorf("exploration execution was lost")
		case explorationdriver.ObservationTerminal:
			output, err := driver.Output(ctx, handle, teamcontract.MaxMessageBytes)
			if err != nil {
				s.mu.Lock()
				s.markTurnFailureLocked(teamID, turn, teamcontract.TurnFailed, boundedDiagnostic(err.Error()))
				s.mu.Unlock()
				return err
			}
			return s.commitContribution(teamID, turn, output)
		}
	}
}

func (s *Service) commitContribution(teamID string, turn teamcontract.ParticipantTurnV1, output string) error {
	var contribution teamcontract.ContributionV1
	if err := teamcontract.DecodeStrict([]byte(output), &contribution); err != nil {
		s.mu.Lock()
		s.markTurnFailureLocked(teamID, turn, teamcontract.TurnFailed, boundedDiagnostic(err.Error()))
		s.mu.Unlock()
		return err
	}
	if err := teamcontract.ValidateContribution(contribution); err != nil {
		s.mu.Lock()
		s.markTurnFailureLocked(teamID, turn, teamcontract.TurnFailed, boundedDiagnostic(err.Error()))
		s.mu.Unlock()
		return err
	}
	canonical, err := teamcontract.CanonicalJSON(contribution)
	if err != nil {
		return err
	}
	hash, _, err := teamcontract.CanonicalHash(contribution)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var current teamcontract.ParticipantTurnV1
	if err := s.state.ReadTeamTurn(teamID, turn.TurnID, &current); err != nil {
		return err
	}
	if current.State != teamcontract.TurnActive {
		return nil
	}
	turn = current
	messages, err := s.state.ReadTeamMessages(teamID)
	if err != nil {
		return err
	}
	messageID := fmt.Sprintf("message-%s", turn.TurnID)
	foundMessage := false
	for _, message := range messages {
		if message.TurnID == turn.TurnID {
			messageID = message.MessageID
			foundMessage = true
			break
		}
	}
	if !foundMessage {
		message := teamcontract.TeamMessageV1{SchemaVersion: teamcontract.SchemaVersion, MessageID: messageID, TeamID: teamID, Sequence: uint64(len(messages) + 1), Kind: teamcontract.MessageContribution, Actor: turn.ParticipantID, TurnID: turn.TurnID, Content: string(canonical), CreatedAt: s.now().UTC(), ContentHash: hash}
		if err := s.state.AppendTeamMessage(message); err != nil {
			return err
		}
	}
	turn.State, turn.ContributionMessage, turn.CompletedAt, turn.UpdatedAt = teamcontract.TurnResponded, messageID, ptrTime(s.now().UTC()), s.now().UTC()
	if err := s.state.WriteTeamTurn(turn); err != nil {
		return err
	}
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(teamID, &snapshot); err != nil {
		return err
	}
	for index := range snapshot.Participants {
		if snapshot.Participants[index].ParticipantID == turn.ParticipantID {
			snapshot.Participants[index].State = teamcontract.ParticipantResponded
			snapshot.Participants[index].CurrentTurnID = turn.TurnID
			if err := s.state.WriteTeamParticipant(snapshot.Participants[index], teamID); err != nil {
				return err
			}
		}
	}
	allTerminal := true
	for _, participant := range snapshot.Participants {
		if participant.State == teamcontract.ParticipantPending || participant.State == teamcontract.ParticipantRunning {
			allTerminal = false
			break
		}
	}
	snapshot.StateVersion, snapshot.UpdatedAt = snapshot.StateVersion+1, s.now().UTC()
	closed, closeSummary := settleTerminalRound(&snapshot, allTerminal)
	if err := s.state.WriteTeamSnapshot(snapshot); err != nil {
		return err
	}
	if err := s.appendTeamEventLockedFromTurn(teamID, turn, teamcontract.EventParticipantEvent, "turn responded"); err != nil {
		return err
	}
	if closed {
		if err := s.appendTeamEventLocked(snapshot, teamcontract.EventTeamClosed, closeSummary, "", ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) markTurnFailureLocked(teamID string, turn teamcontract.ParticipantTurnV1, state teamcontract.TurnState, diagnostic string) error {
	var current teamcontract.ParticipantTurnV1
	if err := s.state.ReadTeamTurn(teamID, turn.TurnID, &current); err != nil {
		return err
	}
	switch current.State {
	case teamcontract.TurnPrepared, teamcontract.TurnDispatching, teamcontract.TurnActive:
	default:
		return nil
	}
	turn = current
	turn.State, turn.Diagnostic, turn.UpdatedAt = state, boundedDiagnostic(diagnostic), s.now().UTC()
	if err := s.state.WriteTeamTurn(turn); err != nil {
		return err
	}
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(teamID, &snapshot); err != nil {
		return err
	}
	for index := range snapshot.Participants {
		if snapshot.Participants[index].ParticipantID == turn.ParticipantID {
			if state == teamcontract.TurnCancelled {
				snapshot.Participants[index].State = teamcontract.ParticipantCancelled
			} else if state == teamcontract.TurnIndeterminate {
				snapshot.Participants[index].State = teamcontract.ParticipantIndeterminate
			} else {
				snapshot.Participants[index].State = teamcontract.ParticipantFailed
			}
			if err := s.state.WriteTeamParticipant(snapshot.Participants[index], teamID); err != nil {
				return err
			}
		}
	}
	snapshot.StateVersion, snapshot.UpdatedAt = snapshot.StateVersion+1, s.now().UTC()
	allTerminal := true
	for _, participant := range snapshot.Participants {
		if participant.State == teamcontract.ParticipantPending || participant.State == teamcontract.ParticipantRunning {
			allTerminal = false
			break
		}
	}
	closed, closeSummary := settleTerminalRound(&snapshot, allTerminal)
	if err := s.state.WriteTeamSnapshot(snapshot); err != nil {
		return err
	}
	if err := s.appendTeamEventLockedFromTurn(teamID, turn, teamcontract.EventParticipantEvent, turn.Diagnostic); err != nil {
		return err
	}
	if closed {
		return s.appendTeamEventLocked(snapshot, teamcontract.EventTeamClosed, closeSummary, "", "")
	}
	return nil
}

func settleTerminalRound(snapshot *teamcontract.TeamSessionV1, allTerminal bool) (bool, string) {
	if !allTerminal {
		return false, ""
	}
	if snapshot.Mode == teamcontract.ModePanel {
		snapshot.State, snapshot.CloseReason = teamcontract.LifecycleClosed, teamcontract.ClosePanelSettled
		return true, "panel settled"
	}
	if snapshot.State == teamcontract.LifecycleClosing {
		return false, ""
	}
	if snapshot.State != teamcontract.LifecycleCancelling {
		snapshot.State, snapshot.CloseReason = teamcontract.LifecycleOpen, ""
	}
	return false, ""
}

func (s *Service) appendTeamEventLocked(snapshot teamcontract.TeamSessionV1, eventType teamcontract.EventType, summary, messageID, turnID string) error {
	events, err := s.state.ReadTeamEvents(snapshot.TeamID)
	if err != nil {
		return err
	}
	for _, existing := range events {
		if existing.Type == eventType && existing.TurnID == turnID && existing.MessageID == messageID {
			return nil
		}
	}
	event := teamcontract.TeamEventV1{SchemaVersion: teamcontract.SchemaVersion, TeamID: snapshot.TeamID, Sequence: uint64(len(events) + 1), Type: eventType, StateVersion: snapshot.StateVersion, Summary: boundedDiagnostic(summary), MessageID: messageID, TurnID: turnID, CreatedAt: s.now().UTC()}
	return s.state.AppendTeamEvent(event)
}

func (s *Service) appendTeamEventLockedFromTurn(teamID string, turn teamcontract.ParticipantTurnV1, eventType teamcontract.EventType, summary string) error {
	var snapshot teamcontract.TeamSessionV1
	if err := s.state.ReadTeamSnapshot(teamID, &snapshot); err != nil {
		return err
	}
	return s.appendTeamEventLocked(snapshot, eventType, summary, turn.ContributionMessage, turn.TurnID)
}

func (s *Service) turnPrompt(teamID, participantID string) string {
	snapshot, err := s.Get(teamID)
	if err != nil {
		return ""
	}
	for _, participant := range snapshot.Participants {
		if participant.ParticipantID == participantID {
			return snapshot.Topic + "\n\n" + snapshot.Instructions + "\n\nRole: " + participant.Role
		}
	}
	return snapshot.Topic
}

func boundedDiagnostic(value string) string {
	data := []byte(value)
	if len(data) > teamcontract.MaxWarningBytes {
		data = data[:teamcontract.MaxWarningBytes]
		for !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
		return string(data)
	}
	return value
}

func ptrTime(value time.Time) *time.Time { return &value }

func (s *Service) normalizeStart(request teamcontract.TeamStartRequestV1, project string) (teamcontract.TeamStartRequestV1, []teamcontract.ParticipantV1, string, error) {
	s.mu.Lock()
	catalog := routing.CanonicalCatalogV1(s.catalog)
	s.mu.Unlock()
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
		defaults := defaultParticipantModels(catalog)
		if len(defaults) < teamcontract.MinParticipants {
			return teamcontract.TeamStartRequestV1{}, nil, "", fmt.Errorf("%w: at least %d available Team routes are required", ErrCapabilityUnavailable, teamcontract.MinParticipants)
		}
		specs = []teamcontract.ParticipantSpecV1{
			{Label: "architect", Role: "propose a coherent architecture and tradeoffs", ModelID: defaults[0]},
			{Label: "reviewer", Role: "challenge assumptions and identify failure modes", ModelID: defaults[1]},
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
			return teamcontract.TeamStartRequestV1{}, nil, "", fmt.Errorf("%w: participant model %q is absent from trusted catalog", ErrInvalidArgument, spec.ModelID)
		}
		if _, exists := seenModels[model.ID]; exists {
			return teamcontract.TeamStartRequestV1{}, nil, "", fmt.Errorf("%w: participant model %q is duplicated", ErrInvalidArgument, model.ID)
		}
		seenModels[model.ID] = struct{}{}
		participantID := fmt.Sprintf("participant-%d", index+1)
		participants = append(participants, teamcontract.ParticipantV1{ParticipantID: participantID, Label: spec.Label, Role: spec.Role, ModelID: model.ID, Driver: model.Target.Driver, Target: model.Target.Provider, State: teamcontract.ParticipantPending})
		specs[index] = spec
	}
	normalized.Participants = specs
	return normalized, participants, catalogHash, nil
}

func (s *Service) participantCost(participants []teamcontract.ParticipantV1, catalogHash string) int {
	catalog, ok := s.catalogByHashLocked(catalogHash)
	if !ok {
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

func defaultParticipantModels(catalog routing.CapabilityCatalogV1) []string {
	wanted := []string{"codex/architect/gpt-5.6-sol", "codex/reviewer/gpt-5.6-sol"}
	selected := make([]string, 0, teamcontract.MinParticipants)
	for _, id := range wanted {
		for _, model := range catalog.Models {
			if model.ID == id {
				selected = append(selected, id)
				break
			}
		}
	}
	if len(selected) == teamcontract.MinParticipants {
		return selected
	}
	selected = selected[:0]
	for _, model := range catalog.Models {
		selected = append(selected, model.ID)
		if len(selected) == teamcontract.MinParticipants {
			break
		}
	}
	return selected
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
