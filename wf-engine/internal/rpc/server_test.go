package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/store"
)

type fakeBackend struct {
	result backend.BackendResult
	wait   chan struct{}
}

func (f *fakeBackend) Name() string                 { return "ccpanes" }
func (f *fakeBackend) Doctor(context.Context) error { return nil }
func (f *fakeBackend) Launch(context.Context, backend.LaunchSpec) (*backend.Session, error) {
	return &backend.Session{ID: "session-1"}, nil
}
func (f *fakeBackend) Wait(context.Context, backend.Session) (*backend.BackendResult, error) {
	if f.wait != nil {
		<-f.wait
	}
	result := f.result
	return &result, nil
}
func (f *fakeBackend) Output(context.Context, backend.Session, int) (string, error) { return "", nil }
func (f *fakeBackend) Cancel(context.Context, backend.Session) error                { return nil }

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *safeBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

func serve(t *testing.T, input string, backendImpl *fakeBackend) *safeBuffer {
	t.Helper()
	output := &safeBuffer{}
	server := NewServer(strings.NewReader(input), output, run.NewService(backendImpl, store.New(t.TempDir())))
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	return output
}

func request(id int, method string, params any) string {
	message := map[string]any{"jsonrpc": "2.0", "protocolVersion": 1, "id": id, "method": method}
	if params != nil {
		message["params"] = params
	}
	data, _ := json.Marshal(message)
	return string(data) + "\n"
}

func TestHandshake(t *testing.T) {
	output := serve(t, request(1, "engine.hello", nil), &fakeBackend{})
	if !strings.Contains(output.String(), `"engineVersion":"0.1.0"`) || !strings.Contains(output.String(), `"supportedBackends":["ccpanes"]`) {
		t.Fatalf("unexpected response: %s", output.String())
	}
}

func TestUnknownMethod(t *testing.T) {
	output := serve(t, request(1, "unknown", nil), &fakeBackend{})
	if !strings.Contains(output.String(), `"code":-32601`) {
		t.Fatalf("unexpected response: %s", output.String())
	}
}

func TestMalformedLineDoesNotStopServer(t *testing.T) {
	output := serve(t, "{bad json}\n"+request(2, "engine.hello", nil), &fakeBackend{})
	if !strings.Contains(output.String(), `"code":-32700`) || !strings.Contains(output.String(), `"id":2`) {
		t.Fatalf("unexpected response: %s", output.String())
	}
}

func TestUnsupportedProtocolVersion(t *testing.T) {
	input := `{"jsonrpc":"2.0","protocolVersion":99,"id":1,"method":"engine.hello"}` + "\n"
	output := serve(t, input, &fakeBackend{})
	if !strings.Contains(output.String(), "unsupported protocol version") {
		t.Fatalf("unexpected response: %s", output.String())
	}
}

func TestOversizedMessageDoesNotStopServer(t *testing.T) {
	input := strings.Repeat("x", MaxMessageSize+1) + "\n" + request(2, "engine.hello", nil)
	output := serve(t, input, &fakeBackend{})
	if !strings.Contains(output.String(), "request is too large") || !strings.Contains(output.String(), `"id":2`) {
		t.Fatalf("unexpected response: %s", output.String())
	}
}

func TestOrderedNotificationsAndTerminalGet(t *testing.T) {
	gate := make(chan struct{})
	b := &fakeBackend{result: backend.BackendResult{Status: "succeeded", Summary: "done"}, wait: gate}
	service := run.NewService(b, store.New(t.TempDir()))
	output := &safeBuffer{}
	server := NewServer(strings.NewReader(request(1, "run.start", map[string]any{"project": "p", "task": "t"})), output, service)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	var start struct {
		Result struct {
			RunID string `json:"runId"`
		} `json:"result"`
	}
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if strings.Contains(line, `"id":1`) {
			_ = json.Unmarshal([]byte(line), &start)
		}
	}
	if start.Result.RunID == "" {
		t.Fatalf("missing run id: %s", output.String())
	}
	close(gate)
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(output.String(), `"status":"succeeded"`) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	text := output.String()
	created := strings.Index(text, `"sequence":1`)
	dispatching := strings.Index(text, `"status":"dispatching"`)
	running := strings.Index(text, `"status":"running"`)
	succeeded := strings.LastIndex(text, `"status":"succeeded"`)
	if !(created >= 0 && dispatching > created && running > dispatching && succeeded > running) {
		t.Fatalf("notifications not ordered: %s", text)
	}
	getOutput := &safeBuffer{}
	getServer := NewServer(strings.NewReader(request(2, "run.get", map[string]any{"runId": start.Result.RunID})), getOutput, service)
	if err := getServer.Serve(context.Background()); err != nil {
		t.Fatalf("run.get Serve() error = %v", err)
	}
	if !strings.Contains(getOutput.String(), `"status":"succeeded"`) || !strings.Contains(getOutput.String(), `"summary":"done"`) {
		t.Fatalf("unexpected terminal run.get response: %s", getOutput.String())
	}
}
