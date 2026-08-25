package codexprocess

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

const appServerProtocol = "codex-app-server/v2"

type appServerClient struct {
	command *exec.Cmd
	stdin   io.WriteCloser

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan appServerResponse
	done    chan error
	stderr  *boundedWriter
}

type boundedWriter struct {
	mu    sync.Mutex
	data  bytes.Buffer
	limit int64
}

func (w *boundedWriter) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written := len(value)
	remaining := w.limit - int64(w.data.Len())
	if remaining > 0 {
		if int64(len(value)) > remaining {
			value = value[:remaining]
		}
		_, _ = w.data.Write(value)
	}
	return written, nil
}

func (w *boundedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.String()
}

type appServerResponse struct {
	Result json.RawMessage
	Error  *appServerError
}

type appServerError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type appServerEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *appServerError `json:"error,omitempty"`
}

func startAppServer(ctx context.Context, executable, cwd string, stderrLimit int64) (*appServerClient, error) {
	command := exec.Command(executable, "app-server", "--listen", "stdio://")
	command.Dir = cwd
	configureBackgroundCommand(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	stderr := &boundedWriter{limit: stderrLimit}
	command.Stderr = stderr
	client := &appServerClient{command: command, stdin: stdin, nextID: 1, pending: make(map[int64]chan appServerResponse), done: make(chan error, 1), stderr: stderr}
	if err := command.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	go client.read(stdout)
	go func() {
		err := command.Wait()
		client.failPending(fmt.Errorf("Codex app-server exited: %w", err))
		client.done <- err
	}()
	var initialized struct {
		UserAgent string `json:"userAgent"`
	}
	if err := client.request(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "fishyume", "title": "Fishyume AgentSession Driver", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true, "requestAttestation": false},
	}, &initialized); err != nil {
		_ = client.close()
		return nil, fmt.Errorf("initialize Codex app-server: %w", err)
	}
	if err := client.notify("initialized", nil); err != nil {
		_ = client.close()
		return nil, err
	}
	return client, nil
}

func (c *appServerClient) read(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var envelope appServerEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			c.failPending(fmt.Errorf("decode Codex app-server message: %w", err))
			continue
		}
		if len(envelope.ID) == 0 {
			continue
		}
		if envelope.Method != "" {
			_ = c.write(map[string]any{"id": json.RawMessage(envelope.ID), "error": map[string]any{"code": -32601, "message": "Fishyume Session Driver does not accept server requests"}})
			continue
		}
		var id int64
		if err := json.Unmarshal(envelope.ID, &id); err != nil {
			continue
		}
		c.mu.Lock()
		response := c.pending[id]
		delete(c.pending, id)
		c.mu.Unlock()
		if response != nil {
			response <- appServerResponse{Result: envelope.Result, Error: envelope.Error}
		}
	}
	if err := scanner.Err(); err != nil {
		c.failPending(fmt.Errorf("read Codex app-server output: %w", err))
	}
}

func (c *appServerClient) request(ctx context.Context, method string, params any, result any) error {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	response := make(chan appServerResponse, 1)
	c.pending[id] = response
	c.mu.Unlock()
	if err := c.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case value := <-response:
		if value.Error != nil {
			return fmt.Errorf("Codex app-server %s failed (%d): %s", method, value.Error.Code, value.Error.Message)
		}
		if result == nil || len(value.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(value.Result, result); err != nil {
			return fmt.Errorf("decode Codex app-server %s response: %w", method, err)
		}
		return nil
	}
}

func (c *appServerClient) notify(method string, params any) error {
	message := map[string]any{"method": method}
	if params != nil {
		message["params"] = params
	}
	return c.write(message)
}

func (c *appServerClient) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write Codex app-server request: %w", err)
	}
	return nil
}

func (c *appServerClient) failPending(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[int64]chan appServerResponse)
	c.mu.Unlock()
	for _, response := range pending {
		response <- appServerResponse{Error: &appServerError{Code: -32099, Message: err.Error()}}
	}
}

func (c *appServerClient) close() error {
	if c == nil || c.command == nil || c.command.Process == nil {
		return nil
	}
	_ = c.stdin.Close()
	select {
	case <-c.done:
		return nil
	case <-time.After(2 * time.Second):
	}
	if err := c.command.Process.Kill(); err != nil {
		return err
	}
	select {
	case <-c.done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("Codex app-server did not exit after termination")
	}
}

type appThreadResponse struct {
	Thread         appThread       `json:"thread"`
	Model          string          `json:"model"`
	ModelProvider  string          `json:"modelProvider"`
	CWD            string          `json:"cwd"`
	ApprovalPolicy json.RawMessage `json:"approvalPolicy"`
	Sandbox        appSandbox      `json:"sandbox"`
}

type appSandbox struct {
	Type string `json:"type"`
}

type appThread struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"sessionId"`
	CWD           string    `json:"cwd"`
	Ephemeral     bool      `json:"ephemeral"`
	ModelProvider string    `json:"modelProvider"`
	Turns         []appTurn `json:"turns"`
}

type appTurn struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Items  []appThreadItem `json:"items"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type appThreadItem struct {
	Type     string `json:"type"`
	ClientID string `json:"clientId,omitempty"`
	Text     string `json:"text,omitempty"`
}

type appTurnStartResponse struct {
	Turn appTurn `json:"turn"`
}

type appThreadReadResponse struct {
	Thread appThread `json:"thread"`
}
