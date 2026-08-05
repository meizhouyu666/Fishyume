package run

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

const protocolVersion = 2

// State schema versioning is independent from the JSON-RPC protocol version.
const stateSchemaVersion = 2

const (
	startupIdleReconcileChecks = 20
	startupIdleReconcileDelay  = 500 * time.Millisecond
	cancelRequestPollInterval  = 25 * time.Millisecond
	cancelStateReadGrace       = 2 * time.Second
	cancelSessionPersistGrace  = 30 * time.Second
	cancelBackendTimeout       = 30 * time.Second
)

type StartRequest struct {
	Project string `json:"project"`
	Backend string `json:"backend,omitempty"`
	Tool    string `json:"tool"`
	Runtime string `json:"runtime"`
	Task    string `json:"task"`
}

type StartWorkflowRequest struct {
	Project    string               `json:"project"`
	Backend    string               `json:"backend,omitempty"`
	Filename   string               `json:"filename"`
	Content    string               `json:"content"`
	Inputs     map[string]any       `json:"inputs,omitempty"`
	Normalized *workflow.Normalized `json:"normalized,omitempty"`
}

type ResumeAction struct {
	Type                     string `json:"type"`
	NodeID                   string `json:"nodeId"`
	Reason                   string `json:"reason,omitempty"`
	AcknowledgeDuplicateRisk bool   `json:"acknowledgeDuplicateRisk,omitempty"`
}

type ResumeRequest struct {
	RunID  string        `json:"runId"`
	Action *ResumeAction `json:"action,omitempty"`
}

type DoctorReport struct {
	BackendReady      bool   `json:"backendReady"`
	BackendDiagnostic string `json:"backendDiagnostic"`
	ProjectChecked    bool   `json:"projectChecked"`
	ProjectReady      bool   `json:"projectReady"`
	ProjectDiagnostic string `json:"projectDiagnostic,omitempty"`
}

type StatusView struct {
	ProtocolVersion  int               `json:"protocolVersion"`
	Legacy           bool              `json:"legacy"`
	Run              *WorkflowSnapshot `json:"run,omitempty"`
	LegacyRun        *LegacySnapshot   `json:"legacyRun,omitempty"`
	Nodes            []NodeSnapshot    `json:"nodes,omitempty"`
	ActiveAttempt    *AttemptSnapshot  `json:"activeAttempt,omitempty"`
	ActiveNodes      []NodeSummary     `json:"activeNodes,omitempty"`
	ActiveAttempts   []AttemptSnapshot `json:"activeAttempts,omitempty"`
	WaitingApprovals []NodeSummary     `json:"waitingApprovals,omitempty"`
	Diagnostics      []NodeDiagnostic  `json:"diagnostics,omitempty"`
}

type NodeDiagnostic struct {
	NodeID  string `json:"nodeId"`
	Reason  Reason `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type EventSink func(WorkflowEvent)

type controller struct {
	cancel     context.CancelFunc
	done       chan struct{}
	lease      *store.Lease
	generation uint64
	stopping   bool
	err        error
}

type serviceTestHooks struct {
	beforeControllerMutation func(point string)
	afterLaunch              func()
	idleReconcileDelay       func(context.Context) error
	cancelRequestDelay       func(context.Context) error
}

type Service struct {
	registry       *backend.Registry
	defaultBackend string
	store          *store.Store
	leases         *store.LeaseManager
	now            func() time.Time
	getenv         func(string) string

	mu             sync.Mutex
	controllers    map[string]*controller
	nextGeneration uint64
	persistMu      sync.Mutex
	sinkMu         sync.RWMutex
	eventSink      EventSink
	testHooks      serviceTestHooks
}

func NewService(b backend.AgentBackend, stores ...*store.Store) *Service {
	registry := backend.NewRegistry()
	if err := registry.Register(b); err != nil {
		panic(err)
	}
	service := NewServiceWithRegistry(registry, b.Name(), stores...)
	// The single-Backend constructor is a deterministic compatibility/test
	// entry point; production composition uses NewServiceWithRegistry.
	service.getenv = func(string) string { return "" }
	return service
}

func NewServiceWithRegistry(registry *backend.Registry, defaultBackend string, stores ...*store.Store) *Service {
	var state *store.Store
	if len(stores) > 0 {
		state = stores[0]
	} else if defaultStore, err := store.NewDefault(); err == nil {
		state = defaultStore
	}
	if registry == nil {
		registry = backend.NewRegistry()
	}
	defaultBackend = strings.TrimSpace(defaultBackend)
	if defaultBackend == "" {
		defaultBackend = "ccpanes"
	}
	service := &Service{registry: registry, defaultBackend: defaultBackend, store: state, now: time.Now, getenv: os.Getenv, controllers: make(map[string]*controller)}
	if state != nil {
		service.leases = store.NewLeaseManager(state)
	}
	return service
}

func (s *Service) SupportedBackends() []string {
	return s.registry.Names()
}

func (s *Service) SetEventSink(sink EventSink) {
	s.sinkMu.Lock()
	defer s.sinkMu.Unlock()
	s.eventSink = sink
}

func (s *Service) Doctor(ctx context.Context, project, requestedBackend string) DoctorReport {
	project = strings.TrimSpace(project)
	candidate, _, err := s.selectBackend(requestedBackend, "")
	if err != nil {
		return DoctorReport{BackendDiagnostic: err.Error()}
	}
	backendReport := candidate.Doctor(ctx, backend.DoctorRequest{Workspace: project})
	report := DoctorReport{BackendReady: backendReport.Ready, BackendDiagnostic: doctorDiagnostic(backendReport)}
	if project == "" {
		return report
	}
	report.ProjectChecked = true
	report.ProjectReady = backendReport.Ready
	report.ProjectDiagnostic = doctorDiagnosticNamed(backendReport, "workspace")
	if report.ProjectDiagnostic == "" {
		report.ProjectDiagnostic = report.BackendDiagnostic
	}
	return report
}

func (s *Service) selectBackend(explicit, workflowDefault string) (backend.AgentBackend, string, error) {
	name := strings.TrimSpace(explicit)
	if name == "" {
		name = strings.TrimSpace(workflowDefault)
	}
	if name == "" && s.getenv != nil {
		name = strings.TrimSpace(s.getenv("FISHYUME_BACKEND"))
	}
	if name == "" {
		name = s.defaultBackend
	}
	candidate, err := s.registry.Get(name)
	if err != nil {
		return nil, "", err
	}
	return candidate, name, nil
}

func doctorDiagnostic(report backend.DoctorReport) string {
	if len(report.Diagnostics) == 0 {
		if report.Ready {
			return "backend " + report.Backend + " is ready"
		}
		return "backend " + report.Backend + " is not ready"
	}
	parts := make([]string, 0, len(report.Diagnostics))
	for _, diagnostic := range report.Diagnostics {
		parts = append(parts, diagnostic.Name+": "+diagnostic.Message)
	}
	return strings.Join(parts, "; ")
}

func doctorDiagnosticNamed(report backend.DoctorReport, name string) string {
	for _, diagnostic := range report.Diagnostics {
		if diagnostic.Name == name {
			return diagnostic.Message
		}
	}
	return ""
}

func (s *Service) Start(ctx context.Context, request StartRequest) (WorkflowSnapshot, error) {
	request.Project = strings.TrimSpace(request.Project)
	request.Task = strings.TrimSpace(request.Task)
	if request.Project == "" || request.Task == "" {
		return WorkflowSnapshot{}, errors.New("project and task are required")
	}
	if request.Tool == "" {
		request.Tool = "codex"
	}
	if request.Runtime == "" {
		request.Runtime = "local"
	}
	doc := workflow.Document{
		APIVersion: workflow.APIVersion, Name: "ad-hoc", Inputs: map[string]workflow.InputDeclaration{},
		Defaults:  workflow.Defaults{Tool: request.Tool, Runtime: request.Runtime},
		Execution: workflow.Execution{MaxConcurrency: 1},
		Nodes:     map[string]workflow.Node{"agent-1": {Type: "agent", Task: request.Task, DependsOn: []string{}, RequiredSkills: []string{}}},
	}
	order, err := workflow.Validate(doc)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	candidate, backendName, err := s.selectBackend(request.Backend, "")
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	doc.Defaults.Backend = backendName
	normalized := workflow.Normalized{Document: doc, Inputs: map[string]any{}, TopologicalOrder: order}
	if err := validateBackendCapabilities(candidate, normalized); err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := ensureBackendReady(ctx, candidate, request.Project); err != nil {
		return WorkflowSnapshot{}, err
	}
	return s.startNormalized(ctx, request.Project, normalized, "run", backendName)
}

func (s *Service) StartWorkflow(ctx context.Context, request StartWorkflowRequest) (WorkflowSnapshot, error) {
	request.Project = strings.TrimSpace(request.Project)
	if request.Project == "" {
		return WorkflowSnapshot{}, errors.New("project is required")
	}
	if strings.TrimSpace(request.Content) == "" && request.Normalized == nil {
		return WorkflowSnapshot{}, errors.New("workflow content is required")
	}
	if strings.TrimSpace(request.Content) != "" && request.Normalized != nil {
		return WorkflowSnapshot{}, errors.New("provide workflow content or normalized document, not both")
	}
	if request.Filename == "" {
		request.Filename = "workflow.yaml"
	}
	var normalized workflow.Normalized
	var err error
	if request.Normalized != nil {
		normalized = *request.Normalized
		normalized.TopologicalOrder, err = workflow.Validate(normalized.Document)
		if err == nil {
			provided := request.Inputs
			if provided == nil {
				provided = normalized.Inputs
			}
			normalized.Inputs, err = workflow.ResolveInputs(normalized.Document, provided)
		}
	} else {
		normalized, err = workflow.Parse([]byte(request.Content), request.Filename, request.Inputs)
	}
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	candidate, backendName, err := s.selectBackend(request.Backend, normalized.Document.Defaults.Backend)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	normalized.Document.Defaults.Backend = backendName
	if err := validateBackendCapabilities(candidate, normalized); err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := ensureBackendReady(ctx, candidate, request.Project); err != nil {
		return WorkflowSnapshot{}, err
	}
	return s.startNormalized(ctx, request.Project, normalized, "run", backendName)
}

func ensureBackendReady(ctx context.Context, candidate backend.AgentBackend, project string) error {
	report := candidate.Doctor(ctx, backend.DoctorRequest{Workspace: project})
	if report.Ready {
		return nil
	}
	return fmt.Errorf("Backend %q is not ready: %s", candidate.Name(), doctorDiagnostic(report))
}

func validateBackendCapabilities(candidate backend.AgentBackend, normalized workflow.Normalized) error {
	capabilities := candidate.Capabilities()
	for _, nodeID := range normalized.TopologicalOrder {
		node := normalized.Document.Nodes[nodeID]
		if node.Type != "agent" {
			continue
		}
		tool := node.Tool
		if tool == "" {
			tool = normalized.Document.Defaults.Tool
		}
		if tool == "" {
			tool = "codex"
		}
		runtimeKind := node.Runtime
		if runtimeKind == "" {
			runtimeKind = normalized.Document.Defaults.Runtime
		}
		if runtimeKind == "" {
			runtimeKind = "local"
		}
		if !containsCapability(capabilities.Tools, tool) {
			return fmt.Errorf("Backend %q does not support tool %q required by node %q", candidate.Name(), tool, nodeID)
		}
		if !containsCapability(capabilities.Runtimes, runtimeKind) {
			return fmt.Errorf("Backend %q does not support runtime %q required by node %q", candidate.Name(), runtimeKind, nodeID)
		}
	}
	return nil
}

func containsCapability(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *Service) startNormalized(_ context.Context, project string, normalized workflow.Normalized, command, backendName string) (WorkflowSnapshot, error) {
	if s.store == nil || s.leases == nil {
		return WorkflowSnapshot{}, errors.New("workflow state directory is unavailable")
	}
	id, err := newRunID()
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := s.store.InitWorkflowRun(id); err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := s.store.WriteWorkflow(id, normalized); err != nil {
		return WorkflowSnapshot{}, err
	}
	now := s.now().UTC()
	nodeSummaries := make(map[string]NodeSummary, len(normalized.Document.Nodes))
	for _, nodeID := range normalized.TopologicalOrder {
		definition := normalized.Document.Nodes[nodeID]
		node := NodeSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, RunID: id, ID: nodeID, Type: definition.Type, Phase: NodePhasePending, CreatedAt: now, UpdatedAt: now}
		if err := s.store.WriteNode(id, nodeID, node); err != nil {
			return WorkflowSnapshot{}, err
		}
		nodeSummaries[nodeID] = summarizeNode(node)
	}
	run := WorkflowSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, ID: id, WorkflowName: normalized.Document.Name, Project: project,
		Backend: backendName, Phase: PhaseCreated, Inputs: normalized.Inputs, TopologicalOrder: normalized.TopologicalOrder,
		Nodes: nodeSummaries, StateDir: s.store.RunDir(id), CreatedAt: now, UpdatedAt: now}
	if err := s.persistRun(&run, nil, "run.created", "workflow run created"); err != nil {
		return WorkflowSnapshot{}, err
	}
	lease, err := s.leases.Acquire(id, command)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	s.startController(id, lease, func(ctx context.Context, generation uint64) { s.control(ctx, id, generation) })
	return run, nil
}

func (s *Service) startController(runID string, lease *store.Lease, control func(context.Context, uint64)) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.nextGeneration++
	entry := &controller{cancel: cancel, done: make(chan struct{}), lease: lease, generation: s.nextGeneration}
	s.controllers[runID] = entry
	s.mu.Unlock()
	go func() {
		defer cancel()
		defer close(entry.done)
		defer func() {
			_ = lease.Release()
			s.mu.Lock()
			if s.controllers[runID] == entry {
				delete(s.controllers, runID)
			}
			s.mu.Unlock()
		}()
		heartbeatErrors := lease.KeepAlive(ctx)
		go func() {
			if heartbeatErr, ok := <-heartbeatErrors; ok && heartbeatErr != nil {
				cancel()
			}
		}()
		monitorDone := make(chan struct{})
		monitorCtx, stopMonitor := context.WithCancel(ctx)
		go func() {
			defer close(monitorDone)
			s.monitorCancellationRequests(monitorCtx, runID, entry.generation)
		}()
		control(ctx, entry.generation)
		stopMonitor()
		<-monitorDone
	}()
}

func (s *Service) Status(runID string) (StatusView, error) {
	if s.store == nil {
		return StatusView{}, errors.New("workflow state directory is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.store
	kind, err := state.DetectSnapshot(runID)
	if err != nil && os.IsNotExist(err) {
		if legacy := s.store.LegacyFallback(); legacy != nil {
			if legacyKind, legacyErr := legacy.DetectSnapshot(runID); legacyErr == nil {
				state, kind, err = legacy, legacyKind, nil
			}
		}
	}
	if err != nil {
		return StatusView{}, err
	}
	if kind == store.SnapshotLegacyM1 {
		var legacy LegacySnapshot
		if err := state.ReadSnapshot(runID, &legacy); err != nil {
			return StatusView{}, err
		}
		return StatusView{ProtocolVersion: protocolVersion, Legacy: true, LegacyRun: &legacy}, nil
	}
	if _, err := resetEventSequence(state, runID); err != nil {
		return StatusView{}, fmt.Errorf("invalid event history: %w", err)
	}
	run, nodes, err := s.loadRunFrom(state, runID)
	if err != nil {
		return StatusView{}, err
	}
	legacyActiveNodeID := run.ActiveNodeID
	view := StatusView{ProtocolVersion: protocolVersion, Run: &run, Nodes: nodes}
	for _, node := range nodes {
		if node.Phase == NodePhaseRunning || node.Phase == NodePhaseWaiting {
			view.ActiveNodes = append(view.ActiveNodes, summarizeNode(node))
			if node.Type == "agent" && node.CurrentAttempt > 0 {
				var attempt AttemptSnapshot
				if err := state.ReadAttempt(runID, node.ID, node.CurrentAttempt, &attempt); err != nil {
					return StatusView{}, err
				}
				if attempt.StateSchemaVersion == 0 {
					attempt.StateSchemaVersion = 1
				}
				if err := validateActiveAttempt(run, node, attempt); err != nil {
					return StatusView{}, err
				}
				view.ActiveAttempts = append(view.ActiveAttempts, attempt)
			}
		}
		if node.Phase == NodePhaseWaiting && node.Type == "approval" {
			view.WaitingApprovals = append(view.WaitingApprovals, summarizeNode(node))
		}
		if node.Reason != "" || node.Diagnostic != "" {
			view.Diagnostics = append(view.Diagnostics, NodeDiagnostic{NodeID: node.ID, Reason: node.Reason, Message: node.Diagnostic})
		}
	}
	if len(view.ActiveAttempts) == 1 {
		view.ActiveAttempt = &view.ActiveAttempts[0]
	}
	if view.ActiveAttempt == nil && run.StateSchemaVersion <= 1 && legacyActiveNodeID != "" {
		for _, node := range nodes {
			if node.ID != legacyActiveNodeID || node.CurrentAttempt < 1 {
				continue
			}
			var attempt AttemptSnapshot
			if err := state.ReadAttempt(runID, node.ID, node.CurrentAttempt, &attempt); err != nil {
				return StatusView{}, err
			}
			if attempt.StateSchemaVersion == 0 {
				attempt.StateSchemaVersion = 1
			}
			view.ActiveAttempt = &attempt
			break
		}
	}
	if len(view.ActiveNodes) == 1 {
		run.ActiveNodeID = view.ActiveNodes[0].ID
	} else {
		run.ActiveNodeID = ""
	}
	return view, nil
}

func (s *Service) Get(runID string) (WorkflowSnapshot, error) {
	view, err := s.Status(runID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if view.Legacy {
		return WorkflowSnapshot{}, fmt.Errorf("legacy M1 run %q is read-only", runID)
	}
	return *view.Run, nil
}

func (s *Service) Resume(_ context.Context, request ResumeRequest) (WorkflowSnapshot, error) {
	if request.RunID == "" {
		return WorkflowSnapshot{}, errors.New("runId is required")
	}
	kind, err := s.store.DetectSnapshot(request.RunID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if kind == store.SnapshotLegacyM1 {
		return WorkflowSnapshot{}, fmt.Errorf("legacy M1 run %q is status-only and cannot be resumed", request.RunID)
	}
	if _, err := s.resetEventSequence(request.RunID); err != nil {
		return WorkflowSnapshot{}, fmt.Errorf("invalid event history: %w", err)
	}
	run, _, err := s.loadRun(request.RunID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if run.Phase == PhaseCompleted && request.Action == nil {
		return run, nil
	}
	lease, err := s.acquireLease(request.RunID, "resume", time.Second)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if request.Action != nil {
		if err := validateResumeAction(*request.Action); err != nil {
			_ = lease.Release()
			return WorkflowSnapshot{}, err
		}
		if err := s.applyAction(&run, *request.Action); err != nil {
			_ = lease.Release()
			return WorkflowSnapshot{}, err
		}
		if run.Phase == PhaseCompleted {
			_ = lease.Release()
			return run, nil
		}
	} else {
		if run.CancelRequested {
			_ = lease.Release()
			return WorkflowSnapshot{}, fmt.Errorf("run %q has cancellation pending; use fishyume cancel to retry", run.ID)
		}
		if run.Phase == PhaseWaiting && run.Reason == ReasonApprovalRequired {
			_ = lease.Release()
			return run, nil
		}
		run.Phase, run.Conclusion, run.Reason, run.Summary, run.UpdatedAt = PhaseRunning, "", "", "controller resumed", s.now().UTC()
		if err := s.persistRun(&run, nil, "run.resumed", run.Summary); err != nil {
			_ = lease.Release()
			return WorkflowSnapshot{}, err
		}
	}
	s.startController(request.RunID, lease, func(ctx context.Context, generation uint64) { s.control(ctx, request.RunID, generation) })
	updated, err := s.Get(request.RunID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	return updated, nil
}

func (s *Service) Detach(runID string) (WorkflowSnapshot, error) {
	s.mu.Lock()
	active := s.controllers[runID]
	if active == nil {
		s.mu.Unlock()
		run, err := s.Get(runID)
		if err != nil {
			return WorkflowSnapshot{}, err
		}
		if run.Phase == PhaseCompleted {
			return run, nil
		}
		return run, fmt.Errorf("run %q has no controller in this engine process", runID)
	}
	active.stopping = true
	run, _, err := s.loadRun(runID)
	if err != nil {
		s.mu.Unlock()
		active.cancel()
		<-active.done
		return WorkflowSnapshot{}, err
	}
	run.Phase, run.Reason, run.Summary, run.UpdatedAt = PhasePaused, ReasonControllerDetach, "controller detached; active Agent session left running", s.now().UTC()
	persistErr := s.persistRun(&run, nil, "run.paused", run.Summary)
	active.cancel()
	s.mu.Unlock()
	<-active.done
	if persistErr != nil {
		return WorkflowSnapshot{}, persistErr
	}
	return s.Get(runID)
}

func (s *Service) Cancel(ctx context.Context, runID string) (WorkflowSnapshot, error) {
	run, _, err := s.loadRunForCancellation(ctx, runID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if run.Phase == PhaseCompleted {
		return run, nil
	}
	request, err := s.store.RequestCancellation(runID, s.now().UTC())
	if err != nil {
		return WorkflowSnapshot{}, fmt.Errorf("persist cancellation request: %w", err)
	}
	for {
		if response, responseErr := s.store.ReadCancellationResponse(runID, request.ID); responseErr == nil {
			result, _, loadErr := s.loadRunForCancellation(ctx, runID)
			if loadErr != nil {
				return WorkflowSnapshot{}, loadErr
			}
			if response.Status == store.CancelResponseCompleted {
				return result, nil
			}
			return result, errors.New(response.Message)
		}
		lease, acquireErr := s.leases.Acquire(runID, "cancel")
		if acquireErr == nil {
			result, cancelErr := s.handleCancellationRequest(ctx, runID)
			resolveErr := s.resolveCancellationRequest(runID, request.ID, cancelErr)
			releaseErr := lease.Release()
			if cancelErr != nil || resolveErr != nil || releaseErr != nil {
				return result, errors.Join(cancelErr, resolveErr, releaseErr)
			}
			return result, nil
		}
		var conflict *store.LeaseConflictError
		if !errors.As(acquireErr, &conflict) {
			return WorkflowSnapshot{}, acquireErr
		}
		if err := s.waitCancellationPoll(ctx); err != nil {
			return WorkflowSnapshot{}, err
		}
	}
}

func (s *Service) loadRunForCancellation(ctx context.Context, runID string) (WorkflowSnapshot, []NodeSnapshot, error) {
	deadline := time.Now().Add(cancelStateReadGrace)
	for {
		run, nodes, err := s.loadRun(runID)
		if err == nil {
			return run, nodes, nil
		}
		if time.Now().After(deadline) {
			return WorkflowSnapshot{}, nil, err
		}
		if waitErr := s.waitCancellationPoll(ctx); waitErr != nil {
			return WorkflowSnapshot{}, nil, waitErr
		}
	}
}

func (s *Service) monitorCancellationRequests(ctx context.Context, runID string, generation uint64) {
	for {
		request, err := s.store.ReadCancellationRequest(runID)
		if err == nil {
			cancelCtx, cancel := context.WithTimeout(context.Background(), cancelBackendTimeout)
			_, cancelErr := s.handleCancellationRequest(cancelCtx, runID)
			cancel()
			_ = s.resolveCancellationRequest(runID, request.ID, cancelErr)
			s.stopController(runID, generation)
			return
		}
		if err := s.waitCancellationPoll(ctx); err != nil {
			return
		}
	}
}

func (s *Service) handleCancellationRequest(ctx context.Context, runID string) (WorkflowSnapshot, error) {
	activeNodeID, attemptNumber, err := s.markCancellationIntent(runID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if activeNodeID != "" && attemptNumber > 0 {
		attempt, handle, handleErr := s.waitForCancellationHandle(ctx, runID, activeNodeID, attemptNumber)
		if handleErr != nil {
			return s.persistCancelFailure(runID, activeNodeID, handleErr.Error())
		}
		alreadyCancelled := attempt.Phase == NodePhaseCompleted && attempt.Conclusion == ConclusionCancelled
		if !alreadyCancelled && handle != nil {
			candidate, err := s.registry.Get(attempt.Backend)
			if err != nil {
				return s.persistCancelFailure(runID, activeNodeID, err.Error())
			}
			result, err := candidate.Cancel(ctx, *handle)
			if err != nil {
				message := "cancel Backend execution: " + err.Error()
				return s.persistCancelFailure(runID, activeNodeID, message)
			}
			if result == nil || result.State != backend.CancelConfirmed {
				message := "Backend did not confirm execution cancellation"
				if result != nil && strings.TrimSpace(result.Diagnostic) != "" {
					message += ": " + result.Diagnostic
				}
				return s.persistCancelFailure(runID, activeNodeID, message)
			}
		}
	}
	return s.finalizeCancellation(runID, activeNodeID)
}

func (s *Service) markCancellationIntent(runID string) (string, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, nodes, err := s.loadRun(runID)
	if err != nil {
		return "", 0, err
	}
	if run.Phase == PhaseCompleted {
		return "", 0, nil
	}
	if !run.CancelRequested || run.Phase != PhaseCancelling {
		run.CancelRequested, run.Phase, run.Conclusion, run.Reason, run.Summary, run.UpdatedAt = true, PhaseCancelling, "", "", "workflow cancellation requested", s.now().UTC()
		if err := s.persistRun(&run, nil, "run.cancelling", run.Summary); err != nil {
			return "", 0, err
		}
	}
	for index := range nodes {
		if nodes[index].CurrentAttempt > 0 && (nodes[index].Phase == NodePhaseRunning || nodes[index].Phase == NodePhaseWaiting) {
			return nodes[index].ID, nodes[index].CurrentAttempt, nil
		}
	}
	return "", 0, nil
}

func (s *Service) waitForCancellationHandle(ctx context.Context, runID, nodeID string, attemptNumber int) (AttemptSnapshot, *backend.ExecutionHandle, error) {
	deadline := time.Now().Add(cancelSessionPersistGrace)
	for {
		var attempt AttemptSnapshot
		if err := s.store.ReadAttempt(runID, nodeID, attemptNumber, &attempt); err != nil {
			if time.Now().After(deadline) {
				return AttemptSnapshot{}, nil, fmt.Errorf("read active Attempt while waiting for session persistence: %w", err)
			}
			if waitErr := s.waitCancellationPoll(ctx); waitErr != nil {
				return AttemptSnapshot{}, nil, waitErr
			}
			continue
		}
		candidate, err := s.registry.Get(attempt.Backend)
		if err != nil {
			return attempt, nil, err
		}
		if handle, err := s.executionHandle(candidate, attempt); err != nil {
			return attempt, nil, err
		} else if handle != nil {
			return attempt, handle, nil
		}
		switch attempt.LaunchState {
		case LaunchPrepared:
			return attempt, nil, nil
		case "":
			return attempt, nil, errors.New("cannot confirm cancellation because the active Attempt predates durable launch-state tracking and has no persisted execution handle")
		case LaunchFinishedWithoutHandle, LaunchFinishedWithoutSession:
			return attempt, nil, errors.New("cannot confirm cancellation because Backend launch finished without a persisted execution handle")
		case LaunchHandlePersisted, LaunchSessionPersisted:
			return attempt, nil, errors.New("cannot confirm cancellation because launch state says the execution handle was persisted but handle data is missing")
		case LaunchDispatching:
			if time.Now().After(deadline) {
				return attempt, nil, errors.New("cannot confirm cancellation because Backend launch did not persist an execution handle before the cancellation grace expired")
			}
		}
		if err := s.waitCancellationPoll(ctx); err != nil {
			return attempt, nil, err
		}
	}
}

func (s *Service) persistCancelFailure(runID, activeNodeID, message string) (WorkflowSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, nodes, err := s.loadRun(runID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	var activeNode *NodeSnapshot
	for index := range nodes {
		if nodes[index].ID == activeNodeID {
			activeNode = &nodes[index]
			break
		}
	}
	run.CancelRequested, run.Phase, run.Conclusion, run.Reason, run.Summary, run.UpdatedAt = true, PhaseWaiting, "", ReasonCancelFailed, message, s.now().UTC()
	if err := s.persistRun(&run, activeNode, "run.cancel_failed", run.Summary); err != nil {
		return WorkflowSnapshot{}, errors.Join(errors.New(message), err)
	}
	return run, errors.New(message)
}

func (s *Service) finalizeCancellation(runID, activeNodeID string) (WorkflowSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, nodes, err := s.loadRun(runID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if run.Phase == PhaseCompleted {
		return run, nil
	}
	var activeNode *NodeSnapshot
	for index := range nodes {
		node := &nodes[index]
		if node.ID == activeNodeID {
			activeNode = node
			if node.CurrentAttempt > 0 {
				var attempt AttemptSnapshot
				if err := s.store.ReadAttempt(runID, node.ID, node.CurrentAttempt, &attempt); err != nil {
					return WorkflowSnapshot{}, err
				}
				if attempt.Phase != NodePhaseCompleted || attempt.Conclusion != ConclusionCancelled {
					now := s.now().UTC()
					attempt.Phase, attempt.Conclusion, attempt.Reason, attempt.UpdatedAt, attempt.CompletedAt = NodePhaseCompleted, ConclusionCancelled, ReasonUserRequested, now, &now
					if err := s.writeAttempt(attempt, false); err != nil {
						return WorkflowSnapshot{}, err
					}
				}
			}
			now := s.now().UTC()
			node.Phase, node.Conclusion, node.Reason, node.UpdatedAt = NodePhaseCompleted, ConclusionCancelled, ReasonUserRequested, now
			if err := s.store.WriteNode(runID, node.ID, node); err != nil {
				return WorkflowSnapshot{}, err
			}
			run.Nodes[node.ID] = summarizeNode(*node)
		}
	}
	for index := range nodes {
		node := &nodes[index]
		if node.Phase == NodePhasePending || node.Phase == NodePhaseReady || (node.Phase == NodePhaseWaiting && node.Type == "approval") {
			node.Phase, node.Reason, node.UpdatedAt = NodePhaseSkipped, ReasonWorkflowCancelled, s.now().UTC()
			if err := s.store.WriteNode(runID, node.ID, node); err != nil {
				return WorkflowSnapshot{}, err
			}
			run.Nodes[node.ID] = summarizeNode(*node)
		}
	}
	run.CancelRequested, run.Phase, run.Conclusion, run.Reason, run.Summary, run.ActiveNodeID, run.UpdatedAt = true, PhaseCompleted, ConclusionCancelled, ReasonUserRequested, "workflow cancelled", "", s.now().UTC()
	if err := s.persistRun(&run, activeNode, "run.cancelled", run.Summary); err != nil {
		return WorkflowSnapshot{}, err
	}
	return run, nil
}

func (s *Service) resolveCancellationRequest(runID, requestID string, cancelErr error) error {
	response := store.CancelResponse{RequestID: requestID, Status: store.CancelResponseCompleted, UpdatedAt: s.now().UTC()}
	if cancelErr != nil {
		response.Status, response.Message = store.CancelResponseFailed, cancelErr.Error()
	}
	return s.store.ResolveCancellation(runID, response)
}

func (s *Service) waitCancellationPoll(ctx context.Context) error {
	if hook := s.testHooks.cancelRequestDelay; hook != nil {
		return hook(ctx)
	}
	timer := time.NewTimer(cancelRequestPollInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) stopController(runID string, generation uint64) {
	s.mu.Lock()
	active := s.controllers[runID]
	if active != nil && active.generation == generation {
		active.stopping = true
		active.cancel()
	}
	s.mu.Unlock()
}

func (s *Service) controller(runID string) *controller {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.controllers[runID]
}

func (s *Service) WaitControllers(ctx context.Context) error {
	for {
		s.mu.Lock()
		controllers := make([]*controller, 0, len(s.controllers))
		for _, active := range s.controllers {
			controllers = append(controllers, active)
		}
		s.mu.Unlock()
		if len(controllers) == 0 {
			return nil
		}
		for _, active := range controllers {
			select {
			case <-active.done:
				if active.err != nil {
					return active.err
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (s *Service) acquireLease(runID, command string, wait time.Duration) (*store.Lease, error) {
	deadline := time.Now().Add(wait)
	for {
		lease, err := s.leases.Acquire(runID, command)
		if err == nil {
			return lease, nil
		}
		var conflict *store.LeaseConflictError
		if !errors.As(err, &conflict) || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var errControllerInactive = errors.New("controller is no longer active")

func (s *Service) controllerMutation(runID string, generation uint64, point string, action func(*WorkflowSnapshot, []NodeSnapshot) error) error {
	if hook := s.testHooks.beforeControllerMutation; hook != nil {
		hook(point)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	active := s.controllers[runID]
	if active == nil || active.generation != generation || active.stopping {
		return errControllerInactive
	}
	run, nodes, err := s.loadRun(runID)
	if err != nil {
		return err
	}
	if run.CancelRequested || run.Phase == PhasePaused || run.Phase == PhaseCancelling || run.Phase == PhaseCompleted {
		return errControllerInactive
	}
	return action(&run, nodes)
}

func (s *Service) control(ctx context.Context, runID string, generation uint64) {
	for {
		if ctx.Err() != nil {
			return
		}
		run, nodes, err := s.loadRun(runID)
		if err != nil {
			return
		}
		var normalized workflow.Normalized
		if err := s.store.ReadWorkflow(runID, &normalized); err != nil {
			s.pauseControllerOnError(runID, generation, err)
			return
		}
		if run.Phase == PhaseCompleted || run.CancelRequested {
			return
		}
		if active := findActiveNode(nodes); active != nil && active.CurrentAttempt > 0 {
			progressed, err := s.reconcileAttempt(ctx, runID, active.ID, active.CurrentAttempt, generation)
			if err != nil {
				if ctx.Err() == nil && !errors.Is(err, errControllerInactive) {
					s.pauseControllerOnError(runID, generation, err)
				}
				return
			}
			if !progressed {
				return
			}
			continue
		}
		progressed, stop, err := s.scheduleOne(ctx, runID, generation, normalized)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, errControllerInactive) {
				s.pauseControllerOnError(runID, generation, err)
			}
			return
		}
		if stop || !progressed {
			return
		}
	}
}

type pendingLaunch struct {
	runID      string
	nodeID     string
	attempt    int
	backend    string
	launchSpec backend.AgentExecutionSpec
}

func (s *Service) scheduleOne(ctx context.Context, runID string, generation uint64, normalized workflow.Normalized) (bool, bool, error) {
	var launch *pendingLaunch
	progressed, stop := false, false
	point := "node.schedule"
	if _, nodes, err := s.loadRun(runID); err == nil {
		for _, nodeID := range normalized.TopologicalOrder {
			for _, node := range nodes {
				if node.ID == nodeID && (node.Phase == NodePhasePending || node.Phase == NodePhaseReady) {
					if normalized.Document.Nodes[nodeID].Type == "approval" {
						point = "approval.waiting"
					} else {
						point = "agent.prelaunch"
					}
					break
				}
			}
			if point != "node.schedule" {
				break
			}
		}
	}
	err := s.controllerMutation(runID, generation, point, func(run *WorkflowSnapshot, nodes []NodeSnapshot) error {
		nodeMap := make(map[string]*NodeSnapshot, len(nodes))
		results := make(map[string]workflow.Result)
		for index := range nodes {
			nodeMap[nodes[index].ID] = &nodes[index]
			if nodes[index].Result != nil {
				results[nodes[index].ID] = *nodes[index].Result
			}
		}
		for _, nodeID := range normalized.TopologicalOrder {
			node := nodeMap[nodeID]
			if node.Phase != NodePhasePending && node.Phase != NodePhaseReady {
				continue
			}
			definition := normalized.Document.Nodes[nodeID]
			stable, upstreamFailed := true, false
			for _, dependency := range definition.DependsOn {
				dependencyNode := nodeMap[dependency]
				if dependencyNode.Phase != NodePhaseCompleted && dependencyNode.Phase != NodePhaseSkipped {
					stable = false
					break
				}
				if dependencyNode.Conclusion == ConclusionFailed || dependencyNode.Conclusion == ConclusionIndeterminate || dependencyNode.Conclusion == ConclusionCancelled || dependencyNode.Reason == ReasonUpstreamFailed {
					upstreamFailed = true
				}
			}
			if !stable {
				continue
			}
			if upstreamFailed {
				node.Phase, node.Reason, node.UpdatedAt = NodePhaseSkipped, ReasonUpstreamFailed, s.now().UTC()
				if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
					return err
				}
				run.Nodes[node.ID] = summarizeNode(*node)
				progressed = true
				return s.persistRun(run, node, "node.skipped", "upstream failed")
			}
			if definition.When != nil {
				matches, err := workflow.Evaluate(*definition.When, results)
				if err != nil {
					return err
				}
				if !matches {
					node.Phase, node.Reason, node.UpdatedAt = NodePhaseSkipped, ReasonConditionFalse, s.now().UTC()
					if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
						return err
					}
					run.Nodes[node.ID] = summarizeNode(*node)
					progressed = true
					return s.persistRun(run, node, "node.skipped", "condition evaluated false")
				}
			}
			if definition.Type == "approval" {
				template, err := workflow.ParseTemplate(definition.Prompt, normalized.Document.Inputs, ancestorSet(normalized.Document, node.ID))
				if err != nil {
					return err
				}
				renderedPrompt, err := template.Render(normalized.Inputs, results)
				if err != nil {
					return err
				}
				now := s.now().UTC()
				node.Phase, node.Reason, node.UpdatedAt = NodePhaseWaiting, ReasonApprovalRequired, now
				run.Phase, run.Reason, run.ActiveNodeID, run.Summary, run.UpdatedAt = PhaseWaiting, ReasonApprovalRequired, node.ID, renderedPrompt, now
				if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
					return err
				}
				run.Nodes[node.ID] = summarizeNode(*node)
				progressed, stop = true, true
				return s.persistRun(run, node, "node.approval_required", renderedPrompt)
			}
			template, err := workflow.ParseTemplate(definition.Task, normalized.Document.Inputs, ancestorSet(normalized.Document, node.ID))
			if err != nil {
				return err
			}
			prompt, err := template.Render(normalized.Inputs, results)
			if err != nil {
				return err
			}
			if len(definition.RequiredSkills) > 0 {
				prompt = "Required skills: " + strings.Join(definition.RequiredSkills, ", ") + "\n\n" + prompt
			}
			if len([]byte(prompt)) > workflow.MaxPromptBytes {
				return fmt.Errorf("rendered prompt exceeds %d bytes", workflow.MaxPromptBytes)
			}
			number, now := node.CurrentAttempt+1, s.now().UTC()
			hash := sha256.Sum256([]byte(prompt))
			attempt := AttemptSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, RunID: run.ID, NodeID: node.ID, Number: number, Phase: NodePhaseRunning, LaunchState: LaunchPrepared,
				Backend: run.Backend, PromptHash: hex.EncodeToString(hash[:]), StartedAt: now, UpdatedAt: now}
			if err := s.writeAttempt(attempt, true); err != nil {
				return err
			}
			node.Phase, node.Reason, node.Conclusion, node.CurrentAttempt, node.UpdatedAt = NodePhaseRunning, "", "", number, now
			run.Phase, run.Reason, run.Conclusion, run.ActiveNodeID, run.Summary, run.UpdatedAt = PhaseRunning, "", "", node.ID, "launching Agent", now
			if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
				return err
			}
			run.Nodes[node.ID] = summarizeNode(*node)
			if err := s.persistRun(run, node, "node.running", "launching Agent attempt"); err != nil {
				return err
			}
			tool, runtimeKind := definition.Tool, definition.Runtime
			if tool == "" {
				tool = normalized.Document.Defaults.Tool
			}
			if tool == "" {
				tool = "codex"
			}
			if runtimeKind == "" {
				runtimeKind = normalized.Document.Defaults.Runtime
			}
			if runtimeKind == "" {
				runtimeKind = "local"
			}
			launch = &pendingLaunch{runID: run.ID, nodeID: node.ID, attempt: number, backend: run.Backend,
				launchSpec: backend.AgentExecutionSpec{RunID: run.ID, NodeID: node.ID, Attempt: number, Workspace: run.Project, Tool: tool, Runtime: runtimeKind,
					Instructions: prompt, RequiredSkills: append([]string(nil), definition.RequiredSkills...), ResultContract: backend.ResultContract{MaxBytes: workflow.MaxResultBytes}}}
			progressed = true
			return nil
		}
		conclusion, reason := ConclusionSucceeded, Reason("")
		for _, node := range nodes {
			if node.Conclusion == ConclusionFailed {
				conclusion, reason = ConclusionFailed, ReasonUpstreamFailed
				break
			}
			if node.Conclusion == ConclusionIndeterminate {
				conclusion = ConclusionIndeterminate
				break
			}
			if node.Conclusion == ConclusionRejected {
				conclusion = ConclusionRejected
			}
		}
		if conclusion == ConclusionRejected && hasEligibleRejectedBranch(normalized.Document, nodes) {
			conclusion, reason = ConclusionSucceeded, ""
		}
		run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.Summary, run.UpdatedAt = PhaseCompleted, conclusion, reason, "", "workflow completed", s.now().UTC()
		stop = true
		return s.persistRun(run, nil, "run.completed", run.Summary)
	})
	if err != nil {
		return false, true, err
	}
	if launch != nil {
		continueScheduling, err := s.launchAgent(ctx, generation, *launch)
		return progressed && continueScheduling, !continueScheduling, err
	}
	return progressed, stop, nil
}

func (s *Service) launchAgent(ctx context.Context, generation uint64, launch pendingLaunch) (bool, error) {
	if err := s.beginBackendLaunch(launch, generation); err != nil {
		return false, err
	}
	candidate, err := s.registry.Get(launch.backend)
	if err != nil {
		return false, err
	}
	handle, launchErr := candidate.Start(ctx, launch.launchSpec)
	if hook := s.testHooks.afterLaunch; hook != nil {
		hook()
	}
	if handle != nil && handle.ID != "" {
		if err := s.persistExecutionHandle(launch, *handle); err != nil {
			return false, err
		}
	} else if err := s.markLaunchFinishedWithoutHandle(launch); err != nil {
		return false, err
	}
	if launchErr != nil {
		if handle != nil && handle.ID != "" {
			return false, s.waiting(launch.runID, launch.nodeID, launch.attempt, generation, ReasonCompletionMissing, "Agent execution started but Backend post-start setup failed: "+launchErr.Error())
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return true, s.finishIndeterminate(launch.runID, launch.nodeID, launch.attempt, generation, "backend launch outcome is unknown: "+launchErr.Error())
	}
	if handle == nil || handle.ID == "" {
		return true, s.finishIndeterminate(launch.runID, launch.nodeID, launch.attempt, generation, "Backend launch returned no execution handle")
	}
	if ctx.Err() != nil {
		return false, errControllerInactive
	}
	return s.waitAttempt(ctx, launch.runID, launch.nodeID, launch.attempt, generation, candidate, *handle)
}

func (s *Service) beginBackendLaunch(launch pendingLaunch, generation uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	active := s.controllers[launch.runID]
	if active == nil || active.generation != generation || active.stopping {
		return errControllerInactive
	}
	run, _, err := s.loadRun(launch.runID)
	if err != nil {
		return err
	}
	if run.CancelRequested || run.Phase == PhaseCancelling || run.Phase == PhaseCompleted {
		return errControllerInactive
	}
	var attempt AttemptSnapshot
	if err := s.store.ReadAttempt(launch.runID, launch.nodeID, launch.attempt, &attempt); err != nil {
		return err
	}
	attempt.LaunchState, attempt.UpdatedAt = LaunchDispatching, s.now().UTC()
	return s.writeAttempt(attempt, false)
}

func (s *Service) markLaunchFinishedWithoutHandle(launch pendingLaunch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var attempt AttemptSnapshot
	if err := s.store.ReadAttempt(launch.runID, launch.nodeID, launch.attempt, &attempt); err != nil {
		return err
	}
	if attempt.Execution != nil && attempt.Execution.ID != "" {
		return nil
	}
	attempt.LaunchState, attempt.UpdatedAt = LaunchFinishedWithoutHandle, s.now().UTC()
	return s.writeAttempt(attempt, false)
}

func (s *Service) persistExecutionHandle(launch pendingLaunch, handle backend.ExecutionHandle) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := backend.ValidateExecutionHandle(handle); err != nil {
		return err
	}
	if handle.Backend != launch.backend {
		return fmt.Errorf("Backend %q returned a handle for %q", launch.backend, handle.Backend)
	}
	var attempt AttemptSnapshot
	if err := s.store.ReadAttempt(launch.runID, launch.nodeID, launch.attempt, &attempt); err != nil {
		return err
	}
	if attempt.Execution != nil && attempt.Execution.ID != "" && attempt.Execution.ID != handle.ID {
		return fmt.Errorf("attempt %d already owns execution %q", attempt.Number, attempt.Execution.ID)
	}
	attempt.LaunchState = LaunchHandlePersisted
	attempt.Execution = &handle
	attempt.UpdatedAt = s.now().UTC()
	return s.writeAttempt(attempt, false)
}

func (s *Service) executionHandle(candidate backend.AgentBackend, attempt AttemptSnapshot) (*backend.ExecutionHandle, error) {
	if attempt.Execution != nil {
		handle := *attempt.Execution
		if err := backend.ValidateExecutionHandle(handle); err != nil {
			return nil, err
		}
		if handle.Backend != attempt.Backend {
			return nil, fmt.Errorf("Attempt Backend %q does not match execution handle Backend %q", attempt.Backend, handle.Backend)
		}
		return &handle, nil
	}
	if attempt.legacyExecution == nil || attempt.legacyExecution.SessionID == "" {
		return nil, nil
	}
	decoder, ok := candidate.(backend.LegacySessionDecoder)
	if !ok {
		return nil, fmt.Errorf("Backend %q cannot decode a legacy M2.1.1 session", candidate.Name())
	}
	metadata := make(map[string]string, len(attempt.legacyExecution.Metadata))
	for key, value := range attempt.legacyExecution.Metadata {
		metadata[key] = value
	}
	return decoder.DecodeLegacySession(backend.Session{ID: attempt.legacyExecution.SessionID, Metadata: metadata})
}

func (s *Service) reconcileAttempt(ctx context.Context, runID, nodeID string, attemptNumber int, generation uint64) (bool, error) {
	var attempt AttemptSnapshot
	if err := s.store.ReadAttempt(runID, nodeID, attemptNumber, &attempt); err != nil {
		return false, err
	}
	candidate, err := s.registry.Get(attempt.Backend)
	if err != nil {
		return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonCompletionMissing, "Could not select persisted Backend: "+err.Error())
	}
	handle, err := s.executionHandle(candidate, attempt)
	if err != nil {
		return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonCompletionMissing, "Could not decode persisted execution handle: "+err.Error())
	}
	if handle == nil {
		return true, s.finishIndeterminate(runID, nodeID, attemptNumber, generation, "Attempt exists without a persisted execution handle; refusing duplicate launch")
	}
	return s.waitAttempt(ctx, runID, nodeID, attemptNumber, generation, candidate, *handle)
}

func (s *Service) waitAttempt(ctx context.Context, runID, nodeID string, attemptNumber int, generation uint64, candidate backend.AgentBackend, handle backend.ExecutionHandle) (bool, error) {
	for {
		observation, err := candidate.Observe(ctx, handle)
		if output, outputErr := candidate.Output(context.Background(), handle, 200); outputErr == nil {
			if writeErr := s.store.WriteNodeOutput(runID, nodeID, attemptNumber, output); writeErr != nil {
				return false, writeErr
			}
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		if err != nil {
			return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonCompletionMissing, "Backend observation transport failed: "+err.Error())
		}
		if observation == nil {
			return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonCompletionMissing, "Backend returned no execution observation")
		}
		switch observation.State {
		case backend.ObservationTerminal:
			if err := backend.ValidateExecutionObservation(*observation); err != nil {
				return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonInvalidResult, err.Error())
			}
			return s.finishResult(runID, nodeID, attemptNumber, generation, observation.Result)
		case backend.ObservationWaitingInput:
			return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonAgentWaitingInput, "Agent is waiting for input")
		case backend.ObservationResultPending:
			return s.reconcileResultPending(ctx, runID, nodeID, attemptNumber, generation, candidate, handle)
		case backend.ObservationLost, backend.ObservationExited:
			return true, s.finishIndeterminate(runID, nodeID, attemptNumber, generation, "Agent execution was lost without a valid terminal result")
		case backend.ObservationError:
			return s.finishResult(runID, nodeID, attemptNumber, generation, &backend.AgentResult{Status: "failed", Summary: "Agent execution reported an error"})
		case backend.ObservationActive:
			if err := s.waitStartupIdleReconcile(ctx); err != nil {
				return false, err
			}
		default:
			return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonCompletionMissing, "Backend returned unsupported observation state "+string(observation.State))
		}
	}
}

func (s *Service) reconcileResultPending(ctx context.Context, runID, nodeID string, attemptNumber int, generation uint64, candidate backend.AgentBackend, handle backend.ExecutionHandle) (bool, error) {
	for check := 0; check < startupIdleReconcileChecks; check++ {
		if err := s.waitStartupIdleReconcile(ctx); err != nil {
			return false, err
		}
		observation, err := candidate.Observe(ctx, handle)
		if err != nil {
			return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonCompletionMissing, "Backend result reconciliation failed: "+err.Error())
		}
		if observation == nil {
			return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonCompletionMissing, "Backend result reconciliation returned no observation")
		}
		switch observation.State {
		case backend.ObservationTerminal:
			if err := backend.ValidateExecutionObservation(*observation); err != nil {
				return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonInvalidResult, err.Error())
			}
			return s.finishResult(runID, nodeID, attemptNumber, generation, observation.Result)
		case backend.ObservationActive:
			return s.waitAttempt(ctx, runID, nodeID, attemptNumber, generation, candidate, handle)
		case backend.ObservationWaitingInput:
			return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonAgentWaitingInput, "Agent is waiting for input")
		case backend.ObservationResultPending:
			continue
		case backend.ObservationExited, backend.ObservationLost:
			return true, s.finishIndeterminate(runID, nodeID, attemptNumber, generation, "Agent execution was lost without a valid terminal result")
		case backend.ObservationError:
			return s.finishResult(runID, nodeID, attemptNumber, generation, &backend.AgentResult{Status: "failed", Summary: "Agent execution reported an error"})
		default:
			return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonCompletionMissing, "Backend result reconciliation returned unsupported state "+string(observation.State))
		}
	}
	return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonCompletionMissing, "Agent result remained unavailable after bounded reconciliation")
}

func (s *Service) waitStartupIdleReconcile(ctx context.Context) error {
	if hook := s.testHooks.idleReconcileDelay; hook != nil {
		return hook(ctx)
	}
	timer := time.NewTimer(startupIdleReconcileDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) finishResult(runID, nodeID string, attemptNumber int, generation uint64, result *backend.AgentResult) (bool, error) {
	if result == nil {
		return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonCompletionMissing, "Backend returned no result")
	}
	normalized := workflow.Result{Summary: result.Summary, Artifacts: result.Artifacts, Warnings: result.Warnings, Checks: result.Checks,
		Usage: workflow.Usage{InputTokensEstimated: result.Usage.InputTokensEstimated, OutputTokensEstimated: result.Usage.OutputTokensEstimated}}
	switch strings.ToLower(result.Status) {
	case "succeeded", "completed":
		if err := workflow.ValidateResult(normalized); err != nil {
			return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonInvalidResult, err.Error())
		}
		err := s.controllerMutation(runID, generation, "result.succeeded", func(run *WorkflowSnapshot, nodes []NodeSnapshot) error {
			node, err := findNode(nodes, nodeID)
			if err != nil {
				return err
			}
			var attempt AttemptSnapshot
			if err := s.store.ReadAttempt(runID, nodeID, attemptNumber, &attempt); err != nil {
				return err
			}
			now := s.now().UTC()
			attempt.Phase, attempt.Conclusion, attempt.ResultConsumed, attempt.UpdatedAt, attempt.CompletedAt = NodePhaseCompleted, ConclusionSucceeded, true, now, &now
			node.Phase, node.Conclusion, node.Reason, node.Result, node.UpdatedAt = NodePhaseCompleted, ConclusionSucceeded, "", &normalized, now
			if err := s.store.WriteResult(runID, nodeID, attemptNumber, normalized); err != nil {
				return err
			}
			if err := s.store.WriteNode(runID, nodeID, node); err != nil {
				return err
			}
			if err := s.writeAttempt(attempt, false); err != nil {
				return err
			}
			run.Nodes[nodeID], run.ActiveNodeID, run.Phase, run.Reason, run.Summary, run.UpdatedAt = summarizeNode(*node), "", PhaseRunning, "", normalized.Summary, now
			return s.persistRun(run, node, "node.completed", normalized.Summary)
		})
		return err == nil, err
	case "failed", "error":
		err := s.controllerMutation(runID, generation, "result.failed", func(run *WorkflowSnapshot, nodes []NodeSnapshot) error {
			node, err := findNode(nodes, nodeID)
			if err != nil {
				return err
			}
			var attempt AttemptSnapshot
			if err := s.store.ReadAttempt(runID, nodeID, attemptNumber, &attempt); err != nil {
				return err
			}
			now := s.now().UTC()
			attempt.Phase, attempt.Conclusion, attempt.ResultConsumed, attempt.UpdatedAt, attempt.CompletedAt = NodePhaseCompleted, ConclusionFailed, true, now, &now
			node.Phase, node.Conclusion, node.Result, node.UpdatedAt = NodePhaseCompleted, ConclusionFailed, &normalized, now
			if err := s.store.WriteResult(runID, nodeID, attemptNumber, normalized); err != nil {
				return err
			}
			if err := s.store.WriteNode(runID, nodeID, node); err != nil {
				return err
			}
			if err := s.writeAttempt(attempt, false); err != nil {
				return err
			}
			run.Nodes[nodeID], run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.Summary, run.UpdatedAt = summarizeNode(*node), PhaseCompleted, ConclusionFailed, ReasonUpstreamFailed, "", result.Summary, now
			if err := s.skipUnstarted(run, ReasonUpstreamFailed); err != nil {
				return err
			}
			return s.persistRun(run, node, "run.completed", run.Summary)
		})
		return false, err
	case "waiting_input", "blocked", "waitinginput":
		return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonAgentWaitingInput, result.Summary)
	case "completion_missing", "idle":
		return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonCompletionMissing, result.Summary)
	case "invalid_result":
		return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonInvalidResult, result.Summary)
	case "indeterminate", "exited", "lost":
		return true, s.finishIndeterminate(runID, nodeID, attemptNumber, generation, result.Summary)
	default:
		return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonCompletionMissing, "unrecognized Backend result status "+result.Status)
	}
}

func (s *Service) waiting(runID, nodeID string, attemptNumber int, generation uint64, reason Reason, message string) error {
	return s.controllerMutation(runID, generation, "node.waiting", func(run *WorkflowSnapshot, nodes []NodeSnapshot) error {
		node, err := findNode(nodes, nodeID)
		if err != nil {
			return err
		}
		var attempt AttemptSnapshot
		if err := s.store.ReadAttempt(runID, nodeID, attemptNumber, &attempt); err != nil {
			return err
		}
		now := s.now().UTC()
		attempt.Phase, attempt.Reason, attempt.UpdatedAt = NodePhaseWaiting, reason, now
		node.Phase, node.Reason, node.UpdatedAt = NodePhaseWaiting, reason, now
		run.Phase, run.Reason, run.ActiveNodeID, run.Summary, run.UpdatedAt = PhaseWaiting, reason, node.ID, message, now
		if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
			return err
		}
		if err := s.writeAttempt(attempt, false); err != nil {
			return err
		}
		run.Nodes[node.ID] = summarizeNode(*node)
		return s.persistRun(run, node, "node.waiting", message)
	})
}

func (s *Service) finishIndeterminate(runID, nodeID string, attemptNumber int, generation uint64, message string) error {
	return s.controllerMutation(runID, generation, "result.indeterminate", func(run *WorkflowSnapshot, nodes []NodeSnapshot) error {
		node, err := findNode(nodes, nodeID)
		if err != nil {
			return err
		}
		var attempt AttemptSnapshot
		if err := s.store.ReadAttempt(runID, nodeID, attemptNumber, &attempt); err != nil {
			return err
		}
		now := s.now().UTC()
		attempt.Phase, attempt.Conclusion, attempt.UpdatedAt, attempt.CompletedAt = NodePhaseCompleted, ConclusionIndeterminate, now, &now
		node.Phase, node.Conclusion, node.Reason, node.UpdatedAt = NodePhaseCompleted, ConclusionIndeterminate, "", now
		run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.Summary, run.UpdatedAt = PhaseCompleted, ConclusionIndeterminate, "", "", message, now
		if err := s.writeAttempt(attempt, false); err != nil {
			return err
		}
		if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
			return err
		}
		run.Nodes[node.ID] = summarizeNode(*node)
		if err := s.skipUnstarted(run, ReasonUpstreamFailed); err != nil {
			return err
		}
		return s.persistRun(run, node, "run.completed", message)
	})
}

func (s *Service) applyAction(run *WorkflowSnapshot, action ResumeAction) error {
	if action.NodeID == "" {
		return errors.New("resume action nodeId is required")
	}
	var node NodeSnapshot
	if err := s.store.ReadNode(run.ID, action.NodeID, &node); err != nil {
		return err
	}
	switch action.Type {
	case "approve", "reject":
		decision := strings.TrimSuffix(action.Type, "e")
		if action.Type == "approve" {
			decision = "approved"
		} else {
			decision = "rejected"
		}
		if node.Result != nil && node.Result.Decision != "" {
			if node.Result.Decision == decision && node.Result.Reason == action.Reason {
				if run.Phase == PhaseCompleted {
					return nil
				}
				now := s.now().UTC()
				run.Nodes[node.ID], run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.Summary, run.UpdatedAt = summarizeNode(node), PhaseRunning, "", "", "", "approval "+decision, now
				return s.persistRun(run, &node, "node.approval_decided", run.Summary)
			}
			return fmt.Errorf("approval node %q already has conflicting decision %q", node.ID, node.Result.Decision)
		}
		if node.Type != "approval" || node.Phase != NodePhaseWaiting || node.Reason != ReasonApprovalRequired {
			return fmt.Errorf("node %q is not the current waiting approval", node.ID)
		}
		result := &workflow.Result{Decision: decision, Reason: action.Reason}
		now := s.now().UTC()
		node.Phase, node.Result, node.Reason, node.UpdatedAt = NodePhaseCompleted, result, "", now
		if decision == "approved" {
			node.Conclusion = ConclusionSucceeded
		} else {
			node.Conclusion = ConclusionRejected
		}
		if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
			return err
		}
		run.Nodes[node.ID], run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.Summary, run.UpdatedAt = summarizeNode(node), PhaseRunning, "", "", "", "approval "+decision, now
		return s.persistRun(run, &node, "node.approval_decided", run.Summary)
	case "retry":
		allowed := node.Phase == NodePhaseWaiting && (node.Reason == ReasonAgentWaitingInput || node.Reason == ReasonCompletionMissing || node.Reason == ReasonInvalidResult)
		allowed = allowed || (node.Phase == NodePhaseCompleted && (node.Conclusion == ConclusionFailed || node.Conclusion == ConclusionIndeterminate))
		if !allowed || node.CurrentAttempt < 1 {
			return fmt.Errorf("node %q is not in a retryable state", node.ID)
		}
		if node.Conclusion == ConclusionIndeterminate && !action.AcknowledgeDuplicateRisk {
			return fmt.Errorf("retrying indeterminate node %q requires acknowledgeDuplicateRisk", node.ID)
		}
		now := s.now().UTC()
		node.Phase, node.Conclusion, node.Reason, node.Result, node.UpdatedAt = NodePhaseReady, "", "", nil, now
		if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
			return err
		}
		run.Nodes[node.ID] = summarizeNode(node)
		if err := s.resetDownstream(run, node.ID); err != nil {
			return err
		}
		run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.Summary, run.CancelRequested, run.UpdatedAt = PhaseRunning, "", "", "", "explicit retry requested", false, now
		return s.persistRun(run, &node, "node.retry_requested", run.Summary)
	default:
		return fmt.Errorf("unknown resume action %q", action.Type)
	}
}

func validateResumeAction(action ResumeAction) error {
	if action.NodeID == "" {
		return errors.New("resume action nodeId is required")
	}
	switch action.Type {
	case "approve":
		if action.Reason != "" || action.AcknowledgeDuplicateRisk {
			return errors.New("approve action does not accept reason or acknowledgeDuplicateRisk")
		}
	case "reject":
		if action.AcknowledgeDuplicateRisk {
			return errors.New("reject action does not accept acknowledgeDuplicateRisk")
		}
	case "retry":
		if action.Reason != "" {
			return errors.New("retry action does not accept reason")
		}
	default:
		return fmt.Errorf("unknown resume action %q", action.Type)
	}
	return nil
}

func (s *Service) resetDownstream(run *WorkflowSnapshot, target string) error {
	var normalized workflow.Normalized
	if err := s.store.ReadWorkflow(run.ID, &normalized); err != nil {
		return err
	}
	descendants := map[string]bool{target: true}
	for _, id := range normalized.TopologicalOrder {
		for _, dependency := range normalized.Document.Nodes[id].DependsOn {
			if descendants[dependency] {
				descendants[id] = true
			}
		}
	}
	for id := range descendants {
		if id == target {
			continue
		}
		var node NodeSnapshot
		if err := s.store.ReadNode(run.ID, id, &node); err != nil {
			return err
		}
		if node.Phase == NodePhaseSkipped {
			node.Phase, node.Reason, node.Conclusion, node.Result, node.UpdatedAt = NodePhasePending, "", "", nil, s.now().UTC()
			if err := s.store.WriteNode(run.ID, id, node); err != nil {
				return err
			}
			run.Nodes[id] = summarizeNode(node)
		}
	}
	return nil
}

func (s *Service) skipUnstarted(run *WorkflowSnapshot, reason Reason) error {
	for _, id := range run.TopologicalOrder {
		var node NodeSnapshot
		if err := s.store.ReadNode(run.ID, id, &node); err != nil {
			return err
		}
		if node.Phase == NodePhasePending || node.Phase == NodePhaseReady || (node.Phase == NodePhaseWaiting && node.Type == "approval") {
			node.Phase, node.Reason, node.UpdatedAt = NodePhaseSkipped, reason, s.now().UTC()
			if err := s.store.WriteNode(run.ID, id, node); err != nil {
				return err
			}
			run.Nodes[id] = summarizeNode(node)
		}
	}
	return nil
}

func (s *Service) resetEventSequence(runID string) (uint64, error) {
	return resetEventSequence(s.store, runID)
}

func resetEventSequence(state *store.Store, runID string) (uint64, error) {
	var sequence uint64
	err := state.ReadEvents(runID, func(raw json.RawMessage) error {
		var event struct {
			Sequence uint64 `json:"sequence"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return err
		}
		if event.Sequence <= sequence {
			return fmt.Errorf("event sequence is not strictly increasing")
		}
		sequence = event.Sequence
		return nil
	})
	return sequence, err
}

func (s *Service) persistRun(run *WorkflowSnapshot, node *NodeSnapshot, eventType, message string) error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if err := ValidateWorkflowSnapshot(*run); err != nil {
		return err
	}
	if err := s.store.WriteSnapshot(run.ID, run); err != nil {
		return err
	}
	sequence, err := s.resetEventSequence(run.ID)
	if err != nil {
		return err
	}
	event := WorkflowEvent{ProtocolVersion: protocolVersion, RunID: run.ID, Sequence: sequence + 1, Type: eventType, Phase: run.Phase,
		Conclusion: run.Conclusion, Reason: run.Reason, Message: message, Timestamp: s.now().UTC()}
	if node != nil {
		event.NodeID, event.NodePhase = node.ID, node.Phase
	}
	if err := s.store.AppendEvent(run.ID, event); err != nil {
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

func (s *Service) writeAttempt(attempt AttemptSnapshot, create bool) error {
	if err := ValidateAttemptSnapshot(attempt); err != nil {
		return err
	}
	if create {
		return s.store.WriteAttempt(attempt.RunID, attempt.NodeID, attempt.Number, attempt)
	}
	return s.store.UpdateAttempt(attempt.RunID, attempt.NodeID, attempt.Number, attempt)
}

func (s *Service) loadRun(runID string) (WorkflowSnapshot, []NodeSnapshot, error) {
	return s.loadRunFrom(s.store, runID)
}

func (s *Service) loadRunFrom(state *store.Store, runID string) (WorkflowSnapshot, []NodeSnapshot, error) {
	var run WorkflowSnapshot
	if err := state.ReadSnapshot(runID, &run); err != nil {
		return run, nil, err
	}
	if err := ValidateWorkflowSnapshot(run); err != nil {
		return run, nil, fmt.Errorf("invalid run snapshot: %w", err)
	}
	if run.StateSchemaVersion == 0 {
		run.StateSchemaVersion = 1
	}
	nodes := make([]NodeSnapshot, 0, len(run.TopologicalOrder))
	for _, id := range run.TopologicalOrder {
		var node NodeSnapshot
		if err := state.ReadNode(runID, id, &node); err != nil {
			return run, nil, err
		}
		if err := ValidateNodeSnapshot(node); err != nil {
			return run, nil, fmt.Errorf("invalid node %q: %w", id, err)
		}
		if node.StateSchemaVersion == 0 {
			node.StateSchemaVersion = 1
		}
		nodes = append(nodes, node)
	}
	return run, nodes, nil
}

func (s *Service) pauseControllerOnError(runID string, generation uint64, cause error) {
	err := s.controllerMutation(runID, generation, "controller.error", func(run *WorkflowSnapshot, _ []NodeSnapshot) error {
		run.Phase, run.Reason, run.Summary, run.UpdatedAt = PhaseWaiting, ReasonCompletionMissing, cause.Error(), s.now().UTC()
		return s.persistRun(run, nil, "run.waiting", cause.Error())
	})
	if err == nil || errors.Is(err, errControllerInactive) {
		return
	}
	s.mu.Lock()
	if active := s.controllers[runID]; active != nil && active.generation == generation {
		active.err = errors.Join(cause, err)
	}
	s.mu.Unlock()
}

func summarizeNode(node NodeSnapshot) NodeSummary {
	return NodeSummary{ID: node.ID, Type: node.Type, Phase: node.Phase, Conclusion: node.Conclusion, Reason: node.Reason, Diagnostic: node.Diagnostic, CurrentAttempt: node.CurrentAttempt}
}

func validateActiveAttempt(run WorkflowSnapshot, node NodeSnapshot, attempt AttemptSnapshot) error {
	if err := ValidateAttemptSnapshot(attempt); err != nil {
		return fmt.Errorf("invalid active Attempt for node %q: %w", node.ID, err)
	}
	if attempt.RunID != run.ID || attempt.NodeID != node.ID || attempt.Number != node.CurrentAttempt {
		return fmt.Errorf("active Attempt identity does not match run %q node %q attempt %d", run.ID, node.ID, node.CurrentAttempt)
	}
	if attempt.Backend != run.Backend {
		return fmt.Errorf("active Attempt Backend %q does not match run Backend %q", attempt.Backend, run.Backend)
	}
	if attempt.Phase == NodePhaseCompleted || node.Phase == NodePhaseCompleted || node.Phase == NodePhaseSkipped {
		return fmt.Errorf("active node %q and Attempt phases are inconsistent", node.ID)
	}
	return nil
}

func findNode(nodes []NodeSnapshot, nodeID string) (*NodeSnapshot, error) {
	for index := range nodes {
		if nodes[index].ID == nodeID {
			return &nodes[index], nil
		}
	}
	return nil, fmt.Errorf("node %q is missing from run snapshot", nodeID)
}
func findActiveNode(nodes []NodeSnapshot) *NodeSnapshot {
	for index := range nodes {
		if nodes[index].Phase == NodePhaseRunning || (nodes[index].Phase == NodePhaseWaiting && nodes[index].Type == "agent") {
			return &nodes[index]
		}
	}
	return nil
}

func ancestorSet(doc workflow.Document, nodeID string) map[string]bool {
	result := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		for _, dependency := range doc.Nodes[id].DependsOn {
			if !result[dependency] {
				result[dependency] = true
				visit(dependency)
			}
		}
	}
	visit(nodeID)
	return result
}

func hasEligibleRejectedBranch(doc workflow.Document, nodes []NodeSnapshot) bool {
	state := map[string]NodeSnapshot{}
	for _, node := range nodes {
		state[node.ID] = node
	}
	for id, definition := range doc.Nodes {
		if definition.When != nil && mentionsRejected(*definition.When) {
			node := state[id]
			if node.Reason != ReasonConditionFalse {
				return true
			}
		}
	}
	return false
}

func mentionsRejected(condition workflow.Condition) bool {
	if condition.Node != "" && condition.Field == "result.decision" && fmt.Sprint(condition.Equals) == "rejected" {
		return true
	}
	for _, child := range condition.All {
		if mentionsRejected(child) {
			return true
		}
	}
	for _, child := range condition.Any {
		if mentionsRejected(child) {
			return true
		}
	}
	return condition.Not != nil && mentionsRejected(*condition.Not)
}

func newRunID() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return "run-" + hex.EncodeToString(value), nil
}
