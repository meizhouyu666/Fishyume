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

func (c *Client) Launch(ctx context.Context, project, tool, runtimeKind, prompt, title string) (launchResult, error) {
	value, err := c.invoke(ctx, "launch", "launch", "--prompt", prompt, "--cli", tool,
		"--runtime", runtimeKind, "--title", title, project)
	if err != nil {
		return launchResult{}, err
	}
	result := launchResult{LaunchID: findString(value, "launchId", "launchID", "id"), SessionID: findString(value, "sessionId", "sessionID")}
	if result.SessionID == "" {
		return launchResult{}, errorsf("launch response did not contain a sessionId")
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

func (c *Client) QueryBinding(ctx context.Context, project, sessionID, bindingID string) (*taskBinding, error) {
	value, err := c.Call(ctx, "query_task_bindings", map[string]any{"projectPath": project, "sessionId": sessionID, "limit": 20})
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
	_, err := c.invoke(ctx, "sessions kill", "sessions", "kill", sessionID)
	return err
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
