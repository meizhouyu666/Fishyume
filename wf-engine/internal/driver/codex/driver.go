package codex

import (
	"context"
	"encoding/json"
	"fmt"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/backend/directcli"
)

type Config = directcli.Config

type Driver struct {
	legacy *directcli.Backend
}

func New(config Config) *Driver { return &Driver{legacy: directcli.New(config)} }

func (*Driver) Name() string { return "codex" }

func (*Driver) Capabilities() agent.DriverCapabilities {
	return agent.DriverCapabilities{
		Targets:                  []string{"local"},
		SupportsOutput:           true,
		SupportsWaitingInput:     true,
		SupportsRecovery:         true,
		SupportsConfirmedCancel:  true,
		SupportsConcurrentCancel: true,
	}
}

func (d *Driver) Doctor(ctx context.Context, request agent.DoctorRequest) agent.DoctorReport {
	report := d.legacy.Doctor(ctx, backend.DoctorRequest{Workspace: request.Workspace, Tool: "codex", Runtime: request.Target})
	converted := agent.DoctorReport{Driver: d.Name(), Ready: report.Ready}
	for _, item := range report.Diagnostics {
		converted.Diagnostics = append(converted.Diagnostics, agent.Diagnostic{Name: item.Name, Status: string(item.Status), Message: item.Message})
	}
	return converted
}

func (d *Driver) Start(ctx context.Context, envelope agent.AttemptEnvelope) (*agent.ExecutionHandle, error) {
	if err := agent.ValidateAttemptEnvelope(envelope); err != nil {
		return nil, err
	}
	prompt := envelope.Prompt
	if prompt == "" {
		prompt = envelope.Task
	}
	handle, err := d.legacy.Start(ctx, backend.AgentExecutionSpec{
		RunID: envelope.Identity.RunID, NodeID: envelope.Identity.NodeID, Attempt: envelope.Identity.Attempt,
		Workspace: envelope.Workspace, Tool: "codex", Runtime: "local", Instructions: prompt,
		RequiredSkills: append([]string(nil), envelope.Context.RequiredSkills...),
		ResultContract: backend.ResultContract{Schema: append(json.RawMessage(nil), envelope.ResultContract.Schema...), MaxBytes: envelope.ResultContract.MaxBytes},
	})
	if handle == nil {
		return nil, err
	}
	return &agent.ExecutionHandle{Driver: d.Name(), Target: "local", SchemaVersion: handle.SchemaVersion, ID: handle.ID, Data: append(json.RawMessage(nil), handle.Data...)}, err
}

func (d *Driver) Observe(ctx context.Context, handle agent.ExecutionHandle) (*agent.ExecutionObservation, error) {
	legacy, err := d.legacy.Observe(ctx, toLegacyHandle(handle))
	if legacy == nil {
		return nil, err
	}
	observation := &agent.ExecutionObservation{State: agent.ObservationState(legacy.State), Diagnostic: legacy.Diagnostic}
	if legacy.Result != nil {
		observation.Result = toAgentResult(*legacy.Result)
	}
	if err == nil {
		observation.Events = normalizedEvents(*observation)
	}
	return observation, err
}

func (d *Driver) Output(ctx context.Context, handle agent.ExecutionHandle, lines int) (string, error) {
	return d.legacy.Output(ctx, toLegacyHandle(handle), lines)
}

func (d *Driver) Cancel(ctx context.Context, handle agent.ExecutionHandle) (*agent.CancelResult, error) {
	result, err := d.legacy.Cancel(ctx, toLegacyHandle(handle))
	if result == nil {
		return nil, err
	}
	return &agent.CancelResult{State: agent.CancelState(result.State), Diagnostic: result.Diagnostic}, err
}

func toLegacyHandle(handle agent.ExecutionHandle) backend.ExecutionHandle {
	return backend.ExecutionHandle{Driver: "direct", Target: "local", Backend: "direct", SchemaVersion: handle.SchemaVersion, ID: handle.ID, Data: append(json.RawMessage(nil), handle.Data...)}
}

func toAgentResult(result backend.AgentResult) *agent.AgentResult {
	questions := make([]agent.InputQuestion, len(result.Questions))
	for index, question := range result.Questions {
		questions[index] = agent.InputQuestion{ID: question.ID, Prompt: question.Prompt, Choices: append([]string(nil), question.Choices...), Required: question.Required}
	}
	return &agent.AgentResult{Status: result.Status, Summary: result.Summary, Artifacts: result.Artifacts, Warnings: result.Warnings, Checks: result.Checks, Questions: questions,
		Usage: agent.Usage{InputTokensEstimated: result.Usage.InputTokensEstimated, OutputTokensEstimated: result.Usage.OutputTokensEstimated}}
}

func normalizedEvents(observation agent.ExecutionObservation) []agent.DriverEvent {
	switch observation.State {
	case agent.ObservationActive:
		return []agent.DriverEvent{{Type: agent.EventAttemptProgress, Message: "Codex execution is active"}}
	case agent.ObservationWaitingInput:
		return []agent.DriverEvent{{Type: agent.EventAttemptNeedsInput, Diagnostic: observation.Diagnostic}}
	case agent.ObservationResultPending:
		return []agent.DriverEvent{{Type: agent.EventAttemptResultPending, Diagnostic: observation.Diagnostic}}
	case agent.ObservationTerminal:
		if observation.Result != nil && observation.Result.Status == "needs_input" {
			return []agent.DriverEvent{{Type: agent.EventAttemptNeedsInput, Message: observation.Result.Summary, Result: observation.Result}}
		}
		return []agent.DriverEvent{{Type: agent.EventAttemptCompleted, Result: observation.Result}}
	case agent.ObservationLost:
		return []agent.DriverEvent{{Type: agent.EventAttemptDiagnostic, Diagnostic: observation.Diagnostic}}
	default:
		panic(fmt.Sprintf("unsupported Codex observation state %q", observation.State))
	}
}

func RunSupervisor(configPath string) int { return directcli.RunSupervisor(configPath) }
