package run

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"wf.local/wf-engine/internal/backend"
)

type activeAttemptRef struct {
	nodeID  string
	attempt int
}

type observedAttempt struct {
	ref         activeAttemptRef
	observation *backend.ExecutionObservation
	err         error
}

func findActiveAttempts(nodes []NodeSnapshot) []activeAttemptRef {
	active := make([]activeAttemptRef, 0)
	for _, node := range nodes {
		if node.Type == "agent" && node.CurrentAttempt > 0 && (node.Phase == NodePhaseRunning || node.Phase == NodePhaseWaiting) {
			active = append(active, activeAttemptRef{nodeID: node.ID, attempt: node.CurrentAttempt})
		}
	}
	return active
}

func (s *Service) reconcileAttempts(ctx context.Context, runID string, generation uint64, refs []activeAttemptRef) (bool, bool, error) {
	results := make([]observedAttempt, len(refs))
	var group sync.WaitGroup
	for index, ref := range refs {
		group.Add(1)
		go func(index int, ref activeAttemptRef) {
			defer group.Done()
			results[index] = s.observeAttempt(ctx, runID, ref)
		}(index, ref)
	}
	group.Wait()
	progressed := false
	recordWaiting := func(ref activeAttemptRef, reason Reason, message string) error {
		changed, err := s.waitingIfChanged(runID, ref.nodeID, ref.attempt, generation, reason, message)
		if changed {
			progressed = true
		}
		return err
	}
	for _, result := range results {
		if result.err != nil {
			if errors.Is(result.err, context.Canceled) || errors.Is(result.err, context.DeadlineExceeded) {
				return progressed, false, result.err
			}
			if err := recordWaiting(result.ref, ReasonCompletionMissing, result.err.Error()); err != nil {
				return progressed, false, err
			}
			continue
		}
		observation := result.observation
		if observation == nil {
			if err := recordWaiting(result.ref, ReasonCompletionMissing, "Backend returned no execution observation"); err != nil {
				return progressed, false, err
			}
			continue
		}
		switch observation.State {
		case backend.ObservationTerminal:
			if err := backend.ValidateExecutionObservation(*observation); err != nil {
				if waitErr := recordWaiting(result.ref, ReasonInvalidResult, err.Error()); waitErr != nil {
					return progressed, false, waitErr
				}
				continue
			}
			if _, err := s.finishResult(runID, result.ref.nodeID, result.ref.attempt, generation, observation.Result); err != nil {
				return progressed, false, err
			}
			progressed = true
		case backend.ObservationWaitingInput:
			message := observation.Diagnostic
			if message == "" {
				message = "Agent is waiting for input"
			}
			if err := recordWaiting(result.ref, ReasonAgentWaitingInput, message); err != nil {
				return progressed, false, err
			}
		case backend.ObservationResultPending:
			message := observation.Diagnostic
			if message == "" {
				message = "Agent result remained unavailable after bounded reconciliation"
			}
			if err := recordWaiting(result.ref, ReasonCompletionMissing, message); err != nil {
				return progressed, false, err
			}
		case backend.ObservationLost, backend.ObservationExited:
			if err := s.finishIndeterminate(runID, result.ref.nodeID, result.ref.attempt, generation, "Agent execution was lost without a valid terminal result"); err != nil {
				return progressed, false, err
			}
			progressed = true
		case backend.ObservationError:
			if _, err := s.finishResult(runID, result.ref.nodeID, result.ref.attempt, generation, &backend.AgentResult{Status: "failed", Summary: "Agent execution reported an error"}); err != nil {
				return progressed, false, err
			}
			progressed = true
		case backend.ObservationActive:
		default:
			return progressed, false, fmt.Errorf("Backend returned unsupported observation state %q", observation.State)
		}
	}
	_, nodes, err := s.loadRun(runID)
	if err != nil {
		return progressed, false, err
	}
	active := findActiveAttempts(nodes)
	allWaiting := len(active) > 0
	for _, ref := range active {
		node, err := findNode(nodes, ref.nodeID)
		if err != nil {
			return progressed, false, err
		}
		if node.Phase != NodePhaseWaiting {
			allWaiting = false
			break
		}
	}
	return progressed, allWaiting, nil
}

func (s *Service) observeAttempt(ctx context.Context, runID string, ref activeAttemptRef) observedAttempt {
	result := observedAttempt{ref: ref}
	var attempt AttemptSnapshot
	if err := s.store.ReadAttempt(runID, ref.nodeID, ref.attempt, &attempt); err != nil {
		result.err = err
		return result
	}
	candidate, err := s.registry.Get(attemptDriver(attempt))
	if err != nil {
		result.err = fmt.Errorf("select persisted Backend: %w", err)
		return result
	}
	handle, err := s.executionHandle(candidate, attempt)
	if err != nil {
		result.err = fmt.Errorf("decode persisted execution handle: %w", err)
		return result
	}
	if handle == nil {
		result.observation = &backend.ExecutionObservation{State: backend.ObservationLost, Diagnostic: "Attempt exists without a persisted execution handle"}
		return result
	}
	observation, err := candidate.Observe(ctx, *handle)
	if output, outputErr := candidate.Output(context.Background(), *handle, 200); outputErr == nil {
		if writeErr := s.store.WriteNodeOutput(runID, ref.nodeID, ref.attempt, output); writeErr != nil && err == nil {
			err = writeErr
		}
	}
	if err != nil {
		result.err = fmt.Errorf("Backend observation failed: %w", err)
		return result
	}
	if observation != nil && observation.State == backend.ObservationResultPending {
		for check := 0; check < startupIdleReconcileChecks; check++ {
			if err := s.waitStartupIdleReconcile(ctx); err != nil {
				result.err = err
				return result
			}
			observation, err = candidate.Observe(ctx, *handle)
			if err != nil {
				result.err = fmt.Errorf("Backend result reconciliation failed: %w", err)
				return result
			}
			if observation == nil || observation.State != backend.ObservationResultPending {
				break
			}
		}
	}
	result.observation = observation
	return result
}
