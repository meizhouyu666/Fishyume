package run

import (
	"time"

	"wf.local/wf-engine/internal/workflow"
)

func aggregateRunState(run *WorkflowSnapshot, nodes []NodeSnapshot, doc workflow.Document, now time.Time) (string, bool) {
	activeRunning := 0
	waitingAgents := make([]NodeSnapshot, 0)
	waitingApprovals := make([]NodeSnapshot, 0)
	pending := 0
	failure := false
	activeIDs := make([]string, 0)
	for _, node := range nodes {
		switch node.Phase {
		case NodePhaseRunning:
			if node.Type == "agent" {
				activeRunning++
				activeIDs = append(activeIDs, node.ID)
			}
		case NodePhaseWaiting:
			activeIDs = append(activeIDs, node.ID)
			if node.Type == "agent" {
				waitingAgents = append(waitingAgents, node)
			} else {
				waitingApprovals = append(waitingApprovals, node)
			}
		case NodePhasePending, NodePhaseReady:
			pending++
		}
		if node.Phase == NodePhaseCompleted && (node.Conclusion == ConclusionFailed || node.Conclusion == ConclusionIndeterminate || node.Conclusion == ConclusionCancelled) {
			failure = true
		}
	}
	compatActive := ""
	if len(activeIDs) == 1 {
		compatActive = activeIDs[0]
	}
	if activeRunning > 0 {
		run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.UpdatedAt = PhaseRunning, "", "", compatActive, now
		if failure {
			run.Summary = "workflow failure detected; draining active sibling executions"
		}
		return "run.running", false
	}
	if len(waitingAgents) > 0 {
		reason, summary := waitingAgents[0].Reason, waitingAgents[0].Diagnostic
		if summary == "" {
			summary = "Agent execution is waiting"
		}
		run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.Summary, run.UpdatedAt = PhaseWaiting, "", reason, compatActive, summary, now
		return "run.waiting", true
	}
	if len(waitingApprovals) > 0 {
		summary := waitingApprovals[0].Diagnostic
		if summary == "" {
			summary = "approval required"
		}
		run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.Summary, run.UpdatedAt = PhaseWaiting, "", ReasonApprovalRequired, compatActive, summary, now
		return "run.waiting", true
	}
	if pending > 0 {
		run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.UpdatedAt = PhaseRunning, "", "", compatActive, now
		return "run.running", false
	}
	conclusion, reason := aggregateConclusion(nodes, doc)
	run.Phase, run.Conclusion, run.Reason, run.ActiveNodeID, run.Summary, run.UpdatedAt = PhaseCompleted, conclusion, reason, "", "workflow completed", now
	return "run.completed", true
}
