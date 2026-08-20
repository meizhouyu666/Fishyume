package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

type fakeCore struct {
	mu                       sync.Mutex
	reports                  []run.DriverCapabilityReport
	views                    map[string]run.StatusView
	events                   map[string][]run.WorkflowEvent
	attempts                 map[string]run.AttemptSnapshot
	outputs                  map[string]string
	workflows                map[string]workflow.Normalized
	sinks                    map[int]run.EventSink
	nextSink                 int
	started                  run.StartWorkflowRequest
	startCount               int
	resumed                  run.ResumeRequest
	resumeCount              int
	cancelled                string
	cancelBeforePrecondition func(*run.WorkflowSnapshot)
}

func (f *fakeCore) DriverCapabilityReports(context.Context, string) []run.DriverCapabilityReport {
	return append([]run.DriverCapabilityReport(nil), f.reports...)
}
func (f *fakeCore) StartWorkflow(_ context.Context, request run.StartWorkflowRequest) (run.WorkflowSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = request
	f.startCount++
	now := time.Unix(100, 0).UTC()
	snapshot := run.WorkflowSnapshot{ID: request.RunID, WorkflowName: request.Normalized.Document.Name, Project: request.Project, ResolvedDriver: "codex", ResolvedTarget: "local", StateVersion: 1, Phase: run.PhaseCreated, TopologicalOrder: append([]string(nil), request.Normalized.TopologicalOrder...), Nodes: map[string]run.NodeSummary{}, CreatedAt: now, UpdatedAt: now}
	if f.views == nil {
		f.views = map[string]run.StatusView{}
	}
	f.views[request.RunID] = run.StatusView{Run: &snapshot, Nodes: []run.NodeSnapshot{}}
	return snapshot, nil
}
func (f *fakeCore) Status(id string) (run.StatusView, error) {
	view, ok := f.views[id]
	if !ok {
		return run.StatusView{}, &missingError{}
	}
	return view, nil
}
func (f *fakeCore) Resume(_ context.Context, request run.ResumeRequest) (run.WorkflowSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumed = request
	f.resumeCount++
	view := f.views[request.RunID]
	snapshot := *view.Run
	snapshot.StateVersion++
	snapshot.Phase = run.PhaseRunning
	if request.Action != nil && request.Action.ActionID != "" {
		if snapshot.ActionReceipts == nil {
			snapshot.ActionReceipts = map[string]run.ActionReceipt{}
		}
		snapshot.ActionReceipts[request.Action.ActionID] = run.ActionReceipt{ActionID: request.Action.ActionID, RequestHash: request.Action.ActionRequestHash, StateVersion: snapshot.StateVersion, Phase: snapshot.Phase, Conclusion: snapshot.Conclusion}
	}
	if request.Action != nil && request.Action.NodeID != "" {
		for index := range view.Nodes {
			if view.Nodes[index].ID == request.Action.NodeID {
				view.Nodes[index].Phase, view.Nodes[index].Reason, view.Nodes[index].Result = run.NodePhaseReady, "", nil
			}
		}
	}
	view.Run = &snapshot
	f.views[request.RunID] = view
	return snapshot, nil
}
func (f *fakeCore) Cancel(_ context.Context, id string) (run.WorkflowSnapshot, error) {
	f.cancelled = id
	view := f.views[id]
	snapshot := *view.Run
	snapshot.StateVersion++
	snapshot.Phase, snapshot.Conclusion = run.PhaseCompleted, run.ConclusionCancelled
	return snapshot, nil
}
func (f *fakeCore) CancelWithPrecondition(ctx context.Context, request run.CancelRequest) (run.WorkflowSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	view := f.views[request.RunID]
	snapshot := *view.Run
	if f.cancelBeforePrecondition != nil {
		f.cancelBeforePrecondition(&snapshot)
		view.Run = &snapshot
		f.views[request.RunID] = view
	}
	if request.ExpectedStateVersion == nil || snapshot.StateVersion != *request.ExpectedStateVersion {
		return run.WorkflowSnapshot{}, fmt.Errorf("state version conflict: expected %v, current %d", request.ExpectedStateVersion, snapshot.StateVersion)
	}
	f.cancelled = request.RunID
	snapshot.StateVersion++
	snapshot.Phase, snapshot.Conclusion = run.PhaseCompleted, run.ConclusionCancelled
	if snapshot.ActionReceipts == nil {
		snapshot.ActionReceipts = map[string]run.ActionReceipt{}
	}
	snapshot.ActionReceipts[request.ActionID] = run.ActionReceipt{ActionID: request.ActionID, RequestHash: request.ActionRequestHash, StateVersion: snapshot.StateVersion, Phase: snapshot.Phase, Conclusion: snapshot.Conclusion}
	view.Run = &snapshot
	f.views[request.RunID] = view
	return snapshot, nil
}
func (f *fakeCore) ListRunIDs() ([]string, error) {
	ids := make([]string, 0, len(f.views))
	for id := range f.views {
		ids = append(ids, id)
	}
	return ids, nil
}
func (f *fakeCore) ReadEvents(id string) ([]run.WorkflowEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]run.WorkflowEvent(nil), f.events[id]...), nil
}
func (f *fakeCore) ReadAttempt(runID, nodeID string, number int) (run.AttemptSnapshot, error) {
	return f.attempts[runID+"/"+nodeID], nil
}
func (f *fakeCore) ReadAttemptOutput(runID, nodeID string, number int) (string, error) {
	return f.outputs[runID+"/"+nodeID], nil
}
func (f *fakeCore) ReadWorkflow(runID string) (workflow.Normalized, error) {
	if normalized, ok := f.workflows[runID]; ok {
		return normalized, nil
	}
	return workflow.Normalized{}, os.ErrNotExist
}
func (f *fakeCore) Detach(id string) (run.WorkflowSnapshot, error) {
	view, err := f.Status(id)
	if err != nil || view.Run == nil {
		return run.WorkflowSnapshot{}, err
	}
	return *view.Run, nil
}
func (f *fakeCore) WaitControllers(context.Context) error { return nil }
func (f *fakeCore) AddEventSink(sink run.EventSink) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sinks == nil {
		f.sinks = map[int]run.EventSink{}
	}
	f.nextSink++
	id := f.nextSink
	f.sinks[id] = sink
	return func() {
		f.mu.Lock()
		delete(f.sinks, id)
		f.mu.Unlock()
	}
}
func (f *fakeCore) appendEvent(event run.WorkflowEvent) {
	f.mu.Lock()
	f.events[event.RunID] = append(f.events[event.RunID], event)
	sinks := make([]run.EventSink, 0, len(f.sinks))
	for _, sink := range f.sinks {
		sinks = append(sinks, sink)
	}
	f.mu.Unlock()
	for _, sink := range sinks {
		sink(event)
	}
}

type missingError struct{}

func (*missingError) Error() string { return "run not found" }

func TestWorkflowAuthoringApplicationAPI(t *testing.T) {
	core := newFakeCore()
	service := NewService(core, "codex", store.New(t.TempDir()))
	capabilities, appErr := service.SystemCapabilities(context.Background(), SystemCapabilitiesRequest{Project: "project"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if len(capabilities.Drivers) != 1 || capabilities.Drivers[0].Driver != "codex" || !reflect.DeepEqual(capabilities.ActionTypes, []ActionType{ActionApprove, ActionReject, ActionAnswer, ActionRetry, ActionCancel}) {
		t.Fatalf("unexpected capabilities: %+v", capabilities)
	}
	if capabilities.AuthoringGuide.SchemaVersion != AuthoringGuideVersion || capabilities.AuthoringGuide.WorkflowAPIVersion != WorkflowSchemaVersion || len(capabilities.AuthoringGuide.RecommendedFlow) == 0 {
		t.Fatalf("unexpected authoring guide: %+v", capabilities.AuthoringGuide)
	}
	if capabilities.RoutingCatalog.InspectMethod != "routing.catalog" || capabilities.RoutingCatalog.ModelCount != 2 || capabilities.RoutingCatalog.CatalogHash == "" {
		t.Fatalf("unexpected routing catalog summary: %+v", capabilities.RoutingCatalog)
	}
	catalog, appErr := service.RoutingCatalog(context.Background(), RoutingCatalogRequest{})
	if appErr != nil || catalog.CatalogHash != capabilities.RoutingCatalog.CatalogHash || len(catalog.Catalog.Models) != 2 || catalog.DynamicAvailability {
		t.Fatalf("routing catalog = %+v, error = %v", catalog, appErr)
	}
	catalog.Catalog.Models[0].ID = "mutated"
	reloaded, appErr := service.RoutingCatalog(context.Background(), RoutingCatalogRequest{})
	if appErr != nil || reloaded.Catalog.Models[0].ID == "mutated" {
		t.Fatalf("routing catalog mutation escaped response isolation: %+v, error = %v", reloaded, appErr)
	}
	encodedCapabilities, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedCapabilities), "M5_6_DYNAMIC_PROJECT_SECRET") || len(encodedCapabilities) > MaxSchemaResponseBytes {
		t.Fatalf("capabilities leaked dynamic data or exceeded bound")
	}
	request := WorkflowValidateRequest{Project: "project", Workflow: WorkflowInput{Document: validWorkflowDocument()}, Inputs: map[string]any{}}
	validated, appErr := service.WorkflowValidate(context.Background(), request)
	if appErr != nil || !validated.Valid || len(validated.Issues) != 0 || len(validated.CapabilityGaps) != 0 {
		t.Fatalf("validate = %+v, error = %v", validated, appErr)
	}
	explained, appErr := service.WorkflowExplain(context.Background(), request)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if !reflect.DeepEqual(explained.TopologicalOrder, []string{"plan", "approve"}) || !reflect.DeepEqual(explained.ParallelLayers, [][]string{{"plan"}, {"approve"}}) || explained.Nodes[0].Agent == nil || explained.Nodes[0].Agent.Driver != "codex" {
		t.Fatalf("unexpected explanation: %+v", explained)
	}
	invalid := request
	invalid.Workflow = WorkflowInput{Document: json.RawMessage(`{"apiVersion":"fishyume/v1"}`)}
	validated, appErr = service.WorkflowValidate(context.Background(), invalid)
	if appErr != nil || validated.Valid || len(validated.Issues) != 1 || validated.Issues[0].Path != "$" {
		t.Fatalf("invalid validate = %+v, error = %v", validated, appErr)
	}
}

func TestDocumentedSmokeWorkflowValidatesThroughApplicationAPI(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "examples", "fishyume-smoke.yaml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(newFakeCore(), "codex", store.New(t.TempDir()))
	validated, appErr := service.WorkflowValidate(context.Background(), WorkflowValidateRequest{
		Project:  "project",
		Workflow: WorkflowInput{Source: &WorkflowSource{Filename: filepath.Base(path), Content: string(content)}},
	})
	if appErr != nil || !validated.Valid || len(validated.Issues) != 0 || len(validated.CapabilityGaps) != 0 || len(validated.Warnings) != 0 {
		t.Fatalf("documented smoke workflow validate = %+v, error = %v", validated, appErr)
	}
	explained, appErr := service.WorkflowExplain(context.Background(), WorkflowValidateRequest{
		Project:  "project",
		Workflow: WorkflowInput{Source: &WorkflowSource{Filename: filepath.Base(path), Content: string(content)}},
	})
	if appErr != nil || !reflect.DeepEqual(explained.ParallelLayers[0], []string{"plan", "review"}) {
		t.Fatalf("documented smoke workflow explain = %+v, error = %v", explained, appErr)
	}
	for _, node := range explained.Nodes {
		if node.Agent != nil && (node.Agent.Driver != "codex" || node.Agent.Target != "local") {
			t.Fatalf("documented smoke node %s resolved Agent = %+v", node.ID, node.Agent)
		}
	}
}

func TestDocumentedV2HostWorkflowValidatesAndExplainsExplicitContext(t *testing.T) {
	path := filepath.Join("..", "..", "..", "docs", "examples", "fishyume-v2-host.yaml")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(newFakeCore(), "codex", store.New(t.TempDir()))
	request := WorkflowValidateRequest{
		Project:  "project",
		Workflow: WorkflowInput{Source: &WorkflowSource{Filename: filepath.Base(path), Content: string(content)}},
		ContextBindings: workflow.ContextBindings{MemoryByNode: map[string][]workflow.MemoryBinding{
			"implement": {{ID: "memory-example", Reason: "Selected project constraint"}},
		}},
	}
	validated, appErr := service.WorkflowValidate(context.Background(), request)
	if appErr != nil || !validated.Valid || len(validated.Issues) != 0 || len(validated.CapabilityGaps) != 0 {
		t.Fatalf("documented v2 workflow validate = %+v, error = %v", validated, appErr)
	}
	explained, appErr := service.WorkflowExplain(context.Background(), request)
	if appErr != nil {
		t.Fatal(appErr)
	}
	var implement ExplainNode
	for _, node := range explained.Nodes {
		if node.ID == "implement" {
			implement = node
		}
	}
	if !reflect.DeepEqual(implement.ContextSources, []string{"plan"}) || !reflect.DeepEqual(implement.ProjectInstructions, []string{"AGENTS.md"}) || len(implement.MemoryBindings) != 1 || implement.ContextPolicyVersion != "context-policy/v1" {
		t.Fatalf("documented v2 context explanation = %+v", implement)
	}
	requestsPath := filepath.Join("..", "..", "..", "docs", "examples", "fishyume-v2-host-requests.json")
	requests, err := os.ReadFile(requestsPath)
	if err != nil {
		t.Fatal(err)
	}
	var requestSet map[string]any
	if err := json.Unmarshal(requests, &requestSet); err != nil {
		t.Fatalf("canonical Host request set is not JSON: %v", err)
	}
	if requestSet["exactIntent"] == nil || requestSet["run.start"] == nil || requestSet["run.result"] == nil {
		t.Fatalf("canonical Host request set is incomplete: %v", requestSet)
	}
}

func TestRunApplicationQueriesActionsAndResult(t *testing.T) {
	core := newFakeCore()
	service := NewService(core, "codex", store.New(t.TempDir()))
	started, appErr := service.RunStart(context.Background(), RunStartRequest{Project: "project", Workflow: WorkflowInput{Document: validWorkflowDocument()}, ClientRequestID: "request-1"})
	if appErr != nil || started.RunID == "" || core.started.Normalized == nil || core.started.RunID != started.RunID {
		t.Fatalf("start = %+v, error = %v, request = %+v", started, appErr, core.started)
	}
	listed, appErr := service.RunList(context.Background(), RunListRequest{Limit: 1})
	if appErr != nil || len(listed.Items) != 1 || listed.NextCursor == "" {
		t.Fatalf("list = %+v, error = %v", listed, appErr)
	}
	next, appErr := service.RunList(context.Background(), RunListRequest{Limit: 1, Cursor: listed.NextCursor})
	if appErr != nil || len(next.Items) != 1 || next.Items[0].RunID == listed.Items[0].RunID {
		t.Fatalf("next list = %+v, error = %v", next, appErr)
	}
	got, appErr := service.RunGet(context.Background(), RunGetRequest{RunID: "run-waiting"})
	if appErr != nil || len(got.Run.Nodes) != 1 || got.Run.Nodes[0].Attempt == nil || got.Run.Nodes[0].Attempt.Driver != "codex" {
		t.Fatalf("get = %+v, error = %v", got, appErr)
	}
	if got.Run.Nodes[0].Attempt.Activity == nil || got.Run.Nodes[0].Attempt.Activity.Summary != "正在执行命令：go test ./..." {
		t.Fatalf("activity projection = %+v", got.Run.Nodes[0].Attempt.Activity)
	}
	again, appErr := service.RunGet(context.Background(), RunGetRequest{RunID: "run-waiting"})
	if appErr != nil || again.Run.StateVersion != got.Run.StateVersion || !reflect.DeepEqual(again.Run.Nodes[0].Attempt.Activity, got.Run.Nodes[0].Attempt.Activity) {
		t.Fatalf("activity observation mutated state: first=%+v second=%+v error=%v", got.Run, again.Run, appErr)
	}
	attempt := 1
	action, appErr := service.RunAction(context.Background(), RunActionRequest{ActionID: "action-1", RunID: "run-waiting", Type: ActionAnswer, ExpectedStateVersion: 4, NodeID: "plan", ExpectedAttempt: &attempt, Answers: map[string]any{"scope": "core"}})
	if appErr != nil || action.StateVersion != 5 || core.resumed.Action == nil || core.resumed.Action.Type != "answer" || core.resumed.Action.Answers["scope"] != "core" {
		t.Fatalf("action = %+v, error = %v, resume = %+v", action, appErr, core.resumed)
	}
	if _, appErr := service.RunResult(context.Background(), RunResultRequest{RunID: "run-waiting"}); appErr == nil || appErr.Code != CodeNotReady {
		t.Fatalf("waiting result error = %v", appErr)
	}
	result, appErr := service.RunResult(context.Background(), RunResultRequest{RunID: "run-complete"})
	if appErr != nil || result.Conclusion != "succeeded" || len(result.Results) != 1 || result.Results[0].Result == nil || result.Results[0].Result.Summary != "done" {
		t.Fatalf("result = %+v, error = %v", result, appErr)
	}
}

func TestRunGetProjectsTopologyMetadata(t *testing.T) {
	core := newFakeCore()
	core.workflows = map[string]workflow.Normalized{}
	core.workflows["run-waiting"] = workflow.Normalized{
		Document: workflow.Document{Nodes: map[string]workflow.Node{
			"plan":      {Type: "agent"},
			"summarize": {Type: "agent", DependsOn: []string{"plan"}},
			"risks":     {Type: "agent", DependsOn: []string{"plan"}},
			"approve":   {Type: "approval", DependsOn: []string{"summarize", "risks"}},
		}},
		TopologicalOrder: []string{"plan", "summarize", "risks", "approve"},
	}
	view := core.views["run-waiting"]
	snapshot := *view.Run
	snapshot.TopologicalOrder = []string{"plan", "summarize", "risks", "approve"}
	snapshot.Nodes = map[string]run.NodeSummary{
		"plan":      {ID: "plan", Type: "agent", Phase: run.NodePhaseCompleted, Conclusion: run.ConclusionSucceeded},
		"summarize": {ID: "summarize", Type: "agent", Phase: run.NodePhasePending},
		"risks":     {ID: "risks", Type: "agent", Phase: run.NodePhasePending},
		"approve":   {ID: "approve", Type: "approval", Phase: run.NodePhasePending},
	}
	view.Run = &snapshot
	view.Nodes = []run.NodeSnapshot{{ID: "plan", Type: "agent", Phase: run.NodePhaseCompleted, Conclusion: run.ConclusionSucceeded}}
	for _, node := range []run.NodeSnapshot{{ID: "summarize", Type: "agent", Phase: run.NodePhasePending}, {ID: "risks", Type: "agent", Phase: run.NodePhasePending}, {ID: "approve", Type: "approval", Phase: run.NodePhasePending}} {
		view.Nodes = append(view.Nodes, node)
	}
	core.views["run-waiting"] = view
	service := NewService(core, "codex", store.New(t.TempDir()))
	got, appErr := service.RunGet(context.Background(), RunGetRequest{RunID: "run-waiting"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if !reflect.DeepEqual(got.Run.ParallelLayers, [][]string{{"plan"}, {"summarize", "risks"}, {"approve"}}) {
		t.Fatalf("parallel layers=%+v", got.Run.ParallelLayers)
	}
	if len(got.Run.Nodes) != 4 {
		t.Fatalf("topology nodes=%+v", got.Run.Nodes)
	}
	byID := make(map[string]NodeView, len(got.Run.Nodes))
	for _, node := range got.Run.Nodes {
		byID[node.NodeID] = node
	}
	if len(byID["plan"].DependsOn) != 0 || byID["plan"].ParallelLayer != 0 {
		t.Fatalf("root topology=%+v", byID["plan"])
	}
	if !reflect.DeepEqual(byID["summarize"].DependsOn, []string{"plan"}) || byID["summarize"].ParallelLayer != 1 {
		t.Fatalf("summarize topology=%+v", byID["summarize"])
	}
	if !reflect.DeepEqual(byID["risks"].DependsOn, []string{"plan"}) || byID["risks"].ParallelLayer != 1 {
		t.Fatalf("risks topology=%+v", byID["risks"])
	}
	if !reflect.DeepEqual(byID["approve"].DependsOn, []string{"summarize", "risks"}) || byID["approve"].ParallelLayer != 2 {
		t.Fatalf("approve topology=%+v", byID["approve"])
	}
}

func TestCompatibilityResumeWithoutActionUsesApplicationBoundary(t *testing.T) {
	core := newFakeCore()
	service := NewService(core, "codex", store.New(t.TempDir()))
	snapshot, appErr := service.CompatibilityResume(context.Background(), run.ResumeRequest{RunID: "run-waiting"})
	if appErr != nil || snapshot.ID != "run-waiting" || core.resumeCount != 1 || core.resumed.Action != nil {
		t.Fatalf("snapshot=%+v error=%v request=%+v resumes=%d", snapshot, appErr, core.resumed, core.resumeCount)
	}
}

func TestRunEventsPaginationAndBoundedWait(t *testing.T) {
	core := newFakeCore()
	service := NewService(core, "codex", store.New(t.TempDir()))
	first, appErr := service.RunEvents(context.Background(), RunEventsRequest{RunID: "run-waiting", Limit: 1})
	if appErr != nil || len(first.Events) != 1 || first.Events[0].Sequence != 1 || !first.More {
		t.Fatalf("first events = %+v, error = %v", first, appErr)
	}
	second, appErr := service.RunEvents(context.Background(), RunEventsRequest{RunID: "run-waiting", AfterSequence: first.NextAfterSequence, Limit: 10})
	if appErr != nil || len(second.Events) != 1 || second.Events[0].Sequence != 2 || second.More {
		t.Fatalf("second events = %+v, error = %v", second, appErr)
	}
	done := make(chan RunEventsResponse, 1)
	go func() {
		response, _ := service.RunEvents(context.Background(), RunEventsRequest{RunID: "run-waiting", AfterSequence: 2, WaitMS: 1000})
		done <- response
	}()
	time.Sleep(10 * time.Millisecond)
	attempt := 1
	actionDone := make(chan *Error, 1)
	go func() {
		_, actionErr := service.RunAction(context.Background(), RunActionRequest{ActionID: "event-action", RunID: "run-waiting", Type: ActionAnswer, ExpectedStateVersion: 4, NodeID: "plan", ExpectedAttempt: &attempt, Answers: map[string]any{"scope": "core"}})
		actionDone <- actionErr
	}()
	select {
	case actionErr := <-actionDone:
		if actionErr != nil {
			t.Fatalf("concurrent action failed: %v", actionErr)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("event wait blocked an independent action")
	}
	core.appendEvent(run.WorkflowEvent{RunID: "run-waiting", Sequence: 3, Type: "node.answer_submitted", Phase: run.PhaseRunning, Timestamp: time.Unix(30, 0).UTC()})
	select {
	case response := <-done:
		if len(response.Events) != 1 || response.Events[0].Sequence != 3 {
			t.Fatalf("wait response = %+v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("bounded event wait did not wake")
	}
	core.mu.Lock()
	core.events["run-waiting"] = []run.WorkflowEvent{
		{RunID: "run-waiting", Sequence: 1, Type: "large", Phase: run.PhaseRunning, Message: strings.Repeat("a", 300*1024), Timestamp: time.Unix(40, 0).UTC()},
		{RunID: "run-waiting", Sequence: 2, Type: "large", Phase: run.PhaseRunning, Message: strings.Repeat("b", 300*1024), Timestamp: time.Unix(41, 0).UTC()},
	}
	core.mu.Unlock()
	bounded, appErr := service.RunEvents(context.Background(), RunEventsRequest{RunID: "run-waiting", Limit: 100})
	encoded, _ := json.Marshal(bounded)
	if appErr != nil || len(bounded.Events) != 1 || !bounded.More || len(encoded) > MaxResponseBytes {
		t.Fatalf("byte-bounded events len=%d response=%+v error=%v", len(encoded), bounded, appErr)
	}
}

func TestDurableStartIdempotencyAcrossRestartAndFaultWindows(t *testing.T) {
	for _, faultPoint := range []string{"journal_intent", "journal_mutation", "journal_commit"} {
		t.Run(faultPoint, func(t *testing.T) {
			core := newFakeCore()
			state := store.New(t.TempDir())
			failed := false
			state.SetFaultInjectorForTest(func(operation, _ string) error {
				if operation == faultPoint && !failed {
					failed = true
					return errors.New("fixture crash")
				}
				return nil
			})
			request := RunStartRequest{Project: "project", Workflow: WorkflowInput{Document: validWorkflowDocument()}, ClientRequestID: "request-fault"}
			if _, appErr := NewService(core, "codex", state).RunStart(context.Background(), request); appErr == nil || appErr.Code != CodeInternal {
				t.Fatalf("first error = %v", appErr)
			}
			startsAfterFault := core.startCount
			state.SetFaultInjectorForTest(nil)
			secondService := NewService(core, "codex", state)
			if faultPoint != "journal_intent" {
				if appErr := secondService.Recover(context.Background()); appErr != nil {
					t.Fatalf("restart recovery error = %v", appErr)
				}
			}
			response, appErr := secondService.RunStart(context.Background(), request)
			if appErr != nil || response.RunID == "" {
				t.Fatalf("replay response = %+v, error = %v", response, appErr)
			}
			wantStarts := 1
			if faultPoint == "journal_intent" {
				wantStarts = startsAfterFault + 1
			}
			if core.startCount != wantStarts {
				t.Fatalf("start count = %d, want %d", core.startCount, wantStarts)
			}
			replayed, appErr := NewService(core, "codex", state).RunStart(context.Background(), request)
			if appErr != nil || !reflect.DeepEqual(replayed, response) || core.startCount != wantStarts {
				t.Fatalf("committed replay = %+v, error = %v, starts = %d", replayed, appErr, core.startCount)
			}
			conflicting := request
			conflicting.Project = "other-project"
			if _, appErr := NewService(core, "codex", state).RunStart(context.Background(), conflicting); appErr == nil || appErr.Code != CodeConflict {
				t.Fatalf("conflicting replay error = %v", appErr)
			}
		})
	}
}

func TestDurableActionIdempotencyAcrossRestartAndFaultWindows(t *testing.T) {
	for _, faultPoint := range []string{"journal_intent", "journal_mutation", "journal_commit"} {
		t.Run(faultPoint, func(t *testing.T) {
			core := newFakeCore()
			state := store.New(t.TempDir())
			failed := false
			state.SetFaultInjectorForTest(func(operation, _ string) error {
				if operation == faultPoint && !failed {
					failed = true
					return errors.New("fixture crash")
				}
				return nil
			})
			attempt := 1
			request := RunActionRequest{ActionID: "action-fault", RunID: "run-waiting", Type: ActionAnswer, ExpectedStateVersion: 4, NodeID: "plan", ExpectedAttempt: &attempt, Answers: map[string]any{"scope": "core"}}
			if _, appErr := NewService(core, "codex", state).RunAction(context.Background(), request); appErr == nil || appErr.Code != CodeInternal {
				t.Fatalf("first error = %v", appErr)
			}
			resumesAfterFault := core.resumeCount
			state.SetFaultInjectorForTest(nil)
			secondService := NewService(core, "codex", state)
			if faultPoint != "journal_intent" {
				if appErr := secondService.Recover(context.Background()); appErr != nil {
					t.Fatalf("restart recovery error = %v", appErr)
				}
			}
			response, appErr := secondService.RunAction(context.Background(), request)
			if appErr != nil || response.ActionID != request.ActionID {
				t.Fatalf("replay response = %+v, error = %v", response, appErr)
			}
			wantResumes := 1
			if faultPoint == "journal_intent" {
				wantResumes = resumesAfterFault + 1
			}
			if core.resumeCount != wantResumes {
				t.Fatalf("resume count = %d, want %d", core.resumeCount, wantResumes)
			}
			replayed, appErr := NewService(core, "codex", state).RunAction(context.Background(), request)
			if appErr != nil || !reflect.DeepEqual(replayed, response) || core.resumeCount != wantResumes {
				t.Fatalf("committed replay = %+v, error = %v, resumes = %d", replayed, appErr, core.resumeCount)
			}
			conflicting := request
			conflicting.Answers = map[string]any{"scope": "all"}
			if _, appErr := NewService(core, "codex", state).RunAction(context.Background(), conflicting); appErr == nil || appErr.Code != CodeConflict {
				t.Fatalf("conflicting replay error = %v", appErr)
			}
		})
	}
}

func TestPendingStaleActionIsTerminalAndDoesNotBlockRecovery(t *testing.T) {
	core := newFakeCore()
	state := store.New(t.TempDir())
	service := NewService(core, "codex", state)
	attempt := 1
	request := RunActionRequest{ActionID: "stale-intent", RunID: "run-waiting", Type: ActionAnswer, ExpectedStateVersion: 4, NodeID: "plan", ExpectedAttempt: &attempt, Answers: map[string]any{"scope": "core"}}
	hash, canonical, err := canonicalRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.BeginApplicationJournal("action", request.ActionID, hash, canonical, request.RunID, time.Now()); err != nil {
		t.Fatal(err)
	}
	core.mu.Lock()
	view := core.views[request.RunID]
	advanced := *view.Run
	advanced.StateVersion++
	view.Run = &advanced
	core.views[request.RunID] = view
	core.mu.Unlock()
	if appErr := service.Recover(context.Background()); appErr != nil {
		t.Fatalf("recovery should terminalize stale intent: %v", appErr)
	}
	if core.resumeCount != 0 {
		t.Fatalf("stale intent resumed %d times", core.resumeCount)
	}
	if _, appErr := service.RunAction(context.Background(), request); appErr == nil || appErr.Code != CodeConflict {
		t.Fatalf("stale replay error = %v", appErr)
	}
	if appErr := NewService(core, "codex", state).Recover(context.Background()); appErr != nil {
		t.Fatalf("idempotent recovery error = %v", appErr)
	}
}

func TestDifferentActionCannotSatisfyPendingActionIntent(t *testing.T) {
	core := newFakeCore()
	state := store.New(t.TempDir())
	attempt := 1
	pending := RunActionRequest{ActionID: "pending-answer", RunID: "run-waiting", Type: ActionAnswer, ExpectedStateVersion: 4, NodeID: "plan", ExpectedAttempt: &attempt, Answers: map[string]any{"scope": "core"}}
	hash, canonical, err := canonicalRequest(pending)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.BeginApplicationJournal("action", pending.ActionID, hash, canonical, pending.RunID, time.Now()); err != nil {
		t.Fatal(err)
	}
	different := pending
	different.ActionID = "different-answer"
	different.Answers = map[string]any{"scope": "all"}
	if _, appErr := NewService(core, "codex", state).RunAction(context.Background(), different); appErr != nil {
		t.Fatal(appErr)
	}
	if appErr := NewService(core, "codex", state).Recover(context.Background()); appErr != nil {
		t.Fatalf("recovery should not be bricked by another action: %v", appErr)
	}
	if _, appErr := NewService(core, "codex", state).RunAction(context.Background(), pending); appErr == nil || appErr.Code != CodeConflict {
		t.Fatalf("pending action was misattributed: %v", appErr)
	}
}

func TestCancelPreconditionIsRecheckedInsideCoreMutation(t *testing.T) {
	core := newFakeCore()
	core.cancelBeforePrecondition = func(snapshot *run.WorkflowSnapshot) { snapshot.StateVersion++ }
	service := NewService(core, "codex", store.New(t.TempDir()))
	_, appErr := service.RunAction(context.Background(), RunActionRequest{ActionID: "stale-cancel", RunID: "run-waiting", Type: ActionCancel, ExpectedStateVersion: 4})
	if appErr == nil || appErr.Code != CodeConflict {
		t.Fatalf("cancel error = %v", appErr)
	}
	if core.cancelled != "" {
		t.Fatalf("stale cancel recorded side effect for %q", core.cancelled)
	}
}

func TestRecoverReplaysPendingActionAfterCoreNodeMutation(t *testing.T) {
	core := newFakeCore()
	state := store.New(t.TempDir())
	service := NewService(core, "codex", state)
	attempt := 1
	request := RunActionRequest{ActionID: "node-applied", RunID: "run-waiting", Type: ActionAnswer, ExpectedStateVersion: 4, NodeID: "plan", ExpectedAttempt: &attempt, Answers: map[string]any{"scope": "core"}}
	hash, canonical, err := canonicalRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.BeginApplicationJournal("action", request.ActionID, hash, canonical, request.RunID, time.Now()); err != nil {
		t.Fatal(err)
	}
	// This is the durable Core window: Node is already applied while the Run
	// snapshot still exposes the observed stateVersion and no receipt.
	view := core.views[request.RunID]
	view.Nodes[0].Phase, view.Nodes[0].Reason, view.Nodes[0].Result = run.NodePhaseReady, "", nil
	core.views[request.RunID] = view
	if appErr := service.Recover(context.Background()); appErr != nil {
		t.Fatalf("recovery rejected node-applied action: %v", appErr)
	}
	if core.resumeCount != 1 || core.resumed.Action == nil || core.resumed.Action.ActionID != request.ActionID {
		t.Fatalf("recovery did not replay exact action: count=%d request=%+v", core.resumeCount, core.resumed)
	}
}

func newFakeCore() *fakeCore {
	created := time.Unix(10, 0).UTC()
	waitingRun := run.WorkflowSnapshot{ProtocolVersion: 2, StateSchemaVersion: 3, StateVersion: 4, ID: "run-waiting", WorkflowName: "example", Project: "project", ResolvedDriver: "codex", ResolvedTarget: "local", Phase: run.PhaseWaiting, Reason: run.ReasonAgentWaitingInput, TopologicalOrder: []string{"plan"}, Nodes: map[string]run.NodeSummary{"plan": {ID: "plan", Type: "agent", Phase: run.NodePhaseWaiting, Reason: run.ReasonAgentWaitingInput, CurrentAttempt: 1}}, CreatedAt: created, UpdatedAt: created.Add(time.Second)}
	waitingNode := run.NodeSnapshot{RunID: waitingRun.ID, ID: "plan", Type: "agent", Phase: run.NodePhaseWaiting, Reason: run.ReasonAgentWaitingInput, CurrentAttempt: 1, Result: &workflow.Result{Summary: "input required", Questions: []workflow.InputQuestion{{ID: "scope", Prompt: "Which scope?", Choices: []string{"core", "all"}, Required: true}}}}
	completeRun := waitingRun
	completeRun.ID, completeRun.StateVersion, completeRun.Phase, completeRun.Conclusion, completeRun.Summary, completeRun.CreatedAt, completeRun.UpdatedAt = "run-complete", 7, run.PhaseCompleted, run.ConclusionSucceeded, "done", created.Add(-time.Second), created.Add(2*time.Second)
	completeRun.Nodes = map[string]run.NodeSummary{"plan": {ID: "plan", Type: "agent", Phase: run.NodePhaseCompleted, Conclusion: run.ConclusionSucceeded, CurrentAttempt: 1}}
	completeNode := waitingNode
	completeNode.RunID, completeNode.Phase, completeNode.Reason, completeNode.Conclusion, completeNode.Result = completeRun.ID, run.NodePhaseCompleted, "", run.ConclusionSucceeded, &workflow.Result{Summary: "done"}
	return &fakeCore{
		reports: []run.DriverCapabilityReport{{Driver: "codex", Targets: []string{"local"}, Ready: true, MaxConcurrentAgents: 2, SupportsConcurrentCancel: true}},
		views: map[string]run.StatusView{
			"run-waiting":  {Run: &waitingRun, Nodes: []run.NodeSnapshot{waitingNode}},
			"run-complete": {Run: &completeRun, Nodes: []run.NodeSnapshot{completeNode}},
		},
		events: map[string][]run.WorkflowEvent{"run-waiting": {
			{RunID: "run-waiting", Sequence: 1, Type: "run.created", Phase: run.PhaseCreated, Timestamp: created},
			{RunID: "run-waiting", Sequence: 2, Type: "node.waiting", Phase: run.PhaseWaiting, Timestamp: created.Add(time.Second)},
		}},
		attempts: map[string]run.AttemptSnapshot{
			"run-waiting/plan":  {Number: 1, Phase: run.NodePhaseWaiting, ResolvedDriver: "codex", ResolvedTarget: "local", ContextHash: "hash", StartedAt: created, UpdatedAt: created.Add(time.Second)},
			"run-complete/plan": {Number: 1, Phase: run.NodePhaseCompleted, Conclusion: run.ConclusionSucceeded, ResolvedDriver: "codex", ResolvedTarget: "local", ContextHash: "hash", StartedAt: created, UpdatedAt: created.Add(time.Second)},
		},
		outputs: map[string]string{
			"run-waiting/plan": `events:
{"type":"turn.started"}
{"type":"item.started","item":{"type":"command_execution","command":"go test ./...","status":"in_progress"}}`,
		},
	}
}

func validWorkflowDocument() json.RawMessage {
	return json.RawMessage(`{"apiVersion":"fishyume/v1","name":"example","defaults":{"agent":{"driver":"codex","target":"local"}},"execution":{"maxConcurrency":1},"nodes":{"plan":{"type":"agent","task":"Plan"},"approve":{"type":"approval","dependsOn":["plan"],"prompt":"Approve?"}}}`)
}
