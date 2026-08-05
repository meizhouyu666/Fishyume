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
	name   string
	result backend.AgentResult
	wait   chan struct{}
}

func (f *fakeBackend) Name() string {
	if f.name != "" {
		return f.name
	}
	return "ccpanes"
}
func (*fakeBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true}
}
func (f *fakeBackend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: f.Name(), Ready: true}
}
func (f *fakeBackend) Start(context.Context, backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	return &backend.ExecutionHandle{Backend: f.Name(), SchemaVersion: 1, ID: "session-1"}, nil
}
func (f *fakeBackend) Observe(context.Context, backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	if f.wait != nil {
		<-f.wait
	}
	value := f.result
	if value.Status == "" {
		return &backend.ExecutionObservation{State: backend.ObservationResultPending}, nil
	}
	return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &value}, nil
}
func (*fakeBackend) Output(context.Context, backend.ExecutionHandle, int) (string, error) {
	return "", nil
}
func (*fakeBackend) Cancel(context.Context, backend.ExecutionHandle) (*backend.CancelResult, error) {
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}

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

func serve(t *testing.T, input string, backendImpl *fakeBackend) (*safeBuffer, *run.Service) {
	t.Helper()
	output := &safeBuffer{}
	service := run.NewService(backendImpl, store.New(t.TempDir()))
	server := NewServer(strings.NewReader(input), output, service)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("Serve() error=%v", err)
	}
	return output, service
}

func request(id int, method string, params any) string {
	message := map[string]any{"jsonrpc": "2.0", "protocolVersion": 2, "id": id, "method": method}
	if params != nil {
		message["params"] = params
	}
	data, _ := json.Marshal(message)
	return string(data) + "\n"
}

func TestHandshakeV2(t *testing.T) {
	output, _ := serve(t, request(1, "engine.hello", nil), &fakeBackend{})
	if !strings.Contains(output.String(), `"engineVersion":"0.2.1-alpha.1"`) || !strings.Contains(output.String(), `"protocolVersion":2`) || !strings.Contains(output.String(), `"run.startWorkflow"`) {
		t.Fatalf("response=%s", output.String())
	}
}

func TestHandshakeReportsRegistryAndDoctorsSelectedBackend(t *testing.T) {
	registry := backend.NewRegistry()
	for _, candidate := range []*fakeBackend{{name: "direct"}, {name: "ccpanes"}} {
		if err := registry.Register(candidate); err != nil {
			t.Fatal(err)
		}
	}
	output := &safeBuffer{}
	service := run.NewServiceWithRegistry(registry, "ccpanes", store.New(t.TempDir()))
	server := NewServer(strings.NewReader(request(1, "engine.hello", map[string]any{"backend": "direct"})), output, service)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, `"supportedBackends":["ccpanes","direct"]`) || !strings.Contains(text, `"backendDiagnostic":"backend direct is ready"`) {
		t.Fatalf("response=%s", text)
	}
}

func TestProtocolErrorsDoNotStopServer(t *testing.T) {
	input := "{bad json}\n" + `{"jsonrpc":"2.0","protocolVersion":99,"id":2,"method":"engine.hello"}` + "\n" + request(3, "unknown", nil)
	output, _ := serve(t, input, &fakeBackend{})
	text := output.String()
	for _, want := range []string{`"code":-32700`, "unsupported protocol version", `"code":-32601`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
}

func TestOversizedMessageDoesNotStopServer(t *testing.T) {
	output, _ := serve(t, strings.Repeat("x", MaxMessageSize+1)+"\n"+request(2, "engine.hello", nil), &fakeBackend{})
	if !strings.Contains(output.String(), "request is too large") || !strings.Contains(output.String(), `"id":2`) {
		t.Fatalf("response=%s", output.String())
	}
}

func TestOrderedV2NotificationsAndReadOnlyStatus(t *testing.T) {
	gate := make(chan struct{})
	backendImpl := &fakeBackend{result: backend.BackendResult{Status: "succeeded", Summary: "done"}, wait: gate}
	output, service := serve(t, request(1, "run.start", map[string]any{"project": "p", "task": "t"}), backendImpl)
	var runID string
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var value struct {
			ID     int `json:"id"`
			Result struct {
				RunID string `json:"runId"`
			} `json:"result"`
		}
		_ = json.Unmarshal([]byte(line), &value)
		if value.ID == 1 {
			runID = value.Result.RunID
		}
	}
	if runID == "" {
		t.Fatalf("missing run id: %s", output.String())
	}
	close(gate)
	deadline := time.Now().Add(2 * time.Second)
	completed := false
	for time.Now().Before(deadline) {
		snapshot, _ := service.Get(runID)
		if snapshot.Phase == run.PhaseCompleted {
			completed = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !completed {
		t.Fatalf("run did not complete: %s", output.String())
	}
	waitContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.WaitControllers(waitContext); err != nil {
		t.Fatalf("controller did not stop: %v", err)
	}
	text := output.String()
	eventTypes := make([]string, 0, 4)
	var previousSequence uint64
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		var value struct {
			Method string `json:"method"`
			Params struct {
				Sequence uint64 `json:"sequence"`
				Type     string `json:"type"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			t.Fatalf("stdout contains non-protocol line %q", line)
		}
		if value.Method != "run.event" {
			continue
		}
		if value.Params.Sequence <= previousSequence {
			t.Fatalf("notification sequence is not increasing: %s", text)
		}
		previousSequence = value.Params.Sequence
		eventTypes = append(eventTypes, value.Params.Type)
	}
	wantEvents := []string{"run.created", "node.running", "node.completed", "run.completed"}
	if len(eventTypes) != len(wantEvents) {
		t.Fatalf("notifications=%v, want %v: %s", eventTypes, wantEvents, text)
	}
	for index := range wantEvents {
		if eventTypes[index] != wantEvents[index] {
			t.Fatalf("notifications=%v, want %v: %s", eventTypes, wantEvents, text)
		}
	}
	statusOutput := &safeBuffer{}
	statusServer := NewServer(strings.NewReader(request(2, "run.status", map[string]any{"runId": runID})), statusOutput, service)
	if err := statusServer.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusOutput.String(), `"conclusion":"succeeded"`) || !strings.Contains(statusOutput.String(), `"nodes"`) {
		t.Fatalf("status=%s", statusOutput.String())
	}
}

func TestStartWorkflowAndMalformedResumeActions(t *testing.T) {
	doc := "apiVersion: wf/v1\nname: approval\nexecution: {maxConcurrency: 1}\nnodes: {approve: {type: approval, prompt: approve}}\n"
	input := request(1, "run.startWorkflow", map[string]any{"project": "p", "filename": "x.yaml", "content": doc}) +
		request(2, "run.resume", map[string]any{"runId": "run-x", "action": map[string]any{"type": "approve", "nodeId": "a", "extra": true}}) +
		request(3, "run.resume", map[string]any{"runId": "run-x", "action": map[string]any{"type": "both", "nodeId": "a"}})
	output, _ := serve(t, input, &fakeBackend{})
	text := output.String()
	if !strings.Contains(text, `"runId":"run-`) || strings.Count(text, `"code":-32602`) != 2 {
		t.Fatalf("response=%s", text)
	}
}

func TestLegacyRunStatusIsReadable(t *testing.T) {
	state := store.New(t.TempDir())
	if err := state.InitRun("run-legacy"); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteSnapshot("run-legacy", run.LegacySnapshot{ProtocolVersion: 1, ID: "run-legacy", Status: run.RunPaused, NodeStatus: run.NodePaused}); err != nil {
		t.Fatal(err)
	}
	service := run.NewService(&fakeBackend{}, state)
	output := &safeBuffer{}
	server := NewServer(strings.NewReader(request(1, "run.status", map[string]any{"runId": "run-legacy"})), output, service)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"legacy":true`) || !strings.Contains(output.String(), `"status":"paused"`) {
		t.Fatalf("response=%s", output.String())
	}
}
