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
