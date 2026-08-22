package run

import (
	"context"
	"errors"
	"sync"
	"testing"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/store"
)

// readinessBackend makes the evidence boundary explicit without requiring a
// Provider login or a real subprocess. Each test exercises one Driver signal
// and asserts the durable Run/Attempt classification that follows it.
type readinessBackend struct {
	mu          sync.Mutex
	startErr    error
	observation *backend.ExecutionObservation
	observeErr  error
	starts      int
	observes    int
}

func (*readinessBackend) Name() string { return "readiness" }

func (*readinessBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true, SupportsWaitingInput: true}
}

func (*readinessBackend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: "readiness", Ready: true}
}

func (b *readinessBackend) Start(_ context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	b.mu.Lock()
	b.starts++
	startErr := b.startErr
	b.mu.Unlock()
	if startErr != nil {
		return nil, startErr
	}
	return &backend.ExecutionHandle{Backend: b.Name(), Target: spec.Runtime, SchemaVersion: 1, ID: "readiness-execution"}, nil
}

func (b *readinessBackend) Observe(_ context.Context, _ backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	b.mu.Lock()
	b.observes++
	observation, observeErr := b.observation, b.observeErr
	b.mu.Unlock()
	if observeErr != nil {
		return nil, observeErr
	}
	if observation == nil {
		return nil, nil
	}
	copy := *observation
	if observation.Result != nil {
		result := *observation.Result
		copy.Result = &result
	}
	return &copy, nil
}

func (*readinessBackend) Output(context.Context, backend.ExecutionHandle, int) (string, error) {
	return "", nil
}

func (*readinessBackend) Cancel(context.Context, backend.ExecutionHandle) (*backend.CancelResult, error) {
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}

func TestM68CoreReadinessFailureMatrix(t *testing.T) {
	tests := []struct {
		name              string
		startErr          error
		observeErr        error
		observation       *backend.ExecutionObservation
		wantPhase         Phase
		wantConclusion    Conclusion
		wantReason        Reason
		wantAttemptPhase  NodePhase
		wantAttemptResult Conclusion
		wantSideEffect    agent.SideEffectStatus
		wantStarts        int
		wantObserves      int
	}{
		{
			name:              "launch outcome unknown is indeterminate",
			startErr:          errors.New("driver process exited during launch"),
			wantPhase:         PhaseCompleted,
			wantConclusion:    ConclusionIndeterminate,
			wantAttemptPhase:  NodePhaseCompleted,
			wantAttemptResult: ConclusionIndeterminate,
			wantSideEffect:    agent.SideEffectUnknown,
			wantStarts:        1,
		},
		{
			name:             "observation transport failure is retryable waiting",
			observeErr:       errors.New("driver transport unavailable"),
			wantPhase:        PhaseWaiting,
			wantReason:       ReasonCompletionMissing,
			wantAttemptPhase: NodePhaseWaiting,
			wantStarts:       1,
			wantObserves:     1,
		},
		{
			name:              "lost execution is indeterminate",
			observation:       &backend.ExecutionObservation{State: backend.ObservationLost, Diagnostic: "process identity disappeared"},
			wantPhase:         PhaseCompleted,
			wantConclusion:    ConclusionIndeterminate,
			wantAttemptPhase:  NodePhaseCompleted,
			wantAttemptResult: ConclusionIndeterminate,
			wantSideEffect:    agent.SideEffectUnknown,
			wantStarts:        1,
			wantObserves:      1,
		},
		{
			name:             "unsupported observation is retryable waiting",
			observation:      &backend.ExecutionObservation{State: backend.ObservationState("unsupported")},
			wantPhase:        PhaseWaiting,
			wantReason:       ReasonCompletionMissing,
			wantAttemptPhase: NodePhaseWaiting,
			wantStarts:       1,
			wantObserves:     1,
		},
		{
			name:             "absent observation is retryable waiting",
			wantPhase:        PhaseWaiting,
			wantReason:       ReasonCompletionMissing,
			wantAttemptPhase: NodePhaseWaiting,
			wantStarts:       1,
			wantObserves:     1,
		},
		{
			name: "terminal failure without evidence is conservatively unknown",
			observation: &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{
				Status: "failed", Summary: "provider rejected request",
			}},
			wantPhase:         PhaseCompleted,
			wantConclusion:    ConclusionFailed,
			wantReason:        ReasonUpstreamFailed,
			wantAttemptPhase:  NodePhaseCompleted,
			wantAttemptResult: ConclusionFailed,
			wantSideEffect:    agent.SideEffectUnknown,
			wantStarts:        1,
			wantObserves:      1,
		},
		{
			name: "terminal failure with explicit no-side-effect evidence stays failed",
			observation: &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{
				Status: "failed", Summary: "provider unavailable", SideEffectStatus: agent.SideEffectNone,
			}},
			wantPhase:         PhaseCompleted,
			wantConclusion:    ConclusionFailed,
			wantReason:        ReasonUpstreamFailed,
			wantAttemptPhase:  NodePhaseCompleted,
			wantAttemptResult: ConclusionFailed,
			wantSideEffect:    agent.SideEffectNone,
			wantStarts:        1,
			wantObserves:      1,
		},
		{
			name: "terminal indeterminate requires explicit acknowledgement later",
			observation: &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{
				Status: "indeterminate", Summary: "provider outcome unknown",
			}},
			wantPhase:         PhaseCompleted,
			wantConclusion:    ConclusionIndeterminate,
			wantAttemptPhase:  NodePhaseCompleted,
			wantAttemptResult: ConclusionIndeterminate,
			wantSideEffect:    agent.SideEffectUnknown,
			wantStarts:        1,
			wantObserves:      1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := &readinessBackend{startErr: test.startErr, observation: test.observation, observeErr: test.observeErr}
			state := store.New(t.TempDir())
			service := NewService(candidate, state)
			started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Content: `apiVersion: fishyume/v1
name: m6-8-readiness
defaults:
  agent: {driver: readiness, target: local}
execution: {maxConcurrency: 1}
nodes:
  work: {type: agent, task: exercise failure evidence}
`})
			if err != nil {
				t.Fatal(err)
			}
			final := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool {
				return snapshot.Phase == test.wantPhase && (test.wantPhase != PhaseWaiting || snapshot.Reason == test.wantReason)
			})
			if test.wantPhase == PhaseWaiting {
				waitForControllers(t, service)
			}
			if final.Conclusion != test.wantConclusion || final.Reason != test.wantReason {
				t.Fatalf("run=%+v", final)
			}

			var attempt AttemptSnapshot
			if err := state.ReadAttempt(started.ID, "work", 1, &attempt); err != nil {
				t.Fatal(err)
			}
			if attempt.Phase != test.wantAttemptPhase || attempt.Conclusion != test.wantAttemptResult || attempt.SideEffectStatus != test.wantSideEffect {
				t.Fatalf("attempt=%+v", attempt)
			}
			candidate.mu.Lock()
			starts, observes := candidate.starts, candidate.observes
			candidate.mu.Unlock()
			if starts != test.wantStarts || observes != test.wantObserves {
				t.Fatalf("driver calls starts=%d observes=%d, want %d/%d", starts, observes, test.wantStarts, test.wantObserves)
			}
		})
	}
}
