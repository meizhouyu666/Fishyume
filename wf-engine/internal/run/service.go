package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
)

type StartRequest struct {
	Project string `json:"project"`
	Tool    string `json:"tool"`
	Runtime string `json:"runtime"`
	Task    string `json:"task"`
}

type DoctorReport struct {
	BackendReady      bool   `json:"backendReady"`
	BackendDiagnostic string `json:"backendDiagnostic"`
	ProjectChecked    bool   `json:"projectChecked"`
	ProjectReady      bool   `json:"projectReady"`
	ProjectDiagnostic string `json:"projectDiagnostic,omitempty"`
}

type EventSink func(RunEvent)

type activeRun struct {
	mu              sync.Mutex
	persistMu       sync.Mutex
	snapshot        RunSnapshot
	sequence        uint64
	session         *backend.Session
	sessionKilled   bool
	launchPending   bool
	launchDone      chan struct{}
	cancelRequested bool
	killInFlight    bool
	killDone        chan struct{}
	cancel          context.CancelFunc
	detached        bool
	cancelled       bool
}

type serviceHooks struct {
	beforeDispatch func()
	beforeRunning  func()
	afterExecute   func()
	onCancelWait   func()
}

type Service struct {
	backend backend.Backend
	store   *store.Store
	now     func() time.Time

	mu        sync.RWMutex
	runs      map[string]*activeRun
	sinkMu    sync.RWMutex
	eventSink EventSink
	hooks     serviceHooks
}

func NewService(b backend.Backend, stores ...*store.Store) *Service {
	var state *store.Store
	if len(stores) > 0 {
		state = stores[0]
	} else if defaultStore, err := store.NewDefault(); err == nil {
		state = defaultStore
	}
	return &Service{backend: b, store: state, now: time.Now, runs: make(map[string]*activeRun)}
}

func (s *Service) SetEventSink(sink EventSink) {
	s.sinkMu.Lock()
	defer s.sinkMu.Unlock()
	s.eventSink = sink
}

func (s *Service) Doctor(ctx context.Context, project string) DoctorReport {
	report := DoctorReport{}
	if err := s.backend.Doctor(ctx); err != nil {
		report.BackendDiagnostic = err.Error()
		return report
	}
	report.BackendReady = true
	report.BackendDiagnostic = "CC-Panes control CLI, release orchestrator, and daemon are ready"
	if strings.TrimSpace(project) == "" {
		return report
	}
	report.ProjectChecked = true
	doctor, ok := s.backend.(backend.ProjectDoctor)
	if !ok {
		report.ProjectDiagnostic = "backend does not support project registration checks"
		return report
	}
	if err := doctor.DoctorProject(ctx, project); err != nil {
		report.ProjectDiagnostic = err.Error()
		return report
	}
	report.ProjectReady = true
	report.ProjectDiagnostic = "project is registered in CC-Panes"
	return report
}

func (s *Service) Start(_ context.Context, request StartRequest) (RunSnapshot, error) {
	request.Project = strings.TrimSpace(request.Project)
	request.Task = strings.TrimSpace(request.Task)
	if request.Project == "" || request.Task == "" {
		return RunSnapshot{}, errors.New("project and task are required")
	}
	if request.Tool == "" {
		request.Tool = "codex"
	}
	if request.Runtime == "" {
		request.Runtime = "local"
	}
	if request.Tool != "codex" && request.Tool != "claude" && request.Tool != "opencode" {
		return RunSnapshot{}, fmt.Errorf("unsupported tool %q", request.Tool)
	}
	if request.Runtime != "local" && request.Runtime != "wsl" && request.Runtime != "ssh" {
		return RunSnapshot{}, fmt.Errorf("unsupported runtime %q", request.Runtime)
	}

	id, err := newRunID()
	if err != nil {
		return RunSnapshot{}, err
	}
	if s.store == nil {
		return RunSnapshot{}, errors.New("workflow state directory is unavailable")
	}
	if err := s.store.InitRun(id); err != nil {
		return RunSnapshot{}, err
	}
	now := s.now().UTC()
	runCtx, cancel := context.WithCancel(context.Background())
	rt := &activeRun{snapshot: RunSnapshot{
		ProtocolVersion: 1,
		ID:              id,
		Status:          RunCreated,
		NodeStatus:      NodeCreated,
		Project:         request.Project,
		Tool:            request.Tool,
		Runtime:         request.Runtime,
		Backend:         s.backend.Name(),
		StateDir:        s.store.RunDir(id),
		CreatedAt:       now,
		UpdatedAt:       now,
	}, cancel: cancel}
	s.mu.Lock()
	s.runs[id] = rt
	s.mu.Unlock()
	if err := s.record(rt, "run.created", "run created"); err != nil {
		return RunSnapshot{}, err
	}
	snapshot := s.snapshot(rt)
	go s.execute(runCtx, rt, request)
	return snapshot, nil
}

func (s *Service) execute(ctx context.Context, rt *activeRun, request StartRequest) {
	if s.hooks.afterExecute != nil {
		defer s.hooks.afterExecute()
	}
	if s.hooks.beforeDispatch != nil {
		s.hooks.beforeDispatch()
	}
	moved, err := s.transitionIfActive(rt, RunDispatching, NodeDispatching, "run.dispatching", "dispatching agent", RunCreated)
	if err != nil || !moved {
		return
	}
	if err := s.backend.Doctor(ctx); err != nil {
		if s.stopped(rt) {
			return
		}
		s.fail(rt, fmt.Errorf("backend doctor: %w", err))
		return
	}
	rt.persistMu.Lock()
	rt.mu.Lock()
	if !activeLocked(rt) {
		rt.mu.Unlock()
		rt.persistMu.Unlock()
		return
	}
	rt.launchPending = true
	rt.launchDone = make(chan struct{})
	rt.mu.Unlock()
	rt.persistMu.Unlock()
	session, err := s.backend.Launch(ctx, backend.LaunchSpec{
		RunID: requestID(rt), Project: request.Project, Tool: request.Tool,
		Runtime: request.Runtime, Prompt: request.Task,
	})
	rt.persistMu.Lock()
	rt.mu.Lock()
	rt.launchPending = false
	if rt.launchDone != nil {
		close(rt.launchDone)
		rt.launchDone = nil
	}
	if err == nil {
		rt.session = session
	}
	stopped := !activeLocked(rt)
	rt.mu.Unlock()
	rt.persistMu.Unlock()
	if err != nil {
		if stopped {
			return
		}
		s.fail(rt, fmt.Errorf("backend launch: %w", err))
		return
	}
	if stopped {
		return
	}
	if s.hooks.beforeRunning != nil {
		s.hooks.beforeRunning()
	}
	moved, err = s.transitionIfActive(rt, RunRunning, NodeRunning, "run.running", "agent session is running", RunDispatching)
	if err != nil || !moved {
		return
	}
	result, err := s.backend.Wait(ctx, *session)
	if output, outputErr := s.backend.Output(context.Background(), *session, 200); outputErr == nil {
		_ = s.store.WriteOutput(requestID(rt), output)
	}
	rt.mu.Lock()
	stopped = !activeLocked(rt)
	rt.mu.Unlock()
	if stopped {
		return
	}
	if err != nil {
		s.fail(rt, fmt.Errorf("backend wait: %w", err))
		return
	}
	if result == nil {
		s.fail(rt, errors.New("backend wait returned no result"))
		return
	}
	status, nodeStatus := normalizedStatus(result.Status)
	_, _ = s.transitionIfActive(rt, status, nodeStatus, "run.terminal", result.Summary, RunRunning)
}

func (s *Service) Get(runID string) (RunSnapshot, error) {
	rt, err := s.lookup(runID)
	if err != nil {
		return RunSnapshot{}, err
	}
	return s.snapshot(rt), nil
}

func (s *Service) Detach(runID string) (RunSnapshot, error) {
	rt, err := s.lookup(runID)
	if err != nil {
		return RunSnapshot{}, err
	}
	rt.persistMu.Lock()
	defer rt.persistMu.Unlock()
	rt.mu.Lock()
	if rt.snapshot.Status.Terminal() {
		result := rt.snapshot
		rt.mu.Unlock()
		return result, nil
	}
	rt.detached = true
	cancel := rt.cancel
	rt.snapshot.Status = RunPaused
	rt.snapshot.NodeStatus = NodePaused
	rt.snapshot.Summary = "detached; agent session left running"
	rt.snapshot.UpdatedAt = s.now().UTC()
	rt.mu.Unlock()
	cancel()
	if err := s.recordLocked(rt, "run.paused", "detached; agent session left running"); err != nil {
		return RunSnapshot{}, err
	}
	return s.snapshot(rt), nil
}

func (s *Service) Cancel(ctx context.Context, runID string) (RunSnapshot, error) {
	rt, err := s.lookup(runID)
	if err != nil {
		return RunSnapshot{}, err
	}
	for {
		rt.persistMu.Lock()
		rt.mu.Lock()
		if rt.snapshot.Status == RunSucceeded || rt.snapshot.Status == RunFailed || rt.snapshot.Status == RunCancelled {
			result := rt.snapshot
			rt.mu.Unlock()
			rt.persistMu.Unlock()
			return result, nil
		}
		rt.cancelRequested = true
		if rt.killInFlight {
			done := rt.killDone
			rt.mu.Unlock()
			rt.persistMu.Unlock()
			if s.hooks.onCancelWait != nil {
				s.hooks.onCancelWait()
			}
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return s.snapshot(rt), ctx.Err()
			}
		}
		if rt.session != nil && !rt.sessionKilled {
			rt.killInFlight = true
			rt.killDone = make(chan struct{})
			done := rt.killDone
			copy := *rt.session
			rt.mu.Unlock()
			rt.persistMu.Unlock()

			killErr := s.backend.Cancel(ctx, copy)

			rt.persistMu.Lock()
			rt.mu.Lock()
			rt.killInFlight = false
			close(done)
			rt.killDone = nil
			if killErr != nil {
				rt.cancelRequested = false
				rt.mu.Unlock()
				rt.persistMu.Unlock()
				return s.snapshot(rt), fmt.Errorf("cancel backend session: %w", killErr)
			}
			rt.sessionKilled = true
			rt.cancelRequested = false
			rt.cancelled = true
			cancel := rt.cancel
			rt.snapshot.Status = RunCancelled
			rt.snapshot.NodeStatus = NodeCancelled
			rt.snapshot.Summary = "agent session cancelled"
			rt.snapshot.UpdatedAt = s.now().UTC()
			rt.mu.Unlock()
			cancel()
			if err := s.recordLocked(rt, "run.cancelled", "agent session cancelled"); err != nil {
				rt.persistMu.Unlock()
				return RunSnapshot{}, err
			}
			rt.persistMu.Unlock()
			return s.snapshot(rt), nil
		}
		if rt.launchPending {
			done := rt.launchDone
			cancelLaunch := rt.cancel
			rt.mu.Unlock()
			rt.persistMu.Unlock()
			cancelLaunch()
			if s.hooks.onCancelWait != nil {
				s.hooks.onCancelWait()
			}
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return s.snapshot(rt), ctx.Err()
			}
		}
		rt.cancelRequested = false
		rt.cancelled = true
		cancel := rt.cancel
		rt.snapshot.Status = RunCancelled
		rt.snapshot.NodeStatus = NodeCancelled
		rt.snapshot.Summary = "run cancelled before an agent session was created"
		rt.snapshot.UpdatedAt = s.now().UTC()
		rt.mu.Unlock()
		cancel()
		if err := s.recordLocked(rt, "run.cancelled", "run cancelled before an agent session was created"); err != nil {
			rt.persistMu.Unlock()
			return RunSnapshot{}, err
		}
		rt.persistMu.Unlock()
		return s.snapshot(rt), nil
	}
}

func (s *Service) transitionIfActive(rt *activeRun, status RunStatus, nodeStatus NodeStatus, eventType, message string, expected ...RunStatus) (bool, error) {
	rt.persistMu.Lock()
	defer rt.persistMu.Unlock()
	rt.mu.Lock()
	if !activeLocked(rt) || !containsStatus(expected, rt.snapshot.Status) {
		rt.mu.Unlock()
		return false, nil
	}
	rt.snapshot.Status = status
	rt.snapshot.NodeStatus = nodeStatus
	rt.snapshot.Summary = message
	rt.snapshot.UpdatedAt = s.now().UTC()
	rt.mu.Unlock()
	return true, s.recordLocked(rt, eventType, message)
}

func containsStatus(statuses []RunStatus, status RunStatus) bool {
	for _, candidate := range statuses {
		if candidate == status {
			return true
		}
	}
	return false
}

func activeLocked(rt *activeRun) bool {
	return !rt.detached && !rt.cancelled && !rt.cancelRequested
}

func (s *Service) record(rt *activeRun, eventType, message string) error {
	rt.persistMu.Lock()
	defer rt.persistMu.Unlock()
	return s.recordLocked(rt, eventType, message)
}

func (s *Service) recordLocked(rt *activeRun, eventType, message string) error {
	rt.mu.Lock()
	rt.sequence++
	event := RunEvent{ProtocolVersion: 1, RunID: rt.snapshot.ID, Sequence: rt.sequence,
		Type: eventType, Status: rt.snapshot.Status, NodeStatus: rt.snapshot.NodeStatus,
		Message: message, Timestamp: s.now().UTC()}
	snapshot := rt.snapshot
	rt.mu.Unlock()
	if err := s.store.WriteSnapshot(snapshot.ID, snapshot); err != nil {
		return err
	}
	if err := s.store.AppendEvent(snapshot.ID, event); err != nil {
		return err
	}
	s.sinkMu.RLock()
	sink := s.eventSink
	s.sinkMu.RUnlock()
	if sink != nil {
		sink(event)
	}
	return nil
}

func (s *Service) fail(rt *activeRun, err error) {
	_, _ = s.transitionIfActive(rt, RunFailed, NodeFailed, "run.failed", err.Error(), RunCreated, RunDispatching, RunRunning)
}

func (s *Service) stopped(rt *activeRun) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return !activeLocked(rt)
}

func (s *Service) lookup(runID string) (*activeRun, error) {
	s.mu.RLock()
	rt := s.runs[runID]
	s.mu.RUnlock()
	if rt == nil {
		return nil, fmt.Errorf("run %q not found", runID)
	}
	return rt, nil
}

func (s *Service) snapshot(rt *activeRun) RunSnapshot {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.snapshot
}

func requestID(rt *activeRun) string {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.snapshot.ID
}

func normalizedStatus(status string) (RunStatus, NodeStatus) {
	switch strings.ToLower(status) {
	case "succeeded", "completed":
		return RunSucceeded, NodeSucceeded
	case "blocked", "waitinginput":
		return RunBlocked, NodeBlocked
	case "indeterminate", "idle", "exited":
		return RunIndeterminate, NodeIndeterminate
	case "paused":
		return RunPaused, NodePaused
	case "cancelled", "canceled":
		return RunCancelled, NodeCancelled
	default:
		return RunFailed, NodeFailed
	}
}

func newRunID() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return "run-" + hex.EncodeToString(bytes), nil
}
