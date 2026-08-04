package ccpanes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type Runner interface {
	Run(context.Context, string, ...string) (commandResult, error)
}

type ExecRunner struct {
	PrefixArgs []string
}

func (r ExecRunner) Run(ctx context.Context, path string, args ...string) (commandResult, error) {
	command := exec.CommandContext(ctx, path, append(r.PrefixArgs, args...)...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := commandResult{stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return result, err
	}
	result.exitCode = exitErr.ExitCode()
	return result, nil
}

type Client struct {
	path   string
	runner Runner
}

const waitPollTimeoutMS = 5000

func NewClient(path string) *Client { return &Client{path: path, runner: ExecRunner{}} }

func NewClientWithRunner(path string, runner Runner) *Client {
	return &Client{path: path, runner: runner}
}

func (c *Client) invoke(ctx context.Context, category string, args ...string) (any, error) {
	fullArgs := append([]string{"--json", "--release"}, args...)
	result, err := c.runner.Run(ctx, c.path, fullArgs...)
	if err != nil {
		return nil, fmt.Errorf("cc-panes-ctl %s could not start: %w", category, err)
	}
	if result.exitCode != 0 {
		if strings.HasPrefix(category, "call wait_for_session") {
			diagnostic := strings.TrimSpace(string(result.stderr))
			if len(diagnostic) > 512 {
				diagnostic = diagnostic[:512]
			}
			if diagnostic != "" {
				return nil, fmt.Errorf("cc-panes-ctl %s failed with exit code %d: %s", category, result.exitCode, diagnostic)
			}
		}
		return nil, fmt.Errorf("cc-panes-ctl %s failed with exit code %d", category, result.exitCode)
	}
	value, err := decodeStructured(result.stdout)
	if err != nil {
		return nil, fmt.Errorf("decode cc-panes-ctl %s response: %w", category, err)
	}
	return value, nil
}

func (c *Client) Status(ctx context.Context) (any, error) {
	return c.invoke(ctx, "status", "status")
}

func (c *Client) Call(ctx context.Context, tool string, payload any) (any, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s payload: %w", tool, err)
	}
	return c.invoke(ctx, "call "+tool, "call", tool, "--json", string(data))
}

func (c *Client) LaunchTask(ctx context.Context, project, tool, runtimeKind, prompt, title, profileID string) (launchResult, error) {
	value, err := c.Call(ctx, "launch_task", map[string]any{
		"projectPath": project,
		"cliTool":     tool,
		"runtimeKind": runtimeKind,
		"prompt":      prompt,
		"title":       title,
		"profileId":   profileID,
	})
	if err != nil {
		return launchResult{}, err
	}
	result := launchResult{
		LaunchID:  findString(value, "launchId", "launchID", "id"),
		SessionID: findString(value, "sessionId", "sessionID"),
		Metadata:  stringFields(value),
	}
	if result.SessionID == "" {
		return launchResult{}, errorsf("launch_task response did not contain a sessionId")
	}
	return result, nil
}

func (c *Client) WaitForSession(ctx context.Context, sessionID string) (string, error) {
	waitFor := []string{"idle", "waitingInput", "error", "exited"}
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		value, err := c.Call(ctx, "wait_for_session", map[string]any{
			"sessionId": sessionID,
			"waitFor":   waitFor,
			"timeoutMs": waitPollTimeoutMS,
		})
		if err != nil {
			return "", err
		}
		status := findString(value, "finalStatus", "status", "state", "sessionStatus")
		switch strings.ToLower(status) {
		case "idle", "exited", "waitinginput", "error":
			return status, nil
		case "initializing", "active", "thinking", "toolrunning", "compacting":
			continue
		case "":
			return "", errorsf("wait response did not contain finalStatus")
		default:
			return "", errorsf("wait response contained unsupported session state %q", status)
		}
	}
}

func (c *Client) ObserveSession(ctx context.Context, sessionID string) (string, error) {
	value, err := c.Call(ctx, "wait_for_session", map[string]any{
		"sessionId": sessionID,
		"waitFor":   []string{"idle", "waitingInput", "error", "exited"},
		"timeoutMs": 1000,
	})
	if err != nil {
		return "", err
	}
	status := findString(value, "finalStatus", "status", "state", "sessionStatus")
	switch strings.ToLower(status) {
	case "initializing", "active", "thinking", "toolrunning", "compacting", "idle", "waitinginput", "error", "exited":
		return status, nil
	case "":
		return "", errorsf("wait response did not contain finalStatus")
	default:
		return "", errorsf("wait response contained unsupported session state %q", status)
	}
}

func (c *Client) QueryBinding(ctx context.Context, project, sessionID, bindingID string) (*taskBinding, error) {
	binding, err := c.queryBinding(ctx, map[string]any{"projectPath": project, "sessionId": sessionID, "limit": 20}, bindingID)
	if err != nil {
		return nil, err
	}
	if binding != nil {
		return binding, nil
	}
	return c.queryBinding(ctx, map[string]any{"projectPath": project, "limit": 50}, bindingID)
}

func (c *Client) queryBinding(ctx context.Context, payload map[string]any, bindingID string) (*taskBinding, error) {
	value, err := c.Call(ctx, "query_task_bindings", payload)
	if err != nil {
		return nil, err
	}
	items := findArray(value, "items")
	for _, item := range items {
		data, _ := json.Marshal(item)
		var binding taskBinding
		if json.Unmarshal(data, &binding) == nil && binding.ID == bindingID {
			return &binding, nil
		}
	}
	return nil, nil
}

func (c *Client) ReadOutput(ctx context.Context, sessionID string, lines int) (string, error) {
	value, err := c.invoke(ctx, "sessions read", "sessions", "read", "--lines", strconv.Itoa(lines), sessionID)
	if err != nil {
		return "", err
	}
	if text := findString(value, "output", "text", "content"); text != "" {
		return text, nil
	}
	data, _ := json.Marshal(value)
	return string(data), nil
}

func (c *Client) Kill(ctx context.Context, sessionID string) error {
	value, err := c.Call(ctx, "kill_session", map[string]any{"sessionId": sessionID})
	if err != nil {
		return err
	}
	response, ok := value.(map[string]any)
	if !ok {
		return errorsf("kill_session response was not an object")
	}
	success, ok := response["success"].(bool)
	if !ok {
		return errorsf("kill_session response did not contain a boolean success field")
	}
	if !success {
		return errorsf("kill_session response reported success=false")
	}
	if returned, exists := response["sessionId"]; exists {
		returnedSessionID, ok := returned.(string)
		if !ok || returnedSessionID == "" {
			return errorsf("kill_session response contained an invalid sessionId")
		}
		if returnedSessionID != sessionID {
			return errorsf("kill_session response sessionId %q did not match requested session %q", returnedSessionID, sessionID)
		}
	}
	return nil
}

func decodeStructured(data []byte) (any, error) {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return unwrap(value)
}

func unwrap(value any) (any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return value, nil
	}
	if failed, _ := object["isError"].(bool); failed {
		return nil, errorsf("MCP call returned an error envelope")
	}
	if content, ok := object["content"].([]any); ok {
		for _, block := range content {
			if typed, ok := block.(map[string]any); ok {
				if text, ok := typed["text"].(string); ok {
					var nested any
					if json.Unmarshal([]byte(text), &nested) == nil {
						return unwrap(nested)
					}
				}
			}
		}
	}
	if result, exists := object["result"]; exists {
		return unwrap(result)
	}
	return value, nil
}

func findString(value any, keys ...string) string {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range keys {
			if text, ok := typed[key].(string); ok && text != "" {
				return text
			}
		}
		for _, nested := range typed {
			if found := findString(nested, keys...); found != "" {
				return found
			}
		}
	case []any:
		for _, nested := range typed {
			if found := findString(nested, keys...); found != "" {
				return found
			}
		}
	}
	return ""
}

func findArray(value any, key string) []any {
	if object, ok := value.(map[string]any); ok {
		if items, ok := object[key].([]any); ok {
			return items
		}
		for _, nested := range object {
			if items := findArray(nested, key); items != nil {
				return items
			}
		}
	}
	return nil
}

func stringFields(value any) map[string]string {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	fields := make(map[string]string)
	for key, value := range object {
		if text, ok := value.(string); ok && text != "" {
			fields[key] = text
		}
	}
	return fields
}

func containsProject(value any, project string) bool {
	target := filepath.Clean(project)
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if strings.EqualFold(key, "path") || strings.EqualFold(key, "projectPath") {
				if text, ok := nested.(string); ok && strings.EqualFold(filepath.Clean(text), target) {
					return true
				}
			}
			if containsProject(nested, project) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsProject(nested, project) {
				return true
			}
		}
	}
	return false
}

func errorsf(format string, args ...any) error { return fmt.Errorf(format, args...) }
