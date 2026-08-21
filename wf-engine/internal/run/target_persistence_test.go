package run

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/driver/scheduleradapter"
	"wf.local/wf-engine/internal/store"
)

type multiTargetDriver struct {
	mu              sync.Mutex
	starts          []agent.AttemptEnvelope
	recoveryRelease chan struct{}
}

func (*multiTargetDriver) Name() string { return "multi-target" }
func (*multiTargetDriver) Capabilities() agent.DriverCapabilities {
	return agent.DriverCapabilities{Targets: []string{"local", "wsl"}, SupportsOutput: true, SupportsWaitingInput: true, SupportsRecovery: true, SupportsConfirmedCancel: true}
}
func (d *multiTargetDriver) Doctor(context.Context, agent.DoctorRequest) agent.DoctorReport {
	return agent.DoctorReport{Driver: d.Name(), Ready: true}
}
func (d *multiTargetDriver) Start(_ context.Context, envelope agent.AttemptEnvelope) (*agent.ExecutionHandle, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.starts = append(d.starts, envelope)
	return &agent.ExecutionHandle{Driver: d.Name(), Target: envelope.Target, SchemaVersion: 1, ID: fmt.Sprintf("exec-%d", len(d.starts))}, nil
}
func (d *multiTargetDriver) Observe(ctx context.Context, handle agent.ExecutionHandle) (*agent.ExecutionObservation, error) {
	if handle.ID == "exec-1" {
		return &agent.ExecutionObservation{State: agent.ObservationTerminal, Result: &agent.AgentResult{Status: "succeeded", Summary: "retry fixture", Questions: []agent.InputQuestion{{ID: "invalid", Prompt: "invalid for success", Required: true}}}}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-d.recoveryRelease:
		return &agent.ExecutionObservation{State: agent.ObservationTerminal, Result: &agent.AgentResult{Status: "succeeded", Summary: "recovered"}}, nil
	}
}
func (*multiTargetDriver) Output(context.Context, agent.ExecutionHandle, int) (string, error) {
	return "", nil
}
func (*multiTargetDriver) Cancel(context.Context, agent.ExecutionHandle) (*agent.CancelResult, error) {
	return &agent.CancelResult{State: agent.CancelConfirmed}, nil
}
func (d *multiTargetDriver) startEnvelopes() []agent.AttemptEnvelope {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]agent.AttemptEnvelope(nil), d.starts...)
}

func TestNodeTargetPersistsAcrossInitialRetryAndRecovery(t *testing.T) {
	candidate := &multiTargetDriver{recoveryRelease: make(chan struct{})}
	state := store.New(t.TempDir())
	document := `apiVersion: fishyume/v1
name: target-persistence
defaults:
  agent: {driver: multi-target, target: local}
execution: {maxConcurrency: 1}
nodes:
  agent-1:
    type: agent
    agent: {target: wsl}
    task: work
`
	first := NewService(scheduleradapter.New(candidate), state)
	started, err := first.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Filename: "workflow.yaml", Content: document})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, first, started.ID, func(snapshot WorkflowSnapshot) bool {
		return snapshot.Phase == PhaseWaiting && snapshot.Reason == ReasonInvalidResult
	})
	waitForControllers(t, first)
	assertTarget := func(number int) AttemptSnapshot {
		var attempt AttemptSnapshot
		if err := state.ReadAttempt(started.ID, "agent-1", number, &attempt); err != nil {
			t.Fatal(err)
		}
		if attempt.ResolvedTarget != "wsl" || attempt.Execution == nil || attempt.Execution.Target != "wsl" {
			t.Fatalf("attempt %d target metadata=%+v", number, attempt)
		}
		if err := ValidateAttemptSnapshot(attempt); err != nil {
			t.Fatal(err)
		}
		return attempt
	}
	assertTarget(1)

	retry := NewService(scheduleradapter.New(candidate), state)
	if _, err := retry.Resume(context.Background(), ResumeRequest{RunID: started.ID, Action: &ResumeAction{Type: "retry", NodeID: "agent-1"}}); err != nil {
		t.Fatal(err)
	}
	waitForRun(t, retry, started.ID, func(snapshot WorkflowSnapshot) bool {
		view, viewErr := retry.Status(snapshot.ID)
		return viewErr == nil && view.ActiveAttempt != nil && view.ActiveAttempt.Number == 2 && view.ActiveAttempt.Execution != nil
	})
	attempt2 := assertTarget(2)
	if _, err := retry.Detach(started.ID); err != nil {
		t.Fatal(err)
	}
	close(candidate.recoveryRelease)

	recovered := NewService(scheduleradapter.New(candidate), state)
	if _, err := recovered.Resume(context.Background(), ResumeRequest{RunID: started.ID}); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, recovered, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	waitForControllers(t, recovered)
	if final.Conclusion != ConclusionSucceeded || len(candidate.startEnvelopes()) != 2 {
		t.Fatalf("final=%+v starts=%+v", final, candidate.startEnvelopes())
	}
	assertTarget(2)
	if got := []string{candidate.startEnvelopes()[0].Target, candidate.startEnvelopes()[1].Target}; strings.Join(got, ",") != "wsl,wsl" {
		t.Fatalf("launch targets=%v", got)
	}

	mismatched := attempt2
	handle := *mismatched.Execution
	handle.Target = "local"
	mismatched.Execution = &handle
	if err := ValidateAttemptSnapshot(mismatched); err == nil || !strings.Contains(err.Error(), "Target") {
		t.Fatalf("target mismatch validation error=%v", err)
	}
}

func TestParallelLaunchPersistsResolvedNodeTarget(t *testing.T) {
	candidate := &multiTargetDriver{recoveryRelease: make(chan struct{})}
	state := store.New(t.TempDir())
	document := `apiVersion: fishyume/v1
name: parallel-target-persistence
defaults:
  agent: {driver: multi-target, target: local}
execution: {maxConcurrency: 2}
nodes:
  agent-1:
    type: agent
    agent: {target: wsl}
    task: work
`
	service := NewService(scheduleradapter.New(candidate), state)
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Filename: "workflow.yaml", Content: document})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool {
		return snapshot.Phase == PhaseWaiting && snapshot.Reason == ReasonInvalidResult
	})
	waitForControllers(t, service)
	var attempt AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "agent-1", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	if attempt.ResolvedTarget != "wsl" || attempt.Execution == nil || attempt.Execution.Target != "wsl" {
		t.Fatalf("parallel attempt target metadata=%+v", attempt)
	}
	if envelopes := candidate.startEnvelopes(); len(envelopes) != 1 || envelopes[0].Target != "wsl" {
		t.Fatalf("parallel launch envelopes=%+v", envelopes)
	}
}
