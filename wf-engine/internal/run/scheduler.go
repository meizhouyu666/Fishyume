package run

import (
	"fmt"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/workflow"
)

const FishyumeSafetyConcurrencyCeiling = 8

type SchedulingDecision struct {
	ReadyAgents       []string
	ReadyApprovals    []string
	SkipUpstream      []string
	SkipConditions    []string
	SkipFailurePolicy []string
	EffectiveLimit    int
	ActiveAgents      int
	AvailableAgents   int
	StopScheduling    bool
	Complete          bool
}

// PlanSchedule derives a deterministic scheduling decision without Backend I/O.
// A zero backend limit means the Backend did not declare a limit and the
// Fishyume safety ceiling is used.
func PlanSchedule(normalized workflow.Normalized, run WorkflowSnapshot, nodes []NodeSnapshot, backendLimit int) (SchedulingDecision, error) {
	if normalized.Document.Execution.MaxConcurrency < 1 {
		return SchedulingDecision{}, fmt.Errorf("workflow maxConcurrency must be positive")
	}
	if backendLimit < 0 {
		return SchedulingDecision{}, fmt.Errorf("Backend maxConcurrentAgents cannot be negative")
	}
	effective := FishyumeSafetyConcurrencyCeiling
	if normalized.Document.Execution.MaxConcurrency < effective {
		effective = normalized.Document.Execution.MaxConcurrency
	}
	if backendLimit > 0 && backendLimit < effective {
		effective = backendLimit
	}
	if effective < 1 {
		return SchedulingDecision{}, fmt.Errorf("effective concurrency must be positive")
	}
	byID := make(map[string]NodeSnapshot, len(nodes))
	results := make(map[string]workflow.Result)
	active := 0
	failure := run.Conclusion == ConclusionFailed || run.Conclusion == ConclusionIndeterminate || run.Conclusion == ConclusionCancelled
	for _, node := range nodes {
		byID[node.ID] = node
		if node.Result != nil {
			results[node.ID] = *node.Result
		}
		if node.Type == "agent" && (node.Phase == NodePhaseRunning || node.Phase == NodePhaseWaiting) && node.CurrentAttempt > 0 {
			active++
		}
		if node.Phase == NodePhaseCompleted && (node.Conclusion == ConclusionFailed || node.Conclusion == ConclusionIndeterminate || node.Conclusion == ConclusionCancelled) {
			failure = true
		}
	}
	decision := SchedulingDecision{EffectiveLimit: effective, ActiveAgents: active, StopScheduling: failure}
	decision.AvailableAgents = effective - active
	if decision.AvailableAgents < 0 {
		decision.AvailableAgents = 0
	}
	for _, nodeID := range normalized.TopologicalOrder {
		node, ok := byID[nodeID]
		if ok && decision.StopScheduling && node.Type == "approval" && node.Phase == NodePhaseWaiting {
			decision.SkipFailurePolicy = append(decision.SkipFailurePolicy, nodeID)
			continue
		}
		if !ok || (node.Phase != NodePhasePending && node.Phase != NodePhaseReady) {
			continue
		}
		definition := normalized.Document.Nodes[nodeID]
		stable, upstreamFailed := true, false
		for _, dependency := range definition.DependsOn {
			dependencyNode := byID[dependency]
			if dependencyNode.Phase != NodePhaseCompleted && dependencyNode.Phase != NodePhaseSkipped {
				stable = false
				break
			}
			if dependencyNode.Conclusion == ConclusionFailed || dependencyNode.Conclusion == ConclusionIndeterminate || dependencyNode.Conclusion == ConclusionCancelled || dependencyNode.Reason == ReasonUpstreamFailed {
				upstreamFailed = true
			}
		}
		if !stable {
			continue
		}
		eligibleFailureBranch := false
		if definition.When != nil {
			matches, err := workflow.Evaluate(*definition.When, results)
			if err != nil {
				// A skipped failed ancestor has no result to evaluate. Propagate the
				// upstream failure instead of turning the Run into completion_missing.
				// Evaluate first so all/any retain their short-circuit semantics.
				if upstreamFailed && conditionResultMissing(*definition.When, results) {
					decision.SkipUpstream = append(decision.SkipUpstream, nodeID)
					continue
				}
				return SchedulingDecision{}, fmt.Errorf("node %q condition: %w", nodeID, err)
			}
			if !matches {
				decision.SkipConditions = append(decision.SkipConditions, nodeID)
				continue
			}
			eligibleFailureBranch = upstreamFailed
		} else if upstreamFailed {
			decision.SkipUpstream = append(decision.SkipUpstream, nodeID)
			continue
		}
		if decision.StopScheduling && !eligibleFailureBranch {
			decision.SkipFailurePolicy = append(decision.SkipFailurePolicy, nodeID)
			continue
		}
		if definition.Type == "approval" {
			decision.ReadyApprovals = append(decision.ReadyApprovals, nodeID)
			continue
		}
		if decision.AvailableAgents > 0 {
			decision.ReadyAgents = append(decision.ReadyAgents, nodeID)
			decision.AvailableAgents--
		}
	}
	decision.Complete = true
	for _, node := range nodes {
		if node.Phase != NodePhaseCompleted && node.Phase != NodePhaseSkipped {
			decision.Complete = false
			break
		}
	}
	return decision, nil
}

func conditionResultMissing(condition workflow.Condition, results map[string]workflow.Result) bool {
	if condition.Node != "" {
		_, exists := results[condition.Node]
		return !exists
	}
	for _, child := range condition.All {
		if conditionResultMissing(child, results) {
			return true
		}
	}
	for _, child := range condition.Any {
		if conditionResultMissing(child, results) {
			return true
		}
	}
	return condition.Not != nil && conditionResultMissing(*condition.Not, results)
}

func backendConcurrencyLimit(candidate backend.AgentBackend) (int, error) {
	if candidate == nil {
		return 0, fmt.Errorf("Backend is required")
	}
	limit := candidate.Capabilities().MaxConcurrentAgents
	if limit < 0 {
		return 0, fmt.Errorf("Backend maxConcurrentAgents cannot be negative")
	}
	return limit, nil
}

func EffectiveConcurrency(workflowLimit, backendLimit int) (int, error) {
	if workflowLimit < 1 {
		return 0, fmt.Errorf("workflow maxConcurrency must be positive")
	}
	if backendLimit < 0 {
		return 0, fmt.Errorf("Backend maxConcurrentAgents cannot be negative")
	}
	limit := FishyumeSafetyConcurrencyCeiling
	if workflowLimit < limit {
		limit = workflowLimit
	}
	if backendLimit > 0 && backendLimit < limit {
		limit = backendLimit
	}
	return limit, nil
}
