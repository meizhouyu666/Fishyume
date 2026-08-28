package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

type fakeWorkflowBackend struct {
	mu              sync.Mutex
	launches        int
	prompts         []string
	waitResults     []backend.BackendResult
	waitBlock       bool
	observations    map[string][]backend.Observation
	cancelFailures  int
	cancelAttempts  int
	successfulKills int
	waitCalls       int
	reconcileCalls  int
	cancelEntered   chan struct{}
	cancelRelease   chan struct{}
	waitReturn      chan backend.BackendResult
	waitReturned    chan struct{}
	preferObserve   bool
}

func (*fakeWorkflowBackend) Name() string { return "fake" }
func (*fakeWorkflowBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true, SupportsWaitingInput: true}
}
func (b *fakeWorkflowBackend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: b.Name(), Ready: true}
}
func (b *fakeWorkflowBackend) Start(_ context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.launches++
	b.prompts = append(b.prompts, spec.Instructions)
	id := fmt.Sprintf("session-%d", b.launches)
	data, _ := json.Marshal(map[string]any{"project": spec.Workspace, "bindingId": "binding-" + id})
	return &backend.ExecutionHandle{Backend: b.Name(), SchemaVersion: 1, ID: id, Data: data}, nil
}
func (b *fakeWorkflowBackend) Observe(ctx context.Context, handle backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	b.mu.Lock()
	block := b.waitBlock
	waitReturn, waitReturned := b.waitReturn, b.waitReturned
	queue := b.observations[handle.ID]
	if b.preferObserve && len(queue) > 0 {
		b.reconcileCalls++
		observation := queue[0]
		b.observations[handle.ID] = queue[1:]
		if observation.State == backend.ObservationActive {
			b.preferObserve = false
		}
		b.mu.Unlock()
		return &observation, nil
	}
	if b.preferObserve {
		b.reconcileCalls++
		b.mu.Unlock()
		return &backend.ExecutionObservation{State: backend.ObservationResultPending}, nil
	}
	if len(b.waitResults) > 0 {
		b.waitCalls++
		result := b.waitResults[0]
		b.waitResults = b.waitResults[1:]
		if result.Status == "completion_missing" || result.Status == "idle" {
			b.preferObserve = true
		}
		b.mu.Unlock()
		return observationFromResult(result), nil
	}
	if len(queue) > 0 {
		b.reconcileCalls++
		observation := queue[0]
		b.observations[handle.ID] = queue[1:]
		b.mu.Unlock()
		return &observation, nil
	}
	b.waitCalls++
	b.mu.Unlock()
	if block {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case result := <-waitReturn:
			if waitReturned != nil {
				select {
				case <-waitReturned:
				default:
					close(waitReturned)
				}
			}
			return observationFromResult(result), nil
		}
	}
	return &backend.ExecutionObservation{State: backend.ObservationResultPending, Diagnostic: "missing"}, nil
}

func observationFromResult(result backend.BackendResult) *backend.ExecutionObservation {
	switch result.Status {
	case "waiting_input":
		return &backend.ExecutionObservation{State: backend.ObservationWaitingInput, Diagnostic: result.Summary}
	case "completion_missing", "idle":
		return &backend.ExecutionObservation{State: backend.ObservationResultPending, Diagnostic: result.Summary}
	default:
		copy := backend.AgentResult(result)
		return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &copy}
	}
}

func instantCancelPoll(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
func (*fakeWorkflowBackend) Output(context.Context, backend.ExecutionHandle, int) (string, error) {
	return "recent output", nil
}
func (b *fakeWorkflowBackend) Cancel(_ context.Context, _ backend.ExecutionHandle) (*backend.CancelResult, error) {
	b.mu.Lock()
	b.cancelAttempts++
	entered, release := b.cancelEntered, b.cancelRelease
	if b.cancelFailures > 0 {
		b.cancelFailures--
		b.mu.Unlock()
		return &backend.CancelResult{State: backend.CancelNotConfirmed, Diagnostic: "fixture kill failed"}, nil
	}
	b.mu.Unlock()
	if entered != nil {
		select {
		case <-entered:
		default:
			close(entered)
		}
	}
	if release != nil {
		<-release
	}
	b.mu.Lock()
	b.successfulKills++
	b.mu.Unlock()
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}
func waitForRun(t *testing.T, service *Service, runID string, predicate func(WorkflowSnapshot) bool) WorkflowSnapshot {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := service.Get(runID)
		if err == nil && predicate(snapshot) {
			if snapshot.Phase == PhaseCompleted {
				waitForControllers(t, service)
			}
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	snapshot, _ := service.Get(runID)
	t.Fatalf("run did not reach expected state: %+v", snapshot)
	return WorkflowSnapshot{}
}

func waitForControllers(t *testing.T, service *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := service.WaitControllers(ctx); err != nil {
		t.Fatalf("controllers did not stop: %v", err)
	}
}

func workflowFixture() string {
	return `apiVersion: wf/v1
name: integration
inputs:
  goal: {required: true}
defaults: {tool: codex, runtime: local}
execution: {maxConcurrency: 1}
nodes:
  plan:
    type: agent
    task: "Plan {{ inputs.goal }}"
  approve:
    type: approval
    dependsOn: [plan]
    prompt: "Approve {{ nodes.plan.result.summary }}"
  implement:
    type: agent
    dependsOn: [approve]
    when: {node: approve, field: result.decision, equals: approved}
    task: "Implement {{ nodes.plan.result.summary }}"
    requiredSkills: [go-testing]
  reject_note:
    type: agent
    dependsOn: [approve]
    when: {node: approve, field: result.decision, equals: rejected}
    task: "Record rejection {{ nodes.approve.result.reason }}"
`
}

func TestFailedAuditSkipsConditionalDescendantsAndCompletes(t *testing.T) {
	b := &fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "failed", Summary: "audit found a blocked gate"}}, observations: map[string][]backend.Observation{}}
	service := NewService(b, store.New(t.TempDir()))
	content := `apiVersion: fishyume/v2
name: failed-audit
defaults: {agent: {driver: codex, target: local}}
execution: {maxConcurrency: 1}
nodes:
  audit: {type: agent, task: audit}
  synthesis: {type: agent, dependsOn: [audit], task: synthesize}
  approve: {type: approval, dependsOn: [synthesis], prompt: approve}
  implement:
    type: agent
    dependsOn: [approve]
    when: {node: approve, field: result.decision, equals: approved}
    task: implement
  verify: {type: agent, dependsOn: [implement], task: verify}
`
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: content})
	if err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionFailed {
		t.Fatalf("final=%+v", final)
	}
	view, err := service.Status(final.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"synthesis", "approve", "implement", "verify"} {
		var node *NodeSnapshot
		for index := range view.Nodes {
			if view.Nodes[index].ID == id {
				node = &view.Nodes[index]
				break
			}
		}
		if node == nil || node.Phase != NodePhaseSkipped || node.Reason != ReasonUpstreamFailed {
			t.Fatalf("node %s = %+v", id, node)
		}
	}
}

func TestStatusDerivesMultipleActiveNodesAndAttempts(t *testing.T) {
	state := store.New(t.TempDir())
	runID := "run-parallel-status"
	if err := state.InitWorkflowRun(runID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	snapshot := WorkflowSnapshot{
		ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, ID: runID,
		WorkflowName: "parallel", Project: "p", Backend: "fake", Phase: PhaseRunning,
		TopologicalOrder: []string{"a", "b"}, Nodes: map[string]NodeSummary{}, StateDir: state.RunDir(runID),
		CreatedAt: now, UpdatedAt: now,
	}
	for _, nodeID := range snapshot.TopologicalOrder {
		node := NodeSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, RunID: runID, ID: nodeID, Type: "agent", Phase: NodePhaseRunning, CurrentAttempt: 1, CreatedAt: now, UpdatedAt: now}
		if err := state.WriteNode(runID, nodeID, node); err != nil {
			t.Fatal(err)
		}
		snapshot.Nodes[nodeID] = summarizeNode(node)
		attempt := AttemptSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, RunID: runID, NodeID: nodeID, Number: 1, Phase: NodePhaseRunning, Backend: "fake", LaunchState: LaunchHandlePersisted, Execution: &backend.ExecutionHandle{Backend: "fake", SchemaVersion: 1, ID: "handle-" + nodeID, Data: json.RawMessage(`{}`)}, PromptHash: "hash", StartedAt: now, UpdatedAt: now}
		if err := state.WriteAttempt(runID, nodeID, 1, attempt); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.WriteSnapshot(runID, snapshot); err != nil {
		t.Fatal(err)
	}
	service := NewService(&fakeWorkflowBackend{}, state)
	view, err := service.Status(runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.ActiveNodes) != 2 || len(view.ActiveAttempts) != 2 || view.ActiveAttempt != nil || view.Run.ActiveNodeID != "" {
		t.Fatalf("parallel status=%+v", view)
	}
	var malformed AttemptSnapshot
	if err := state.ReadAttempt(runID, "b", 1, &malformed); err != nil {
		t.Fatal(err)
	}
	malformed.ResolvedDriver = "other"
	if err := state.UpdateAttempt(runID, "b", 1, malformed); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Status(runID); err == nil || !strings.Contains(err.Error(), "invalid active Attempt") {
		t.Fatalf("malformed active Attempt error=%v", err)
	}
}

func TestResumeReconcilesMultipleActiveAttemptsWithoutDuplicateStart(t *testing.T) {
	state := store.New(t.TempDir())
	runID := "run-parallel-resume"
	if err := state.InitWorkflowRun(runID); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	doc := workflow.Document{APIVersion: workflow.APIVersion, Name: "parallel-resume", Defaults: workflow.Defaults{Backend: "fake", Tool: "codex", Runtime: "local"}, Execution: workflow.Execution{MaxConcurrency: 2}, Nodes: map[string]workflow.Node{
		"a": {Type: "agent", Task: "a"}, "b": {Type: "agent", Task: "b"},
	}}
	order, err := workflow.Validate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.WriteWorkflow(runID, workflow.Normalized{Document: doc, TopologicalOrder: order}); err != nil {
		t.Fatal(err)
	}
	snapshot := WorkflowSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, ID: runID, WorkflowName: doc.Name, Project: "p", Backend: "fake", Phase: PhasePaused, Reason: ReasonControllerDetach, TopologicalOrder: order, Nodes: map[string]NodeSummary{}, StateDir: state.RunDir(runID), CreatedAt: now, UpdatedAt: now}
	for _, nodeID := range order {
		node := NodeSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, RunID: runID, ID: nodeID, Type: "agent", Phase: NodePhaseRunning, CurrentAttempt: 1, CreatedAt: now, UpdatedAt: now}
		if err := state.WriteNode(runID, nodeID, node); err != nil {
			t.Fatal(err)
		}
		snapshot.Nodes[nodeID] = summarizeNode(node)
		attempt := AttemptSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: stateSchemaVersion, RunID: runID, NodeID: nodeID, Number: 1, Phase: NodePhaseRunning, Backend: "fake", LaunchState: LaunchHandlePersisted, Execution: &backend.ExecutionHandle{Backend: "fake", SchemaVersion: 1, ID: "session-" + nodeID, Data: json.RawMessage(`{}`)}, PromptHash: "hash", StartedAt: now, UpdatedAt: now}
		if err := state.WriteAttempt(runID, nodeID, 1, attempt); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.WriteSnapshot(runID, snapshot); err != nil {
		t.Fatal(err)
	}
	b := &fakeWorkflowBackend{observations: map[string][]backend.Observation{
		"session-a": {{State: backend.ObservationTerminal, Result: &backend.BackendResult{Status: "succeeded", Summary: "a done"}}},
		"session-b": {{State: backend.ObservationTerminal, Result: &backend.BackendResult{Status: "succeeded", Summary: "b done"}}},
	}}
	service := NewService(b, state)
	if _, err := service.Resume(context.Background(), ResumeRequest{RunID: runID}); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, service, runID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
	b.mu.Lock()
	launches, observes := b.launches, b.reconcileCalls
	b.mu.Unlock()
	if launches != 0 || observes != 2 {
		t.Fatalf("launches=%d observes=%d", launches, observes)
	}
}

func TestWorkflowApprovalResumeContextAndConditionalSkip(t *testing.T) {
	b := &fakeWorkflowBackend{waitResults: []backend.BackendResult{
		{Status: "succeeded", Summary: "safe plan", Artifacts: []string{"plan.md"}},
		{Status: "succeeded", Summary: "implemented", Checks: []string{"go test ./..."}},
	}, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	first := NewService(b, state)
	started, err := first.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "project", Filename: "workflow.yaml", Content: workflowFixture(), Inputs: map[string]any{"goal": "M2"}})
	if err != nil {
		t.Fatal(err)
	}
	if started.StateSchemaVersion != 3 || started.ProtocolVersion != 2 {
		t.Fatalf("state schema and protocol must be independent: %+v", started)
	}
	var normalized workflow.Normalized
	if err := state.ReadWorkflow(started.ID, &normalized); err != nil || normalized.Document.APIVersion != workflow.APIVersion {
		t.Fatalf("normalized workflow schema=%q err=%v", normalized.Document.APIVersion, err)
	}
	waiting := waitForRun(t, first, started.ID, func(run WorkflowSnapshot) bool {
		return run.Phase == PhaseWaiting && run.Reason == ReasonApprovalRequired
	})
	if waiting.ActiveNodeID != "approve" {
		t.Fatalf("active approval=%q", waiting.ActiveNodeID)
	}
	if waiting.Summary != "Approve safe plan" {
		t.Fatalf("approval prompt was not rendered: %q", waiting.Summary)
	}
	second := NewService(b, state)
	if _, err := second.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: &ResumeAction{Type: "approve", NodeID: "approve"}}); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, second, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
	view, err := second.Status(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]NodeSnapshot{}
	for _, node := range view.Nodes {
		states[node.ID] = node
	}
	if states["reject_note"].Phase != NodePhaseSkipped || states["reject_note"].Reason != ReasonConditionFalse {
		t.Fatalf("reject node=%+v", states["reject_note"])
	}
	b.mu.Lock()
	prompts := append([]string(nil), b.prompts...)
	launches := b.launches
	b.mu.Unlock()
	if launches != 2 {
		t.Fatalf("launches=%d", launches)
	}
	if !strings.Contains(prompts[1], `"requiredSkills":["go-testing"]`) || !strings.Contains(prompts[1], "safe plan") {
		t.Fatalf("downstream prompt=%q", prompts[1])
	}
	if strings.Contains(prompts[1], "recent output") || !strings.Contains(prompts[1], "plan.md") {
		t.Fatalf("prompt omitted explicit ancestor context or leaked diagnostics=%q", prompts[1])
	}
}

func TestRejectionWithoutEligibleBranchConcludesRejected(t *testing.T) {
	doc := `apiVersion: wf/v1
name: rejection
execution: {maxConcurrency: 1}
nodes:
  approve: {type: approval, prompt: approve}
  only_approved:
    type: agent
    dependsOn: [approve]
    when: {node: approve, field: result.decision, equals: approved}
    task: work
`
	b := &fakeWorkflowBackend{observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	first := NewService(b, state)
	started, err := first.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: doc})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, first, started.ID, func(run WorkflowSnapshot) bool { return run.Reason == ReasonApprovalRequired })
	second := NewService(b, state)
	if _, err := second.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: &ResumeAction{Type: "reject", NodeID: "approve", Reason: "no"}}); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, second, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionRejected {
		t.Fatalf("final=%+v", final)
	}
}

func TestCrashResumeReconcilesWithoutDuplicateLaunch(t *testing.T) {
	b := &fakeWorkflowBackend{waitBlock: true, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	first := NewService(b, state)
	started, err := first.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, first, started.ID, func(run WorkflowSnapshot) bool {
		view, _ := first.Status(run.ID)
		return run.Phase == PhaseRunning && view.ActiveAttempt != nil && view.ActiveAttempt.Execution != nil
	})
	stopControllerForTest(t, first, started.ID)
	b.mu.Lock()
	b.waitBlock = false
	b.observations["session-1"] = []backend.Observation{{State: backend.ObservationTerminal, Result: &backend.BackendResult{Status: "succeeded", Summary: "reconciled"}}}
	b.mu.Unlock()
	second := NewService(b, state)
	if _, err := second.Resume(context.Background(), ResumeRequest{RunID: started.ID}); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, second, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
	b.mu.Lock()
	launches := b.launches
	b.mu.Unlock()
	if launches != 1 {
		t.Fatalf("resume duplicated launch: %d", launches)
	}
}

func TestServeRecoveryReconcilesBeforeScheduling(t *testing.T) {
	b := &fakeWorkflowBackend{waitBlock: true, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	first := NewService(b, state)
	started, err := first.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, first, started.ID, func(run WorkflowSnapshot) bool {
		view, _ := first.Status(run.ID)
		return view.ActiveAttempt != nil && view.ActiveAttempt.Execution != nil
	})
	stopControllerForTest(t, first, started.ID)
	b.mu.Lock()
	b.waitBlock = false
	b.observations["session-1"] = []backend.Observation{{State: backend.ObservationTerminal, Result: &backend.BackendResult{Status: "succeeded", Summary: "recovered"}}}
	b.mu.Unlock()
	recovered := NewService(b, state)
	if err := recovered.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, recovered, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
	b.mu.Lock()
	launches := b.launches
	b.mu.Unlock()
	if launches != 1 {
		t.Fatalf("recovery dispatched duplicate Attempt: launches=%d", launches)
	}
	if active, err := recovered.HasNonTerminalRuns(); err != nil || active {
		t.Fatalf("terminal Run reported active=%t err=%v", active, err)
	}
}

func TestWaitingApprovalKeepsControlPlaneActiveWithoutController(t *testing.T) {
	document := "apiVersion: wf/v1\nname: approval\nexecution: {maxConcurrency: 1}\nnodes: {approve: {type: approval, prompt: approve}}\n"
	service := NewService(&fakeWorkflowBackend{observations: map[string][]backend.Observation{}}, store.New(t.TempDir()))
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: document})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Reason == ReasonApprovalRequired })
	waitForControllers(t, service)
	if service.ActiveControllerCount() != 0 {
		t.Fatalf("waiting Approval retained controller")
	}
	if active, err := service.HasNonTerminalRuns(); err != nil || !active {
		t.Fatalf("waiting Approval active=%t err=%v", active, err)
	}
}

func TestNewAttemptPersistsGenericExecutionHandleOnly(t *testing.T) {
	b := &fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "succeeded", Summary: "done"}}, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	data, err := os.ReadFile(state.AttemptPath(started.ID, "agent-1", 1))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`"execution"`, `"handle_persisted"`, `"resultConsumed": true`} {
		if !strings.Contains(text, want) {
			t.Fatalf("Attempt omitted %s: %s", want, text)
		}
	}
	for _, forbidden := range []string{`"session"`, `"taskBindingId"`, `"launchMetadata"`, `"bindingConsumed"`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("new Attempt persisted legacy field %s: %s", forbidden, text)
		}
	}
}

func TestResumeReconciliationClassifiesDelayedMissingAndExited(t *testing.T) {
	for _, test := range []struct {
		name           string
		observations   []backend.Observation
		wantPhase      Phase
		wantConclusion Conclusion
		wantReason     Reason
	}{
		{"delayed-binding", []backend.Observation{{State: backend.ObservationCompletionMissing}, {State: backend.ObservationTerminal, Result: &backend.BackendResult{Status: "succeeded", Summary: "late result"}}}, PhaseCompleted, ConclusionSucceeded, ""},
		{"persistent-idle", []backend.Observation{{State: backend.ObservationCompletionMissing}, {State: backend.ObservationCompletionMissing}, {State: backend.ObservationCompletionMissing}}, PhaseWaiting, "", ReasonCompletionMissing},
		{"waiting-input", []backend.Observation{{State: backend.ObservationWaitingInput}}, PhaseWaiting, "", ReasonAgentWaitingInput},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := &fakeWorkflowBackend{waitBlock: true, observations: map[string][]backend.Observation{}}
			state := store.New(t.TempDir())
			first := NewService(b, state)
			started, err := first.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
			if err != nil {
				t.Fatal(err)
			}
			waitForRun(t, first, started.ID, func(run WorkflowSnapshot) bool {
				view, _ := first.Status(run.ID)
				return view.ActiveAttempt != nil && view.ActiveAttempt.Execution != nil
			})
			stopControllerForTest(t, first, started.ID)
			b.mu.Lock()
			b.waitBlock = false
			b.observations["session-1"] = append([]backend.Observation(nil), test.observations...)
			b.mu.Unlock()
			second := NewService(b, state)
			second.testHooks.idleReconcileDelay = func(context.Context) error { return nil }
			if _, err := second.Resume(context.Background(), ResumeRequest{RunID: started.ID}); err != nil {
				t.Fatal(err)
			}
			final := waitForRun(t, second, started.ID, func(run WorkflowSnapshot) bool {
				return run.Phase == test.wantPhase && (test.wantPhase != PhaseWaiting || run.Reason == test.wantReason)
			})
			if final.Conclusion != test.wantConclusion || final.Reason != test.wantReason {
				t.Fatalf("final=%+v", final)
			}
			waitForControllers(t, second)
			b.mu.Lock()
			launches := b.launches
			b.mu.Unlock()
			if launches != 1 {
				t.Fatalf("launches=%d", launches)
			}
		})
	}
}

func TestStartupIdleReconciliationDoesNotPrematurelyCompleteMissing(t *testing.T) {
	tests := []struct {
		name             string
		waitResults      []backend.BackendResult
		observations     []backend.Observation
		wantPhase        Phase
		wantConclusion   Conclusion
		wantReason       Reason
		wantWaitCalls    int
		wantReconciles   int
		wantBindingTaken bool
	}{
		{
			name: "active-startup-continues-normal-wait",
			waitResults: []backend.BackendResult{
				{Status: "completion_missing", Summary: "startup idle"},
				{Status: "succeeded", Summary: "completed after startup"},
			},
			observations:     []backend.Observation{{State: backend.ObservationActive}},
			wantPhase:        PhaseCompleted,
			wantConclusion:   ConclusionSucceeded,
			wantWaitCalls:    2,
			wantReconciles:   1,
			wantBindingTaken: true,
		},
		{
			name:         "late-terminal-binding-wins",
			waitResults:  []backend.BackendResult{{Status: "completion_missing", Summary: "startup idle"}},
			observations: []backend.Observation{{State: backend.ObservationTerminal, Result: &backend.BackendResult{Status: "succeeded", Summary: "binding completed"}}},
			wantPhase:    PhaseCompleted, wantConclusion: ConclusionSucceeded, wantWaitCalls: 1,
			wantReconciles: 1, wantBindingTaken: true,
		},
		{
			name:         "sustained-idle-becomes-completion-missing",
			waitResults:  []backend.BackendResult{{Status: "completion_missing", Summary: "startup idle"}},
			observations: []backend.Observation{{State: backend.ObservationCompletionMissing}, {State: backend.ObservationCompletionMissing}},
			wantPhase:    PhaseWaiting, wantReason: ReasonCompletionMissing, wantWaitCalls: 1,
			wantReconciles: startupIdleReconcileChecks,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			b := &fakeWorkflowBackend{waitResults: test.waitResults, observations: map[string][]backend.Observation{"session-1": test.observations}}
			state := store.New(t.TempDir())
			service := NewService(b, state)
			service.testHooks.idleReconcileDelay = func(context.Context) error { return nil }
			started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
			if err != nil {
				t.Fatal(err)
			}
			final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool {
				return run.Phase == test.wantPhase && (test.wantPhase != PhaseWaiting || run.Reason == test.wantReason)
			})
			waitForControllers(t, service)
			if final.Conclusion != test.wantConclusion || final.Reason != test.wantReason {
				t.Fatalf("final=%+v", final)
			}
			var attempt AttemptSnapshot
			if err := state.ReadAttempt(started.ID, "agent-1", 1, &attempt); err != nil {
				t.Fatal(err)
			}
			if attempt.ResultConsumed != test.wantBindingTaken {
				t.Fatalf("attempt=%+v want resultConsumed=%t", attempt, test.wantBindingTaken)
			}
			b.mu.Lock()
			launches, waitCalls, reconciles := b.launches, b.waitCalls, b.reconcileCalls
			b.mu.Unlock()
			if launches != 1 || waitCalls != test.wantWaitCalls || reconciles != test.wantReconciles {
				t.Fatalf("launches=%d waitCalls=%d reconciles=%d", launches, waitCalls, reconciles)
			}
		})
	}
}

func TestStartupIdleReconciliationProductionBound(t *testing.T) {
	grace := time.Duration(startupIdleReconcileChecks) * startupIdleReconcileDelay
	if grace < 5*time.Second || grace > 15*time.Second {
		t.Fatalf("startup idle grace=%s, want 5s..15s", grace)
	}
	if grace != 10*time.Second {
		t.Fatalf("startup idle grace=%s, want 10s", grace)
	}
	if startupIdleReconcileChecks < 2 {
		t.Fatalf("startup idle reconcile checks=%d, want repeated observations", startupIdleReconcileChecks)
	}
}

func TestStartupIdleReconciliationDelayHonorsCancellation(t *testing.T) {
	service := NewService(&fakeWorkflowBackend{}, store.New(t.TempDir()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.waitStartupIdleReconcile(ctx); err != context.Canceled {
		t.Fatalf("waitStartupIdleReconcile error=%v, want context.Canceled", err)
	}
}

func TestAttemptWithoutPersistedHandleBecomesIndeterminateWithoutLaunch(t *testing.T) {
	b := &fakeWorkflowBackend{observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	service.testHooks.idleReconcileDelay = func(context.Context) error { return nil }
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted || run.Phase == PhaseWaiting })
	waitForControllers(t, service)
	// The normal fixture launches quickly; rewrite a paused active Attempt to emulate a crash in the launch/persist gap.
	// A separate run is assembled from its durable snapshots so resume must reconcile instead of relaunching.
	var attempt AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "agent-1", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	attempt.Execution = nil
	attempt.Phase = NodePhaseRunning
	attempt.Conclusion = ""
	if err := state.UpdateAttempt(started.ID, "agent-1", 1, attempt); err != nil {
		t.Fatal(err)
	}
	var node NodeSnapshot
	_ = state.ReadNode(started.ID, "agent-1", &node)
	node.Phase, node.Conclusion = NodePhaseRunning, ""
	_ = state.WriteNode(started.ID, "agent-1", node)
	run, _ := service.Get(started.ID)
	run.Phase, run.Conclusion, run.ActiveNodeID = PhasePaused, "", "agent-1"
	run.Nodes["agent-1"] = summarizeNode(node)
	_ = state.WriteSnapshot(started.ID, run)
	b.mu.Lock()
	before := b.launches
	b.mu.Unlock()
	resumer := NewService(b, state)
	if _, err := resumer.Resume(context.Background(), ResumeRequest{RunID: started.ID}); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, resumer, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionIndeterminate {
		t.Fatalf("final=%+v", final)
	}
	b.mu.Lock()
	after := b.launches
	b.mu.Unlock()
	if after != before {
		t.Fatalf("resume relaunched attempt: before=%d after=%d", before, after)
	}
}

func TestExplicitRetryPreservesAttemptHistory(t *testing.T) {
	b := &fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "invalid_result", Summary: "bad"}, {Status: "succeeded", Summary: "fixed"}}, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	first := NewService(b, state)
	started, err := first.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, first, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseWaiting && run.Reason == ReasonInvalidResult })
	second := NewService(b, state)
	if _, err := second.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: &ResumeAction{Type: "retry", NodeID: "agent-1"}}); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, second, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
	attempts, err := state.ListAttempts(started.ID, "agent-1")
	if err != nil || fmt.Sprint(attempts) != "[1 2]" {
		t.Fatalf("attempts=%v err=%v", attempts, err)
	}
}

func TestFailedResultUsesWorkflowValidationAndCanRetry(t *testing.T) {
	b := &fakeWorkflowBackend{waitResults: []backend.BackendResult{
		{Status: "failed", Summary: strings.Repeat("x", workflow.MaxSummaryBytes+1)},
		{Status: "failed", Summary: "valid failure"},
	}, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	first := NewService(b, state)
	started, err := first.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitForRun(t, first, started.ID, func(run WorkflowSnapshot) bool {
		return run.Phase == PhaseWaiting && run.Reason == ReasonInvalidResult
	})
	waitForControllers(t, first)
	if !strings.Contains(waiting.Summary, "result summary exceeds") {
		t.Fatalf("waiting=%+v", waiting)
	}
	view, err := first.Status(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 1 || view.Nodes[0].Phase != NodePhaseWaiting || view.Nodes[0].Conclusion != "" || view.Nodes[0].Result != nil {
		t.Fatalf("invalid failed result changed node completion state: %+v", view.Nodes)
	}
	var attempt AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "agent-1", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	if attempt.Phase != NodePhaseWaiting || attempt.Reason != ReasonInvalidResult || attempt.Conclusion != "" || attempt.ResultConsumed || attempt.CompletedAt != nil {
		t.Fatalf("invalid failed result consumed Attempt: %+v", attempt)
	}
	if _, err := os.Stat(state.ResultPath(started.ID, "agent-1", 1)); err == nil || !os.IsNotExist(err) {
		t.Fatalf("invalid failed result file exists or cannot be checked: %v", err)
	}
	events, err := first.ReadEvents(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "node.completed" || event.Type == "run.completed" || event.Conclusion == ConclusionFailed {
			t.Fatalf("invalid failed result emitted terminal failure: %+v", events)
		}
	}

	second := NewService(b, state)
	if _, err := second.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: &ResumeAction{Type: "retry", NodeID: "agent-1"}}); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, second, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionFailed {
		t.Fatalf("final=%+v", final)
	}
	attempts, err := state.ListAttempts(started.ID, "agent-1")
	if err != nil || fmt.Sprint(attempts) != "[1 2]" {
		t.Fatalf("attempts=%v err=%v", attempts, err)
	}
	var valid workflow.Result
	if err := state.ReadResult(started.ID, "agent-1", 2, &valid); err != nil || valid.Summary != "valid failure" {
		t.Fatalf("valid failed result=%+v err=%v", valid, err)
	}
}

func TestIndeterminateRetryRequiresAcknowledgement(t *testing.T) {
	b := &fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "indeterminate", Summary: "lost"}}, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	first := NewService(b, state)
	started, err := first.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, first, started.ID, func(run WorkflowSnapshot) bool { return run.Conclusion == ConclusionIndeterminate })
	second := NewService(b, state)
	_, err = second.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: &ResumeAction{Type: "retry", NodeID: "agent-1"}})
	if err == nil || !strings.Contains(err.Error(), "acknowledgeDuplicateRisk") {
		t.Fatalf("error=%v", err)
	}
}

func TestCancelFailureRetryAndConcurrentIdempotence(t *testing.T) {
	b := &fakeWorkflowBackend{waitBlock: true, cancelFailures: 1, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	var eventsMu sync.Mutex
	var events []WorkflowEvent
	service.SetEventSink(func(event WorkflowEvent) { eventsMu.Lock(); events = append(events, event); eventsMu.Unlock() })
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool {
		view, _ := service.Status(run.ID)
		return view.ActiveAttempt != nil && view.ActiveAttempt.Execution != nil
	})
	failed, err := service.Cancel(context.Background(), started.ID)
	if err == nil || failed.Phase != PhaseWaiting || failed.Reason != ReasonCancelFailed || !failed.CancelRequested {
		t.Fatalf("failed cancel=%+v err=%v", failed, err)
	}
	type result struct {
		run WorkflowSnapshot
		err error
	}
	outputs := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			value, cancelErr := service.Cancel(context.Background(), started.ID)
			outputs <- result{value, cancelErr}
		}()
	}
	for i := 0; i < 2; i++ {
		output := <-outputs
		if output.err != nil || output.run.Conclusion != ConclusionCancelled {
			t.Fatalf("cancel=%+v err=%v", output.run, output.err)
		}
	}
	b.mu.Lock()
	attempts, kills := b.cancelAttempts, b.successfulKills
	b.mu.Unlock()
	if attempts != 2 || kills != 1 {
		t.Fatalf("cancel attempts=%d successful=%d", attempts, kills)
	}
	eventsMu.Lock()
	captured := append([]WorkflowEvent(nil), events...)
	eventsMu.Unlock()
	var terminal []WorkflowEvent
	for _, event := range captured {
		if event.Conclusion == ConclusionFailed {
			t.Fatalf("cancel sequence emitted failed: %+v", captured)
		}
		if event.Phase == PhaseCompleted {
			terminal = append(terminal, event)
		}
	}
	if len(terminal) != 1 || terminal[0].Conclusion != ConclusionCancelled || terminal[0].Type != "run.cancelled" {
		t.Fatalf("terminal cancel events=%+v; all=%+v", terminal, captured)
	}
}

func TestCrossProcessCancelCoordinatesWithLeaseOwner(t *testing.T) {
	b := &fakeWorkflowBackend{waitBlock: true, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	owner := NewService(b, state)
	owner.testHooks.cancelRequestDelay = instantCancelPoll
	started, err := owner.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, owner, started.ID, func(run WorkflowSnapshot) bool {
		view, _ := owner.Status(run.ID)
		return view.ActiveAttempt != nil && view.ActiveAttempt.Execution != nil
	})
	requester := NewService(b, state)
	requester.testHooks.cancelRequestDelay = instantCancelPoll
	cancelled, err := requester.Cancel(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Phase != PhaseCompleted || cancelled.Conclusion != ConclusionCancelled || cancelled.Reason != ReasonUserRequested || !cancelled.CancelRequested {
		t.Fatalf("cancelled=%+v", cancelled)
	}
	if _, err := os.Stat(state.CancelRequestPath(started.ID)); !os.IsNotExist(err) {
		t.Fatalf("cancel request was not cleaned up: %v", err)
	}
	b.mu.Lock()
	kills := b.cancelAttempts
	b.mu.Unlock()
	if kills != 1 {
		t.Fatalf("backend kills=%d, want 1", kills)
	}
}

func TestCrossProcessCancelIntentPreventsFailedTerminalWhileKillInFlight(t *testing.T) {
	b := &fakeWorkflowBackend{
		waitBlock: true, observations: map[string][]backend.Observation{},
		cancelEntered: make(chan struct{}), cancelRelease: make(chan struct{}),
		waitReturn: make(chan backend.BackendResult, 1), waitReturned: make(chan struct{}),
	}
	state := store.New(t.TempDir())
	owner := NewService(b, state)
	owner.testHooks.cancelRequestDelay = instantCancelPoll
	var eventsMu sync.Mutex
	var events []WorkflowEvent
	owner.SetEventSink(func(event WorkflowEvent) { eventsMu.Lock(); events = append(events, event); eventsMu.Unlock() })
	started, err := owner.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, owner, started.ID, func(run WorkflowSnapshot) bool {
		view, _ := owner.Status(run.ID)
		return view.ActiveAttempt != nil && view.ActiveAttempt.Execution != nil
	})
	requester := NewService(b, state)
	requester.testHooks.cancelRequestDelay = instantCancelPoll
	result := make(chan error, 1)
	go func() { _, cancelErr := requester.Cancel(context.Background(), started.ID); result <- cancelErr }()
	<-b.cancelEntered
	inFlight, err := requester.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !inFlight.CancelRequested || inFlight.Phase != PhaseCancelling || inFlight.Conclusion != "" {
		t.Fatalf("cancellation intent was not durable before kill: %+v", inFlight)
	}
	b.waitReturn <- backend.BackendResult{Status: "failed", Summary: "session ended while kill was in flight"}
	<-b.waitReturned
	eventsMu.Lock()
	for _, event := range events {
		if event.Phase == PhaseCompleted || event.Conclusion == ConclusionFailed {
			eventsMu.Unlock()
			t.Fatalf("premature terminal event during kill: %+v", events)
		}
	}
	eventsMu.Unlock()
	close(b.cancelRelease)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, requester, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionCancelled {
		t.Fatalf("final=%+v", final)
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	terminal := 0
	for _, event := range events {
		if event.Phase == PhaseCompleted {
			terminal++
			if event.Type != "run.cancelled" || event.Conclusion != ConclusionCancelled {
				t.Fatalf("unexpected terminal event: %+v", event)
			}
		}
		if event.Conclusion == ConclusionFailed {
			t.Fatalf("failed event during cancellation: %+v", events)
		}
	}
	if terminal != 1 {
		t.Fatalf("terminal events=%d; all=%+v", terminal, events)
	}
}

func TestCrossProcessCancelFailureIsTruthfulAndRetryable(t *testing.T) {
	b := &fakeWorkflowBackend{waitBlock: true, cancelFailures: 1, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	owner := NewService(b, state)
	owner.testHooks.cancelRequestDelay = instantCancelPoll
	started, err := owner.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, owner, started.ID, func(run WorkflowSnapshot) bool {
		view, _ := owner.Status(run.ID)
		return view.ActiveAttempt != nil && view.ActiveAttempt.Execution != nil
	})
	requester := NewService(b, state)
	requester.testHooks.cancelRequestDelay = instantCancelPoll
	failed, err := requester.Cancel(context.Background(), started.ID)
	if err == nil || failed.Phase != PhaseWaiting || failed.Reason != ReasonCancelFailed || !failed.CancelRequested || failed.Conclusion != "" {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
	if waitErr := owner.WaitControllers(context.Background()); waitErr != nil {
		t.Fatal(waitErr)
	}
	retry := NewService(b, state)
	retry.testHooks.cancelRequestDelay = instantCancelPoll
	cancelled, err := retry.Cancel(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Conclusion != ConclusionCancelled || cancelled.Reason != ReasonUserRequested {
		t.Fatalf("cancelled=%+v", cancelled)
	}
	b.mu.Lock()
	kills := b.cancelAttempts
	b.mu.Unlock()
	if kills != 2 {
		t.Fatalf("backend kills=%d, want failed attempt plus one retry", kills)
	}
}

func TestCrossProcessCancelWaitsForSessionPersistence(t *testing.T) {
	b := &fakeWorkflowBackend{waitBlock: true, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	owner := NewService(b, state)
	owner.testHooks.cancelRequestDelay = instantCancelPoll
	launchReturned, releaseLaunch := make(chan struct{}), make(chan struct{})
	var once sync.Once
	owner.testHooks.afterLaunch = func() { once.Do(func() { close(launchReturned); <-releaseLaunch }) }
	started, err := owner.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	<-launchReturned
	requester := NewService(b, state)
	requester.testHooks.cancelRequestDelay = instantCancelPoll
	result := make(chan error, 1)
	go func() { _, cancelErr := requester.Cancel(context.Background(), started.ID); result <- cancelErr }()
	for {
		request, requestErr := state.ReadCancellationRequest(started.ID)
		if requestErr == nil && request.ID != "" {
			break
		}
		runtime.Gosched()
	}
	close(releaseLaunch)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	final, err := requester.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Conclusion != ConclusionCancelled {
		t.Fatalf("final=%+v", final)
	}
	b.mu.Lock()
	kills := b.cancelAttempts
	b.mu.Unlock()
	if kills != 1 {
		t.Fatalf("backend kills=%d, want 1 after session persistence", kills)
	}
}

func TestCrossProcessCancelBeforeLaunchDispatchDoesNotLaunchOrKill(t *testing.T) {
	b := &fakeWorkflowBackend{waitBlock: true, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	owner := NewService(b, state)
	owner.testHooks.cancelRequestDelay = instantCancelPoll
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	owner.testHooks.beforeControllerMutation = func(point string) {
		if point == "agent.prelaunch" {
			once.Do(func() { close(entered); <-release })
		}
	}
	started, err := owner.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	requester := NewService(b, state)
	requester.testHooks.cancelRequestDelay = instantCancelPoll
	type cancelResult struct {
		run WorkflowSnapshot
		err error
	}
	result := make(chan cancelResult, 1)
	go func() {
		run, cancelErr := requester.Cancel(context.Background(), started.ID)
		result <- cancelResult{run: run, err: cancelErr}
	}()
	output := <-result
	close(release)
	if output.err != nil {
		t.Fatal(output.err)
	}
	if output.run.Conclusion != ConclusionCancelled {
		t.Fatalf("cancelled=%+v", output.run)
	}
	b.mu.Lock()
	launches, kills := b.launches, b.cancelAttempts
	b.mu.Unlock()
	if launches != 0 || kills != 0 {
		t.Fatalf("launches=%d kills=%d, want neither before dispatch", launches, kills)
	}
	view, err := requester.Status(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 1 || view.Nodes[0].Phase != NodePhaseSkipped || view.Nodes[0].Reason != ReasonWorkflowCancelled {
		t.Fatalf("nodes=%+v", view.Nodes)
	}
}

func TestConcurrentCrossProcessCancelIsIdempotent(t *testing.T) {
	b := &fakeWorkflowBackend{waitBlock: true, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	owner := NewService(b, state)
	owner.testHooks.cancelRequestDelay = instantCancelPoll
	started, err := owner.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, owner, started.ID, func(run WorkflowSnapshot) bool {
		view, _ := owner.Status(run.ID)
		return view.ActiveAttempt != nil && view.ActiveAttempt.Execution != nil
	})
	services := []*Service{NewService(b, state), NewService(b, state)}
	results := make(chan error, len(services))
	for _, service := range services {
		service.testHooks.cancelRequestDelay = instantCancelPoll
		go func(service *Service) {
			run, cancelErr := service.Cancel(context.Background(), started.ID)
			if cancelErr == nil && run.Conclusion != ConclusionCancelled {
				cancelErr = fmt.Errorf("unexpected cancellation result: %+v", run)
			}
			results <- cancelErr
		}(service)
	}
	for range services {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	b.mu.Lock()
	kills := b.cancelAttempts
	b.mu.Unlock()
	if kills != 1 {
		t.Fatalf("backend kills=%d, want 1", kills)
	}
}

func TestStaleCancelRequestIsRecoveredAfterOwnerExit(t *testing.T) {
	b := &fakeWorkflowBackend{waitBlock: true, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	owner := NewService(b, state)
	started, err := owner.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, owner, started.ID, func(run WorkflowSnapshot) bool {
		view, _ := owner.Status(run.ID)
		return view.ActiveAttempt != nil && view.ActiveAttempt.Execution != nil
	})
	stopControllerForTest(t, owner, started.ID)
	request, err := state.RequestCancellation(started.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	recovery := NewService(b, state)
	recovery.testHooks.cancelRequestDelay = instantCancelPoll
	cancelled, err := recovery.Cancel(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Conclusion != ConclusionCancelled {
		t.Fatalf("cancelled=%+v", cancelled)
	}
	response, err := state.ReadCancellationResponse(started.ID, request.ID)
	if err != nil || response.Status != store.CancelResponseCompleted {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	if _, err := os.Stat(state.CancelRequestPath(started.ID)); !os.IsNotExist(err) {
		t.Fatalf("stale request was not cleaned up: %v", err)
	}
}

func TestCancelWaitingApprovalSkipsApprovalWithoutBackendKill(t *testing.T) {
	doc := `apiVersion: wf/v1
name: cancel-approval
execution: {maxConcurrency: 1}
nodes:
  approve: {type: approval, prompt: approve}
  later: {type: agent, dependsOn: [approve], task: later}
`
	b := &fakeWorkflowBackend{observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: doc})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Reason == ReasonApprovalRequired })
	cancelled, err := service.Cancel(context.Background(), started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Conclusion != ConclusionCancelled {
		t.Fatalf("cancelled=%+v", cancelled)
	}
	view, _ := service.Status(started.ID)
	for _, node := range view.Nodes {
		if node.Phase != NodePhaseSkipped || node.Reason != ReasonWorkflowCancelled {
			t.Fatalf("node=%+v", node)
		}
	}
	b.mu.Lock()
	kills := b.cancelAttempts
	b.mu.Unlock()
	if kills != 0 {
		t.Fatalf("approval cancellation killed backend %d times", kills)
	}
}

func TestApprovalDecisionIsIdempotentAndConflicts(t *testing.T) {
	doc := `apiVersion: wf/v1
name: approval
execution: {maxConcurrency: 1}
nodes: {approve: {type: approval, prompt: approve}}
`
	b := &fakeWorkflowBackend{observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	first := NewService(b, state)
	started, err := first.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: doc})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, first, started.ID, func(run WorkflowSnapshot) bool { return run.Reason == ReasonApprovalRequired })
	second := NewService(b, state)
	action := ResumeAction{Type: "approve", NodeID: "approve"}
	if _, err := second.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: &action}); err != nil {
		t.Fatal(err)
	}
	waitForRun(t, second, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	third := NewService(b, state)
	if _, err := third.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: &action}); err != nil {
		t.Fatal(err)
	}
	_, err = third.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: &ResumeAction{Type: "reject", NodeID: "approve"}})
	if err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflict error=%v", err)
	}
}

func TestDetachDoesNotInterruptControllerMutation(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		point   string
	}{
		{name: "before-first-node-write", content: `apiVersion: wf/v1
name: detach-agent
execution: {maxConcurrency: 1}
nodes: {agent: {type: agent, task: work}}
`, point: "agent.prelaunch"},
		{name: "approval-waiting", content: `apiVersion: wf/v1
name: detach-approval
execution: {maxConcurrency: 1}
nodes: {approve: {type: approval, prompt: approve}}
`, point: "approval.waiting"},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := &fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "succeeded", Summary: "done"}}, observations: map[string][]backend.Observation{}}
			state := store.New(t.TempDir())
			service := NewService(b, state)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			service.testHooks.beforeControllerMutation = func(point string) {
				if point == test.point {
					once.Do(func() { close(entered); <-release })
				}
			}
			started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: test.content})
			if err != nil {
				t.Fatal(err)
			}
			<-entered
			detached, detachErr := service.Detach(started.ID)
			if detachErr != nil {
				t.Fatal(detachErr)
			}
			if detached.Phase != PhaseCreated {
				t.Fatalf("detach mutated phase: %+v", detached)
			}
			close(release)
			final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool {
				return run.Phase == PhaseCompleted || run.Reason == ReasonApprovalRequired
			})
			if final.Phase == PhasePaused || final.Reason == ReasonControllerDetach {
				t.Fatalf("detach paused controller: %+v", final)
			}
			b.mu.Lock()
			launches := b.launches
			b.mu.Unlock()
			wantLaunches := 1
			if test.point == "approval.waiting" {
				wantLaunches = 0
			}
			if launches != wantLaunches {
				t.Fatalf("launches=%d want=%d", launches, wantLaunches)
			}
		})
	}
}

func TestDetachAfterLaunchKeepsControllerAndDoesNotRelaunch(t *testing.T) {
	b := &fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "succeeded", Summary: "done"}}, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	launchReturned, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	service.testHooks.afterLaunch = func() { once.Do(func() { close(launchReturned); <-release }) }
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	<-launchReturned
	detached, err := service.Detach(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detached.Phase != PhaseRunning {
		t.Fatalf("detach mutated active run: %+v", detached)
	}
	close(release)
	final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
	var attempt AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "agent-1", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	if attempt.Execution == nil || attempt.Execution.ID != "session-1" {
		t.Fatalf("execution handle was not persisted after detach: %+v", attempt)
	}
	b.mu.Lock()
	launches := b.launches
	b.mu.Unlock()
	if launches != 1 {
		t.Fatalf("resume duplicated launch: %d", launches)
	}
}

func TestDetachBeforeResultPersistenceDoesNotChangeOutcome(t *testing.T) {
	b := &fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "succeeded", Summary: "first result"}}, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	resultReady, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	service.testHooks.beforeControllerMutation = func(point string) {
		if point == "result.succeeded" {
			once.Do(func() { close(resultReady); <-release })
		}
	}
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	<-resultReady
	detached, err := service.Detach(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detached.Phase != PhaseRunning {
		t.Fatalf("detach mutated run: %+v", detached)
	}
	close(release)
	final := waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
	b.mu.Lock()
	launches := b.launches
	b.mu.Unlock()
	if launches != 1 {
		t.Fatalf("resume duplicated launch: %d", launches)
	}
}

func stopControllerForTest(t *testing.T, service *Service, runID string) {
	t.Helper()
	active := service.controller(runID)
	if active == nil {
		t.Fatalf("run %s has no active controller", runID)
	}
	active.cancel()
	select {
	case <-active.done:
	case <-time.After(3 * time.Second):
		t.Fatalf("controller for %s did not stop", runID)
	}
}

func failOnce(match func(operation, path string) bool) store.FaultInjector {
	var mu sync.Mutex
	failed := false
	return func(operation, path string) error {
		mu.Lock()
		defer mu.Unlock()
		if !failed && match(operation, path) {
			failed = true
			return fmt.Errorf("fixture store failure")
		}
		return nil
	}
}

func isNodeSnapshotPath(path, nodeID string) bool {
	return filepath.Base(path) == "node.json" && filepath.Base(filepath.Dir(path)) == nodeID
}

func TestNodeSnapshotFaultMatcherIsCrossPlatform(t *testing.T) {
	for _, path := range []string{
		filepath.Join("root", "runs", "run-1", "nodes", "agent-1", "node.json"),
		"root/runs/run-1/nodes/agent-1/node.json",
	} {
		if !isNodeSnapshotPath(path, "agent-1") {
			t.Fatalf("path %q did not match agent-1 node snapshot", path)
		}
	}
	if isNodeSnapshotPath(filepath.Join("root", "nodes", "other", "node.json"), "agent-1") {
		t.Fatal("matcher accepted another node's snapshot")
	}
}

func TestPrelaunchPersistenceFailurePreventsBackendLaunch(t *testing.T) {
	for _, test := range []struct {
		name  string
		match func(string, string) bool
	}{
		{"attempt", func(operation, path string) bool {
			return operation == "write_json" && strings.Contains(path, "attempt.json")
		}},
		{"node", func(operation, path string) bool {
			return operation == "write_json" && isNodeSnapshotPath(path, "agent-1")
		}},
		{"run", func(operation, path string) bool {
			return operation == "write_json" && strings.HasSuffix(path, "run.json")
		}},
		{"event", func(operation, _ string) bool { return operation == "append_event" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := &fakeWorkflowBackend{observations: map[string][]backend.Observation{}}
			state := store.New(t.TempDir())
			service := NewService(b, state)
			var once sync.Once
			service.testHooks.beforeControllerMutation = func(point string) {
				if point == "agent.prelaunch" {
					once.Do(func() { state.SetFaultInjectorForTest(failOnce(test.match)) })
				}
			}
			started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := service.WaitControllers(ctx); err != nil {
				t.Fatal(err)
			}
			b.mu.Lock()
			launches := b.launches
			b.mu.Unlock()
			if launches != 0 {
				t.Fatalf("launches=%d", launches)
			}
			run, err := service.Get(started.ID)
			if err != nil {
				t.Fatal(err)
			}
			if run.Phase == PhaseCompleted && run.Conclusion == ConclusionSucceeded {
				t.Fatalf("store failure produced success: %+v", run)
			}
		})
	}
}

func TestSessionPersistenceFailurePreventsWait(t *testing.T) {
	b := &fakeWorkflowBackend{observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	service.testHooks.afterLaunch = func() {
		state.SetFaultInjectorForTest(failOnce(func(operation, path string) bool {
			return operation == "write_json" && strings.Contains(path, "attempt.json")
		}))
	}
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := service.WaitControllers(ctx); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	waits := b.waitCalls
	b.mu.Unlock()
	if waits != 0 {
		t.Fatalf("backend Wait called %d times without durable session metadata", waits)
	}
	run, err := service.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase == PhaseCompleted && run.Conclusion == ConclusionSucceeded {
		t.Fatalf("session persistence failure produced success: %+v", run)
	}
}

func TestResultPersistenceFailureDoesNotEmitSucceeded(t *testing.T) {
	b := &fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "succeeded", Summary: "done"}}, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	var eventsMu sync.Mutex
	var events []WorkflowEvent
	service.SetEventSink(func(event WorkflowEvent) { eventsMu.Lock(); events = append(events, event); eventsMu.Unlock() })
	var once sync.Once
	service.testHooks.beforeControllerMutation = func(point string) {
		if point == "result.succeeded" {
			once.Do(func() {
				state.SetFaultInjectorForTest(failOnce(func(operation, path string) bool {
					return operation == "write_json" && strings.Contains(path, "result.json")
				}))
			})
		}
	}
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := service.WaitControllers(ctx); err != nil {
		t.Fatal(err)
	}
	eventsMu.Lock()
	captured := append([]WorkflowEvent(nil), events...)
	eventsMu.Unlock()
	for _, event := range captured {
		if event.Conclusion == ConclusionSucceeded || event.Type == "node.completed" || event.Type == "run.completed" {
			t.Fatalf("result store failure emitted success: %+v", captured)
		}
	}
	run, err := service.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase == PhaseCompleted && run.Conclusion == ConclusionSucceeded {
		t.Fatalf("result store failure persisted success: %+v", run)
	}
}

func TestSkipPersistenceFailureDoesNotEmitFailedTerminal(t *testing.T) {
	doc := `apiVersion: wf/v1
name: skip-store-failure
execution: {maxConcurrency: 1}
nodes:
  first: {type: agent, task: fail}
  later: {type: agent, dependsOn: [first], task: later}
`
	b := &fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "failed", Summary: "failed"}}, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	var eventsMu sync.Mutex
	var events []WorkflowEvent
	service.SetEventSink(func(event WorkflowEvent) { eventsMu.Lock(); events = append(events, event); eventsMu.Unlock() })
	var once sync.Once
	service.testHooks.beforeControllerMutation = func(point string) {
		if point == "result.failed" {
			once.Do(func() {
				state.SetFaultInjectorForTest(failOnce(func(operation, path string) bool {
					return operation == "write_json" && isNodeSnapshotPath(path, "later")
				}))
			})
		}
	}
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: doc})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := service.WaitControllers(ctx); err != nil {
		t.Fatal(err)
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	for _, event := range events {
		if event.Phase == PhaseCompleted {
			t.Fatalf("skip persistence failure emitted terminal event: %+v", events)
		}
	}
	run, err := service.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Phase == PhaseCompleted {
		t.Fatalf("skip persistence failure persisted terminal run: %+v", run)
	}
}

func TestApprovalEventFailureIsRetryableWithoutConflictingDecision(t *testing.T) {
	doc := `apiVersion: wf/v1
name: approval-store-failure
execution: {maxConcurrency: 1}
nodes: {approve: {type: approval, prompt: approve}}
`
	b := &fakeWorkflowBackend{observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	first := NewService(b, state)
	started, err := first.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: doc})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, first, started.ID, func(run WorkflowSnapshot) bool { return run.Reason == ReasonApprovalRequired })
	state.SetFaultInjectorForTest(failOnce(func(operation, _ string) bool { return operation == "append_event" }))
	second := NewService(b, state)
	action := &ResumeAction{Type: "approve", NodeID: "approve"}
	if _, err := second.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: action}); err == nil {
		t.Fatal("approval succeeded despite event persistence failure")
	}
	third := NewService(b, state)
	if _, err := third.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: action}); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, third, started.ID, func(run WorkflowSnapshot) bool { return run.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
}

func TestCancelRequestPersistenceFailureDoesNotKill(t *testing.T) {
	b := &fakeWorkflowBackend{waitBlock: true, observations: map[string][]backend.Observation{}}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool {
		view, _ := service.Status(run.ID)
		return view.ActiveAttempt != nil && view.ActiveAttempt.Execution != nil
	})
	state.SetFaultInjectorForTest(failOnce(func(operation, path string) bool {
		return operation == "write_json" && strings.HasSuffix(path, "run.json")
	}))
	if _, err := service.Cancel(context.Background(), started.ID); err == nil {
		t.Fatal("cancel succeeded despite cancel-request persistence failure")
	}
	b.mu.Lock()
	attempts := b.cancelAttempts
	b.mu.Unlock()
	if attempts != 0 {
		t.Fatalf("backend cancel called %d times before durable cancel request", attempts)
	}
}

func TestCancelInFlightEmitsOnlyCancelledTerminal(t *testing.T) {
	b := &fakeWorkflowBackend{waitBlock: true, observations: map[string][]backend.Observation{}, cancelEntered: make(chan struct{}), cancelRelease: make(chan struct{})}
	state := store.New(t.TempDir())
	service := NewService(b, state)
	var eventsMu sync.Mutex
	var events []WorkflowEvent
	service.SetEventSink(func(event WorkflowEvent) { eventsMu.Lock(); events = append(events, event); eventsMu.Unlock() })
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool {
		view, _ := service.Status(run.ID)
		return view.ActiveAttempt != nil && view.ActiveAttempt.Execution != nil
	})
	result := make(chan error, 1)
	go func() { _, cancelErr := service.Cancel(context.Background(), started.ID); result <- cancelErr }()
	<-b.cancelEntered
	waitForRun(t, service, started.ID, func(run WorkflowSnapshot) bool { return run.CancelRequested && run.Phase == PhaseCancelling })
	eventsMu.Lock()
	for _, event := range events {
		if event.Phase == PhaseCompleted || event.Conclusion == ConclusionFailed {
			eventsMu.Unlock()
			t.Fatalf("terminal event during cancel in-flight: %+v", events)
		}
	}
	eventsMu.Unlock()
	close(b.cancelRelease)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	final, err := service.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Conclusion != ConclusionCancelled {
		t.Fatalf("final=%+v", final)
	}
	eventsMu.Lock()
	defer eventsMu.Unlock()
	terminal := 0
	for _, event := range events {
		if event.Phase == PhaseCompleted {
			terminal++
			if event.Type != "run.cancelled" || event.Conclusion != ConclusionCancelled {
				t.Fatalf("unexpected terminal event: %+v", event)
			}
		}
		if event.Conclusion == ConclusionFailed {
			t.Fatalf("failed event during cancellation: %+v", events)
		}
	}
	if terminal != 1 {
		t.Fatalf("terminal events=%d all=%+v", terminal, events)
	}
}

func TestLegacyStatusIsReadOnly(t *testing.T) {
	state := store.New(t.TempDir())
	if err := state.InitRun("run-legacy"); err != nil {
		t.Fatal(err)
	}
	legacy := LegacySnapshot{ProtocolVersion: 1, ID: "run-legacy", Status: RunPaused, NodeStatus: NodePaused, Project: "p", StateDir: state.RunDir("run-legacy"), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := state.WriteSnapshot("run-legacy", legacy); err != nil {
		t.Fatal(err)
	}
	service := NewService(&fakeWorkflowBackend{}, state)
	view, err := service.Status("run-legacy")
	if err != nil || !view.Legacy || view.LegacyRun.Status != RunPaused {
		t.Fatalf("view=%+v err=%v", view, err)
	}
	if _, err := service.Resume(context.Background(), ResumeRequest{RunID: "run-legacy"}); err == nil {
		t.Fatal("legacy run resumed")
	}
}
