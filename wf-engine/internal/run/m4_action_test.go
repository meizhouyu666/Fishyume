package run

import (
	"context"
	"strings"
	"testing"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

func TestNeedsInputAnswerCreatesNewAttemptWithQuestionAndAnswerContext(t *testing.T) {
	candidate := &fakeWorkflowBackend{
		waitResults: []backend.BackendResult{
			{Status: "needs_input", Summary: "choose scope", Questions: []backend.InputQuestion{{ID: "scope", Prompt: "Which scope?", Choices: []string{"core", "all"}, Required: true}}},
			{Status: "succeeded", Summary: "done"},
		},
	}
	service := NewService(candidate, store.New(t.TempDir()))
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Filename: "workflow.yaml", Content: `apiVersion: fishyume/v1
name: answer
defaults:
  agent: {driver: fake, target: local}
execution: {maxConcurrency: 1}
nodes:
  plan: {type: agent, task: Plan}
`})
	if err != nil {
		t.Fatal(err)
	}
	waiting := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool {
		return snapshot.Phase == PhaseWaiting && snapshot.Reason == ReasonAgentWaitingInput
	})
	expectedVersion, expectedAttempt := waiting.StateVersion, 1
	if _, err := service.Resume(context.Background(), ResumeRequest{RunID: started.ID, ExpectedStateVersion: &expectedVersion, Action: &ResumeAction{Type: "answer", NodeID: "plan", ExpectedAttempt: &expectedAttempt, Answers: map[string]any{"scope": "core"}}}); err != nil {
		t.Fatal(err)
	}
	completed := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	if completed.Conclusion != ConclusionSucceeded || completed.Nodes["plan"].CurrentAttempt != 2 || candidate.launches != 2 {
		t.Fatalf("completed = %+v, launches = %d", completed, candidate.launches)
	}
	var second AttemptSnapshot
	if err := service.store.ReadAttempt(started.ID, "plan", 2, &second); err != nil {
		t.Fatal(err)
	}
	lastComponent := second.ContextManifest.Components[len(second.ContextManifest.Components)-1]
	if lastComponent.Name != "input-answer" || lastComponent.Source != "run.action.answer" {
		t.Fatalf("second Attempt manifest is missing answer provenance: %+v", second.ContextManifest)
	}
	if len(candidate.prompts) != 2 || !strings.Contains(candidate.prompts[1], `"inputAnswer":{"attempt":1`) || !strings.Contains(candidate.prompts[1], `"prompt":"Which scope?"`) || !strings.Contains(candidate.prompts[1], `"answers":{"scope":"core"}`) {
		t.Fatalf("second prompt does not contain canonical question and answer context: %s", candidate.prompts[1])
	}
	var first AttemptSnapshot
	if err := service.store.ReadAttempt(started.ID, "plan", 1, &first); err != nil {
		t.Fatal(err)
	}
	if first.Phase != NodePhaseWaiting || first.Reason != ReasonAgentWaitingInput || !first.ResultConsumed {
		t.Fatalf("first Attempt was resumed instead of retained: %+v", first)
	}
}

func TestNeedsInputAnswerValidatesQuestionIdentityAndChoices(t *testing.T) {
	questions := []workflow.InputQuestion{{ID: "scope", Prompt: "Which scope?", Choices: []string{"core", "all"}, Required: true}}
	if _, err := validateAndEncodeAnswers(1, questions, map[string]any{}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("missing required answer error = %v", err)
	}
	if _, err := validateAndEncodeAnswers(1, questions, map[string]any{"other": "core"}); err == nil {
		t.Fatal("unknown question was accepted")
	}
	if _, err := validateAndEncodeAnswers(1, questions, map[string]any{"scope": "unsupported"}); err == nil || !strings.Contains(err.Error(), "choices") {
		t.Fatalf("invalid choice error = %v", err)
	}
	if _, err := validateAndEncodeAnswers(1, questions, map[string]any{"scope": []string{"core"}}); err == nil || !strings.Contains(err.Error(), "string, number, or boolean") {
		t.Fatalf("structured answer error = %v", err)
	}
}
