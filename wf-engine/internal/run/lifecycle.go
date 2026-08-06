package run

import (
	"fmt"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/contextcompiler"
	"wf.local/wf-engine/internal/workflow"
)

type Phase string

const (
	PhaseCreated    Phase = "created"
	PhaseRunning    Phase = "running"
	PhaseWaiting    Phase = "waiting"
	PhasePaused     Phase = "paused"
	PhaseCancelling Phase = "cancelling"
	PhaseCompleted  Phase = "completed"
)

type NodePhase string

const (
	NodePhasePending   NodePhase = "pending"
	NodePhaseReady     NodePhase = "ready"
	NodePhaseRunning   NodePhase = "running"
	NodePhaseWaiting   NodePhase = "waiting"
	NodePhaseCompleted NodePhase = "completed"
	NodePhaseSkipped   NodePhase = "skipped"
)

type Conclusion string

const (
	ConclusionSucceeded     Conclusion = "succeeded"
	ConclusionFailed        Conclusion = "failed"
	ConclusionCancelled     Conclusion = "cancelled"
	ConclusionRejected      Conclusion = "rejected"
	ConclusionIndeterminate Conclusion = "indeterminate"
)

type Reason string

const (
	ReasonApprovalRequired  Reason = "approval_required"
	ReasonAgentWaitingInput Reason = "agent_waiting_input"
	ReasonCompletionMissing Reason = "completion_missing"
	ReasonInvalidResult     Reason = "invalid_result"
	ReasonCancelFailed      Reason = "cancel_failed"
	ReasonConditionFalse    Reason = "condition_false"
	ReasonUpstreamFailed    Reason = "upstream_failed"
	ReasonFailurePolicy     Reason = "failure_policy"
	ReasonWorkflowCancelled Reason = "workflow_cancelled"
	ReasonControllerDetach  Reason = "controller_detached"
	ReasonUserRequested     Reason = "user_requested"
)

type WorkflowSnapshot struct {
	ProtocolVersion      int                    `json:"protocolVersion"`
	StateSchemaVersion   int                    `json:"stateSchemaVersion"`
	ID                   string                 `json:"id"`
	WorkflowName         string                 `json:"workflowName"`
	Project              string                 `json:"project"`
	ResolvedDriver       string                 `json:"resolvedDriver"`
	ResolvedTarget       string                 `json:"resolvedTarget"`
	DeprecationWarnings  []string               `json:"deprecationWarnings,omitempty"`
	Backend              string                 `json:"-"`
	EffectiveConcurrency int                    `json:"effectiveConcurrency,omitempty"`
	Phase                Phase                  `json:"phase"`
	Conclusion           Conclusion             `json:"conclusion,omitempty"`
	Reason               Reason                 `json:"reason,omitempty"`
	Summary              string                 `json:"summary,omitempty"`
	Inputs               map[string]any         `json:"inputs,omitempty"`
	TopologicalOrder     []string               `json:"topologicalOrder"`
	Nodes                map[string]NodeSummary `json:"nodes"`
	ActiveNodeID         string                 `json:"activeNodeId,omitempty"`
	CancelRequested      bool                   `json:"cancelRequested"`
	StateDir             string                 `json:"stateDir"`
	CreatedAt            time.Time              `json:"createdAt"`
	UpdatedAt            time.Time              `json:"updatedAt"`
}

type NodeSummary struct {
	ID             string     `json:"id"`
	Type           string     `json:"type"`
	Phase          NodePhase  `json:"phase"`
	Conclusion     Conclusion `json:"conclusion,omitempty"`
	Reason         Reason     `json:"reason,omitempty"`
	Diagnostic     string     `json:"diagnostic,omitempty"`
	CurrentAttempt int        `json:"currentAttempt,omitempty"`
}

type NodeSnapshot struct {
	ProtocolVersion    int              `json:"protocolVersion"`
	StateSchemaVersion int              `json:"stateSchemaVersion"`
	RunID              string           `json:"runId"`
	ID                 string           `json:"id"`
	Type               string           `json:"type"`
	Phase              NodePhase        `json:"phase"`
	Conclusion         Conclusion       `json:"conclusion,omitempty"`
	Reason             Reason           `json:"reason,omitempty"`
	Diagnostic         string           `json:"diagnostic,omitempty"`
	Result             *workflow.Result `json:"result,omitempty"`
	CurrentAttempt     int              `json:"currentAttempt,omitempty"`
	CreatedAt          time.Time        `json:"createdAt"`
	UpdatedAt          time.Time        `json:"updatedAt"`
}

type LaunchState string

const (
	LaunchPrepared              LaunchState = "prepared"
	LaunchDispatching           LaunchState = "dispatching"
	LaunchHandlePersisted       LaunchState = "handle_persisted"
	LaunchFinishedWithoutHandle LaunchState = "finished_without_handle"

	// Deprecated M2.1.1 launch states retained for compatibility reads.
	LaunchSessionPersisted       LaunchState = "session_persisted"
	LaunchFinishedWithoutSession LaunchState = "finished_without_session"
)

type AttemptSnapshot struct {
	ProtocolVersion        int                      `json:"protocolVersion"`
	StateSchemaVersion     int                      `json:"stateSchemaVersion"`
	RunID                  string                   `json:"runId"`
	NodeID                 string                   `json:"nodeId"`
	Number                 int                      `json:"number"`
	Phase                  NodePhase                `json:"phase"`
	Conclusion             Conclusion               `json:"conclusion,omitempty"`
	Reason                 Reason                   `json:"reason,omitempty"`
	ResolvedDriver         string                   `json:"resolvedDriver"`
	ResolvedTarget         string                   `json:"resolvedTarget"`
	Backend                string                   `json:"-"`
	LaunchState            LaunchState              `json:"launchState,omitempty"`
	Execution              *backend.ExecutionHandle `json:"execution,omitempty"`
	ResultConsumed         bool                     `json:"resultConsumed"`
	ContextCompilerVersion string                   `json:"contextCompilerVersion,omitempty"`
	ContextManifest        contextcompiler.Manifest `json:"contextManifest"`
	ContextHash            string                   `json:"contextHash"`
	PromptHash             string                   `json:"-"`
	StartedAt              time.Time                `json:"startedAt"`
	UpdatedAt              time.Time                `json:"updatedAt"`
	CompletedAt            *time.Time               `json:"completedAt,omitempty"`

	legacyExecution *legacyExecutionSnapshot
}

type WorkflowEvent struct {
	ProtocolVersion int        `json:"protocolVersion"`
	RunID           string     `json:"runId"`
	Sequence        uint64     `json:"sequence"`
	Type            string     `json:"type"`
	Phase           Phase      `json:"phase"`
	Conclusion      Conclusion `json:"conclusion,omitempty"`
	Reason          Reason     `json:"reason,omitempty"`
	NodeID          string     `json:"nodeId,omitempty"`
	NodePhase       NodePhase  `json:"nodePhase,omitempty"`
	Message         string     `json:"message,omitempty"`
	Timestamp       time.Time  `json:"timestamp"`
}

type LegacySnapshot struct {
	ProtocolVersion int        `json:"protocolVersion"`
	ID              string     `json:"id"`
	Status          RunStatus  `json:"status"`
	NodeStatus      NodeStatus `json:"nodeStatus"`
	Project         string     `json:"project"`
	Summary         string     `json:"summary,omitempty"`
	StateDir        string     `json:"stateDir"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

func ValidateWorkflowSnapshot(snapshot WorkflowSnapshot) error {
	if !validPhase(snapshot.Phase) {
		return fmt.Errorf("unknown run phase %q", snapshot.Phase)
	}
	if snapshot.Conclusion != "" && !validConclusion(snapshot.Conclusion) {
		return fmt.Errorf("unknown run conclusion %q", snapshot.Conclusion)
	}
	if snapshot.Reason != "" && !validReason(snapshot.Reason) {
		return fmt.Errorf("unknown run reason %q", snapshot.Reason)
	}
	terminal := snapshot.Phase == PhaseCompleted
	if terminal != (snapshot.Conclusion != "") {
		return fmt.Errorf("run phase %q and conclusion %q are inconsistent", snapshot.Phase, snapshot.Conclusion)
	}
	if snapshot.Phase != PhaseWaiting && snapshot.Phase != PhasePaused && snapshot.Phase != PhaseCancelling && snapshot.Reason != "" && !terminal {
		return fmt.Errorf("active run phase %q cannot have reason %q", snapshot.Phase, snapshot.Reason)
	}
	return nil
}

func ValidateNodeSnapshot(snapshot NodeSnapshot) error {
	if !validNodePhase(snapshot.Phase) {
		return fmt.Errorf("unknown node phase %q", snapshot.Phase)
	}
	if snapshot.Conclusion != "" && !validConclusion(snapshot.Conclusion) {
		return fmt.Errorf("unknown node conclusion %q", snapshot.Conclusion)
	}
	if snapshot.Reason != "" && !validReason(snapshot.Reason) {
		return fmt.Errorf("unknown node reason %q", snapshot.Reason)
	}
	if snapshot.Phase == NodePhaseCompleted && snapshot.Conclusion == "" {
		return fmt.Errorf("completed node requires a conclusion")
	}
	if snapshot.Phase != NodePhaseCompleted && snapshot.Conclusion != "" {
		return fmt.Errorf("node phase %q cannot have conclusion %q", snapshot.Phase, snapshot.Conclusion)
	}
	if snapshot.Phase == NodePhaseSkipped && snapshot.Reason == "" {
		return fmt.Errorf("skipped node requires a reason")
	}
	return nil
}

func ValidateAttemptSnapshot(snapshot AttemptSnapshot) error {
	if snapshot.Number < 1 {
		return fmt.Errorf("attempt number must be positive")
	}
	if snapshot.Phase != NodePhaseRunning && snapshot.Phase != NodePhaseWaiting && snapshot.Phase != NodePhaseCompleted {
		return fmt.Errorf("attempt has invalid phase %q", snapshot.Phase)
	}
	if snapshot.Conclusion != "" && !validConclusion(snapshot.Conclusion) {
		return fmt.Errorf("attempt has unknown conclusion %q", snapshot.Conclusion)
	}
	if snapshot.Reason != "" && !validReason(snapshot.Reason) {
		return fmt.Errorf("attempt has unknown reason %q", snapshot.Reason)
	}
	if snapshot.Phase == NodePhaseCompleted && snapshot.Conclusion == "" {
		return fmt.Errorf("completed attempt requires a conclusion")
	}
	if snapshot.Phase != NodePhaseCompleted && snapshot.Conclusion != "" {
		return fmt.Errorf("active attempt cannot have conclusion %q", snapshot.Conclusion)
	}
	if snapshot.Phase == NodePhaseWaiting && snapshot.Reason == "" {
		return fmt.Errorf("waiting attempt requires a reason")
	}
	if snapshot.ResolvedDriver != "" && snapshot.Backend != "" && snapshot.ResolvedDriver != snapshot.Backend {
		return fmt.Errorf("attempt Driver %q conflicts with deprecated Backend alias %q", snapshot.ResolvedDriver, snapshot.Backend)
	}
	if snapshot.Execution != nil {
		if err := backend.ValidateExecutionHandle(*snapshot.Execution); err != nil {
			return fmt.Errorf("attempt has invalid execution handle: %w", err)
		}
		if snapshot.Execution.DriverName() != attemptDriver(snapshot) {
			return fmt.Errorf("attempt Driver %q does not match execution handle Driver %q", attemptDriver(snapshot), snapshot.Execution.DriverName())
		}
	}
	if snapshot.LaunchState != "" && snapshot.LaunchState != LaunchPrepared && snapshot.LaunchState != LaunchDispatching && snapshot.LaunchState != LaunchHandlePersisted && snapshot.LaunchState != LaunchFinishedWithoutHandle && snapshot.LaunchState != LaunchSessionPersisted && snapshot.LaunchState != LaunchFinishedWithoutSession {
		return fmt.Errorf("attempt has invalid launch state %q", snapshot.LaunchState)
	}
	return nil
}

func validPhase(phase Phase) bool {
	switch phase {
	case PhaseCreated, PhaseRunning, PhaseWaiting, PhasePaused, PhaseCancelling, PhaseCompleted:
		return true
	}
	return false
}
func validNodePhase(phase NodePhase) bool {
	switch phase {
	case NodePhasePending, NodePhaseReady, NodePhaseRunning, NodePhaseWaiting, NodePhaseCompleted, NodePhaseSkipped:
		return true
	}
	return false
}
func validConclusion(conclusion Conclusion) bool {
	switch conclusion {
	case ConclusionSucceeded, ConclusionFailed, ConclusionCancelled, ConclusionRejected, ConclusionIndeterminate:
		return true
	}
	return false
}
func validReason(reason Reason) bool {
	switch reason {
	case ReasonApprovalRequired, ReasonAgentWaitingInput, ReasonCompletionMissing, ReasonInvalidResult, ReasonCancelFailed, ReasonConditionFalse, ReasonUpstreamFailed, ReasonFailurePolicy, ReasonWorkflowCancelled, ReasonControllerDetach, ReasonUserRequested:
		return true
	}
	return false
}
