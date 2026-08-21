// Package driveradapter is the temporary M4 compatibility bridge from the
// deprecated backend surface to the Agent Driver contract. New production
// composition registers Drivers through this adapter; historical tests and
// snapshot decoders may continue to use backend.AgentBackend during M4.1.
package driveradapter

import (
	"context"
	"encoding/json"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/backend"
)

type Adapter struct {
	driver agent.AgentDriver
}

func New(driver agent.AgentDriver) *Adapter { return &Adapter{driver: driver} }

func (a *Adapter) Name() string { return a.driver.Name() }

func (a *Adapter) Capabilities() backend.Capabilities {
	value := a.driver.Capabilities()
	return backend.Capabilities{
		Tools:                    []string{a.driver.Name()},
		Runtimes:                 append([]string(nil), value.Targets...),
		SupportsOutput:           value.SupportsOutput,
		SupportsWaitingInput:     value.SupportsWaitingInput,
		MaxConcurrentAgents:      value.MaxConcurrentAttempts,
		SupportsConcurrentCancel: value.SupportsConcurrentCancel,
	}
}

func (a *Adapter) Doctor(ctx context.Context, request backend.DoctorRequest) backend.DoctorReport {
	report := a.driver.Doctor(ctx, agent.DoctorRequest{Workspace: request.Workspace, Target: request.Runtime})
	converted := backend.DoctorReport{Backend: report.Driver, Ready: report.Ready}
	for _, item := range report.Diagnostics {
		converted.Diagnostics = append(converted.Diagnostics, backend.Diagnostic{Name: item.Name, Status: backend.DiagnosticStatus(item.Status), Message: item.Message})
	}
	return converted
}

func (a *Adapter) Start(ctx context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	target := spec.Runtime
	if target == "" {
		target = "local"
	}
	envelope := agent.AttemptEnvelope{
		ProtocolVersion: agent.ProtocolVersion,
		Identity:        agent.AttemptIdentity{RunID: spec.RunID, NodeID: spec.NodeID, Attempt: spec.Attempt},
		Workspace:       spec.Workspace,
		Target:          target,
		Task:            spec.Instructions,
		Context:         agent.AttemptContext{UpstreamResults: []agent.UpstreamResult{}, RequiredSkills: append([]string(nil), spec.RequiredSkills...)},
		Constraints:     map[string]string{},
		Budget:          map[string]int64{},
		ResultContract:  agent.ResultContract{Schema: append(json.RawMessage(nil), spec.ResultContract.Schema...), MaxBytes: spec.ResultContract.MaxBytes},
		Prompt:          spec.Instructions,
	}
	if spec.Envelope != nil {
		envelope = *spec.Envelope
		if envelope.Target == "" {
			envelope.Target = target
		}
	}
	handle, err := a.driver.Start(ctx, envelope)
	if handle == nil {
		return nil, err
	}
	return &backend.ExecutionHandle{Driver: handle.Driver, Target: handle.Target, Backend: handle.Driver, SchemaVersion: handle.SchemaVersion, ID: handle.ID, Data: append(json.RawMessage(nil), handle.Data...)}, err
}

func (a *Adapter) Observe(ctx context.Context, handle backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	observation, err := a.driver.Observe(ctx, toDriverHandle(handle))
	if observation == nil {
		return nil, err
	}
	converted := &backend.ExecutionObservation{State: backend.ObservationState(observation.State), Diagnostic: observation.Diagnostic}
	if observation.Result != nil {
		converted.Result = toBackendResult(*observation.Result)
	}
	return converted, err
}

func (a *Adapter) Output(ctx context.Context, handle backend.ExecutionHandle, lines int) (string, error) {
	return a.driver.Output(ctx, toDriverHandle(handle), lines)
}

func (a *Adapter) Cancel(ctx context.Context, handle backend.ExecutionHandle) (*backend.CancelResult, error) {
	result, err := a.driver.Cancel(ctx, toDriverHandle(handle))
	if result == nil {
		return nil, err
	}
	return &backend.CancelResult{State: backend.CancelState(result.State), Diagnostic: result.Diagnostic}, err
}

func toDriverHandle(handle backend.ExecutionHandle) agent.ExecutionHandle {
	target := handle.Target
	if target == "" {
		target = "local"
	}
	return agent.ExecutionHandle{Driver: handle.DriverName(), Target: target, SchemaVersion: handle.SchemaVersion, ID: handle.ID, Data: append(json.RawMessage(nil), handle.Data...)}
}

func toBackendResult(result agent.AgentResult) *backend.AgentResult {
	questions := make([]backend.InputQuestion, len(result.Questions))
	for index, question := range result.Questions {
		questions[index] = backend.InputQuestion{ID: question.ID, Prompt: question.Prompt, Choices: append([]string(nil), question.Choices...), Required: question.Required}
	}
	return &backend.AgentResult{Status: result.Status, Summary: result.Summary, Artifacts: result.Artifacts, Warnings: result.Warnings, Checks: result.Checks, Questions: questions,
		Usage: backend.Usage{InputTokensEstimated: result.Usage.InputTokensEstimated, OutputTokensEstimated: result.Usage.OutputTokensEstimated}, SideEffectStatus: result.SideEffectStatus}
}
