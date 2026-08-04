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
	discoveryErr error
}

func New() (*Backend, error) {
	path, err := Discover()
	if err != nil {
		return nil, err
	}
	return &Backend{client: NewClient(path)}, nil
}

func NewWithClient(client *Client) *Backend { return &Backend{client: client} }

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
	title := "wf " + spec.RunID
	created, err := b.client.Call(ctx, "create_task_binding", map[string]any{
		"title": title, "projectPath": spec.Project, "prompt": spec.Prompt,
		"role": "task", "cliTool": spec.Tool,
		"metadata": map[string]any{"runId": spec.RunID, "workflowEngine": "wf-m1"},
	})
	if err != nil {
		return nil, err
	}
	bindingID := findString(created, "id", "bindingId")
	if bindingID == "" {
		return nil, fmt.Errorf("create_task_binding response did not contain an id")
	}
	launched, err := b.client.Launch(ctx, spec.Project, spec.Tool, spec.Runtime, completionPrompt(spec.Prompt, bindingID), title)
	if err != nil {
		return nil, err
	}
	_, err = b.client.Call(ctx, "update_task_binding", map[string]any{
		"id": bindingID, "status": "running", "progress": 10,
		"sessionId": launched.SessionID,
		"metadata":  map[string]any{"runId": spec.RunID, "launchId": launched.LaunchID},
	})
	if err != nil {
		return nil, err
	}
	return &backend.Session{ID: launched.SessionID, Metadata: map[string]string{
		"bindingId": bindingID, "launchId": launched.LaunchID, "project": spec.Project,
	}}, nil
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
		switch strings.ToLower(binding.Status) {
		case "completed":
			if binding.ExitCode != nil && *binding.ExitCode != 0 {
				return &backend.BackendResult{Status: "failed", Summary: binding.CompletionSummary}, nil
			}
			if strings.TrimSpace(binding.CompletionSummary) == "" {
				return &backend.BackendResult{Status: "indeterminate", Summary: "completed TaskBinding lacked completionSummary"}, nil
			}
			return resultFromBinding("succeeded", binding), nil
		case "failed":
			return resultFromBinding("failed", binding), nil
		}
	}
	switch strings.ToLower(state) {
	case "waitinginput", "waiting_input":
		return &backend.BackendResult{Status: "blocked", Summary: "agent session is waiting for input before a terminal TaskBinding"}, nil
	case "idle", "exited":
		return &backend.BackendResult{Status: "indeterminate", Summary: "agent session ended without a terminal TaskBinding"}, nil
	case "error":
		return &backend.BackendResult{Status: "failed", Summary: "agent session reported a failure before a terminal TaskBinding"}, nil
	default:
		return &backend.BackendResult{Status: "indeterminate", Summary: "session state was not backed by a terminal TaskBinding"}, nil
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
	return userPrompt + "\n\n--- wf engine-owned completion contract ---\n" +
		"Updating TaskBinding " + bindingID + " is mandatory before you finish. " +
		"Call update_task_binding with status completed or failed, progress 100, an appropriate exitCode, " +
		"and a concise completionSummary. Metadata should include artifacts, warnings, checks, and estimated usage when available. " +
		"Do not modify workflow-engine plan or architecture files; this completion contract requests only the binding update.\n" +
		"--- end wf completion contract ---"
}

func resultFromBinding(status string, binding *taskBinding) *backend.BackendResult {
	result := &backend.BackendResult{Status: status, Summary: binding.CompletionSummary}
	result.Artifacts = stringSlice(binding.Metadata["artifacts"])
	result.Warnings = stringSlice(binding.Metadata["warnings"])
	result.Checks = stringSlice(binding.Metadata["checks"])
	if usage, ok := binding.Metadata["usage"].(map[string]any); ok {
		result.Usage.InputTokensEstimated = intValue(usage["inputTokensEstimated"])
		result.Usage.OutputTokensEstimated = intValue(usage["outputTokensEstimated"])
	}
	return result
}

func stringSlice(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func intValue(value any) int {
	if number, ok := value.(float64); ok {
		return int(number)
	}
	return 0
}
