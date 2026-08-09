package run

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
)

func TestCompletedRunRejectsStaleCancelInsideServiceMutation(t *testing.T) {
	state := store.New(t.TempDir())
	b := &fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "succeeded", Summary: "done"}}, observations: map[string][]backend.Observation{}}
	service := NewService(b, state)
	started, err := service.Start(context.Background(), StartRequest{Project: "p", Task: "work"})
	if err != nil {
		t.Fatal(err)
	}
	completed := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	expected := completed.StateVersion - 1
	request, err := state.RequestCancellationWithPrecondition(started.ID, time.Now().UTC(), &expected, "stale-cancel", "hash-stale")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.handleConcurrentCancellationRequest(context.Background(), started.ID)
	if err == nil || !strings.Contains(err.Error(), "state version conflict") {
		t.Fatalf("stale cancellation error = %v", err)
	}
	current, err := service.Get(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := current.ActionReceipts[request.ActionID]; ok {
		t.Fatalf("stale cancellation recorded receipt: %+v", current.ActionReceipts)
	}
	if current.Conclusion != ConclusionSucceeded {
		t.Fatalf("stale cancellation changed terminal run: %+v", current)
	}
}

func TestActionIntentReplaysApprovalAfterRunReceiptFailure(t *testing.T) {
	state := store.New(t.TempDir())
	b := &fakeWorkflowBackend{observations: map[string][]backend.Observation{}}
	first := NewService(b, state)
	started, err := first.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: "apiVersion: wf/v1\nname: approval\nexecution: {maxConcurrency: 1}\nnodes: {approve: {type: approval, prompt: approve}}\n"})
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitForRun(t, first, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Reason == ReasonApprovalRequired })
	state.SetFaultInjectorForTest(failOnce(func(operation, path string) bool {
		return operation == "write_json" && strings.HasSuffix(path, "run.json")
	}))
	action := ResumeAction{ActionID: "approval-action", ActionRequestHash: "approval-hash", Type: "approve", NodeID: "approve"}
	if _, err := first.Resume(context.Background(), ResumeRequest{RunID: started.ID, ExpectedStateVersion: &waiting.StateVersion, Action: &action}); err == nil {
		t.Fatal("approval succeeded despite Run receipt write failure")
	}
	state.SetFaultInjectorForTest(nil)
	different := action
	different.ActionRequestHash = "other-hash"
	if _, err := first.Resume(context.Background(), ResumeRequest{RunID: started.ID, ExpectedStateVersion: &waiting.StateVersion, Action: &different}); err == nil || !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("different approval replay error = %v", err)
	}
	second := NewService(b, state)
	if _, err := second.Resume(context.Background(), ResumeRequest{RunID: started.ID, ExpectedStateVersion: &waiting.StateVersion, Action: &action}); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, second, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	if final.Nodes["approve"].Conclusion != ConclusionSucceeded || final.ActionReceipts[action.ActionID].RequestHash != action.ActionRequestHash {
		t.Fatalf("approval replay = %+v", final)
	}
	if _, err := os.Stat(state.ActionIntentPath(started.ID, action.ActionID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("approval intent was retained: %v", err)
	}
}

func TestActionIntentReplaysAnswerAndRetryWithoutDuplicateAttempt(t *testing.T) {
	for _, fixture := range []struct {
		name      string
		results   []backend.BackendResult
		action    func(WorkflowSnapshot) ResumeAction
		assertRun func(*testing.T, WorkflowSnapshot, *fakeWorkflowBackend)
	}{
		{name: "answer", results: []backend.BackendResult{{Status: "needs_input", Summary: "choose", Questions: []backend.InputQuestion{{ID: "scope", Prompt: "Scope", Choices: []string{"core"}, Required: true}}}, {Status: "succeeded", Summary: "done"}}, action: func(snapshot WorkflowSnapshot) ResumeAction {
			attempt := snapshot.Nodes["plan"].CurrentAttempt
			return ResumeAction{ActionID: "answer-action", ActionRequestHash: "answer-hash", Type: "answer", NodeID: "plan", ExpectedAttempt: &attempt, Answers: map[string]any{"scope": "core"}}
		}, assertRun: func(t *testing.T, snapshot WorkflowSnapshot, b *fakeWorkflowBackend) {
			if snapshot.Nodes["plan"].CurrentAttempt != 2 || b.launches != 2 {
				t.Fatalf("answer replay duplicated or skipped attempt: run=%+v launches=%d", snapshot, b.launches)
			}
		}},
		{name: "retry", results: []backend.BackendResult{{Status: "failed", Summary: "failed"}, {Status: "succeeded", Summary: "done"}}, action: func(snapshot WorkflowSnapshot) ResumeAction {
			return ResumeAction{ActionID: "retry-action", ActionRequestHash: "retry-hash", Type: "retry", NodeID: "plan"}
		}, assertRun: func(t *testing.T, snapshot WorkflowSnapshot, b *fakeWorkflowBackend) {
			if snapshot.Nodes["plan"].CurrentAttempt != 2 || b.launches != 2 {
				t.Fatalf("retry replay duplicated or skipped attempt: run=%+v launches=%d", snapshot, b.launches)
			}
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			state := store.New(t.TempDir())
			b := &fakeWorkflowBackend{waitResults: fixture.results, observations: map[string][]backend.Observation{}}
			first := NewService(b, state)
			started, err := first.StartWorkflow(context.Background(), StartWorkflowRequest{Project: "p", Content: "apiVersion: wf/v1\nname: action\ndefaults: {tool: codex, runtime: local}\nexecution: {maxConcurrency: 1}\nnodes: {plan: {type: agent, task: work}}\n"})
			if err != nil {
				t.Fatal(err)
			}
			var observed WorkflowSnapshot
			if fixture.name == "answer" {
				observed = waitForRun(t, first, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Reason == ReasonAgentWaitingInput })
			} else {
				observed = waitForRun(t, first, started.ID, func(snapshot WorkflowSnapshot) bool {
					return snapshot.Phase == PhaseCompleted && snapshot.Conclusion == ConclusionFailed
				})
			}
			action := fixture.action(observed)
			state.SetFaultInjectorForTest(failOnce(func(operation, path string) bool {
				return operation == "write_json" && strings.HasSuffix(path, "run.json")
			}))
			if _, err := first.Resume(context.Background(), ResumeRequest{RunID: started.ID, ExpectedStateVersion: &observed.StateVersion, Action: &action}); err == nil {
				t.Fatal("action succeeded despite Run receipt write failure")
			}
			state.SetFaultInjectorForTest(nil)
			different := action
			different.ActionRequestHash += "-other"
			if _, err := first.Resume(context.Background(), ResumeRequest{RunID: started.ID, ExpectedStateVersion: &observed.StateVersion, Action: &different}); err == nil || !strings.Contains(err.Error(), "already bound") {
				t.Fatalf("different action replay error = %v", err)
			}
			second := NewService(b, state)
			if _, err := second.Resume(context.Background(), ResumeRequest{RunID: started.ID, ExpectedStateVersion: &observed.StateVersion, Action: &action}); err != nil {
				t.Fatal(err)
			}
			final := waitForRun(t, second, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
			fixture.assertRun(t, final, b)
		})
	}
}

func TestActionReceiptRetentionIsBounded(t *testing.T) {
	run := WorkflowSnapshot{StateVersion: 1000}
	for index := 0; index < maxActionReceipts+10; index++ {
		run.StateVersion++
		actionID := fmt.Sprintf("action-%03d", index)
		(&Service{}).recordActionReceipt(&run, actionID, "hash")
	}
	if len(run.ActionReceipts) != maxActionReceipts {
		t.Fatalf("receipt retention = %d, want %d", len(run.ActionReceipts), maxActionReceipts)
	}
	if _, ok := run.ActionReceipts["action-000"]; ok {
		t.Fatalf("oldest receipt was not compacted: %+v", run.ActionReceipts["action-000"])
	}
}
