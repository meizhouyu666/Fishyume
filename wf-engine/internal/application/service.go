package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"wf.local/wf-engine/internal/contextcompiler"
	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

type Core interface {
	DriverCapabilityReports(context.Context, string) []run.DriverCapabilityReport
	StartWorkflow(context.Context, run.StartWorkflowRequest) (run.WorkflowSnapshot, error)
	Status(string) (run.StatusView, error)
	Resume(context.Context, run.ResumeRequest) (run.WorkflowSnapshot, error)
	Cancel(context.Context, string) (run.WorkflowSnapshot, error)
	CancelWithPrecondition(context.Context, run.CancelRequest) (run.WorkflowSnapshot, error)
	ListRunIDs() ([]string, error)
	ReadEvents(string) ([]run.WorkflowEvent, error)
	ReadAttempt(string, string, int) (run.AttemptSnapshot, error)
	Detach(string) (run.WorkflowSnapshot, error)
	WaitControllers(context.Context) error
	AddEventSink(run.EventSink) func()
}

type Journal interface {
	ReadApplicationJournal(string, string) (store.ApplicationJournalRecord, error)
	BeginApplicationJournal(string, string, string, json.RawMessage, string, time.Time) (store.ApplicationJournalRecord, error)
	MarkApplicationJournalMutated(string, string, string, json.RawMessage, time.Time) (store.ApplicationJournalRecord, error)
	CommitApplicationJournal(string, string, string, time.Time) (store.ApplicationJournalRecord, error)
	ListPendingApplicationJournals() ([]store.ApplicationJournalRecord, error)
}

type Service struct {
	core          Core
	defaultDriver string
	catalogs      routing.CatalogProvider
	journal       Journal
	memory        MemoryBackend
	now           func() time.Time
	mutationMu    sync.Mutex
}

func NewService(core Core, defaultDriver string, journals ...Journal) *Service {
	return NewServiceWithCatalogs(core, defaultDriver, routing.BuiltinCatalogRegistry(), journals...)
}

func NewServiceWithCatalogs(core Core, defaultDriver string, catalogs routing.CatalogProvider, journals ...Journal) *Service {
	defaultDriver = strings.TrimSpace(defaultDriver)
	if defaultDriver == "" {
		defaultDriver = "codex"
	}
	if catalogs == nil {
		catalogs = routing.BuiltinCatalogRegistry()
	}
	var journal Journal
	if len(journals) > 0 {
		journal = journals[0]
	}
	var memory MemoryBackend
	if candidate, ok := journal.(MemoryBackend); ok {
		memory = candidate
	}
	return &Service{core: core, defaultDriver: defaultDriver, catalogs: catalogs, journal: journal, memory: memory, now: time.Now}
}

func (s *Service) SystemCapabilities(ctx context.Context, request SystemCapabilitiesRequest) (SystemCapabilitiesResponse, *Error) {
	reports := s.core.DriverCapabilityReports(ctx, strings.TrimSpace(request.Project))
	drivers := make([]DriverCapability, 0, len(reports))
	for _, report := range reports {
		targets := append([]string(nil), report.Targets...)
		sort.Strings(targets)
		drivers = append(drivers, DriverCapability{Driver: report.Driver, Targets: targets, Ready: report.Ready, Diagnostic: report.Diagnostic, MaxConcurrentAgents: report.MaxConcurrentAgents, SupportsConcurrentCancel: report.SupportsConcurrentCancel})
	}
	sort.Slice(drivers, func(i, j int) bool { return drivers[i].Driver < drivers[j].Driver })
	catalog, catalogErr := s.routingCatalogResponse()
	if catalogErr != nil {
		return SystemCapabilitiesResponse{}, internalError(catalogErr)
	}
	response := SystemCapabilitiesResponse{APIVersion: APIVersion, WorkflowSchemaVersion: WorkflowSchemaVersion, WorkflowSchema: append(json.RawMessage(nil), WorkflowJSONSchema...), NodeTypes: []string{"agent", "approval"}, ActionTypes: []ActionType{ActionApprove, ActionReject, ActionAnswer, ActionRetry, ActionCancel}, Drivers: drivers, Limits: StableLimits(), ErrorCodes: append([]ErrorCode(nil), StableErrorCodes...), MinimalExample: append(json.RawMessage(nil), MinimalWorkflowExample...), AuthoringGuide: StableAuthoringGuide(), RoutingCatalog: RoutingCatalogSummary{SchemaVersion: catalog.Catalog.SchemaVersion, PolicyVersion: catalog.Catalog.PolicyVersion, Source: catalog.Source, CatalogHash: catalog.CatalogHash, ModelCount: len(catalog.Catalog.Models), InspectMethod: "routing.catalog"}}
	if err := ensureResponseBound(response, MaxSchemaResponseBytes); err != nil {
		return SystemCapabilitiesResponse{}, internalError(err)
	}
	return response, nil
}

func (s *Service) RoutingCatalog(_ context.Context, _ RoutingCatalogRequest) (RoutingCatalogResponse, *Error) {
	response, err := s.routingCatalogResponse()
	if err != nil {
		return RoutingCatalogResponse{}, internalError(err)
	}
	if err := ensureResponseBound(response, MaxSchemaResponseBytes); err != nil {
		return RoutingCatalogResponse{}, internalError(err)
	}
	return response, nil
}

func (s *Service) routingCatalogResponse() (RoutingCatalogResponse, error) {
	catalog, hash, err := s.catalogs.ActiveCatalog()
	if err != nil {
		return RoutingCatalogResponse{}, fmt.Errorf("load active routing catalog: %w", err)
	}
	if err := routing.ValidateCatalog(catalog); err != nil {
		return RoutingCatalogResponse{}, fmt.Errorf("validate active routing catalog: %w", err)
	}
	_, dynamicAvailability := s.catalogs.(routing.TargetAvailabilityGate)
	return RoutingCatalogResponse{
		APIVersion: APIVersion, Source: routingCatalogSource(hash), CatalogHash: hash, Catalog: catalog,
		Limits:     RoutingCatalogLimits{MaxCatalogModels: routing.MaxCatalogModels, MaxCandidates: routing.MaxCandidates, MaxFallbacks: routing.MaxFallbacks, MaxRoutingBudgetBytes: routing.MaxRoutingBudgetBytes, MaxCostUnits: routing.MaxCostUnits},
		ErrorCodes: append([]routing.ErrorCode(nil), routing.StableErrorCodes...), DynamicAvailability: dynamicAvailability,
	}, nil
}

func routingCatalogSource(hash string) string {
	legacyHash, err := routing.CatalogHash(routing.BuiltinCatalogV1())
	if err == nil && hash == legacyHash {
		return routing.BuiltinCatalogSourceV1
	}
	return routing.DynamicCatalogSourceV1
}

func (s *Service) WorkflowValidate(ctx context.Context, request WorkflowValidateRequest) (WorkflowValidateResponse, *Error) {
	response := WorkflowValidateResponse{APIVersion: APIVersion, WorkflowSchemaVersion: WorkflowSchemaVersion, Issues: []ValidationIssue{}, CapabilityGaps: []ValidationIssue{}, RoutingPreviews: []RoutingPreview{}, Warnings: []string{}, RoutingRequirements: []RoutingRequirementView{}}
	normalized, issues := parseWorkflow(request.Workflow, request.Inputs)
	if len(issues) > 0 {
		response.Issues = issues
		return response, nil
	}
	if err := workflow.ValidateContextBindings(normalized.Document, request.ContextBindings); err != nil {
		response.Issues = append(response.Issues, ValidationIssue{Kind: "static", Path: "$.contextBindings", Code: "context_bindings", Message: err.Error()})
		return response, nil
	}
	response.Warnings = append(response.Warnings, normalized.Warnings...)
	response.RoutingRequirements = effectiveRoutingRequirements(normalized)
	response.RoutingPreviews = s.routingPreviews(normalized, request.Driver, request.Target)
	response.CapabilityGaps = s.capabilityGaps(ctx, strings.TrimSpace(request.Project), normalized, request.Driver, request.Target)
	response.Valid = len(response.Issues) == 0
	if err := ensureResponseBound(response, MaxSchemaResponseBytes); err != nil {
		return WorkflowValidateResponse{}, internalError(err)
	}
	return response, nil
}

func (s *Service) WorkflowExplain(ctx context.Context, request WorkflowExplainRequest) (WorkflowExplainResponse, *Error) {
	normalized, issues := parseWorkflow(request.Workflow, request.Inputs)
	if len(issues) > 0 {
		return WorkflowExplainResponse{}, NewError(CodeInvalidWorkflow, "workflow is invalid", map[string]any{"issues": issues})
	}
	if err := workflow.ValidateContextBindings(normalized.Document, request.ContextBindings); err != nil {
		return WorkflowExplainResponse{}, NewError(CodeInvalidWorkflow, "workflow context bindings are invalid", map[string]any{"issues": []ValidationIssue{{Kind: "static", Path: "$.contextBindings", Code: "context_bindings", Message: err.Error()}}})
	}
	layers := make([][]string, 0)
	levels := make(map[string]int, len(normalized.TopologicalOrder))
	nodes := make([]ExplainNode, 0, len(normalized.TopologicalOrder))
	for _, nodeID := range normalized.TopologicalOrder {
		definition := normalized.Document.Nodes[nodeID]
		level := 0
		for _, dependency := range definition.DependsOn {
			if levels[dependency]+1 > level {
				level = levels[dependency] + 1
			}
		}
		levels[nodeID] = level
		for len(layers) <= level {
			layers = append(layers, []string{})
		}
		layers[level] = append(layers[level], nodeID)
		policy := workflow.EffectiveContextPolicy(normalized.Document, definition)
		contextSources := orderedContextSources(normalized, nodeID, policy)
		node := ExplainNode{ID: nodeID, Type: definition.Type, DependsOn: append([]string(nil), definition.DependsOn...), ParallelLayer: level, ContextSources: contextSources, ProjectInstructions: append([]string(nil), policy.ProjectInstructions...), MemoryBindings: append([]workflow.MemoryBinding(nil), request.ContextBindings.MemoryByNode[nodeID]...), ContextPolicyVersion: normalized.ContextPolicyVersion}
		if definition.Type == "approval" {
			node.ApprovalPrompt = definition.Prompt
		} else {
			requirement := workflow.EffectiveRoutingRequirement(normalized.Document, definition)
			node.Routing = &requirement
			driver, target, err := resolvedSelection(normalized, definition, request.Driver, request.Target, s.defaultDriver)
			if err != nil {
				return WorkflowExplainResponse{}, NewError(CodeInvalidWorkflow, "workflow agent selection is invalid", map[string]any{"path": "$.nodes." + nodeID + ".agent", "detail": err.Error()})
			}
			node.Agent = &ResolvedAgent{Driver: driver, Target: target}
		}
		if definition.When != nil {
			condition, err := json.Marshal(definition.When)
			if err != nil {
				return WorkflowExplainResponse{}, internalError(err)
			}
			node.Condition = condition
		}
		nodes = append(nodes, node)
	}
	response := WorkflowExplainResponse{APIVersion: APIVersion, WorkflowSchemaVersion: WorkflowSchemaVersion, Name: normalized.Document.Name, TopologicalOrder: append([]string(nil), normalized.TopologicalOrder...), ParallelLayers: layers, Nodes: nodes, CapabilityGaps: s.capabilityGaps(ctx, strings.TrimSpace(request.Project), normalized, request.Driver, request.Target), RoutingPreviews: s.routingPreviews(normalized, request.Driver, request.Target), Warnings: append([]string(nil), normalized.Warnings...)}
	if err := ensureResponseBound(response, MaxResponseBytes); err != nil {
		return WorkflowExplainResponse{}, internalError(err)
	}
	return response, nil
}

func effectiveRoutingRequirements(normalized workflow.Normalized) []RoutingRequirementView {
	views := make([]RoutingRequirementView, 0, len(normalized.TopologicalOrder))
	for _, nodeID := range normalized.TopologicalOrder {
		node := normalized.Document.Nodes[nodeID]
		if node.Type != "agent" {
			continue
		}
		views = append(views, RoutingRequirementView{NodeID: nodeID, Requirement: workflow.EffectiveRoutingRequirement(normalized.Document, node)})
	}
	return views
}

func (s *Service) routingPreviews(normalized workflow.Normalized, driverOverride, targetOverride string) []RoutingPreview {
	previews := make([]RoutingPreview, 0)
	for _, nodeID := range normalized.TopologicalOrder {
		definition := normalized.Document.Nodes[nodeID]
		if definition.Type != "agent" {
			continue
		}
		requirement := workflow.EffectiveRoutingRequirement(normalized.Document, definition)
		preview := RoutingPreview{NodeID: nodeID, Requirement: requirement}
		driver, target, selectionErr := resolvedSelection(normalized, definition, driverOverride, targetOverride, s.defaultDriver)
		preview.Driver, preview.Target = driver, target
		if selectionErr != nil {
			preview.Issue = routingPreviewIssue("$.nodes."+nodeID+".agent", "driver_selection", selectionErr)
			previews = append(previews, preview)
			continue
		}
		decision, err := previewRoutingDecision(s.catalogs, driver, requirement)
		if err != nil {
			preview.Issue = routingPreviewIssue("$.nodes."+nodeID+".agent.routing", "routing_unavailable", err)
		} else {
			preview.Decision = decision
		}
		previews = append(previews, preview)
	}
	return previews
}

func previewRoutingDecision(catalogs routing.CatalogProvider, driver string, requirement routing.RoutingRequirementV1) (*routing.RoutingDecisionV1, error) {
	catalog, catalogHash, err := catalogs.ActiveCatalog()
	if err != nil {
		return nil, fmt.Errorf("load active routing catalog: %w", err)
	}
	knownDriver := false
	for _, model := range catalog.Models {
		if model.Target.Driver == driver {
			knownDriver = true
			break
		}
	}
	if !knownDriver {
		return nil, fmt.Errorf("driver %q has no trusted routing catalog target", driver)
	}
	requirement = routing.ApplyCodexProductPreference(catalog, driver, requirement)
	decision, err := routing.ResolveV1(routing.ResolveRequestV1{
		Catalog: catalog, CatalogHash: catalogHash, Requirement: requirement,
		Budget: routing.BudgetGrantV1{MaxCostUnits: requirement.MaxCostUnits, ContextBytes: requirement.MaxContextBytes, OutputBytes: requirement.MaxOutputBytes},
	})
	if err != nil {
		return nil, err
	}
	if decision.Selected.Driver != driver {
		return nil, fmt.Errorf("routing selected Driver %q for requested Driver %q", decision.Selected.Driver, driver)
	}
	return &decision, nil
}

func routingPreviewIssue(path, fallbackCode string, err error) *ValidationIssue {
	code := fallbackCode
	var contractErr *routing.ContractError
	if errors.As(err, &contractErr) {
		code = string(contractErr.Code)
	}
	return &ValidationIssue{Kind: "routing", Path: path, Code: code, Message: err.Error()}
}

func (s *Service) RunStart(ctx context.Context, request RunStartRequest) (RunStartResponse, *Error) {
	if err := validateExternalID("clientRequestId", request.ClientRequestID); err != nil {
		return RunStartResponse{}, err
	}
	project := strings.TrimSpace(request.Project)
	if project == "" {
		return RunStartResponse{}, NewError(CodeInvalidArgument, "project is required", map[string]any{"path": "$.project"})
	}
	normalized, issues := parseWorkflow(request.Workflow, request.Inputs)
	if len(issues) > 0 {
		return RunStartResponse{}, NewError(CodeInvalidWorkflow, "workflow is invalid", map[string]any{"issues": issues})
	}
	if err := workflow.ValidateContextBindings(normalized.Document, request.ContextBindings); err != nil {
		return RunStartResponse{}, NewError(CodeInvalidWorkflow, "workflow context bindings are invalid", map[string]any{"issues": []ValidationIssue{{Kind: "static", Path: "$.contextBindings", Code: "context_bindings", Message: err.Error()}}})
	}
	return s.runStartNormalized(ctx, request, project, normalized)
}

func (s *Service) runStartNormalized(ctx context.Context, request RunStartRequest, project string, normalized workflow.Normalized) (RunStartResponse, *Error) {
	if s.journal == nil {
		return RunStartResponse{}, NewError(CodeInternal, "durable application journal is unavailable", nil)
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	requestHash, canonical, err := canonicalRequest(request)
	if err != nil {
		return RunStartResponse{}, internalError(err)
	}
	record, readErr := s.journal.ReadApplicationJournal("start", request.ClientRequestID)
	if readErr == nil {
		if record.RequestHash != requestHash {
			return RunStartResponse{}, conflictError("clientRequestId is already bound to a different payload", map[string]any{"id": request.ClientRequestID})
		}
		if record.State == store.JournalMutated || record.State == store.JournalCommitted {
			return replayJournalResponse[RunStartResponse](s, record)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return RunStartResponse{}, internalError(readErr)
	}
	if gaps := s.capabilityGaps(ctx, project, normalized, request.Driver, request.Target); len(gaps) > 0 {
		return RunStartResponse{}, NewError(CodeCapabilityUnavailable, "workflow requires unavailable driver capabilities", map[string]any{"gaps": gaps})
	}
	if readErr != nil {
		plannedRunID, err := newApplicationRunID()
		if err != nil {
			return RunStartResponse{}, internalError(err)
		}
		record, err = s.journal.BeginApplicationJournal("start", request.ClientRequestID, requestHash, canonical, plannedRunID, s.now())
		if err != nil {
			return RunStartResponse{}, mapJournalError(err, request.ClientRequestID)
		}
	}
	snapshot, err := s.core.StartWorkflow(ctx, run.StartWorkflowRequest{RunID: record.PlannedRunID, InitializationTime: record.CreatedAt, Project: project, Driver: strings.TrimSpace(request.Driver), Target: strings.TrimSpace(request.Target), Normalized: &normalized, Inputs: request.Inputs, ContextBindings: request.ContextBindings})
	if err != nil {
		return RunStartResponse{}, mapCoreError(err, "could not start run")
	}
	return persistJournalResponse(s, "start", request.ClientRequestID, requestHash, startResponse(snapshot))
}

func (s *Service) RunList(_ context.Context, request RunListRequest) (RunListResponse, *Error) {
	limit, appErr := boundedLimit(request.Limit, DefaultListLimit, MaxListLimit, "limit")
	if appErr != nil {
		return RunListResponse{}, appErr
	}
	cursor, appErr := decodeCursor(request.Cursor)
	if appErr != nil {
		return RunListResponse{}, appErr
	}
	ids, err := s.core.ListRunIDs()
	if err != nil {
		return RunListResponse{}, mapCoreError(err, "could not list runs")
	}
	items := make([]RunSummary, 0, len(ids))
	for _, id := range ids {
		view, statusErr := s.core.Status(id)
		if statusErr != nil || view.Legacy || view.Run == nil {
			continue
		}
		item := summarizeRun(*view.Run)
		if request.Filter.Project != "" && item.Project != request.Filter.Project || request.Filter.Phase != "" && item.Phase != request.Filter.Phase || request.Filter.Conclusion != "" && item.Conclusion != request.Filter.Conclusion {
			continue
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt > items[j].CreatedAt
		}
		return items[i].RunID < items[j].RunID
	})
	start := 0
	if cursor != "" {
		start = -1
		for index := range items {
			if items[index].RunID == cursor {
				start = index + 1
				break
			}
		}
		if start < 0 {
			return RunListResponse{}, NewError(CodeInvalidArgument, "cursor does not identify a run in the filtered result", map[string]any{"path": "$.cursor"})
		}
	}
	items = items[start:]
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	response := RunListResponse{APIVersion: APIVersion, Items: items}
	for len(response.Items) > 0 {
		if err := ensureResponseBound(response, MaxResponseBytes); err == nil {
			break
		}
		more = true
		response.Items = response.Items[:len(response.Items)-1]
	}
	if more && len(response.Items) > 0 {
		response.NextCursor = encodeCursor(response.Items[len(response.Items)-1].RunID)
	}
	return response, nil
}

func (s *Service) RunGet(_ context.Context, request RunGetRequest) (RunGetResponse, *Error) {
	if strings.TrimSpace(request.RunID) == "" {
		return RunGetResponse{}, NewError(CodeInvalidArgument, "runId is required", map[string]any{"path": "$.runId"})
	}
	view, err := s.core.Status(request.RunID)
	if err != nil {
		return RunGetResponse{}, mapCoreError(err, "run not found")
	}
	if view.Legacy || view.Run == nil {
		return RunGetResponse{}, NewError(CodeCapabilityUnavailable, "legacy run is available only through compatibility status", map[string]any{"runId": request.RunID})
	}
	result := RunGetResponse{APIVersion: APIVersion, Run: s.mapRunView(view)}
	if err := ensureResponseBound(result, MaxResponseBytes); err != nil {
		return RunGetResponse{}, internalError(err)
	}
	return result, nil
}

func (s *Service) RunEvents(ctx context.Context, request RunEventsRequest) (RunEventsResponse, *Error) {
	if strings.TrimSpace(request.RunID) == "" {
		return RunEventsResponse{}, NewError(CodeInvalidArgument, "runId is required", map[string]any{"path": "$.runId"})
	}
	limit, appErr := boundedLimit(request.Limit, DefaultEventLimit, MaxEventLimit, "limit")
	if appErr != nil {
		return RunEventsResponse{}, appErr
	}
	if request.WaitMS < 0 || request.WaitMS > MaxEventWaitMS {
		return RunEventsResponse{}, NewError(CodeInvalidArgument, fmt.Sprintf("waitMs must be between 0 and %d", MaxEventWaitMS), map[string]any{"path": "$.waitMs", "max": MaxEventWaitMS})
	}
	if _, err := s.core.Status(request.RunID); err != nil {
		return RunEventsResponse{}, mapCoreError(err, "run not found")
	}
	wake := make(chan struct{}, 1)
	unsubscribe := s.core.AddEventSink(func(event run.WorkflowEvent) {
		if event.RunID == request.RunID && event.Sequence > request.AfterSequence {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
	})
	defer unsubscribe()
	response, err := s.readEvents(request.RunID, request.AfterSequence, limit)
	if err != nil {
		return RunEventsResponse{}, mapCoreError(err, "could not read run events")
	}
	if len(response.Events) == 0 && request.WaitMS > 0 {
		timer := time.NewTimer(time.Duration(request.WaitMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return RunEventsResponse{}, NewError(CodeInternal, "event wait was cancelled", map[string]any{"detail": ctx.Err().Error()})
		case <-timer.C:
		case <-wake:
		}
		response, err = s.readEvents(request.RunID, request.AfterSequence, limit)
		if err != nil {
			return RunEventsResponse{}, mapCoreError(err, "could not read run events")
		}
	}
	return response, nil
}

func (s *Service) RunAction(ctx context.Context, request RunActionRequest) (RunActionResponse, *Error) {
	if err := validateExternalID("actionId", request.ActionID); err != nil {
		return RunActionResponse{}, err
	}
	if strings.TrimSpace(request.RunID) == "" {
		return RunActionResponse{}, NewError(CodeInvalidArgument, "runId is required", map[string]any{"path": "$.runId"})
	}
	if request.ExpectedStateVersion == 0 {
		return RunActionResponse{}, NewError(CodeInvalidArgument, "expectedStateVersion is required", map[string]any{"path": "$.expectedStateVersion"})
	}
	if s.journal == nil {
		return RunActionResponse{}, NewError(CodeInternal, "durable application journal is unavailable", nil)
	}
	requestHash, canonical, err := canonicalRequest(request)
	if err != nil {
		return RunActionResponse{}, internalError(err)
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	record, readErr := s.journal.ReadApplicationJournal("action", request.ActionID)
	if readErr == nil {
		if record.RequestHash != requestHash {
			return RunActionResponse{}, conflictError("actionId is already bound to a different payload", map[string]any{"id": request.ActionID})
		}
		if record.State == store.JournalMutated || record.State == store.JournalCommitted {
			return replayActionJournalResponse(s, record)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return RunActionResponse{}, internalError(readErr)
	}
	view, err := s.core.Status(request.RunID)
	if err != nil {
		return RunActionResponse{}, mapCoreError(err, "run not found")
	}
	if view.Legacy || view.Run == nil {
		return RunActionResponse{}, NewError(CodeCapabilityUnavailable, "legacy run does not support Agent-native actions", map[string]any{"runId": request.RunID})
	}
	current := view.Run
	if receipt, ok := current.ActionReceipts[request.ActionID]; ok {
		if receipt.RequestHash != requestHash {
			return RunActionResponse{}, conflictError("actionId is already bound to a different payload", map[string]any{"id": request.ActionID})
		}
		return persistJournalResponse(s, "action", request.ActionID, requestHash, actionResponse(request, *current))
	}
	if current.StateVersion != request.ExpectedStateVersion {
		if readErr == nil {
			appErr := conflictError("state version conflict", map[string]any{"expectedStateVersion": request.ExpectedStateVersion, "currentStateVersion": current.StateVersion})
			return RunActionResponse{}, persistActionError(s, request.ActionID, requestHash, appErr)
		}
		return RunActionResponse{}, conflictError("state version conflict", map[string]any{"expectedStateVersion": request.ExpectedStateVersion, "currentStateVersion": current.StateVersion})
	}
	// A pending Application journal may be resuming a durable Core action intent
	// after its Node mutation committed but before the Run receipt did. The
	// current Node view then no longer describes the original actionable shape;
	// Core owns the exact intent replay and must be allowed to complete it.
	if readErr != nil {
		if appErr := validateActionShape(request, view); appErr != nil {
			return RunActionResponse{}, appErr
		}
	}
	if readErr != nil {
		var beginErr error
		record, beginErr = s.journal.BeginApplicationJournal("action", request.ActionID, requestHash, canonical, request.RunID, s.now())
		if beginErr != nil {
			return RunActionResponse{}, mapJournalError(beginErr, request.ActionID)
		}
	}
	var snapshot run.WorkflowSnapshot
	if request.Type == ActionCancel {
		expected := request.ExpectedStateVersion
		snapshot, err = s.core.CancelWithPrecondition(ctx, run.CancelRequest{RunID: request.RunID, ExpectedStateVersion: &expected, ActionID: request.ActionID, ActionRequestHash: requestHash})
	} else {
		expected := request.ExpectedStateVersion
		snapshot, err = s.core.Resume(ctx, run.ResumeRequest{RunID: request.RunID, ExpectedStateVersion: &expected, Action: &run.ResumeAction{ActionID: request.ActionID, ActionRequestHash: requestHash, Type: string(request.Type), NodeID: request.NodeID, ExpectedAttempt: request.ExpectedAttempt, Reason: request.Reason, Answers: request.Answers, AcknowledgeDuplicateRisk: request.AcknowledgeDuplicateRisk}})
	}
	if err != nil {
		appErr := mapActionError(err)
		if terminalActionError(appErr) {
			if aborter, ok := s.core.(interface {
				AbortActionIntent(string, string, string) error
			}); ok {
				if abortErr := aborter.AbortActionIntent(request.RunID, request.ActionID, requestHash); abortErr != nil {
					return RunActionResponse{}, internalError(abortErr)
				}
			}
			return RunActionResponse{}, persistActionError(s, request.ActionID, requestHash, appErr)
		}
		return RunActionResponse{}, appErr
	}
	return persistJournalResponse(s, "action", request.ActionID, requestHash, actionResponse(request, snapshot))
}

func (s *Service) RunResult(_ context.Context, request RunResultRequest) (RunResultResponse, *Error) {
	view, err := s.core.Status(request.RunID)
	if err != nil {
		return RunResultResponse{}, mapCoreError(err, "run not found")
	}
	if view.Legacy || view.Run == nil {
		return RunResultResponse{}, NewError(CodeCapabilityUnavailable, "legacy run has no Agent-native result", map[string]any{"runId": request.RunID})
	}
	if view.Run.Phase != run.PhaseCompleted {
		return RunResultResponse{}, NewError(CodeNotReady, "run result is not ready", map[string]any{"runId": request.RunID, "phase": view.Run.Phase})
	}
	results := make([]NodeResult, 0, len(view.Nodes))
	for _, nodeID := range view.Run.TopologicalOrder {
		for _, node := range view.Nodes {
			if node.ID == nodeID {
				results = append(results, NodeResult{NodeID: node.ID, Conclusion: string(node.Conclusion), Result: mapResult(node.Result)})
				break
			}
		}
	}
	response := RunResultResponse{APIVersion: APIVersion, RunID: view.Run.ID, Conclusion: string(view.Run.Conclusion), Summary: view.Run.Summary, Results: results, CompletedAt: formatTime(view.Run.UpdatedAt)}
	if err := ensureResponseBound(response, MaxResponseBytes); err != nil {
		return RunResultResponse{}, internalError(err)
	}
	return response, nil
}

func (s *Service) Recover(ctx context.Context) *Error {
	if s.journal == nil {
		return NewError(CodeInternal, "durable application journal is unavailable", nil)
	}
	records, err := s.journal.ListPendingApplicationJournals()
	if err != nil {
		return internalError(err)
	}
	for _, record := range records {
		switch record.Kind {
		case "start":
			var request RunStartRequest
			if err := json.Unmarshal(record.Request, &request); err != nil {
				return internalError(fmt.Errorf("decode pending start journal %q: %w", record.ID, err))
			}
			if _, appErr := s.RunStart(ctx, request); appErr != nil {
				return appErr
			}
		case "action":
			var request RunActionRequest
			if err := json.Unmarshal(record.Request, &request); err != nil {
				return internalError(fmt.Errorf("decode pending action journal %q: %w", record.ID, err))
			}
			if _, appErr := s.RunAction(ctx, request); appErr != nil && !terminalActionError(appErr) {
				return appErr
			}
		default:
			return internalError(fmt.Errorf("unsupported pending journal kind %q", record.Kind))
		}
	}
	return nil
}

func startResponse(snapshot run.WorkflowSnapshot) RunStartResponse {
	return RunStartResponse{APIVersion: APIVersion, RunID: snapshot.ID, StateVersion: snapshot.StateVersion, Attach: "fishyume attach " + snapshot.ID}
}

func actionResponse(request RunActionRequest, snapshot run.WorkflowSnapshot) RunActionResponse {
	if receipt, ok := snapshot.ActionReceipts[request.ActionID]; ok {
		return RunActionResponse{APIVersion: APIVersion, ActionID: request.ActionID, RunID: request.RunID, Type: request.Type, StateVersion: receipt.StateVersion, Phase: string(receipt.Phase), Conclusion: string(receipt.Conclusion)}
	}
	return RunActionResponse{APIVersion: APIVersion, ActionID: request.ActionID, RunID: request.RunID, Type: request.Type, StateVersion: snapshot.StateVersion, Phase: string(snapshot.Phase), Conclusion: string(snapshot.Conclusion)}
}

func canonicalRequest(value any) (string, json.RawMessage, error) {
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), canonical, nil
}

func newApplicationRunID() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate application run id: %w", err)
	}
	return "run-" + hex.EncodeToString(value), nil
}

func persistJournalResponse[T any](s *Service, kind, id, requestHash string, response T) (T, *Error) {
	encoded, err := json.Marshal(response)
	if err != nil {
		var zero T
		return zero, internalError(err)
	}
	record, err := s.journal.MarkApplicationJournalMutated(kind, id, requestHash, encoded, s.now())
	if err != nil {
		var zero T
		return zero, mapJournalError(err, id)
	}
	if _, err := s.journal.CommitApplicationJournal(kind, id, requestHash, s.now()); err != nil {
		var zero T
		return zero, mapJournalError(err, id)
	}
	if string(record.Response) != string(encoded) {
		return decodeJournalResponse[T](record)
	}
	return response, nil
}

func replayJournalResponse[T any](s *Service, record store.ApplicationJournalRecord) (T, *Error) {
	if record.State == store.JournalMutated {
		committed, err := s.journal.CommitApplicationJournal(record.Kind, record.ID, record.RequestHash, s.now())
		if err != nil {
			var zero T
			return zero, mapJournalError(err, record.ID)
		}
		record = committed
	}
	return decodeJournalResponse[T](record)
}

type actionJournalError struct {
	Error *Error `json:"error"`
}

func terminalActionError(err *Error) bool {
	return err != nil && (err.Code == CodeConflict || err.Code == CodeInvalidArgument || err.Code == CodeNotFound || err.Code == CodeCapabilityUnavailable || err.Code == CodeNotReady)
}

func persistActionError(s *Service, id, requestHash string, appErr *Error) *Error {
	encoded, err := json.Marshal(actionJournalError{Error: appErr})
	if err != nil {
		return internalError(err)
	}
	if _, err := s.journal.MarkApplicationJournalMutated("action", id, requestHash, encoded, s.now()); err != nil {
		return mapJournalError(err, id)
	}
	if _, err := s.journal.CommitApplicationJournal("action", id, requestHash, s.now()); err != nil {
		return mapJournalError(err, id)
	}
	return appErr
}

func replayActionJournalResponse(s *Service, record store.ApplicationJournalRecord) (RunActionResponse, *Error) {
	if record.State == store.JournalMutated {
		if _, err := s.journal.CommitApplicationJournal(record.Kind, record.ID, record.RequestHash, s.now()); err != nil {
			return RunActionResponse{}, mapJournalError(err, record.ID)
		}
	}
	var terminal actionJournalError
	if err := json.Unmarshal(record.Response, &terminal); err == nil && terminal.Error != nil {
		return RunActionResponse{}, terminal.Error
	}
	return decodeJournalResponse[RunActionResponse](record)
}

func decodeJournalResponse[T any](record store.ApplicationJournalRecord) (T, *Error) {
	var response T
	if err := json.Unmarshal(record.Response, &response); err != nil {
		return response, internalError(fmt.Errorf("decode %s journal response %q: %w", record.Kind, record.ID, err))
	}
	return response, nil
}

func mapJournalError(err error, id string) *Error {
	var conflict *store.JournalConflictError
	if errors.As(err, &conflict) {
		return conflictError("request id is already bound to a different payload", map[string]any{"id": id})
	}
	return NewError(CodeInternal, "durable application journal failed", map[string]any{"detail": err.Error()})
}

func parseWorkflow(input WorkflowInput, inputs map[string]any) (workflow.Normalized, []ValidationIssue) {
	if (input.Source == nil) == (len(input.Document) == 0) {
		return workflow.Normalized{}, []ValidationIssue{{Kind: "static", Path: "$.workflow", Code: "exactly_one", Message: "provide exactly one of workflow.source or workflow.document"}}
	}
	var data []byte
	filename := "workflow.json"
	if input.Source != nil {
		filename = strings.TrimSpace(input.Source.Filename)
		if filename == "" {
			filename = "workflow.yaml"
		}
		data = []byte(input.Source.Content)
		if len(data) == 0 {
			return workflow.Normalized{}, []ValidationIssue{{Kind: "static", Path: "$.workflow.source.content", Code: "required", Message: "workflow source content is required"}}
		}
	} else {
		data = input.Document
		if !json.Valid(data) {
			return workflow.Normalized{}, []ValidationIssue{{Kind: "static", Path: "$.workflow.document", Code: "invalid_json", Message: "workflow document must be valid JSON"}}
		}
	}
	normalized, err := workflow.Parse(data, filename, inputs)
	if err != nil {
		return workflow.Normalized{}, []ValidationIssue{{Kind: "static", Path: "$", Code: classifyWorkflowError(err), Message: err.Error()}}
	}
	for _, warning := range normalized.Warnings {
		if strings.Contains(warning, "deprecated") {
			return workflow.Normalized{}, []ValidationIssue{{Kind: "static", Path: "$", Code: "legacy_field", Message: "Agent-native workflows must use driver/target fields"}}
		}
	}
	return normalized, nil
}

func classifyWorkflowError(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "decode workflow") || strings.Contains(message, "YAML"):
		return "syntax"
	case strings.Contains(message, "apiVersion"):
		return "api_version"
	case strings.Contains(message, "cycle"):
		return "dependency_cycle"
	case strings.Contains(message, "input"):
		return "input"
	default:
		return "structure"
	}
}

func (s *Service) capabilityGaps(ctx context.Context, project string, normalized workflow.Normalized, driverOverride, targetOverride string) []ValidationIssue {
	reports := s.core.DriverCapabilityReports(ctx, project)
	byName := make(map[string]run.DriverCapabilityReport, len(reports))
	for _, report := range reports {
		byName[report.Driver] = report
	}
	gaps := make([]ValidationIssue, 0)
	for _, nodeID := range normalized.TopologicalOrder {
		definition := normalized.Document.Nodes[nodeID]
		if definition.Type != "agent" {
			continue
		}
		driver, target, err := resolvedSelection(normalized, definition, driverOverride, targetOverride, s.defaultDriver)
		if err != nil {
			gaps = append(gaps, ValidationIssue{Kind: "capability", Path: "$.nodes." + nodeID + ".agent", Code: "selection", Message: err.Error()})
			continue
		}
		report, ok := byName[driver]
		if !ok {
			gaps = append(gaps, ValidationIssue{Kind: "capability", Path: "$.nodes." + nodeID + ".agent.driver", Code: "driver_unavailable", Message: fmt.Sprintf("driver %q is unavailable", driver)})
			continue
		}
		if !contains(report.Targets, target) {
			gaps = append(gaps, ValidationIssue{Kind: "capability", Path: "$.nodes." + nodeID + ".agent.target", Code: "target_unavailable", Message: fmt.Sprintf("driver %q does not support target %q", driver, target)})
		}
		if !report.Ready {
			gaps = append(gaps, ValidationIssue{Kind: "capability", Path: "$.nodes." + nodeID + ".agent.driver", Code: "driver_not_ready", Message: report.Diagnostic})
		}
	}
	return gaps
}

func resolvedSelection(normalized workflow.Normalized, node workflow.Node, driverOverride, targetOverride, defaultDriver string) (string, string, error) {
	driver, target, err := workflow.ResolveAgent(normalized.Document.Defaults, node)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(driverOverride) != "" {
		driver = strings.TrimSpace(driverOverride)
	}
	if driver == "" {
		driver = defaultDriver
	}
	if strings.TrimSpace(targetOverride) != "" {
		target = strings.TrimSpace(targetOverride)
	}
	if target == "" {
		target = "local"
	}
	return driver, target, nil
}

func orderedAncestors(normalized workflow.Normalized, nodeID string) []string {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		for _, dependency := range normalized.Document.Nodes[id].DependsOn {
			if !seen[dependency] {
				seen[dependency] = true
				visit(dependency)
			}
		}
	}
	visit(nodeID)
	result := make([]string, 0, len(seen))
	for _, id := range normalized.TopologicalOrder {
		if seen[id] {
			result = append(result, id)
		}
	}
	return result
}

func orderedContextSources(normalized workflow.Normalized, nodeID string, policy workflow.ContextPolicy) []string {
	if normalized.ContextPolicyVersion != "context-policy/v1" {
		return orderedAncestors(normalized, nodeID)
	}
	selected := make(map[string]bool, len(policy.Dependencies))
	for _, dependency := range policy.Dependencies {
		selected[dependency] = true
	}
	result := make([]string, 0, len(selected))
	for _, candidate := range normalized.TopologicalOrder {
		if selected[candidate] {
			result = append(result, candidate)
		}
	}
	return result
}

func (s *Service) mapRunView(view run.StatusView) RunView {
	snapshot := *view.Run
	var topology map[string]topologyNodeView
	var parallelLayers [][]string
	if reader, ok := s.core.(interface {
		ReadWorkflow(string) (workflow.Normalized, error)
	}); ok {
		if normalized, err := reader.ReadWorkflow(snapshot.ID); err == nil {
			topology, parallelLayers = topologyProjection(normalized, snapshot.TopologicalOrder)
		}
	}
	result := RunView{RunSummary: summarizeRun(snapshot), Summary: snapshot.Summary, CancelRequested: snapshot.CancelRequested, EffectiveConcurrency: snapshot.EffectiveConcurrency, TopologicalOrder: append([]string(nil), snapshot.TopologicalOrder...), ParallelLayers: parallelLayers, Nodes: make([]NodeView, 0, len(view.Nodes)), DeprecationWarnings: append([]string(nil), snapshot.DeprecationWarnings...)}
	for _, nodeID := range snapshot.TopologicalOrder {
		for _, node := range view.Nodes {
			if node.ID != nodeID {
				continue
			}
			mapped := NodeView{NodeID: node.ID, Type: node.Type, DependsOn: []string{}, Phase: string(node.Phase), Conclusion: string(node.Conclusion), Reason: string(node.Reason), Diagnostic: node.Diagnostic, CurrentAttempt: node.CurrentAttempt, Result: mapResult(node.Result)}
			if projected, ok := topology[node.ID]; ok {
				mapped.DependsOn = append([]string(nil), projected.DependsOn...)
				mapped.ParallelLayer = projected.ParallelLayer
			}
			if node.CurrentAttempt > 0 {
				if attempt, err := s.core.ReadAttempt(snapshot.ID, node.ID, node.CurrentAttempt); err == nil {
					mapped.Attempt = &AttemptView{Number: attempt.Number, Phase: string(attempt.Phase), Conclusion: string(attempt.Conclusion), Reason: string(attempt.Reason), Driver: runAttemptDriver(attempt), Target: runAttemptTarget(attempt), RoutingDecision: attempt.RoutingDecision, ExecutionProfile: attempt.ExecutionProfile, RoutingUsage: attempt.RoutingUsage, SideEffectStatus: attempt.SideEffectStatus, FailureClass: attempt.FailureClass, ContextHash: attempt.ContextHash, Context: inspectContext(attempt), MemoryUsage: memoryUsageInspect(attempt), StartedAt: formatTime(attempt.StartedAt), UpdatedAt: formatTime(attempt.UpdatedAt)}
					if reader, ok := s.core.(interface {
						ReadAttemptOutput(string, string, int) (string, error)
					}); ok {
						if output, outputErr := reader.ReadAttemptOutput(snapshot.ID, node.ID, node.CurrentAttempt); outputErr == nil {
							mapped.Attempt.Activity = parseAttemptActivity(output)
						}
					}
					if attempt.CompletedAt != nil {
						mapped.Attempt.CompletedAt = formatTime(*attempt.CompletedAt)
					}
				}
			}
			result.Nodes = append(result.Nodes, mapped)
			break
		}
	}
	return result
}

type topologyNodeView struct {
	DependsOn     []string
	ParallelLayer int
}

func topologyProjection(normalized workflow.Normalized, order []string) (map[string]topologyNodeView, [][]string) {
	levels := make(map[string]int, len(order))
	projected := make(map[string]topologyNodeView, len(order))
	layers := make([][]string, 0)
	for _, nodeID := range order {
		definition, ok := normalized.Document.Nodes[nodeID]
		if !ok {
			continue
		}
		level := 0
		for _, dependency := range definition.DependsOn {
			if candidate := levels[dependency] + 1; candidate > level {
				level = candidate
			}
		}
		levels[nodeID] = level
		for len(layers) <= level {
			layers = append(layers, []string{})
		}
		layers[level] = append(layers[level], nodeID)
		projected[nodeID] = topologyNodeView{DependsOn: append([]string(nil), definition.DependsOn...), ParallelLayer: level}
	}
	return projected, layers
}

func inspectContext(attempt run.AttemptSnapshot) *ContextInspect {
	if attempt.ContextManifestV2 == nil {
		if attempt.ContextCompilerVersion == "" && attempt.ContextHash == "" {
			return nil
		}
		components := make([]ContextComponentInspect, 0, len(attempt.ContextManifest.Components))
		for _, component := range attempt.ContextManifest.Components {
			components = append(components, ContextComponentInspect{ID: component.Name, Kind: component.Name})
		}
		return &ContextInspect{CompilerVersion: attempt.ContextCompilerVersion, Hash: attempt.ContextHash, Components: components, Budget: map[string]int{}, Usage: map[string]int{}}
	}
	manifest := attempt.ContextManifestV2
	components := make([]ContextComponentInspect, 0, len(manifest.Components))
	truncated := false
	for _, component := range manifest.Components {
		components = append(components, ContextComponentInspect{ID: component.ID, Kind: string(component.Kind), Tier: string(component.Tier), SelectionReason: component.Provenance.Reason, ProvenanceSource: component.Provenance.Source, OriginalBytes: component.OriginalBytes, IncludedBytes: component.IncludedBytes, Truncation: string(component.Truncation)})
		truncated = truncated || component.Truncation != contextcompiler.TruncationNone
	}
	omissions := make([]ContextOmissionInspect, 0, len(manifest.Omissions))
	for _, omission := range manifest.Omissions {
		omissions = append(omissions, ContextOmissionInspect{ID: omission.ComponentID, Kind: string(omission.Kind), Reason: string(omission.Reason), OriginalBytes: omission.OriginalBytes})
	}
	var memoryUsage *MemoryUsageInspect
	if attempt.MemoryUsage != nil {
		memoryUsage = &MemoryUsageInspect{RecordIDs: append([]string(nil), attempt.MemoryUsage.RecordIDs...), Committed: attempt.MemoryUsage.Committed}
	}
	return &ContextInspect{SchemaVersion: manifest.SchemaVersion, CompilerVersion: manifest.CompilerVersion, Hash: manifest.EnvelopeHash, Budget: map[string]int{"totalBytes": manifest.Budget.TotalBytes, "requiredBytes": manifest.Budget.RequiredBytes, "importantBytes": manifest.Budget.ImportantBytes, "optionalBytes": manifest.Budget.OptionalBytes}, Usage: map[string]int{"totalBytes": manifest.Usage.TotalBytes, "requiredBytes": manifest.Usage.RequiredBytes, "importantBytes": manifest.Usage.ImportantBytes, "optionalBytes": manifest.Usage.OptionalBytes}, Components: components, Omissions: omissions, Truncated: truncated, MemoryUsage: memoryUsage}
}

func memoryUsageInspect(attempt run.AttemptSnapshot) *MemoryUsageInspect {
	if attempt.MemoryUsage == nil {
		return nil
	}
	return &MemoryUsageInspect{RecordIDs: append([]string(nil), attempt.MemoryUsage.RecordIDs...), Committed: attempt.MemoryUsage.Committed}
}

func summarizeRun(snapshot run.WorkflowSnapshot) RunSummary {
	return RunSummary{RunID: snapshot.ID, WorkflowName: snapshot.WorkflowName, Project: snapshot.Project, Driver: snapshot.ResolvedDriver, Target: snapshot.ResolvedTarget, Phase: string(snapshot.Phase), Conclusion: string(snapshot.Conclusion), StateVersion: snapshot.StateVersion, CreatedAt: formatTime(snapshot.CreatedAt), UpdatedAt: formatTime(snapshot.UpdatedAt)}
}

func runAttemptDriver(attempt run.AttemptSnapshot) string {
	if attempt.ResolvedDriver != "" {
		return attempt.ResolvedDriver
	}
	return attempt.Backend
}

func runAttemptTarget(attempt run.AttemptSnapshot) string {
	if attempt.ResolvedTarget != "" {
		return attempt.ResolvedTarget
	}
	return "local"
}

func mapResult(result *workflow.Result) *Result {
	if result == nil {
		return nil
	}
	questions := make([]Question, len(result.Questions))
	for index, question := range result.Questions {
		questions[index] = Question{ID: question.ID, Prompt: question.Prompt, Choices: append([]string(nil), question.Choices...), Required: question.Required}
	}
	return &Result{Summary: result.Summary, Artifacts: nonNil(result.Artifacts), Warnings: nonNil(result.Warnings), Checks: nonNil(result.Checks), Questions: questions, Decision: result.Decision, Reason: result.Reason, Usage: map[string]int{"inputTokensEstimated": result.Usage.InputTokensEstimated, "outputTokensEstimated": result.Usage.OutputTokensEstimated}}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string(nil), values...)
}

func (s *Service) readEvents(runID string, after uint64, limit int) (RunEventsResponse, error) {
	persisted, err := s.core.ReadEvents(runID)
	if err != nil {
		return RunEventsResponse{}, err
	}
	available := make([]Event, 0)
	for _, event := range persisted {
		if event.Sequence <= after {
			continue
		}
		available = append(available, Event{RunID: event.RunID, Sequence: event.Sequence, Type: event.Type, Phase: string(event.Phase), Conclusion: string(event.Conclusion), Reason: string(event.Reason), NodeID: event.NodeID, NodePhase: string(event.NodePhase), Message: event.Message, Timestamp: formatTime(event.Timestamp)})
	}
	more := len(available) > limit
	if more {
		available = available[:limit]
	}
	response := RunEventsResponse{APIVersion: APIVersion, RunID: runID, Events: available, NextAfterSequence: after, More: more}
	for len(response.Events) > 0 {
		response.NextAfterSequence = response.Events[len(response.Events)-1].Sequence
		if err := ensureResponseBound(response, MaxResponseBytes); err == nil {
			break
		}
		response.More = true
		response.Events = response.Events[:len(response.Events)-1]
	}
	return response, nil
}

func validateActionShape(request RunActionRequest, view run.StatusView) *Error {
	validType := request.Type == ActionApprove || request.Type == ActionReject || request.Type == ActionAnswer || request.Type == ActionRetry || request.Type == ActionCancel
	if !validType {
		return NewError(CodeInvalidArgument, "unsupported action type", map[string]any{"path": "$.type", "value": request.Type})
	}
	if request.Type == ActionCancel {
		if request.NodeID != "" || request.ExpectedAttempt != nil || request.Reason != "" || len(request.Answers) > 0 || request.AcknowledgeDuplicateRisk {
			return NewError(CodeInvalidArgument, "cancel action does not accept node fields", map[string]any{"path": "$"})
		}
		return nil
	}
	if request.NodeID == "" {
		return NewError(CodeInvalidArgument, "nodeId is required for node action", map[string]any{"path": "$.nodeId"})
	}
	var node *run.NodeSnapshot
	for index := range view.Nodes {
		if view.Nodes[index].ID == request.NodeID {
			node = &view.Nodes[index]
			break
		}
	}
	if node == nil {
		return NewError(CodeNotFound, "node not found", map[string]any{"nodeId": request.NodeID})
	}
	if request.Type == ActionAnswer || request.Type == ActionRetry {
		if request.ExpectedAttempt == nil || *request.ExpectedAttempt < 1 {
			return NewError(CodeInvalidArgument, "expectedAttempt is required for answer and retry", map[string]any{"path": "$.expectedAttempt"})
		}
		if node.CurrentAttempt != *request.ExpectedAttempt {
			return conflictError("attempt conflict", map[string]any{"expectedAttempt": *request.ExpectedAttempt, "currentAttempt": node.CurrentAttempt})
		}
	} else if request.ExpectedAttempt != nil {
		return NewError(CodeInvalidArgument, "approval actions do not accept expectedAttempt", map[string]any{"path": "$.expectedAttempt"})
	}
	if request.Type != ActionReject && request.Reason != "" {
		return NewError(CodeInvalidArgument, "reason is accepted only by reject", map[string]any{"path": "$.reason"})
	}
	if request.Type != ActionAnswer && len(request.Answers) > 0 {
		return NewError(CodeInvalidArgument, "answers are accepted only by answer", map[string]any{"path": "$.answers"})
	}
	if request.Type != ActionRetry && request.AcknowledgeDuplicateRisk {
		return NewError(CodeInvalidArgument, "acknowledgeDuplicateRisk is accepted only by retry", map[string]any{"path": "$.acknowledgeDuplicateRisk"})
	}
	return nil
}

func boundedLimit(value, defaultValue, maximum int, path string) (int, *Error) {
	if value == 0 {
		return defaultValue, nil
	}
	if value < 1 || value > maximum {
		return 0, NewError(CodeInvalidArgument, fmt.Sprintf("%s must be between 1 and %d", path, maximum), map[string]any{"path": "$." + path, "max": maximum})
	}
	return value, nil
}

func validateExternalID(name, value string) *Error {
	value = strings.TrimSpace(value)
	if value == "" {
		return NewError(CodeInvalidArgument, name+" is required", map[string]any{"path": "$." + name})
	}
	if len([]byte(value)) > MaxRequestIDBytes {
		return NewError(CodeInvalidArgument, fmt.Sprintf("%s exceeds %d bytes", name, MaxRequestIDBytes), map[string]any{"path": "$." + name, "maxBytes": MaxRequestIDBytes})
	}
	return nil
}

func encodeCursor(runID string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(runID))
}

func decodeCursor(cursor string) (string, *Error) {
	if cursor == "" {
		return "", nil
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(data) == 0 || len(data) > 128 {
		return "", NewError(CodeInvalidArgument, "cursor is invalid", map[string]any{"path": "$.cursor"})
	}
	return string(data), nil
}

func ensureResponseBound(value any, maximum int) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > maximum {
		return fmt.Errorf("response exceeds %d bytes", maximum)
	}
	return nil
}

func mapActionError(err error) *Error {
	message := err.Error()
	if strings.Contains(message, "conflict") || strings.Contains(message, "already has conflicting") || strings.Contains(message, "already pending for a different action") {
		return conflictError("action no longer applies", map[string]any{"detail": message})
	}
	if strings.Contains(message, "not waiting") || strings.Contains(message, "not in a retryable") || strings.Contains(message, "not the current") {
		return conflictError("action no longer applies", map[string]any{"detail": message})
	}
	if strings.Contains(message, "answer") || strings.Contains(message, "action") {
		return NewError(CodeInvalidArgument, "action payload is invalid", map[string]any{"detail": message})
	}
	return mapCoreError(err, "could not apply action")
}

func mapCoreError(err error, message string) *Error {
	if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no such file") || strings.Contains(err.Error(), "cannot find the file") {
		return NewError(CodeNotFound, message, nil)
	}
	if strings.Contains(err.Error(), "not ready") || strings.Contains(err.Error(), "is not ready") {
		return NewError(CodeCapabilityUnavailable, message, map[string]any{"detail": err.Error()})
	}
	if strings.Contains(err.Error(), "conflict") {
		return conflictError(message, map[string]any{"detail": err.Error()})
	}
	return NewError(CodeInternal, message, map[string]any{"detail": err.Error()})
}

func conflictError(message string, data map[string]any) *Error {
	return NewError(CodeConflict, message, data)
}

func internalError(err error) *Error {
	return NewError(CodeInternal, "internal application error", map[string]any{"detail": err.Error()})
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
