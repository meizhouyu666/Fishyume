package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"wf.local/wf-engine/internal/backend"
)

type cancellationTarget struct {
	nodeID  string
	attempt int
}

type cancellationOutcome struct {
	target     cancellationTarget
	confirmed  bool
	diagnostic string
}

func (s *Service) handleConcurrentCancellationRequest(ctx context.Context, runID string) (WorkflowSnapshot, error) {
	targets, err := s.markConcurrentCancellationIntent(runID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	outcomes := make([]cancellationOutcome, len(targets))
	semaphore := make(chan struct{}, FishyumeSafetyConcurrencyCeiling)
	var group sync.WaitGroup
	for index, target := range targets {
		group.Add(1)
		go func(index int, target cancellationTarget) {
			defer group.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				outcomes[index] = cancellationOutcome{target: target, diagnostic: ctx.Err().Error()}
				return
			}
			outcomes[index] = s.cancelTarget(ctx, runID, target)
		}(index, target)
	}
	group.Wait()
	return s.applyCancellationOutcomes(runID, outcomes)
}

func (s *Service) markConcurrentCancellationIntent(runID string) ([]cancellationTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, nodes, err := s.loadRun(runID)
	if err != nil {
		return nil, err
	}
	if run.Phase == PhaseCompleted {
		return nil, nil
	}
	if !run.CancelRequested || run.Phase != PhaseCancelling {
		run.CancelRequested, run.Phase, run.Conclusion, run.Reason, run.Summary, run.UpdatedAt = true, PhaseCancelling, "", "", "workflow cancellation requested", s.now().UTC()
		if err := s.persistRun(&run, nil, "run.cancelling", run.Summary); err != nil {
			return nil, err
		}
	}
	targets := make([]cancellationTarget, 0)
	for _, node := range nodes {
		if node.Type != "agent" || node.CurrentAttempt < 1 || (node.Phase != NodePhaseRunning && node.Phase != NodePhaseWaiting) {
			continue
		}
		var attempt AttemptSnapshot
		if err := s.store.ReadAttempt(runID, node.ID, node.CurrentAttempt, &attempt); err != nil {
			return nil, err
		}
		if attempt.Phase == NodePhaseCompleted && attempt.Conclusion == ConclusionCancelled {
			continue
		}
		targets = append(targets, cancellationTarget{nodeID: node.ID, attempt: node.CurrentAttempt})
	}
	return targets, nil
}

func (s *Service) cancelTarget(ctx context.Context, runID string, target cancellationTarget) cancellationOutcome {
	outcome := cancellationOutcome{target: target}
	attempt, handle, err := s.waitForCancellationHandle(ctx, runID, target.nodeID, target.attempt)
	if err != nil {
		outcome.diagnostic = err.Error()
		return outcome
	}
	if attempt.Phase == NodePhaseCompleted {
		outcome.confirmed = true
		return outcome
	}
	if handle == nil {
		outcome.confirmed = true
		return outcome
	}
	candidate, err := s.registry.Get(attempt.Backend)
	if err != nil {
		outcome.diagnostic = err.Error()
		return outcome
	}
	result, err := candidate.Cancel(ctx, *handle)
	if err != nil {
		outcome.diagnostic = "cancel Backend execution: " + err.Error()
		return outcome
	}
	if result == nil {
		outcome.diagnostic = "Backend returned no cancellation result"
		return outcome
	}
	if err := backend.ValidateCancelResult(*result); err != nil {
		outcome.diagnostic = err.Error()
		return outcome
	}
	if result.State != backend.CancelConfirmed {
		outcome.diagnostic = "Backend did not confirm execution cancellation"
		if strings.TrimSpace(result.Diagnostic) != "" {
			outcome.diagnostic += ": " + result.Diagnostic
		}
		return outcome
	}
	outcome.confirmed = true
	return outcome
}

func (s *Service) applyCancellationOutcomes(runID string, outcomes []cancellationOutcome) (WorkflowSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, nodes, err := s.loadRun(runID)
	if err != nil {
		return WorkflowSnapshot{}, err
	}
	if run.Phase == PhaseCompleted {
		return run, nil
	}
	unresolved := make([]string, 0)
	for _, outcome := range outcomes {
		node, err := findNode(nodes, outcome.target.nodeID)
		if err != nil {
			return WorkflowSnapshot{}, err
		}
		var attempt AttemptSnapshot
		if err := s.store.ReadAttempt(runID, node.ID, outcome.target.attempt, &attempt); err != nil {
			return WorkflowSnapshot{}, err
		}
		now := s.now().UTC()
		if outcome.confirmed {
			attempt.Phase, attempt.Conclusion, attempt.Reason, attempt.UpdatedAt, attempt.CompletedAt = NodePhaseCompleted, ConclusionCancelled, ReasonUserRequested, now, &now
			node.Phase, node.Conclusion, node.Reason, node.Diagnostic, node.UpdatedAt = NodePhaseCompleted, ConclusionCancelled, ReasonUserRequested, "", now
		} else {
			diagnostic := outcome.diagnostic
			if diagnostic == "" {
				diagnostic = "cancellation was not confirmed"
			}
			attempt.Phase, attempt.Conclusion, attempt.Reason, attempt.UpdatedAt, attempt.CompletedAt = NodePhaseWaiting, "", ReasonCancelFailed, now, nil
			node.Phase, node.Conclusion, node.Reason, node.Diagnostic, node.UpdatedAt = NodePhaseWaiting, "", ReasonCancelFailed, diagnostic, now
			unresolved = append(unresolved, node.ID+": "+diagnostic)
		}
		if err := s.store.WriteNode(runID, node.ID, node); err != nil {
			return WorkflowSnapshot{}, err
		}
		if err := s.writeAttempt(attempt, false); err != nil {
			return WorkflowSnapshot{}, err
		}
		run.Nodes[node.ID] = summarizeNode(*node)
		eventType := "node.cancelled"
		if !outcome.confirmed {
			eventType = "node.cancel_failed"
		}
		if err := s.persistRun(&run, node, eventType, node.Diagnostic); err != nil {
			return WorkflowSnapshot{}, err
		}
	}
	if len(unresolved) > 0 {
		run.CancelRequested, run.Phase, run.Conclusion, run.Reason, run.Summary, run.ActiveNodeID, run.UpdatedAt = true, PhaseWaiting, "", ReasonCancelFailed, strings.Join(unresolved, "; "), "", s.now().UTC()
		if len(unresolved) == 1 {
			for _, outcome := range outcomes {
				if !outcome.confirmed {
					run.ActiveNodeID = outcome.target.nodeID
				}
			}
		}
		if err := s.persistRun(&run, nil, "run.cancel_failed", run.Summary); err != nil {
			return WorkflowSnapshot{}, errors.Join(errors.New(run.Summary), err)
		}
		return run, errors.New(run.Summary)
	}
	for index := range nodes {
		node := &nodes[index]
		if node.Phase == NodePhasePending || node.Phase == NodePhaseReady || (node.Phase == NodePhaseWaiting && node.Type == "approval") {
			node.Phase, node.Reason, node.Diagnostic, node.UpdatedAt = NodePhaseSkipped, ReasonWorkflowCancelled, "", s.now().UTC()
			if err := s.store.WriteNode(runID, node.ID, node); err != nil {
				return WorkflowSnapshot{}, err
			}
			run.Nodes[node.ID] = summarizeNode(*node)
		}
	}
	run.CancelRequested, run.Phase, run.Conclusion, run.Reason, run.Summary, run.ActiveNodeID, run.UpdatedAt = true, PhaseCompleted, ConclusionCancelled, ReasonUserRequested, "workflow cancelled", "", s.now().UTC()
	if err := s.persistRun(&run, nil, "run.cancelled", run.Summary); err != nil {
		return WorkflowSnapshot{}, err
	}
	return run, nil
}

func cancellationSummary(outcomes []cancellationOutcome) string {
	parts := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		parts = append(parts, fmt.Sprintf("%s=%t", outcome.target.nodeID, outcome.confirmed))
	}
	return strings.Join(parts, ",")
}
