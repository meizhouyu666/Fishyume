package codex

import (
	"context"
	"encoding/json"
	"fmt"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/driver/codexprocess"
	"wf.local/wf-engine/internal/explorationdriver"
	"wf.local/wf-engine/internal/sessiondriver"
)

type Config = codexprocess.Config

type Driver struct {
	executor *codexprocess.Backend
}

func New(config Config) *Driver { return &Driver{executor: codexprocess.New(config)} }

func (d *Driver) Exploration() explorationdriver.Driver {
	return codexprocess.NewExplorationAdapter(d.executor)
}

func (d *Driver) Session() sessiondriver.Driver {
	return codexprocess.NewSessionAdapter(d.executor)
}

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
	report := d.executor.Doctor(ctx, backend.DoctorRequest{Workspace: request.Workspace, Tool: "codex", Runtime: request.Target})
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
	if envelope.Target != "local" {
		return nil, fmt.Errorf("Codex Driver does not support target %q", envelope.Target)
	}
	prompt := envelope.Prompt
	if prompt == "" {
		prompt = envelope.Task
	}
	handle, err := d.executor.Start(ctx, backend.AgentExecutionSpec{
		RunID: envelope.Identity.RunID, NodeID: envelope.Identity.NodeID, Attempt: envelope.Identity.Attempt,
		Workspace: envelope.Workspace, Tool: "codex", Runtime: envelope.Target, Instructions: prompt,
		Model:          selectedModel(envelope),
		RequiredSkills: append([]string(nil), envelope.Context.RequiredSkills...),
		ResultContract: backend.ResultContract{Schema: append(json.RawMessage(nil), envelope.ResultContract.Schema...), MaxBytes: envelope.ResultContract.MaxBytes},
	})
	if handle == nil {
		return nil, err
	}
	return &agent.ExecutionHandle{Driver: d.Name(), Target: envelope.Target, SchemaVersion: handle.SchemaVersion, ID: handle.ID, Data: append(json.RawMessage(nil), handle.Data...)}, err
}

func selectedModel(envelope agent.AttemptEnvelope) string {
	if envelope.RoutingDecision == nil {
		return ""
	}
	return envelope.RoutingDecision.Selected.Model
}

func (d *Driver) Observe(ctx context.Context, handle agent.ExecutionHandle) (*agent.ExecutionObservation, error) {
	legacy, err := d.executor.Observe(ctx, toProcessHandle(handle))
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
	return d.executor.Output(ctx, toProcessHandle(handle), lines)
}

func (d *Driver) Cancel(ctx context.Context, handle agent.ExecutionHandle) (*agent.CancelResult, error) {
	result, err := d.executor.Cancel(ctx, toProcessHandle(handle))
	if result == nil {
		return nil, err
	}
	return &agent.CancelResult{State: agent.CancelState(result.State), Diagnostic: result.Diagnostic}, err
}

func toProcessHandle(handle agent.ExecutionHandle) backend.ExecutionHandle {
	return backend.ExecutionHandle{Driver: "direct", Target: "local", Backend: "direct", SchemaVersion: handle.SchemaVersion, ID: handle.ID, Data: append(json.RawMessage(nil), handle.Data...)}
}

func toAgentResult(result backend.AgentResult) *agent.AgentResult {
	questions := make([]agent.InputQuestion, len(result.Questions))
	for index, question := range result.Questions {
		questions[index] = agent.InputQuestion{ID: question.ID, Prompt: question.Prompt, Choices: append([]string(nil), question.Choices...), Required: question.Required}
	}
	return &agent.AgentResult{Status: result.Status, Summary: result.Summary, Artifacts: result.Artifacts, Warnings: result.Warnings, Checks: result.Checks, Questions: questions,
		Usage: agent.Usage{InputTokensEstimated: result.Usage.InputTokensEstimated, OutputTokensEstimated: result.Usage.OutputTokensEstimated}, SideEffectStatus: result.SideEffectStatus}
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

func RunSupervisor(configPath string) int { return codexprocess.RunSupervisor(configPath) }
