package run

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/contextcompiler"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

const protocolVersion = 2

// State schema versioning is independent from the JSON-RPC protocol version.
const stateSchemaVersion = 3

const (
	startupIdleReconcileChecks     = 20
	startupIdleReconcileDelay      = 500 * time.Millisecond
	controllerRecoveryAttempts     = 8
	controllerRecoveryInitialDelay = 100 * time.Millisecond
	controllerRecoveryMaxDelay     = 5 * time.Second
	controllerRecoveredSummary     = "controller recovered after heartbeat failure"
	cancelRequestPollInterval      = 25 * time.Millisecond
	cancelStateReadGrace           = 2 * time.Second
	cancelSessionPersistGrace      = 30 * time.Second
	cancelBackendTimeout           = 30 * time.Second
)

type StartRequest struct {
	Project string `json:"project"`
	Driver  string `json:"driver,omitempty"`
	Target  string `json:"target,omitempty"`
	Backend string `json:"backend,omitempty"`
	Tool    string `json:"tool"`
	Runtime string `json:"runtime"`
	Task    string `json:"task"`
}

type StartWorkflowRequest struct {
	RunID              string                   `json:"-"`
	InitializationTime time.Time                `json:"-"`
	Project            string                   `json:"project"`
	Driver             string                   `json:"driver,omitempty"`
	Target             string                   `json:"target,omitempty"`
	Backend            string                   `json:"backend,omitempty"`
	Filename           string                   `json:"filename"`
	Content            string                   `json:"content"`
	Inputs             map[string]any           `json:"inputs,omitempty"`
	Normalized         *workflow.Normalized     `json:"normalized,omitempty"`
	ContextBindings    workflow.ContextBindings `json:"contextBindings,omitempty"`
}

type ResumeAction struct {
	ActionID                 string         `json:"actionId,omitempty"`
	ActionRequestHash        string         `json:"actionRequestHash,omitempty"`
	Type                     string         `json:"type"`
	NodeID                   string         `json:"nodeId"`
	ExpectedAttempt          *int           `json:"expectedAttempt,omitempty"`
	Reason                   string         `json:"reason,omitempty"`
	Answers                  map[string]any `json:"answers,omitempty"`
	AcknowledgeDuplicateRisk bool           `json:"acknowledgeDuplicateRisk,omitempty"`
}

type actionIntent struct {
	Version     int                  `json:"version"`
	ActionID    string               `json:"actionId"`
	RequestHash string               `json:"requestHash"`
	Type        string               `json:"type"`
	NodeID      string               `json:"nodeId"`
	Phase       string               `json:"phase"`
	Applied     int                  `json:"applied"`
	EventType   string               `json:"eventType"`
	Summary     string               `json:"summary"`
	Action      ResumeAction         `json:"action"`
	Mutations   []actionNodeMutation `json:"mutations"`
}

type actionNodeMutation struct {
	Before NodeSnapshot `json:"before"`
	After  NodeSnapshot `json:"after"`
}

const (
	actionIntentVersion  = 2
	actionIntentPlanned  = "planned"
	actionIntentApplying = "applying"
	maxActionReceipts    = 256
)

type ResumeRequest struct {
	RunID                string        `json:"runId"`
	ExpectedStateVersion *uint64       `json:"expectedStateVersion,omitempty"`
	Action               *ResumeAction `json:"action,omitempty"`
}

type CancelRequest struct {
	RunID                string
	ExpectedStateVersion *uint64
	ActionID             string
	ActionRequestHash    string
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
	controllerRecoveryDelay  func(context.Context) error
	cancelRequestDelay       func(context.Context) error
}

type Service struct {
	registry       *backend.Registry
	defaultBackend string
	store          *store.Store
	leases         *store.LeaseManager
	now            func() time.Time
	getenv         func(string) string

	mu             sync.RWMutex
	controllers    map[string]*controller
	nextGeneration uint64
	startMu        sync.Mutex
	persistMu      sync.Mutex
	sinkMu         sync.RWMutex
	eventSinks     map[uint64]EventSink
	nextSinkID     uint64
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
		defaultBackend = "codex"
	}
	service := &Service{registry: registry, defaultBackend: defaultBackend, store: state, now: time.Now, getenv: os.Getenv, controllers: make(map[string]*controller), eventSinks: make(map[uint64]EventSink)}
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
	s.eventSinks = make(map[uint64]EventSink)
	if sink != nil {
		s.nextSinkID++
		s.eventSinks[s.nextSinkID] = sink
	}
}

func (s *Service) AddEventSink(sink EventSink) func() {
	if sink == nil {
		return func() {}
	}
	s.sinkMu.Lock()
	s.nextSinkID++
	id := s.nextSinkID
	s.eventSinks[id] = sink
	s.sinkMu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.sinkMu.Lock()
			delete(s.eventSinks, id)
			s.sinkMu.Unlock()
		})
	}
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
	if name == "direct" {
		name = "codex"
	}
	if name == "ccpanes" {
		return nil, "", fmt.Errorf("CC-Panes is retired for new Runs; historical snapshots remain readable but cannot be selected")
	}
	candidate, err := s.registry.Get(name)
	if err != nil {
		// M4 compatibility for embedded single-Driver tests and callers that
		// still express tool=codex separately from their injected backend name.
		// Production registers the formal "codex" Driver and never takes this
		// path. CC-Panes is intentionally excluded.
		if name == "codex" && s.defaultBackend != "ccpanes" {
			if fallback, fallbackErr := s.registry.Get(s.defaultBackend); fallbackErr == nil {
				return fallback, s.defaultBackend, nil
			}
		}
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
	driver, target, warnings, err := resolveStartSelection(request.Driver, request.Target, request.Backend, request.Tool, request.Runtime)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	doc := workflow.Document{
		APIVersion: workflow.APIVersion, Name: "ad-hoc", Inputs: map[string]workflow.InputDeclaration{},
		Defaults:  workflow.Defaults{Agent: workflow.AgentSelection{Driver: driver, Target: target}},
		Execution: workflow.Execution{MaxConcurrency: 1},
		Nodes:     map[string]workflow.Node{"agent-1": {Type: "agent", Task: request.Task, DependsOn: []string{}, RequiredSkills: []string{}}},
	}
	order, err := workflow.Validate(doc)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	candidate, backendName, err := s.selectBackend(driver, "")
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	doc.Defaults.Agent.Driver = backendName
	normalized := workflow.Normalized{Document: doc, Inputs: map[string]any{}, TopologicalOrder: order, Warnings: warnings}
	if request.Driver == "" && request.Backend == "" && request.Tool == "" && s.getenv != nil && strings.TrimSpace(s.getenv("FISHYUME_BACKEND")) != "" {
		normalized.Warnings = append(normalized.Warnings, "FISHYUME_BACKEND is deprecated; use explicit driver selection")
	}
	if err := validateBackendCapabilities(candidate, normalized); err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := ensureBackendReady(ctx, candidate, request.Project); err != nil {
		return WorkflowSnapshot{}, err
	}
	return s.startNormalized(ctx, request.Project, normalized, "run", backendName, "", time.Time{})
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
	if err := workflow.ValidateContextBindings(normalized.Document, request.ContextBindings); err != nil {
		return WorkflowSnapshot{}, err
	}
	normalized.ContextBindings = cloneContextBindings(request.ContextBindings)
	workflowDriver, workflowTarget, err := workflow.ResolveAgent(normalized.Document.Defaults, workflow.Node{})
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	driver, target, warnings, err := resolveStartSelection(request.Driver, request.Target, request.Backend, "", "")
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if request.Driver == "" && request.Backend == "" {
		driver = workflowDriver
	}
	if request.Target == "" {
		target = workflowTarget
	}
	candidate, backendName, err := s.selectBackend(driver, workflowDriver)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	normalized.Document.Defaults.Agent = workflow.AgentSelection{Driver: backendName, Target: target}
	normalized.Document.Defaults.Backend, normalized.Document.Defaults.Tool, normalized.Document.Defaults.Runtime = "", "", ""
	normalized.Warnings = append(normalized.Warnings, warnings...)
	if request.Driver == "" && request.Backend == "" && workflowDriver == "" && s.getenv != nil && strings.TrimSpace(s.getenv("FISHYUME_BACKEND")) != "" {
		normalized.Warnings = append(normalized.Warnings, "FISHYUME_BACKEND is deprecated; use defaults.agent.driver")
	}
	if err := validateBackendCapabilities(candidate, normalized); err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := ensureBackendReady(ctx, candidate, request.Project); err != nil {
		return WorkflowSnapshot{}, err
	}
	return s.startNormalized(ctx, request.Project, normalized, "run", backendName, request.RunID, request.InitializationTime)
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
	if capabilities.MaxConcurrentAgents < 0 {
		return fmt.Errorf("Backend %q declares negative maxConcurrentAgents", candidate.Name())
	}
	if _, err := EffectiveConcurrency(normalized.Document.Execution.MaxConcurrency, capabilities.MaxConcurrentAgents); err != nil {
		return fmt.Errorf("Backend %q concurrency capability: %w", candidate.Name(), err)
	}
	for _, nodeID := range normalized.TopologicalOrder {
		node := normalized.Document.Nodes[nodeID]
		if node.Type != "agent" {
			continue
		}
		driver, target, err := workflow.ResolveAgent(normalized.Document.Defaults, node)
		if err != nil {
			return err
		}
		if driver != candidate.Name() {
			return fmt.Errorf("Driver %q cannot execute node %q resolved to Driver %q", candidate.Name(), nodeID, driver)
		}
		if !containsCapability(capabilities.Runtimes, target) {
			return fmt.Errorf("Driver %q does not support target %q required by node %q", candidate.Name(), target, nodeID)
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

func legacyToolForDriver(registry *backend.Registry, driver string) string {
	candidate, err := registry.Get(driver)
	if err == nil {
		capabilities := candidate.Capabilities()
		if len(capabilities.Tools) > 0 && strings.TrimSpace(capabilities.Tools[0]) != "" {
			return capabilities.Tools[0]
		}
	}
	return driver
}

func resolveStartSelection(driver, target, legacyBackend, legacyTool, legacyRuntime string) (string, string, []string, error) {
	driver = strings.TrimSpace(driver)
	target = strings.TrimSpace(target)
	legacyBackend = strings.TrimSpace(legacyBackend)
	legacyTool = strings.TrimSpace(legacyTool)
	legacyRuntime = strings.TrimSpace(legacyRuntime)
	warnings := make([]string, 0, 1)
	legacyDriver := legacyBackend
	if legacyDriver == "direct" {
		legacyDriver = "codex"
	}
	if legacyDriver != "" && legacyTool != "" && legacyDriver != legacyTool {
		return "", "", nil, fmt.Errorf("deprecated backend %q conflicts with tool %q", legacyBackend, legacyTool)
	}
	if legacyDriver == "" {
		legacyDriver = legacyTool
	}
	if driver == "" {
		driver = legacyDriver
	} else if legacyDriver != "" && driver != legacyDriver {
		return "", "", nil, fmt.Errorf("driver %q conflicts with deprecated backend/tool selection %q", driver, legacyDriver)
	}
	if target == "" {
		target = legacyRuntime
	} else if legacyRuntime != "" && target != legacyRuntime {
		return "", "", nil, fmt.Errorf("target %q conflicts with deprecated runtime %q", target, legacyRuntime)
	}
	if target == "" {
		target = "local"
	}
	if legacyBackend != "" || legacyTool != "" || legacyRuntime != "" {
		warnings = append(warnings, "backend/tool/runtime are deprecated; use driver/target")
	}
	return driver, target, warnings, nil
}

func agentResultContractSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"required":["status","summary","artifacts","warnings","checks","questions"],"properties":{"status":{"type":"string","enum":["succeeded","failed","needs_input","indeterminate"]},"summary":{"type":"string","minLength":1,"maxLength":16384},"artifacts":{"type":"array","items":{"type":"string"},"maxItems":256},"warnings":{"type":"array","items":{"type":"string"},"maxItems":256},"checks":{"type":"array","items":{"type":"string"},"maxItems":256},"questions":{"type":"array","maxItems":32,"items":{"type":"object","additionalProperties":false,"required":["id","prompt","choices","required"],"properties":{"id":{"type":"string"},"prompt":{"type":"string"},"choices":{"type":"array","items":{"type":"string"},"maxItems":256},"required":{"type":"boolean"}}}}}}`)
}

func (s *Service) startNormalized(_ context.Context, project string, normalized workflow.Normalized, command, backendName, requestedRunID string, initializationTime time.Time) (WorkflowSnapshot, error) {
	if s.store == nil || s.leases == nil {
		return WorkflowSnapshot{}, errors.New("workflow state directory is unavailable")
	}
	s.startMu.Lock()
	defer s.startMu.Unlock()
	id := strings.TrimSpace(requestedRunID)
	if id == "" {
		var err error
		id, err = newRunID()
		if err != nil {
			return WorkflowSnapshot{}, err
		}
	}
	if err := s.store.InitWorkflowRun(id); err != nil {
		return WorkflowSnapshot{}, err
	}
	if err := s.store.EnsureWorkflow(id, normalized); err != nil {
		return WorkflowSnapshot{}, err
	}
	candidate, err := s.registry.Get(backendName)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	effectiveConcurrency, err := EffectiveConcurrency(normalized.Document.Execution.MaxConcurrency, candidate.Capabilities().MaxConcurrentAgents)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	now := s.now().UTC()
	if !initializationTime.IsZero() {
		now = initializationTime.UTC()
	}
	nodeSummaries := make(map[string]NodeSummary, len(normalized.Document.Nodes))
	for _, nodeID := range normalized.TopologicalOrder {
		definition := normalized.Document.Nodes[nodeID]
		node := NodeSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, RunID: id, ID: nodeID, Type: definition.Type, Phase: NodePhasePending, CreatedAt: now, UpdatedAt: now}
		nodeSummaries[nodeID] = summarizeNode(node)
	}
	_, target, err := workflow.ResolveAgent(normalized.Document.Defaults, workflow.Node{})
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	run := WorkflowSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, ID: id, WorkflowName: normalized.Document.Name, Project: project,
		ResolvedDriver: backendName, ResolvedTarget: target, Backend: backendName, DeprecationWarnings: append([]string(nil), normalized.Warnings...), EffectiveConcurrency: effectiveConcurrency, Phase: PhaseCreated, Inputs: normalized.Inputs, TopologicalOrder: normalized.TopologicalOrder,
		Nodes: nodeSummaries, StateDir: s.store.RunDir(id), CreatedAt: now, UpdatedAt: now}
	run.StateVersion = 1
	if err := ValidateWorkflowSnapshot(run); err != nil {
		return WorkflowSnapshot{}, err
	}
	initialEvent := WorkflowEvent{ProtocolVersion: protocolVersion, RunID: id, Sequence: 1, Type: "run.created", Phase: PhaseCreated, Message: "workflow run created", Timestamp: now}
	existing, initialized, err := s.inspectStartedRun(run, initialEvent)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if !initialized {
		for _, nodeID := range normalized.TopologicalOrder {
			definition := normalized.Document.Nodes[nodeID]
			node := NodeSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, RunID: id, ID: nodeID, Type: definition.Type, Phase: NodePhasePending, CreatedAt: now, UpdatedAt: now}
			if err := s.store.EnsureNode(id, nodeID, node); err != nil {
				return WorkflowSnapshot{}, err
			}
		}
		if err := s.store.EnsureSnapshot(id, run); err != nil {
			return WorkflowSnapshot{}, err
		}
		created, err := s.store.EnsureInitialEvent(id, initialEvent)
		if err != nil {
			return WorkflowSnapshot{}, err
		}
		if created {
			s.publishEvent(initialEvent)
		}
		existing = run
	}
	needsController, err := s.runNeedsController(existing)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if s.controller(id) != nil || !needsController {
		return run, nil
	}
	lease, err := s.leases.AcquireRecovery(id, command)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	s.startController(id, lease, func(ctx context.Context, generation uint64) { s.control(ctx, id, generation) })
	return run, nil
}

func (s *Service) inspectStartedRun(initial WorkflowSnapshot, initialEvent WorkflowEvent) (WorkflowSnapshot, bool, error) {
	var existing WorkflowSnapshot
	err := s.store.ReadSnapshot(initial.ID, &existing)
	if errors.Is(err, os.ErrNotExist) {
		var found bool
		if eventErr := s.store.ReadEvents(initial.ID, func(json.RawMessage) error { found = true; return nil }); eventErr != nil {
			return existing, false, eventErr
		}
		if found {
			return existing, false, fmt.Errorf("run %q has events without an initial snapshot", initial.ID)
		}
		return initial, false, nil
	}
	if err != nil {
		return existing, false, err
	}
	first, count, err := s.readFirstEvent(initial.ID)
	if err != nil {
		return existing, false, err
	}
	if count == 0 {
		if !equalDurableJSON(existing, initial) {
			return existing, false, fmt.Errorf("initial snapshot for run %q does not match requested initialization", initial.ID)
		}
		for nodeID, summary := range initial.Nodes {
			var node NodeSnapshot
			if readErr := s.store.ReadNode(initial.ID, nodeID, &node); readErr != nil {
				return existing, false, readErr
			}
			expected := NodeSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, RunID: initial.ID, ID: nodeID, Type: summary.Type, Phase: NodePhasePending, CreatedAt: initial.CreatedAt, UpdatedAt: initial.CreatedAt}
			if !equalDurableJSON(node, expected) {
				return existing, false, fmt.Errorf("initial snapshot for node %q does not match requested initialization", nodeID)
			}
		}
		created, err := s.store.EnsureInitialEvent(initial.ID, initialEvent)
		if err != nil {
			return existing, false, err
		}
		if created {
			s.publishEvent(initialEvent)
		}
		return existing, true, nil
	}
	if !reflect.DeepEqual(first, initialEvent) {
		return existing, false, fmt.Errorf("initial event for run %q does not match requested initialization", initial.ID)
	}
	if err := validateStartedRunIdentity(existing, initial); err != nil {
		return existing, false, err
	}
	if err := s.validateStartedNodes(existing, initial); err != nil {
		return existing, false, err
	}
	return existing, true, nil
}

func (s *Service) readFirstEvent(runID string) (WorkflowEvent, int, error) {
	var first WorkflowEvent
	count := 0
	err := s.store.ReadEvents(runID, func(raw json.RawMessage) error {
		count++
		if count == 1 {
			return json.Unmarshal(raw, &first)
		}
		return nil
	})
	return first, count, err
}

func validateStartedRunIdentity(existing, initial WorkflowSnapshot) error {
	type identity struct {
		ProtocolVersion      int            `json:"protocolVersion"`
		StateSchemaVersion   int            `json:"stateSchemaVersion"`
		ID                   string         `json:"id"`
		WorkflowName         string         `json:"workflowName"`
		Project              string         `json:"project"`
		ResolvedDriver       string         `json:"resolvedDriver"`
		ResolvedTarget       string         `json:"resolvedTarget"`
		DeprecationWarnings  []string       `json:"deprecationWarnings,omitempty"`
		EffectiveConcurrency int            `json:"effectiveConcurrency,omitempty"`
		Inputs               map[string]any `json:"inputs,omitempty"`
		TopologicalOrder     []string       `json:"topologicalOrder"`
		StateDir             string         `json:"stateDir"`
		CreatedAt            time.Time      `json:"createdAt"`
	}
	project := func(snapshot WorkflowSnapshot) identity {
		return identity{ProtocolVersion: snapshot.ProtocolVersion, StateSchemaVersion: snapshot.StateSchemaVersion, ID: snapshot.ID, WorkflowName: snapshot.WorkflowName, Project: snapshot.Project, ResolvedDriver: snapshot.ResolvedDriver, ResolvedTarget: snapshot.ResolvedTarget, DeprecationWarnings: snapshot.DeprecationWarnings, EffectiveConcurrency: snapshot.EffectiveConcurrency, Inputs: snapshot.Inputs, TopologicalOrder: snapshot.TopologicalOrder, StateDir: snapshot.StateDir, CreatedAt: snapshot.CreatedAt}
	}
	if !equalDurableJSON(project(existing), project(initial)) {
		return fmt.Errorf("run %q does not match requested initialization", initial.ID)
	}
	return nil
}

func (s *Service) validateStartedNodes(existing, initial WorkflowSnapshot) error {
	for _, nodeID := range initial.TopologicalOrder {
		var node NodeSnapshot
		if err := s.store.ReadNode(initial.ID, nodeID, &node); err != nil {
			return err
		}
		if err := ValidateNodeSnapshot(node); err != nil {
			return fmt.Errorf("invalid node %q: %w", nodeID, err)
		}
		expected := initial.Nodes[nodeID]
		summary, found := existing.Nodes[nodeID]
		if node.RunID != initial.ID || node.ID != nodeID || node.Type != expected.Type || !node.CreatedAt.Equal(initial.CreatedAt) || !found || summary.ID != nodeID || summary.Type != expected.Type {
			return fmt.Errorf("node %q does not match requested initialization", nodeID)
		}
	}
	return nil
}

func equalDurableJSON(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func (s *Service) runNeedsController(run WorkflowSnapshot) (bool, error) {
	if run.Phase == PhaseCompleted || run.Phase == PhaseWaiting && run.Reason == ReasonApprovalRequired {
		return false, nil
	}
	_, nodes, err := s.loadRun(run.ID)
	if err != nil {
		return false, err
	}
	return s.runNeedsControllerFromState(run, nodes)
}

func (s *Service) runNeedsControllerFromState(run WorkflowSnapshot, nodes []NodeSnapshot) (bool, error) {
	if run.Phase == PhaseCompleted || run.Phase == PhaseWaiting && run.Reason == ReasonApprovalRequired {
		return false, nil
	}
	if run.Phase == PhaseWaiting && run.Reason == ReasonAgentWaitingInput {
		settled, err := s.hasSettledNeedsInput(nodes)
		return !settled, err
	}
	return true, nil
}

func (s *Service) startController(runID string, lease *store.Lease, control func(context.Context, uint64)) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.nextGeneration++
	entry := &controller{cancel: cancel, done: make(chan struct{}), lease: lease, generation: s.nextGeneration}
	s.controllers[runID] = entry
	s.mu.Unlock()
	go func() {
		defer close(entry.done)
		defer func() {
			s.mu.Lock()
			if s.controllers[runID] == entry {
				delete(s.controllers, runID)
			}
			s.mu.Unlock()
		}()
		heartbeatErrors := lease.KeepAlive(ctx)
		heartbeatFailure := make(chan error, 1)
		heartbeatDone := make(chan struct{})
		go func() {
			defer close(heartbeatDone)
			if heartbeatErr, ok := <-heartbeatErrors; ok && heartbeatErr != nil {
				heartbeatFailure <- heartbeatErr
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
		cancel()
		<-heartbeatDone
		select {
		case heartbeatErr := <-heartbeatFailure:
			if err := s.handleControllerHeartbeatFailure(runID, entry, heartbeatErr); err != nil {
				entry.err = err
			}
		default:
			_ = lease.Release()
		}
	}()
}

func (s *Service) handleControllerHeartbeatFailure(runID string, entry *controller, heartbeatErr error) error {
	bound, ownershipErr := entry.lease.Bound()
	var pauseErr error
	if bound {
		pauseErr = s.pauseRunForHeartbeatFailure(runID, entry.generation, heartbeatErr)
		if errors.Is(pauseErr, errControllerInactive) {
			pauseErr = nil
		}
	}
	releaseErr := entry.lease.Release()
	recoveryErr := s.recoverControllerAfterHeartbeat(runID, entry.lease.Record().OwnerID)
	if recoveryErr == nil {
		return nil
	}
	return errors.Join(fmt.Errorf("controller heartbeat failed: %w", heartbeatErr), ownershipErr, pauseErr, releaseErr, recoveryErr)
}

func (s *Service) pauseRunForHeartbeatFailure(runID string, generation uint64, heartbeatErr error) error {
	return s.controllerMutation(runID, generation, "controller.heartbeat_failed", func(run *WorkflowSnapshot, nodes []NodeSnapshot) error {
		if run.Phase == PhaseWaiting && run.Reason == ReasonApprovalRequired {
			return errControllerInactive
		}
		if run.Phase == PhaseWaiting && run.Reason == ReasonAgentWaitingInput {
			settled, err := s.hasSettledNeedsInput(nodes)
			if err != nil {
				return err
			}
			if settled {
				return errControllerInactive
			}
		}
		run.Phase, run.Conclusion, run.Reason = PhasePaused, "", ReasonControllerDetach
		run.Summary = "controller heartbeat failed: " + heartbeatErr.Error()
		run.UpdatedAt = s.now().UTC()
		return s.persistRun(run, nil, "run.paused", run.Summary)
	})
}

func (s *Service) recoverControllerAfterHeartbeat(runID, priorOwnerID string) error {
	var failures []error
	for attempt := 0; attempt < controllerRecoveryAttempts; attempt++ {
		recovered, handedOff, err := s.tryRecoverController(runID, priorOwnerID, "heartbeat-recover")
		if recovered || handedOff {
			return nil
		}
		if err != nil {
			failures = append(failures, err)
		}
		if attempt+1 < controllerRecoveryAttempts {
			if err := s.waitControllerRecovery(context.Background(), attempt); err != nil {
				failures = append(failures, err)
				break
			}
		}
	}
	return fmt.Errorf("recover controller after heartbeat failure: %w", errors.Join(failures...))
}

func (s *Service) tryRecoverController(runID, priorOwnerID, command string) (bool, bool, error) {
	run, nodes, err := s.loadRun(runID)
	if err != nil {
		return false, false, err
	}
	needsController, err := s.runNeedsControllerFromState(run, nodes)
	if err != nil || !needsController {
		return !needsController, false, err
	}
	lease, err := s.leases.AcquireRecovery(runID, command)
	if err != nil {
		var conflict *store.LeaseConflictError
		if errors.As(err, &conflict) && conflict.Current.OwnerID != priorOwnerID {
			return false, true, nil
		}
		return false, false, err
	}
	releaseLease := true
	defer func() {
		if releaseLease {
			_ = lease.Release()
		}
	}()

	s.mu.Lock()
	run, nodes, err = s.loadRun(runID)
	if err == nil {
		needsController, err = s.runNeedsControllerFromState(run, nodes)
	}
	if err == nil && needsController {
		if run.Phase == PhasePaused {
			run.Phase, run.Conclusion, run.Reason, run.Summary = PhaseRunning, "", "", controllerRecoveredSummary
		}
		run.UpdatedAt = s.now().UTC()
		err = s.persistRun(&run, nil, "run.recovered", controllerRecoveredSummary)
	}
	s.mu.Unlock()
	if err != nil {
		return false, false, err
	}
	if !needsController {
		return true, false, nil
	}

	releaseLease = false
	s.startController(runID, lease, func(controllerCtx context.Context, generation uint64) {
		if run.CancelRequested {
			_, _ = s.handleCancellationRequest(controllerCtx, runID)
			return
		}
		s.control(controllerCtx, runID, generation)
	})
	return true, false, nil
}

func (s *Service) waitControllerRecovery(ctx context.Context, attempt int) error {
	if hook := s.testHooks.controllerRecoveryDelay; hook != nil {
		return hook(ctx)
	}
	delay := controllerRecoveryInitialDelay << attempt
	if delay > controllerRecoveryMaxDelay {
		delay = controllerRecoveryMaxDelay
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Service) Status(runID string) (StatusView, error) {
	if s.store == nil {
		return StatusView{}, errors.New("workflow state directory is unavailable")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
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
	lease, err := s.acquireLease(request.RunID, "resume", time.Second)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	releaseLease := true
	defer func() {
		if releaseLease {
			_ = lease.Release()
		}
	}()
	run, nodes, err := s.loadRun(request.RunID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if request.Action != nil && request.Action.ActionID != "" {
		if receipt, ok := run.ActionReceipts[request.Action.ActionID]; ok {
			if receipt.RequestHash != request.Action.ActionRequestHash {
				return WorkflowSnapshot{}, fmt.Errorf("actionId %q is already bound to a different request", request.Action.ActionID)
			}
			_ = s.store.RemoveActionIntent(request.RunID, request.Action.ActionID)
			return run, nil
		}
		var intent actionIntent
		intentErr := s.store.ReadActionIntent(request.RunID, request.Action.ActionID, &intent)
		if intentErr == nil {
			if intent.RequestHash != request.Action.ActionRequestHash || intent.Type != request.Action.Type || intent.NodeID != request.Action.NodeID || !reflect.DeepEqual(intent.Action, *request.Action) {
				return WorkflowSnapshot{}, fmt.Errorf("actionId %q is already bound to a different request", request.Action.ActionID)
			}
			if err := s.resumeActionIntent(&run, *request.Action, intent); err != nil {
				return WorkflowSnapshot{}, err
			}
			if run.Phase == PhaseCompleted {
				return run, nil
			}
			releaseLease = false
			s.startController(request.RunID, lease, func(ctx context.Context, generation uint64) { s.control(ctx, request.RunID, generation) })
			return s.Get(request.RunID)
		} else if !errors.Is(intentErr, os.ErrNotExist) {
			return WorkflowSnapshot{}, intentErr
		}
	}
	if request.ExpectedStateVersion != nil && run.StateVersion != *request.ExpectedStateVersion {
		return WorkflowSnapshot{}, fmt.Errorf("state version conflict: expected %d, current %d", *request.ExpectedStateVersion, run.StateVersion)
	}
	if run.Phase == PhaseCompleted && request.Action == nil {
		return run, nil
	}
	if request.Action == nil && run.Phase == PhaseWaiting && run.Reason == ReasonAgentWaitingInput {
		settled, err := s.hasSettledNeedsInput(nodes)
		if err != nil {
			return WorkflowSnapshot{}, err
		}
		if settled {
			return run, nil
		}
	}
	if request.Action != nil {
		if err := validateResumeAction(*request.Action); err != nil {
			return WorkflowSnapshot{}, err
		}
		if err := s.applyAction(&run, *request.Action); err != nil {
			return WorkflowSnapshot{}, err
		}
		if run.Phase == PhaseCompleted {
			return run, nil
		}
	} else {
		if run.CancelRequested {
			return WorkflowSnapshot{}, fmt.Errorf("run %q has cancellation pending; use fishyume cancel to retry", run.ID)
		}
		if run.Phase == PhaseWaiting && run.Reason == ReasonApprovalRequired {
			return run, nil
		}
		run.Phase, run.Conclusion, run.Reason, run.Summary, run.UpdatedAt = PhaseRunning, "", "", "controller resumed", s.now().UTC()
		if err := s.persistRun(&run, nil, "run.resumed", run.Summary); err != nil {
			_ = lease.Release()
			return WorkflowSnapshot{}, err
		}
	}
	// Controller ownership transfers to startController. Do not defer-release it.
	if run.Phase != PhaseCompleted {
		releaseLease = false
		s.startController(request.RunID, lease, func(ctx context.Context, generation uint64) { s.control(ctx, request.RunID, generation) })
	}
	updated, err := s.Get(request.RunID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	return updated, nil
}

func (s *Service) hasSettledNeedsInput(nodes []NodeSnapshot) (bool, error) {
	found := false
	for _, node := range nodes {
		if node.Type != "agent" {
			continue
		}
		if node.Phase == NodePhaseRunning {
			return false, nil
		}
		if node.Phase != NodePhaseWaiting {
			continue
		}
		if node.Reason != ReasonAgentWaitingInput {
			return false, nil
		}
		found = true
		if node.CurrentAttempt < 1 || node.Result == nil || len(node.Result.Questions) == 0 {
			return false, nil
		}
		var attempt AttemptSnapshot
		if err := s.store.ReadAttempt(node.RunID, node.ID, node.CurrentAttempt, &attempt); err != nil {
			return false, err
		}
		if !attempt.ResultConsumed || attempt.Phase != NodePhaseWaiting || attempt.Reason != ReasonAgentWaitingInput {
			return false, nil
		}
	}
	return found, nil
}

func (s *Service) Detach(runID string) (WorkflowSnapshot, error) {
	return s.Get(runID)
}

func (s *Service) Cancel(ctx context.Context, runID string) (WorkflowSnapshot, error) {
	return s.cancel(ctx, CancelRequest{RunID: runID})
}

func (s *Service) CancelWithPrecondition(ctx context.Context, request CancelRequest) (WorkflowSnapshot, error) {
	if request.ExpectedStateVersion == nil {
		return WorkflowSnapshot{}, errors.New("expected state version is required")
	}
	return s.cancel(ctx, request)
}

func (s *Service) cancel(ctx context.Context, cancelRequest CancelRequest) (WorkflowSnapshot, error) {
	runID := cancelRequest.RunID
	run, _, err := s.loadRunForCancellation(ctx, runID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if cancelRequest.ExpectedStateVersion != nil && run.StateVersion != *cancelRequest.ExpectedStateVersion {
		return WorkflowSnapshot{}, fmt.Errorf("state version conflict: expected %d, current %d", *cancelRequest.ExpectedStateVersion, run.StateVersion)
	}
	if run.Phase == PhaseCompleted {
		return run, nil
	}
	request, err := s.store.RequestCancellationWithPrecondition(runID, s.now().UTC(), cancelRequest.ExpectedStateVersion, cancelRequest.ActionID, cancelRequest.ActionRequestHash)
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
			result, cancelErr := s.handleConcurrentCancellationRequest(ctx, runID)
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
			_, cancelErr := s.handleConcurrentCancellationRequest(cancelCtx, runID)
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
			candidate, err := s.registry.Get(attemptDriver(attempt))
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
		candidate, err := s.registry.Get(attemptDriver(attempt))
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
	s.mu.RLock()
	defer s.mu.RUnlock()
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

func (s *Service) ActiveControllerCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.controllers)
}

func (s *Service) HasNonTerminalRuns() (bool, error) {
	if s.store == nil {
		return false, errors.New("workflow state directory is unavailable")
	}
	ids, err := s.store.ListRunIDs()
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		kind, detectErr := s.store.DetectSnapshot(id)
		if detectErr != nil || kind == store.SnapshotLegacyM1 {
			continue
		}
		var snapshot WorkflowSnapshot
		if readErr := s.store.ReadSnapshot(id, &snapshot); readErr != nil {
			return false, readErr
		}
		if snapshot.Phase != PhaseCompleted {
			return true, nil
		}
	}
	return false, nil
}

// Recover starts controllers for durable non-terminal Runs. Existing Attempts
// are reconciled by control before any scheduler decision is made.
func (s *Service) Recover(ctx context.Context) error {
	if s.store == nil || s.leases == nil {
		return errors.New("workflow state directory is unavailable")
	}
	ids, err := s.store.ListRunIDs()
	if err != nil {
		return err
	}
	for _, runID := range ids {
		kind, detectErr := s.store.DetectSnapshot(runID)
		if detectErr != nil || kind == store.SnapshotLegacyM1 {
			continue
		}
		run, nodes, loadErr := s.loadRun(runID)
		if loadErr != nil {
			return loadErr
		}
		if run.Phase == PhaseCompleted || s.controller(runID) != nil {
			continue
		}
		if run.Phase == PhaseWaiting && run.Reason == ReasonApprovalRequired {
			continue
		}
		if run.Phase == PhaseWaiting && run.Reason == ReasonAgentWaitingInput {
			settled, settledErr := s.hasSettledNeedsInput(nodes)
			if settledErr != nil {
				return settledErr
			}
			if settled {
				continue
			}
		}
		lease, acquireErr := s.leases.AcquireRecovery(runID, "serve-recover")
		if acquireErr != nil {
			return fmt.Errorf("recover run %q: %w", runID, acquireErr)
		}
		if run.Phase == PhasePaused {
			run.Phase, run.Conclusion, run.Reason, run.Summary, run.UpdatedAt = PhaseRunning, "", "", "controller recovered", s.now().UTC()
			if persistErr := s.persistRun(&run, nil, "run.recovered", run.Summary); persistErr != nil {
				_ = lease.Release()
				return persistErr
			}
		}
		s.startController(runID, lease, func(controllerCtx context.Context, generation uint64) {
			if run.CancelRequested {
				_, _ = s.handleCancellationRequest(controllerCtx, runID)
				return
			}
			s.control(controllerCtx, runID, generation)
		})
	}
	return nil
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
		active := findActiveAttempts(nodes)
		hadActive, allWaiting := len(active) > 0, false
		if hadActive {
			progressed, waiting, err := s.reconcileAttempts(ctx, runID, generation, active)
			if err != nil {
				if ctx.Err() == nil && !errors.Is(err, errControllerInactive) {
					s.pauseControllerOnError(runID, generation, err)
				}
				return
			}
			allWaiting = waiting
			if !progressed {
				// Continue below so the scheduler can fill remaining capacity.
			} else if !allWaiting {
				continue
			}
		}
		progressed, stop, err := s.scheduleBatch(ctx, runID, generation, normalized)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, errControllerInactive) {
				s.pauseControllerOnError(runID, generation, err)
			}
			return
		}
		if stop {
			return
		}
		if progressed {
			continue
		}
		if hadActive && !allWaiting {
			if err := s.waitStartupIdleReconcile(ctx); err != nil {
				return
			}
			continue
		}
		return
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
			renderedTask, err := template.Render(normalized.Inputs, results)
			if err != nil {
				return err
			}
			if len([]byte(renderedTask)) > workflow.MaxPromptBytes {
				return fmt.Errorf("rendered prompt exceeds %d bytes", workflow.MaxPromptBytes)
			}
			driver, target, err := workflow.ResolveAgent(normalized.Document.Defaults, definition)
			if err != nil {
				return err
			}
			routingDecision, routingUsage, err := s.prepareAttemptRouting(run.ID, *node, driver, workflow.EffectiveRoutingRequirement(normalized.Document, definition))
			if err != nil {
				return err
			}
			number, now := node.CurrentAttempt+1, s.now().UTC()
			ancestorResults := make(map[string]workflow.Result)
			for ancestorID := range contextDependencySet(normalized, node.ID) {
				if result, ok := results[ancestorID]; ok {
					ancestorResults[ancestorID] = result
				}
			}
			policy := workflow.EffectiveContextPolicy(normalized.Document, definition)
			compiled, err := s.compileRunContext(ContextAssembly{Identity: agentIdentity(run.ID, node.ID, number), Project: run.Project, Target: target, NodeID: node.ID, NodeTask: renderedTask, RequiredSkills: definition.RequiredSkills, WorkflowPolicy: workflowPolicy(normalized.Document), ContextPolicyVersion: normalized.ContextPolicyVersion, ProjectInstructions: policy.ProjectInstructions, DependencyResults: ancestorResults, UserAnswer: node.PendingInputAnswer, MemoryBindings: normalized.ContextBindings.MemoryByNode[node.ID]})
			if err != nil {
				return err
			}
			if routingDecision != nil {
				compiled.Envelope.RoutingDecision = routingDecision
			}
			memoryUsage, err := s.consumeCompiledMemory(run.Project, agentIdentity(run.ID, node.ID, number), compiled.Compilation)
			if err != nil {
				return err
			}
			attempt := AttemptSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, RunID: run.ID, NodeID: node.ID, Number: number, Phase: NodePhaseRunning, LaunchState: LaunchPrepared,
				ResolvedDriver: driver, ResolvedTarget: target, Backend: driver,
				RoutingDecision: routingDecision, RoutingUsage: routingUsage,
				ContextCompilerVersion: contextcompiler.Version, ContextCompilerVersionV2: compiled.Compilation.Manifest.CompilerVersion, ContextManifest: compiled.LegacyManifest, ContextManifestV2: &compiled.Compilation.Manifest, ContextHash: compiled.Compilation.Hash, MemoryUsage: memoryUsage, StartedAt: now, UpdatedAt: now}
			if err := s.writeAttempt(attempt, true); err != nil {
				return err
			}
			node.Phase, node.Reason, node.Conclusion, node.PendingInputAnswer, node.PendingRoutingTarget, node.CurrentAttempt, node.UpdatedAt = NodePhaseRunning, "", "", nil, nil, number, now
			run.Phase, run.Reason, run.Conclusion, run.ActiveNodeID, run.Summary, run.UpdatedAt = PhaseRunning, "", "", node.ID, "launching Agent", now
			if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
				return err
			}
			run.Nodes[node.ID] = summarizeNode(*node)
			if err := s.persistRun(run, node, "node.running", "launching Agent attempt"); err != nil {
				return err
			}
			launch = &pendingLaunch{runID: run.ID, nodeID: node.ID, attempt: number, backend: driver,
				launchSpec: backend.AgentExecutionSpec{RunID: run.ID, NodeID: node.ID, Attempt: number, Workspace: run.Project, Tool: legacyToolForDriver(s.registry, driver), Runtime: target,
					Model: routingModel(routingDecision), Instructions: compiled.Envelope.Prompt, RequiredSkills: append([]string(nil), definition.RequiredSkills...), ResultContract: backend.ResultContract{Schema: compiled.Envelope.ResultContract.Schema, MaxBytes: workflow.MaxResultBytes}, Envelope: &compiled.Envelope}}
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
	if handle.DriverName() != launch.backend {
		return fmt.Errorf("Driver %q returned a handle for %q", launch.backend, handle.DriverName())
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
		if handle.DriverName() != attemptDriver(attempt) {
			return nil, fmt.Errorf("Attempt Driver %q does not match execution handle Driver %q", attemptDriver(attempt), handle.DriverName())
		}
		if executionTarget(handle) != attemptTarget(attempt) {
			return nil, fmt.Errorf("Attempt Target %q does not match execution handle Target %q", attemptTarget(attempt), executionTarget(handle))
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
	candidate, err := s.registry.Get(attemptDriver(attempt))
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
	normalized := workflow.Result{Summary: result.Summary, Artifacts: result.Artifacts, Warnings: result.Warnings, Checks: result.Checks, Questions: workflowQuestions(result.Questions),
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
			attempt.SideEffectStatus = result.SideEffectStatus
			node.Phase, node.Conclusion, node.Reason, node.Diagnostic, node.Result, node.UpdatedAt = NodePhaseCompleted, ConclusionSucceeded, "", "", &normalized, now
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
		if err := workflow.ValidateResult(normalized); err != nil {
			return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonInvalidResult, err.Error())
		}
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
			attempt.SideEffectStatus = result.SideEffectStatus
			if attempt.SideEffectStatus == "" {
				attempt.SideEffectStatus = agent.SideEffectUnknown
			}
			node.Phase, node.Conclusion, node.Reason, node.Diagnostic, node.Result, node.UpdatedAt = NodePhaseCompleted, ConclusionFailed, "", result.Summary, &normalized, now
			if err := s.store.WriteResult(runID, nodeID, attemptNumber, normalized); err != nil {
				return err
			}
			if err := s.store.WriteNode(runID, nodeID, node); err != nil {
				return err
			}
			if err := s.writeAttempt(attempt, false); err != nil {
				return err
			}
			run.Nodes[nodeID], run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.Summary, run.UpdatedAt = summarizeNode(*node), PhaseRunning, "", "", "", result.Summary, now
			return s.persistRun(run, node, "node.completed", run.Summary)
		})
		return err == nil, err
	case "needs_input", "waiting_input", "blocked", "waitinginput":
		if len(normalized.Questions) == 0 {
			return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonInvalidResult, "needs_input result requires at least one question")
		}
		if err := workflow.ValidateResult(normalized); err != nil {
			return false, s.waiting(runID, nodeID, attemptNumber, generation, ReasonInvalidResult, err.Error())
		}
		return false, s.waitingWithResult(runID, nodeID, attemptNumber, generation, ReasonAgentWaitingInput, result.Summary, normalized)
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

func workflowQuestions(questions []backend.InputQuestion) []workflow.InputQuestion {
	result := make([]workflow.InputQuestion, len(questions))
	for index, question := range questions {
		result[index] = workflow.InputQuestion{ID: question.ID, Prompt: question.Prompt, Choices: append([]string(nil), question.Choices...), Required: question.Required}
	}
	return result
}

func (s *Service) waitingWithResult(runID, nodeID string, attemptNumber int, generation uint64, reason Reason, message string, result workflow.Result) error {
	var normalized workflow.Normalized
	if err := s.store.ReadWorkflow(runID, &normalized); err != nil {
		return err
	}
	return s.controllerMutation(runID, generation, "node.waiting_result", func(run *WorkflowSnapshot, nodes []NodeSnapshot) error {
		node, err := findNode(nodes, nodeID)
		if err != nil {
			return err
		}
		var attempt AttemptSnapshot
		if err := s.store.ReadAttempt(runID, nodeID, attemptNumber, &attempt); err != nil {
			return err
		}
		resultChanged := node.Result == nil || !workflowResultsEqual(*node.Result, result)
		nodeChanged := resultChanged || !attempt.ResultConsumed || attempt.Phase != NodePhaseWaiting || attempt.Reason != reason || node.Phase != NodePhaseWaiting || node.Reason != reason || node.Diagnostic != message
		before := *run
		now := s.now().UTC()
		attempt.Phase, attempt.Reason, attempt.ResultConsumed, attempt.UpdatedAt = NodePhaseWaiting, reason, true, now
		node.Phase, node.Reason, node.Diagnostic, node.Result, node.UpdatedAt = NodePhaseWaiting, reason, message, &result, now
		aggregateRunState(run, nodes, normalized.Document, now)
		if !nodeChanged && !aggregateRunStateChanged(before, *run) {
			return nil
		}
		eventType := "run.waiting"
		if nodeChanged {
			if err := s.store.WriteResult(run.ID, node.ID, attemptNumber, result); err != nil {
				return err
			}
			if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
				return err
			}
			if err := s.writeAttempt(attempt, false); err != nil {
				return err
			}
			run.Nodes[node.ID] = summarizeNode(*node)
			eventType = "node.waiting"
		}
		return s.persistRun(run, node, eventType, message)
	})
}

func workflowResultsEqual(left, right workflow.Result) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func (s *Service) waiting(runID, nodeID string, attemptNumber int, generation uint64, reason Reason, message string) error {
	_, err := s.waitingIfChanged(runID, nodeID, attemptNumber, generation, reason, message)
	return err
}

func (s *Service) waitingIfChanged(runID, nodeID string, attemptNumber int, generation uint64, reason Reason, message string) (bool, error) {
	var normalized workflow.Normalized
	if err := s.store.ReadWorkflow(runID, &normalized); err != nil {
		return false, err
	}
	changed := false
	err := s.controllerMutation(runID, generation, "node.waiting", func(run *WorkflowSnapshot, nodes []NodeSnapshot) error {
		node, err := findNode(nodes, nodeID)
		if err != nil {
			return err
		}
		var attempt AttemptSnapshot
		if err := s.store.ReadAttempt(runID, nodeID, attemptNumber, &attempt); err != nil {
			return err
		}
		nodeChanged := attempt.Phase != NodePhaseWaiting || attempt.Reason != reason || node.Phase != NodePhaseWaiting || node.Reason != reason || node.Diagnostic != message
		before := *run
		now := s.now().UTC()
		attempt.Phase, attempt.Reason, attempt.UpdatedAt = NodePhaseWaiting, reason, now
		node.Phase, node.Reason, node.Diagnostic, node.UpdatedAt = NodePhaseWaiting, reason, message, now
		aggregateRunState(run, nodes, normalized.Document, now)
		if !nodeChanged && !aggregateRunStateChanged(before, *run) {
			return nil
		}
		if nodeChanged {
			if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
				return err
			}
			if err := s.writeAttempt(attempt, false); err != nil {
				return err
			}
			run.Nodes[node.ID] = summarizeNode(*node)
		}
		if err := s.persistRun(run, node, "node.waiting", message); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
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
		attempt.SideEffectStatus = agent.SideEffectUnknown
		node.Phase, node.Conclusion, node.Reason, node.Diagnostic, node.UpdatedAt = NodePhaseCompleted, ConclusionIndeterminate, "", message, now
		run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.Summary, run.UpdatedAt = PhaseRunning, "", "", "", message, now
		if err := s.writeAttempt(attempt, false); err != nil {
			return err
		}
		if err := s.store.WriteNode(run.ID, node.ID, node); err != nil {
			return err
		}
		run.Nodes[node.ID] = summarizeNode(*node)
		return s.persistRun(run, node, "node.completed", message)
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
	mutations := make([]actionNodeMutation, 0, 1)
	eventType, summary := "", ""
	switch action.Type {
	case "approve", "reject":
		before := node
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
				return s.finalizeAction(run, &node, action, "node.approval_decided", "approval "+decision)
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
		mutations = append(mutations, actionNodeMutation{Before: before, After: node})
		eventType, summary = "node.approval_decided", "approval "+decision
	case "retry":
		before := node
		allowed := node.Phase == NodePhaseWaiting && (node.Reason == ReasonAgentWaitingInput || node.Reason == ReasonCompletionMissing || node.Reason == ReasonInvalidResult)
		allowed = allowed || (node.Phase == NodePhaseCompleted && (node.Conclusion == ConclusionFailed || node.Conclusion == ConclusionIndeterminate))
		if !allowed || node.CurrentAttempt < 1 {
			return fmt.Errorf("node %q is not in a retryable state", node.ID)
		}
		if node.Conclusion == ConclusionIndeterminate && !action.AcknowledgeDuplicateRisk {
			return fmt.Errorf("retrying indeterminate node %q requires acknowledgeDuplicateRisk", node.ID)
		}
		pendingRoute, err := s.pendingRoutingTarget(run, node, true)
		if err != nil {
			return fmt.Errorf("prepare retry route: %w", err)
		}
		now := s.now().UTC()
		node.Phase, node.Conclusion, node.Reason, node.Result, node.PendingRoutingTarget, node.UpdatedAt = NodePhaseReady, "", "", nil, pendingRoute, now
		mutations = append(mutations, actionNodeMutation{Before: before, After: node})
		downstream, err := s.planDownstreamReset(run, node.ID)
		if err != nil {
			return err
		}
		mutations = append(mutations, downstream...)
		eventType, summary = "node.retry_requested", "explicit retry requested"
	case "answer":
		before := node
		if node.Type != "agent" || node.Phase != NodePhaseWaiting || node.Reason != ReasonAgentWaitingInput || node.Result == nil || len(node.Result.Questions) == 0 {
			return fmt.Errorf("node %q is not waiting for structured input", node.ID)
		}
		if action.ExpectedAttempt == nil || *action.ExpectedAttempt != node.CurrentAttempt {
			return fmt.Errorf("attempt conflict: expected %v, current %d", action.ExpectedAttempt, node.CurrentAttempt)
		}
		inputAnswer, err := validateAndEncodeAnswers(node.CurrentAttempt, node.Result.Questions, action.Answers)
		if err != nil {
			return err
		}
		pendingRoute, err := s.pendingRoutingTarget(run, node, false)
		if err != nil {
			return fmt.Errorf("prepare answer route: %w", err)
		}
		now := s.now().UTC()
		node.Phase, node.Conclusion, node.Reason, node.Diagnostic, node.PendingInputAnswer, node.PendingRoutingTarget, node.Result, node.UpdatedAt = NodePhaseReady, "", "", "", inputAnswer, pendingRoute, nil, now
		mutations = append(mutations, actionNodeMutation{Before: before, After: node})
		downstream, err := s.planDownstreamReset(run, node.ID)
		if err != nil {
			return err
		}
		mutations = append(mutations, downstream...)
		eventType, summary = "node.answer_submitted", "input answer submitted"
	default:
		return fmt.Errorf("unknown resume action %q", action.Type)
	}
	intent := actionIntent{Version: actionIntentVersion, ActionID: action.ActionID, RequestHash: action.ActionRequestHash, Type: action.Type, NodeID: action.NodeID, Phase: actionIntentPlanned, EventType: eventType, Summary: summary, Action: action, Mutations: mutations}
	if action.ActionID == "" {
		intent.Phase = actionIntentApplying
		return s.applyActionIntent(run, action, intent)
	}
	if err := s.prepareActionIntent(run.ID, intent); err != nil {
		return err
	}
	intent.Phase = actionIntentApplying
	if err := s.store.WriteActionIntent(run.ID, action.ActionID, intent); err != nil {
		return err
	}
	return s.applyActionIntent(run, action, intent)
}

func (s *Service) prepareActionIntent(runID string, intent actionIntent) error {
	ids, err := s.store.ListActionIntentIDs(runID)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id != intent.ActionID {
			return fmt.Errorf("run %q already has pending action intent %q", runID, id)
		}
	}
	return s.store.WriteActionIntent(runID, intent.ActionID, intent)
}

func (s *Service) finalizeAction(run *WorkflowSnapshot, node *NodeSnapshot, action ResumeAction, eventType, summary string) error {
	now := s.now().UTC()
	run.Nodes[node.ID] = summarizeNode(*node)
	run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.Summary, run.CancelRequested, run.UpdatedAt = PhaseRunning, "", "", "", summary, false, now
	s.recordActionReceipt(run, action.ActionID, action.ActionRequestHash)
	if err := s.persistRun(run, node, eventType, summary); err != nil {
		return err
	}
	if action.ActionID == "" {
		return nil
	}
	return s.store.RemoveActionIntent(run.ID, action.ActionID)
}

func (s *Service) resumeActionIntent(run *WorkflowSnapshot, action ResumeAction, intent actionIntent) error {
	if intent.Version != actionIntentVersion || (intent.Phase != actionIntentPlanned && intent.Phase != actionIntentApplying) || len(intent.Mutations) == 0 {
		return fmt.Errorf("action intent %q lacks exact replay evidence and was preserved", intent.ActionID)
	}
	if intent.Phase == actionIntentPlanned {
		intent.Phase = actionIntentApplying
		if err := s.store.WriteActionIntent(run.ID, action.ActionID, intent); err != nil {
			return err
		}
	}
	return s.applyActionIntent(run, action, intent)
}

func (s *Service) applyActionIntent(run *WorkflowSnapshot, action ResumeAction, intent actionIntent) error {
	for index, mutation := range intent.Mutations {
		var current NodeSnapshot
		if err := s.store.ReadNode(run.ID, mutation.After.ID, &current); err != nil {
			return err
		}
		// A persisted Applied count is the only evidence that a prior mutation
		// crossed its durable boundary. Before that marker exists, recovery may
		// accept only the exact before/after snapshots from the intent plan.
		if index < intent.Applied {
			run.Nodes[current.ID] = summarizeNode(current)
			continue
		}
		if !reflect.DeepEqual(current, mutation.After) {
			if !reflect.DeepEqual(current, mutation.Before) {
				return fmt.Errorf("action intent %q conflicts with unrelated mutation of node %q", intent.ActionID, current.ID)
			}
			if err := s.store.WriteNode(run.ID, current.ID, mutation.After); err != nil {
				return err
			}
		}
		run.Nodes[mutation.After.ID] = summarizeNode(mutation.After)
		if action.ActionID != "" && intent.Applied < index+1 {
			intent.Applied = index + 1
			if err := s.store.WriteActionIntent(run.ID, action.ActionID, intent); err != nil {
				return err
			}
		}
	}
	target := intent.Mutations[0].After
	return s.finalizeAction(run, &target, action, intent.EventType, intent.Summary)
}

// AbortActionIntent deletes only a plan that durably proves no business write
// could have begun. Applying intents are recovery evidence and are preserved.
func (s *Service) AbortActionIntent(runID, actionID, requestHash string) error {
	var intent actionIntent
	if err := s.store.ReadActionIntent(runID, actionID, &intent); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if intent.RequestHash != requestHash {
		return fmt.Errorf("action intent request hash conflict")
	}
	if intent.Version == actionIntentVersion && intent.Phase == actionIntentPlanned && intent.Applied == 0 {
		return s.store.RemoveActionIntent(runID, actionID)
	}
	return nil
}

func (s *Service) recordActionReceipt(run *WorkflowSnapshot, actionID, requestHash string) {
	if actionID == "" {
		return
	}
	if run.ActionReceipts == nil {
		run.ActionReceipts = map[string]ActionReceipt{}
	}
	run.ActionReceipts[actionID] = ActionReceipt{ActionID: actionID, RequestHash: requestHash, StateVersion: run.StateVersion + 1, Phase: run.Phase, Conclusion: run.Conclusion}
	for len(run.ActionReceipts) > maxActionReceipts {
		var oldestID string
		var oldestVersion uint64
		for id, receipt := range run.ActionReceipts {
			if oldestID == "" || receipt.StateVersion < oldestVersion || (receipt.StateVersion == oldestVersion && id < oldestID) {
				oldestID, oldestVersion = id, receipt.StateVersion
			}
		}
		delete(run.ActionReceipts, oldestID)
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
	case "answer":
		if action.ExpectedAttempt == nil || *action.ExpectedAttempt < 1 {
			return errors.New("answer action requires expectedAttempt")
		}
		if action.Reason != "" || action.AcknowledgeDuplicateRisk {
			return errors.New("answer action does not accept reason or acknowledgeDuplicateRisk")
		}
	default:
		return fmt.Errorf("unknown resume action %q", action.Type)
	}
	return nil
}

func validateAndEncodeAnswers(attempt int, questions []workflow.InputQuestion, answers map[string]any) (json.RawMessage, error) {
	if answers == nil {
		answers = map[string]any{}
	}
	byID := make(map[string]workflow.InputQuestion, len(questions))
	for _, question := range questions {
		byID[question.ID] = question
		value, exists := answers[question.ID]
		if question.Required && !exists {
			return nil, fmt.Errorf("answer for required question %q is missing", question.ID)
		}
		if !exists {
			continue
		}
		if err := validateAnswerScalar(value); err != nil {
			return nil, fmt.Errorf("answer for question %q: %w", question.ID, err)
		}
		if len(question.Choices) > 0 {
			text, ok := value.(string)
			if !ok || !containsString(question.Choices, text) {
				return nil, fmt.Errorf("answer for question %q is not one of the declared choices", question.ID)
			}
		}
	}
	for id, value := range answers {
		if _, ok := byID[id]; !ok {
			return nil, fmt.Errorf("answer references unknown question %q", id)
		}
		if err := validateAnswerScalar(value); err != nil {
			return nil, fmt.Errorf("answer for question %q: %w", id, err)
		}
	}
	encoded, err := json.Marshal(struct {
		Attempt   int                      `json:"attempt"`
		Questions []workflow.InputQuestion `json:"questions"`
		Answers   map[string]any           `json:"answers"`
	}{Attempt: attempt, Questions: questions, Answers: answers})
	if err != nil {
		return nil, fmt.Errorf("encode input answers: %w", err)
	}
	return encoded, nil
}

func validateAnswerScalar(value any) error {
	switch value.(type) {
	case string, bool, json.Number, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return nil
	default:
		return fmt.Errorf("value must be a string, number, or boolean")
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (s *Service) planDownstreamReset(run *WorkflowSnapshot, target string) ([]actionNodeMutation, error) {
	var normalized workflow.Normalized
	if err := s.store.ReadWorkflow(run.ID, &normalized); err != nil {
		return nil, err
	}
	descendants := map[string]bool{target: true}
	for _, id := range normalized.TopologicalOrder {
		for _, dependency := range normalized.Document.Nodes[id].DependsOn {
			if descendants[dependency] {
				descendants[id] = true
			}
		}
	}
	ids := make([]string, 0, len(descendants))
	for id := range descendants {
		if id != target {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	mutations := make([]actionNodeMutation, 0, len(ids))
	for _, id := range ids {
		var node NodeSnapshot
		if err := s.store.ReadNode(run.ID, id, &node); err != nil {
			return nil, err
		}
		if node.Phase == NodePhaseSkipped {
			before := node
			node.Phase, node.Reason, node.Conclusion, node.Result, node.UpdatedAt = NodePhasePending, "", "", nil, s.now().UTC()
			mutations = append(mutations, actionNodeMutation{Before: before, After: node})
		}
	}
	return mutations, nil
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
	run.StateVersion++
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
	s.publishEvent(event)
	return nil
}

func (s *Service) publishEvent(event WorkflowEvent) {
	s.sinkMu.RLock()
	sinks := make([]EventSink, 0, len(s.eventSinks))
	for _, sink := range s.eventSinks {
		sinks = append(sinks, sink)
	}
	s.sinkMu.RUnlock()
	for _, sink := range sinks {
		sink(event)
	}
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
	if attemptDriver(attempt) != runDriver(run) {
		return fmt.Errorf("active Attempt Driver %q does not match run Driver %q", attemptDriver(attempt), runDriver(run))
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

func contextDependencySet(normalized workflow.Normalized, nodeID string) map[string]bool {
	if normalized.ContextPolicyVersion != "context-policy/v1" {
		return ancestorSet(normalized.Document, nodeID)
	}
	policy := workflow.EffectiveContextPolicy(normalized.Document, normalized.Document.Nodes[nodeID])
	selected := make(map[string]bool, len(policy.Dependencies))
	for _, dependency := range policy.Dependencies {
		selected[dependency] = true
	}
	return selected
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
