package backend

import "context"

type LaunchSpec struct {
	RunID    string
	Project  string
	Tool     string
	Runtime  string
	Prompt   string
	StateDir string
}

type Session struct {
	ID       string
	Metadata map[string]string
}

type Usage struct {
	InputTokensEstimated  int `json:"inputTokensEstimated"`
	OutputTokensEstimated int `json:"outputTokensEstimated"`
}

type BackendResult struct {
	Status    string   `json:"status"`
	Summary   string   `json:"summary,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
	Checks    []string `json:"checks,omitempty"`
	Usage     Usage    `json:"usage"`
}

type ObservationState string

const (
	ObservationActive            ObservationState = "active"
	ObservationWaitingInput      ObservationState = "waiting_input"
	ObservationCompletionMissing ObservationState = "completion_missing"
	ObservationExited            ObservationState = "exited"
	ObservationLost              ObservationState = "lost"
	ObservationError             ObservationState = "error"
	ObservationTerminal          ObservationState = "terminal"
)

type Observation struct {
	State  ObservationState
	Result *BackendResult
}

type Backend interface {
	Name() string
	Doctor(context.Context) error
	Launch(context.Context, LaunchSpec) (*Session, error)
	Wait(context.Context, Session) (*BackendResult, error)
	Output(context.Context, Session, int) (string, error)
	Cancel(context.Context, Session) error
}

type ProjectDoctor interface {
	DoctorProject(context.Context, string) error
}

// Reconciler is optional. A Backend that implements it can inspect an existing
// persisted Session without launching another Agent.
type Reconciler interface {
	Reconcile(context.Context, Session) (*Observation, error)
}
