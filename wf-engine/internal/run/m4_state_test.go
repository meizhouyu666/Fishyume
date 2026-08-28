package run

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/driver/scheduleradapter"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

type questionDriver struct {
	mu       sync.Mutex
	observes int
	result   *agent.AgentResult
}

func (*questionDriver) Name() string { return "question-driver" }
func (*questionDriver) Capabilities() agent.DriverCapabilities {
	return agent.DriverCapabilities{Targets: []string{"local"}, SupportsOutput: true, SupportsWaitingInput: true, SupportsRecovery: true, SupportsConfirmedCancel: true}
}
func (d *questionDriver) Doctor(context.Context, agent.DoctorRequest) agent.DoctorReport {
	return agent.DoctorReport{Driver: d.Name(), Ready: true}
}
func (d *questionDriver) Start(_ context.Context, envelope agent.AttemptEnvelope) (*agent.ExecutionHandle, error) {
	return &agent.ExecutionHandle{Driver: d.Name(), Target: envelope.Target, SchemaVersion: 1, ID: envelope.Identity.RunID + "-question"}, nil
}
func (d *questionDriver) Observe(context.Context, agent.ExecutionHandle) (*agent.ExecutionObservation, error) {
	d.mu.Lock()
	d.observes++
	d.mu.Unlock()
	result := d.result
	if result == nil {
		result = &agent.AgentResult{Status: "needs_input", Summary: "approval required", Questions: []agent.InputQuestion{{ID: "approval", Prompt: "Proceed?", Choices: []string{"yes", "no"}, Required: true}}}
	}
	return &agent.ExecutionObservation{State: agent.ObservationTerminal, Result: result, Events: []agent.DriverEvent{{Type: agent.EventAttemptNeedsInput, Result: result}}}, nil
}
func (*questionDriver) Output(context.Context, agent.ExecutionHandle, int) (string, error) {
	return "", nil
}
func (*questionDriver) Cancel(context.Context, agent.ExecutionHandle) (*agent.CancelResult, error) {
	return &agent.CancelResult{State: agent.CancelConfirmed}, nil
}
func (d *questionDriver) observeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.observes
}

func TestNewRunPersistsDriverContextWithoutLegacyOrCCPanesFields(t *testing.T) {
	terminal := backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{Status: "succeeded", Summary: "done"}}
	candidate := newRoutingBackend("codex", terminal)
	state := store.New(t.TempDir())
	service := NewService(candidate, state)
	doc := `apiVersion: fishyume/v1
name: m4-state
defaults:
  agent: {driver: codex, target: local}
execution: {maxConcurrency: 1}
nodes:
  implement: {type: agent, task: implement, requiredSkills: [go]}
`
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Filename: "workflow.yaml", Content: doc})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	waitForControllers(t, service)

	var attempt AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "implement", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	if attempt.ResolvedDriver != "codex" || attempt.ResolvedTarget != "local" || attempt.ContextCompilerVersion != "context-compiler/v1" || len(attempt.ContextManifest.Components) == 0 || len(attempt.ContextHash) != 64 {
		t.Fatalf("attempt metadata=%+v", attempt)
	}
	for _, path := range []string{state.SnapshotPath(started.ID), state.WorkflowPath(started.ID), state.AttemptPath(started.ID, "implement", 1)} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, forbidden := range []string{`"backend"`, `"tool"`, `"runtime"`, `"taskBindingId"`, `"session"`} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("new state %s retained forbidden field %s: %s", path, forbidden, text)
			}
		}
	}
}

func TestNeedsInputQuestionsPersistAcrossRestartWithoutDuplicateObserve(t *testing.T) {
	driver := &questionDriver{}
	state := store.New(t.TempDir())
	first := NewService(scheduleradapter.New(driver), state)
	started, err := first.Start(context.Background(), StartRequest{Project: t.TempDir(), Task: "request approval"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, first, started.ID, func(snapshot WorkflowSnapshot) bool {
		return snapshot.Phase == PhaseWaiting && snapshot.Reason == ReasonAgentWaitingInput
	})
	waitForControllers(t, first)
	assertPersistedQuestion := func(service *Service) {
		view, err := service.Status(started.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Nodes) != 1 || view.Nodes[0].Result == nil || len(view.Nodes[0].Result.Questions) != 1 || view.Nodes[0].Result.Questions[0].ID != "approval" {
			t.Fatalf("status questions=%+v", view.Nodes)
		}
		var persisted workflow.Result
		if err := state.ReadResult(started.ID, "agent-1", 1, &persisted); err != nil {
			t.Fatal(err)
		}
		if len(persisted.Questions) != 1 || persisted.Questions[0].Prompt != "Proceed?" {
			t.Fatalf("persisted result=%+v", persisted)
		}
		var attempt AttemptSnapshot
		if err := state.ReadAttempt(started.ID, "agent-1", 1, &attempt); err != nil {
			t.Fatal(err)
		}
		if !attempt.ResultConsumed || attempt.Reason != ReasonAgentWaitingInput {
			t.Fatalf("attempt=%+v", attempt)
		}
	}
	assertPersistedQuestion(first)
	if driver.observeCount() != 1 {
		t.Fatalf("observes=%d", driver.observeCount())
	}
	waitingEvents := countEventsOfType(t, state, started.ID, "node.waiting")
	eventsBeforeResume := countEvents(t, state, started.ID)

	second := NewService(scheduleradapter.New(driver), state)
	assertPersistedQuestion(second)
	if _, err := second.Resume(context.Background(), ResumeRequest{RunID: started.ID}); err != nil {
		t.Fatal(err)
	}
	waitForControllers(t, second)
	assertPersistedQuestion(second)
	if driver.observeCount() != 1 {
		t.Fatalf("restart re-observed consumed needs_input result: %d", driver.observeCount())
	}
	if after := countEventsOfType(t, state, started.ID, "node.waiting"); after != waitingEvents {
		t.Fatalf("duplicate Observe grew node.waiting events: before=%d after=%d", waitingEvents, after)
	}
	if after := countEvents(t, state, started.ID); after != eventsBeforeResume {
		t.Fatalf("settled needs_input resume grew events: before=%d after=%d", eventsBeforeResume, after)
	}
}

func TestGenericDriverRejectsQuestionsOnNonNeedsInputResult(t *testing.T) {
	driver := &questionDriver{result: &agent.AgentResult{Status: "succeeded", Summary: "done", Questions: []agent.InputQuestion{{ID: "unexpected", Prompt: "Should not exist", Required: true}}}}
	state := store.New(t.TempDir())
	service := NewService(scheduleradapter.New(driver), state)
	started, err := service.Start(context.Background(), StartRequest{Project: t.TempDir(), Task: "invalid questions"})
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool {
		return snapshot.Phase == PhaseWaiting && snapshot.Reason == ReasonInvalidResult
	})
	waitForControllers(t, service)
	if !strings.Contains(waiting.Summary, "only needs_input") {
		t.Fatalf("waiting=%+v", waiting)
	}
	view, err := service.Status(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 1 || view.Nodes[0].Result != nil {
		t.Fatalf("invalid result was persisted: %+v", view.Nodes)
	}
}

func TestNeedsInputResultWriteFailureIsAtomicAndRetryable(t *testing.T) {
	driver := &questionDriver{}
	state := store.New(t.TempDir())
	first := NewService(scheduleradapter.New(driver), state)
	var once sync.Once
	first.testHooks.beforeControllerMutation = func(point string) {
		if point == "node.waiting_result" {
			once.Do(func() {
				state.SetFaultInjectorForTest(failOnce(func(operation, path string) bool {
					return operation == "write_json" && strings.HasSuffix(path, "result.json")
				}))
			})
		}
	}
	started, err := first.Start(context.Background(), StartRequest{Project: t.TempDir(), Task: "request approval"})
	if err != nil {
		t.Fatal(err)
	}
	waitForControllers(t, first)
	var node NodeSnapshot
	if err := state.ReadNode(started.ID, "agent-1", &node); err != nil {
		t.Fatal(err)
	}
	var attempt AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "agent-1", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	if node.Result != nil || attempt.ResultConsumed || attempt.Reason == ReasonAgentWaitingInput {
		t.Fatalf("failed result write partially committed waiting state: node=%+v attempt=%+v", node, attempt)
	}
	var missing workflow.Result
	if err := state.ReadResult(started.ID, "agent-1", 1, &missing); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("result file after injected failure err=%v result=%+v", err, missing)
	}
	if count := countEventsOfType(t, state, started.ID, "node.waiting"); count != 0 {
		t.Fatalf("failed result write emitted node.waiting events=%d", count)
	}

	second := NewService(scheduleradapter.New(driver), state)
	if _, err := second.Resume(context.Background(), ResumeRequest{RunID: started.ID}); err != nil {
		t.Fatal(err)
	}
	waitForRun(t, second, started.ID, func(snapshot WorkflowSnapshot) bool {
		return snapshot.Phase == PhaseWaiting && snapshot.Reason == ReasonAgentWaitingInput
	})
	waitForControllers(t, second)
	view, err := second.Status(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 1 || view.Nodes[0].Result == nil || len(view.Nodes[0].Result.Questions) != 1 {
		t.Fatalf("recovered questions=%+v", view.Nodes)
	}
	if driver.observeCount() != 2 {
		t.Fatalf("observes=%d want=2", driver.observeCount())
	}
	if count := countEventsOfType(t, state, started.ID, "node.waiting"); count != 1 {
		t.Fatalf("recovered node.waiting events=%d", count)
	}
}

func countEventsOfType(t *testing.T, state *store.Store, runID, eventType string) int {
	t.Helper()
	count := 0
	if err := state.ReadEvents(runID, func(data json.RawMessage) error {
		var event WorkflowEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return err
		}
		if event.Type == eventType {
			count++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func countEvents(t *testing.T, state *store.Store, runID string) int {
	t.Helper()
	count := 0
	if err := state.ReadEvents(runID, func(json.RawMessage) error {
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count
}
