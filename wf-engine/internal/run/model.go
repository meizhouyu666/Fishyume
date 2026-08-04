package run

import "time"

type RunStatus string

const (
	RunCreated       RunStatus = "created"
	RunDispatching   RunStatus = "dispatching"
	RunRunning       RunStatus = "running"
	RunSucceeded     RunStatus = "succeeded"
	RunFailed        RunStatus = "failed"
	RunBlocked       RunStatus = "blocked"
	RunIndeterminate RunStatus = "indeterminate"
	RunPaused        RunStatus = "paused"
	RunCancelled     RunStatus = "cancelled"
)

func (s RunStatus) Terminal() bool {
	switch s {
	case RunSucceeded, RunFailed, RunBlocked, RunIndeterminate, RunPaused, RunCancelled:
		return true
	default:
		return false
	}
}

type NodeStatus string

const (
	NodeCreated       NodeStatus = "created"
	NodeDispatching   NodeStatus = "dispatching"
	NodeRunning       NodeStatus = "running"
	NodeSucceeded     NodeStatus = "succeeded"
	NodeFailed        NodeStatus = "failed"
	NodeBlocked       NodeStatus = "blocked"
	NodeIndeterminate NodeStatus = "indeterminate"
	NodePaused        NodeStatus = "paused"
	NodeCancelled     NodeStatus = "cancelled"
)

type RunSnapshot struct {
	ProtocolVersion int        `json:"protocolVersion"`
	ID              string     `json:"id"`
	Status          RunStatus  `json:"status"`
	NodeStatus      NodeStatus `json:"nodeStatus"`
	Project         string     `json:"project"`
	Tool            string     `json:"tool"`
	Runtime         string     `json:"runtime"`
	Backend         string     `json:"backend"`
	Summary         string     `json:"summary,omitempty"`
	StateDir        string     `json:"stateDir"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

type RunEvent struct {
	ProtocolVersion int        `json:"protocolVersion"`
	RunID           string     `json:"runId"`
	Sequence        uint64     `json:"sequence"`
	Type            string     `json:"type"`
	Status          RunStatus  `json:"status"`
	NodeStatus      NodeStatus `json:"nodeStatus"`
	Message         string     `json:"message,omitempty"`
	Timestamp       time.Time  `json:"timestamp"`
}
