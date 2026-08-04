package ccpanes

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"wf.local/wf-engine/internal/backend"
)

type Backend struct {
	client       *Client
	profileID    string
	discoveryErr error
}

func New() (*Backend, error) {
	path, err := Discover()
	if err != nil {
		return nil, err
	}
	profileID, err := ResolveProfileID()
	if err != nil {
		return nil, err
	}
	return &Backend{client: NewClient(path), profileID: profileID}, nil
}

func NewWithClient(client *Client) *Backend {
	profileID, err := ResolveProfileID()
	return &Backend{client: client, profileID: profileID, discoveryErr: err}
}

func NewUnavailable(err error) *Backend { return &Backend{discoveryErr: err} }

func (b *Backend) Name() string { return "ccpanes" }

func (b *Backend) Doctor(ctx context.Context) error {
	if b.discoveryErr != nil {
		return b.discoveryErr
	}
	value, err := b.client.Status(ctx)
	if err != nil {
		return err
	}
	instances := findArray(value, "instances")
	for _, item := range instances {
		object, _ := item.(map[string]any)
		if !strings.EqualFold(findString(object, "instance"), "release") {
			continue
		}
		orchestrator, _ := object["orchestrator"].(map[string]any)
		daemon, _ := object["daemon"].(map[string]any)
		if findString(orchestrator, "lifecycle") == "ready" && findString(daemon, "lifecycle") == "ready" {
			return nil
		}
		return fmt.Errorf("CC-Panes release orchestrator or daemon is not ready")
	}
	return fmt.Errorf("CC-Panes release status was not present")
}

func (b *Backend) DoctorProject(ctx context.Context, project string) error {
	if b.discoveryErr != nil {
		return b.discoveryErr
	}
	value, err := b.client.Call(ctx, "list_projects", map[string]any{})
	if err != nil {
		return err
	}
	if !containsProject(value, project) {
		return fmt.Errorf("project %q is not registered in CC-Panes", filepath.Clean(project))
	}
	return nil
}

func (b *Backend) Launch(ctx context.Context, spec backend.LaunchSpec) (*backend.Session, error) {
	if b.discoveryErr != nil {
		return nil, b.discoveryErr
	}
	title := "Fishyume " + spec.RunID
	created, err := b.client.Call(ctx, "create_task_binding", map[string]any{
		"title": title, "projectPath": spec.Project, "prompt": spec.Prompt,
		"role": "task", "cliTool": spec.Tool,
		"metadata": map[string]any{"runId": spec.RunID, "workflowEngine": "fishyume-m2.1.1"},
	})
	if err != nil {
		return nil, err
	}
	bindingID := findString(created, "id", "bindingId")
	if bindingID == "" {
		return nil, fmt.Errorf("create_task_binding response did not contain an id")
	}
	launched, err := b.client.LaunchTask(ctx, spec.Project, spec.Tool, spec.Runtime, completionPrompt(spec.Prompt, bindingID), title, b.profileID)
	if err != nil {
		return nil, fmt.Errorf("CC-Panes rejected launch_task with profile %q: %w; verify that %s names an existing non-interactive profile allowed for %s/%s Fishyume workers", b.profileID, err, ProfileIDEnv, spec.Tool, spec.Runtime)
	}
	metadata := launched.Metadata
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["bindingId"], metadata["launchId"], metadata["project"] = bindingID, launched.LaunchID, spec.Project
	session := &backend.Session{ID: launched.SessionID, Metadata: metadata}
	_, err = b.client.Call(ctx, "update_task_binding", map[string]any{
		"id": bindingID, "status": "running", "progress": 10,
		"sessionId": launched.SessionID,
		"metadata":  map[string]any{"runId": spec.RunID, "launchId": launched.LaunchID},
	})
	if err != nil {
		return session, err
	}
	return session, nil
}

func (b *Backend) Wait(ctx context.Context, session backend.Session) (*backend.BackendResult, error) {
	if b.discoveryErr != nil {
		return nil, b.discoveryErr
	}
	state, err := b.client.WaitForSession(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	binding, err := b.client.QueryBinding(ctx, session.Metadata["project"], session.ID, session.Metadata["bindingId"])
	if err != nil {
		return nil, err
	}
	if binding != nil {
		if result := terminalBindingResult(binding); result != nil {
			return result, nil
		}
	}
	switch strings.ToLower(state) {
	case "waitinginput", "waiting_input":
		return &backend.BackendResult{Status: "waiting_input", Summary: "agent session is waiting for input before a terminal TaskBinding"}, nil
	case "idle":
		return &backend.BackendResult{Status: "completion_missing", Summary: "agent session is idle without a terminal TaskBinding"}, nil
	case "exited":
		return &backend.BackendResult{Status: "indeterminate", Summary: "agent session exited without a terminal TaskBinding"}, nil
	case "error":
		return &backend.BackendResult{Status: "failed", Summary: "agent session reported a failure before a terminal TaskBinding"}, nil
	default:
		return &backend.BackendResult{Status: "indeterminate", Summary: "session state was not backed by a terminal TaskBinding"}, nil
	}
}

func (b *Backend) Reconcile(ctx context.Context, session backend.Session) (*backend.Observation, error) {
	if b.discoveryErr != nil {
		return nil, b.discoveryErr
	}
	binding, err := b.client.QueryBinding(ctx, session.Metadata["project"], session.ID, session.Metadata["bindingId"])
	if err != nil {
		return nil, err
	}
	if binding != nil {
		if result := terminalBindingResult(binding); result != nil {
			return &backend.Observation{State: backend.ObservationTerminal, Result: result}, nil
		}
	}
	state, err := b.client.ObserveSession(ctx, session.ID)
	if err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "not found") || strings.Contains(message, "notfound") || strings.Contains(message, "does not exist") || strings.Contains(message, "不存在") {
			return &backend.Observation{State: backend.ObservationLost}, nil
		}
		return nil, err
	}
	switch strings.ToLower(state) {
	case "initializing", "active", "thinking", "toolrunning", "compacting":
		return &backend.Observation{State: backend.ObservationActive}, nil
	case "waitinginput":
		return &backend.Observation{State: backend.ObservationWaitingInput}, nil
	case "idle":
		return &backend.Observation{State: backend.ObservationCompletionMissing}, nil
	case "exited":
		return &backend.Observation{State: backend.ObservationExited}, nil
	case "error":
		return &backend.Observation{State: backend.ObservationError}, nil
	default:
		return nil, fmt.Errorf("unsupported reconciled session state %q", state)
	}
}

func (b *Backend) Output(ctx context.Context, session backend.Session, lines int) (string, error) {
	if b.discoveryErr != nil {
		return "", b.discoveryErr
	}
	return b.client.ReadOutput(ctx, session.ID, lines)
}

func (b *Backend) Cancel(ctx context.Context, session backend.Session) error {
	if b.discoveryErr != nil {
		return b.discoveryErr
	}
	return b.client.Kill(ctx, session.ID)
}

func completionPrompt(userPrompt, bindingID string) string {
	return userPrompt + "\n\n--- fishyume engine-owned completion contract ---\n" +
		"Updating TaskBinding " + bindingID + " is mandatory before you finish. " +
		"Call update_task_binding with status completed or failed, progress 100, an appropriate exitCode, " +
		"and a concise completionSummary. Metadata should include artifacts, warnings, checks, and estimated usage when available. " +
		"Do not modify workflow-engine plan or architecture files; this completion contract requests only the binding update.\n" +
		"--- end fishyume completion contract ---"
}

func resultFromBinding(status string, binding *taskBinding) *backend.BackendResult {
	result := &backend.BackendResult{Status: status, Summary: binding.CompletionSummary}
	var ok bool
	if result.Artifacts, ok = strictStringSlice(binding.Metadata["artifacts"]); !ok {
		return malformedBindingResult("artifacts")
	}
	if result.Warnings, ok = strictStringSlice(binding.Metadata["warnings"]); !ok {
		return malformedBindingResult("warnings")
	}
	if result.Checks, ok = strictStringSlice(binding.Metadata["checks"]); !ok {
		return malformedBindingResult("checks")
	}
	if usage, ok := binding.Metadata["usage"].(map[string]any); ok {
		input, inputOK := strictNonNegativeInt(usage["inputTokensEstimated"])
		output, outputOK := strictNonNegativeInt(usage["outputTokensEstimated"])
		if !inputOK || !outputOK {
			return malformedBindingResult("usage")
		}
		result.Usage.InputTokensEstimated, result.Usage.OutputTokensEstimated = input, output
	} else if binding.Metadata["usage"] != nil {
		return malformedBindingResult("usage")
	}
	return result
}

func terminalBindingResult(binding *taskBinding) *backend.BackendResult {
	switch strings.ToLower(binding.Status) {
	case "completed":
		if binding.ExitCode != nil && *binding.ExitCode != 0 {
			return &backend.BackendResult{Status: "failed", Summary: binding.CompletionSummary}
		}
		if strings.TrimSpace(binding.CompletionSummary) == "" {
			return &backend.BackendResult{Status: "invalid_result", Summary: "completed TaskBinding lacked completionSummary"}
		}
		return resultFromBinding("succeeded", binding)
	case "failed":
		return resultFromBinding("failed", binding)
	default:
		return nil
	}
}

func strictStringSlice(value any) ([]string, bool) {
	if value == nil {
		return nil, true
	}
	items, _ := value.([]any)
	if items == nil {
		return nil, false
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, false
		}
		result = append(result, text)
	}
	return result, true
}

func strictNonNegativeInt(value any) (int, bool) {
	if value == nil {
		return 0, true
	}
	number, ok := value.(float64)
	if !ok || number < 0 || number != float64(int(number)) {
		return 0, false
	}
	return int(number), true
}

func malformedBindingResult(field string) *backend.BackendResult {
	return &backend.BackendResult{Status: "invalid_result", Summary: "completed TaskBinding has malformed " + field + " metadata"}
}
