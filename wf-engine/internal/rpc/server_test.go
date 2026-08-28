package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"wf.local/wf-engine/internal/application"
	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/driver/codexprocess"
	"wf.local/wf-engine/internal/routingconfig"
	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/team"
)

func TestTeamSessionLostErrorMapping(t *testing.T) {
	var output bytes.Buffer
	server := &Server{writer: &output}
	server.writeMappedTeamError(1, fmt.Errorf("follow-up failed: %w", team.ErrSessionLost))
	var response Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	data, ok := response.Error.Data.(map[string]any)
	if response.Error == nil || !ok || data["code"] != "session_lost" {
		t.Fatalf("response=%+v", response)
	}
}

type fakeBackend struct {
	name    string
	result  backend.AgentResult
	results []backend.AgentResult
	wait    chan struct{}
	mu      sync.Mutex
	starts  int
}

type routingInspectorFixture struct {
	models []codexprocess.ModelInfo
	probes map[string]bool
}

func (f *routingInspectorFixture) DiscoverModels(context.Context) ([]codexprocess.ModelInfo, error) {
	return append([]codexprocess.ModelInfo(nil), f.models...), nil
}

func (f *routingInspectorFixture) ProbeModel(_ context.Context, model, effort string) codexprocess.ProbeResult {
	return codexprocess.ProbeResult{Model: model, Effort: effort, Available: f.probes[model], Diagnostic: "RPC fixture"}
}

func TestM78RoutingRPCDiscoveryProbeConfigAndEffectiveCatalog(t *testing.T) {
	root := t.TempDir()
	config, err := routingconfig.NewService(root, &routingInspectorFixture{
		models: []codexprocess.ModelInfo{{ID: "sol", Model: "gpt-5.6-sol", DefaultEffort: "medium", SupportedEfforts: []string{"low", "medium", "high"}}},
		probes: map[string]bool{"gpt-5.6-sol": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := store.New(root)
	core := run.NewServiceWithRegistryAndCatalogs(nil, "codex", config, state)
	applicationService := application.NewServiceWithCatalogs(core, "codex", config, state)
	call := func(id int, method string, params any) rpcTestResponse {
		output := &safeBuffer{}
		server := NewServerWithTeamAndConfig(strings.NewReader(request(id, method, params)), output, core, applicationService, nil, config)
		if err := server.Serve(context.Background()); err != nil {
			t.Fatal(err)
		}
		return decodeResponseLines(t, output.String())[id]
	}
	params := map[string]any{"schemaVersion": routingconfig.APIVersion}
	update := map[string]any{"schemaVersion": routingconfig.APIVersion, "mutationId": "disable-luna", "expectedRevision": 1, "routeId": "codex/local/gpt-5.6-luna", "enabled": false}
	responses := map[int]rpcTestResponse{
		1: call(1, "driver.list", params),
		2: call(2, "driver.models.discover", params),
		3: call(3, "driver.models.probe", map[string]any{"schemaVersion": routingconfig.APIVersion, "routeIds": []string{"codex/local/gpt-5.6-sol"}}),
		4: call(4, "routing.config.update", update),
		5: call(5, "routing.config.update", update),
		6: call(6, "routing.config.update", map[string]any{"schemaVersion": routingconfig.APIVersion, "mutationId": "stale", "expectedRevision": 1, "routeId": "codex/local/gpt-5.6-terra", "enabled": false}),
		7: call(7, "routing.catalog.effective", params),
	}
	for _, id := range []int{1, 2, 3, 4, 5, 7} {
		if responses[id].Error != nil {
			t.Fatalf("response %d error = %+v", id, responses[id].Error)
		}
	}
	if responses[6].Error == nil || responses[6].Error.Code != -32009 {
		t.Fatalf("stale update response = %+v", responses[6])
	}
	var first, replay routingconfig.ConfigUpdateResponse
	decodeRPCResult(t, responses[4], &first)
	decodeRPCResult(t, responses[5], &replay)
	if first.Config.Revision != 2 || first.Replayed || !replay.Replayed || replay.Config.Revision != 2 {
		t.Fatalf("updates first=%+v replay=%+v", first, replay)
	}
	var effective routingconfig.EffectiveCatalogResponse
	decodeRPCResult(t, responses[7], &effective)
	if effective.CatalogHash == "" || len(effective.Catalog.Models) != 1 || effective.Catalog.Models[0].Target.Model != "gpt-5.6-sol" || !effective.Routes[1].Routable {
		t.Fatalf("effective catalog = %+v", effective)
	}
}

func TestM78RoutingRPCFailsClosedWithoutConfigService(t *testing.T) {
	output, _ := serve(t, request(1, "driver.list", map[string]any{"schemaVersion": routingconfig.APIVersion}), &fakeBackend{})
	response := decodeResponseLines(t, output.String())[1]
	data, _ := response.Error.Data.(map[string]any)
	if response.Error == nil || response.Error.Code != -32010 || data["code"] != "capability_unavailable" {
		t.Fatalf("response = %+v", response)
	}
}

func TestM79TeamRouteRPCPersistsAndReplaysMutations(t *testing.T) {
	root := t.TempDir()
	config, err := routingconfig.NewService(root, &routingInspectorFixture{})
	if err != nil {
		t.Fatal(err)
	}
	state := store.New(root)
	core := run.NewServiceWithRegistryAndCatalogs(nil, "codex", config, state)
	applicationService := application.NewServiceWithCatalogs(core, "codex", config, state)
	call := func(id int, method string, params any) rpcTestResponse {
		output := &safeBuffer{}
		server := NewServerWithTeamAndConfig(strings.NewReader(request(id, method, params)), output, core, applicationService, nil, config)
		if err := server.Serve(context.Background()); err != nil {
			t.Fatal(err)
		}
		return decodeResponseLines(t, output.String())[id]
	}
	read := call(1, "team.routes.get", map[string]any{"schemaVersion": routingconfig.APIVersion})
	var initial routingconfig.TeamRoutesResponse
	decodeRPCResult(t, read, &initial)
	upsert := map[string]any{"schemaVersion": routingconfig.APIVersion, "mutationId": "rpc-add-claude", "expectedRevision": initial.Config.Revision, "routeId": "claude/default/sonnet", "driver": "claude", "provider": "default", "model": "sonnet", "enabled": true}
	first := call(2, "team.routes.upsert", upsert)
	replayed := call(3, "team.routes.upsert", upsert)
	var created, replay routingconfig.TeamRoutesMutationResponse
	decodeRPCResult(t, first, &created)
	decodeRPCResult(t, replayed, &replay)
	if created.Config.Revision != initial.Config.Revision+1 || created.Replayed || !replay.Replayed {
		t.Fatalf("created=%+v replay=%+v", created, replay)
	}
	removed := call(4, "team.routes.remove", map[string]any{"schemaVersion": routingconfig.APIVersion, "mutationId": "rpc-remove-claude", "expectedRevision": created.Config.Revision, "routeId": "claude/default/sonnet"})
	if removed.Error != nil {
		t.Fatalf("remove error=%+v", removed.Error)
	}
}

func decodeResponseLines(t *testing.T, value string) map[int]rpcTestResponse {
	t.Helper()
	result := map[int]rpcTestResponse{}
	for _, line := range strings.Split(strings.TrimSpace(value), "\n") {
		var response rpcTestResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatalf("decode response %q: %v", line, err)
		}
		result[response.ID] = response
	}
	return result
}

func TestApplicationRPCErrorBoundsOversizedData(t *testing.T) {
	var output bytes.Buffer
	server := &Server{writer: &output}
	server.writeApplicationError(1, application.NewError(application.CodeInvalidArgument, "stable message", map[string]any{"detail": strings.Repeat("x", application.MaxErrorDataBytes*2)}))
	var response Response
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Message != "stable message" {
		t.Fatalf("error=%+v", response.Error)
	}
	data, err := json.Marshal(response.Error.Data)
	if err != nil || len(data) > application.MaxErrorDataBytes {
		t.Fatalf("data len=%d err=%v", len(data), err)
	}
}

func TestFormalApplicationRPCAndConnectionConcurrency(t *testing.T) {
	state := store.New(t.TempDir())
	service := run.NewService(&fakeBackend{}, state)
	applicationService := application.NewService(service, "codex", state)
	serverConnection, clientConnection := net.Pipe()
	clientReader := bufio.NewReader(clientConnection)
	server := NewConnectionServer(serverConnection, serverConnection, service, applicationService, &sync.Mutex{})
	done := make(chan error, 1)
	go func() { done <- server.Serve(context.Background()) }()
	defer func() {
		_ = clientConnection.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve() error=%v", err)
			}
		case <-time.After(time.Second):
			t.Error("connection server did not stop")
		}
	}()

	workflowDocument := map[string]any{
		"apiVersion": "fishyume/v1", "name": "approval-rpc",
		"execution": map[string]any{"maxConcurrency": 1},
		"nodes":     map[string]any{"approve": map[string]any{"type": "approval", "prompt": "Approve?"}},
	}
	writeRPCRequest(t, clientConnection, 1, "system.capabilities", map[string]any{})
	capabilities := readRPCResponse(t, clientConnection, clientReader)
	if capabilities.ID != 1 || capabilities.Error != nil {
		t.Fatalf("capabilities response=%+v", capabilities)
	}
	writeRPCRequest(t, clientConnection, 20, "routing.catalog", map[string]any{})
	var routingCatalog application.RoutingCatalogResponse
	decodeRPCResult(t, readRPCResponse(t, clientConnection, clientReader), &routingCatalog)
	if routingCatalog.CatalogHash == "" || len(routingCatalog.Catalog.Models) == 0 || routingCatalog.DynamicAvailability {
		t.Fatalf("routing.catalog response=%+v", routingCatalog)
	}
	writeRPCRequest(t, clientConnection, 2, "workflow.validate", map[string]any{"workflow": map[string]any{"document": workflowDocument}})
	if response := readRPCResponse(t, clientConnection, clientReader); response.ID != 2 || response.Error != nil {
		t.Fatalf("validate response=%+v", response)
	}
	writeRPCRequest(t, clientConnection, 3, "run.start", map[string]any{"project": "p", "workflow": map[string]any{"document": workflowDocument}, "clientRequestId": "rpc-start-1"})
	started := readRPCResponse(t, clientConnection, clientReader)
	var startResult application.RunStartResponse
	decodeRPCResult(t, started, &startResult)
	if startResult.RunID == "" {
		t.Fatalf("start response=%+v", started)
	}

	var view application.RunGetResponse
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		writeRPCRequest(t, clientConnection, 4, "run.get", map[string]any{"runId": startResult.RunID})
		decodeRPCResult(t, readRPCResponse(t, clientConnection, clientReader), &view)
		if view.Run.Phase == string(run.PhaseWaiting) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if view.Run.Phase != string(run.PhaseWaiting) {
		t.Fatalf("run did not wait for approval: %+v", view.Run)
	}
	writeRPCRequest(t, clientConnection, 5, "run.events", map[string]any{"runId": startResult.RunID, "limit": 100})
	var firstPage application.RunEventsResponse
	decodeRPCResult(t, readRPCResponse(t, clientConnection, clientReader), &firstPage)

	// The connection server handles requests concurrently. Wait for the
	// baseline page before issuing the action so the long poll always starts
	// behind the action event it is expected to observe.
	writeRPCRequest(t, clientConnection, 6, "run.events", map[string]any{"runId": startResult.RunID, "afterSequence": firstPage.NextAfterSequence, "waitMs": 1000})
	writeRPCRequest(t, clientConnection, 7, "run.action", map[string]any{"actionId": "rpc-action-1", "runId": startResult.RunID, "type": "approve", "expectedStateVersion": view.Run.StateVersion, "nodeId": "approve"})
	responses := map[int]rpcTestResponse{}
	for len(responses) < 2 {
		response := readRPCResponse(t, clientConnection, clientReader)
		responses[response.ID] = response
	}
	if responses[7].Error != nil {
		t.Fatalf("action response=%+v", responses[7])
	}
	var eventPage application.RunEventsResponse
	decodeRPCResult(t, responses[6], &eventPage)
	if len(eventPage.Events) == 0 || eventPage.NextAfterSequence <= firstPage.NextAfterSequence {
		t.Fatalf("event wait was not woken by concurrent action: %+v", eventPage)
	}
}

func TestFakeHostAgentFormalApplicationLifecycle(t *testing.T) {
	needsInput := backend.AgentResult{Status: "needs_input", Summary: "scope required", Questions: []backend.InputQuestion{{ID: "scope", Prompt: "Which scope?", Choices: []string{"core", "all"}, Required: true}}}
	succeeded := backend.AgentResult{Status: "succeeded", Summary: "implemented", Checks: []string{"tests"}}
	state := store.New(t.TempDir())
	service := run.NewService(&fakeBackend{results: []backend.AgentResult{needsInput, succeeded}}, state)
	applicationService := application.NewService(service, "codex", state)
	serverConnection, clientConnection := net.Pipe()
	clientReader := bufio.NewReader(clientConnection)
	server := NewConnectionServer(serverConnection, serverConnection, service, applicationService, &sync.Mutex{})
	done := make(chan error, 1)
	go func() { done <- server.Serve(context.Background()) }()
	defer func() {
		_ = clientConnection.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve() error=%v", err)
			}
		case <-time.After(time.Second):
			t.Error("host-agent connection did not stop")
		}
	}()
	document := map[string]any{
		"apiVersion": "fishyume/v1", "name": "host-agent-e2e", "execution": map[string]any{"maxConcurrency": 1},
		"nodes": map[string]any{
			"approve": map[string]any{"type": "approval", "prompt": "Ship?"},
			"agent":   map[string]any{"type": "agent", "dependsOn": []string{"approve"}, "task": "Implement"},
		},
	}
	writeRPCRequest(t, clientConnection, 1, "system.capabilities", nil)
	decodeRPCResult(t, readRPCResponse(t, clientConnection, clientReader), new(application.SystemCapabilitiesResponse))
	for id, method := range map[int]string{2: "workflow.validate", 3: "workflow.explain"} {
		writeRPCRequest(t, clientConnection, id, method, map[string]any{"workflow": map[string]any{"document": document}})
		response := readRPCResponse(t, clientConnection, clientReader)
		if response.Error != nil {
			t.Fatalf("%s error=%+v", method, response.Error)
		}
	}
	writeRPCRequest(t, clientConnection, 4, "run.start", map[string]any{"project": "p", "workflow": map[string]any{"document": document}, "clientRequestId": "host-start-1"})
	var started application.RunStartResponse
	decodeRPCResult(t, readRPCResponse(t, clientConnection, clientReader), &started)
	if started.Attach != "fishyume attach "+started.RunID {
		t.Fatalf("attach command=%q", started.Attach)
	}

	var approval application.RunGetResponse
	pollRunGet(t, clientConnection, clientReader, started.RunID, func(response application.RunGetResponse) bool {
		approval = response
		return response.Run.Phase == string(run.PhaseWaiting) && len(response.Run.Nodes) > 0 && response.Run.Nodes[0].NodeID == "approve"
	})
	writeRPCRequest(t, clientConnection, 5, "run.action", map[string]any{"actionId": "host-approve-1", "runId": started.RunID, "type": "approve", "expectedStateVersion": approval.Run.StateVersion, "nodeId": "approve"})
	if response := readRPCResponse(t, clientConnection, clientReader); response.Error != nil {
		t.Fatalf("approval error=%+v", response.Error)
	}

	var needsInputView application.RunGetResponse
	pollRunGet(t, clientConnection, clientReader, started.RunID, func(response application.RunGetResponse) bool {
		needsInputView = response
		for _, node := range response.Run.Nodes {
			if node.NodeID == "agent" {
				return node.Reason == "agent_waiting_input"
			}
		}
		return false
	})
	var expectedAttempt int
	for _, node := range needsInputView.Run.Nodes {
		if node.NodeID == "agent" {
			expectedAttempt = node.CurrentAttempt
		}
	}
	writeRPCRequest(t, clientConnection, 6, "run.action", map[string]any{"actionId": "host-answer-1", "runId": started.RunID, "type": "answer", "expectedStateVersion": needsInputView.Run.StateVersion, "nodeId": "agent", "expectedAttempt": expectedAttempt, "answers": map[string]any{"scope": "core"}})
	if response := readRPCResponse(t, clientConnection, clientReader); response.Error != nil {
		t.Fatalf("answer error=%+v", response.Error)
	}
	var result application.RunResultResponse
	pollResult(t, clientConnection, clientReader, started.RunID, &result)
	if result.Conclusion != string(run.ConclusionSucceeded) || len(result.Results) != 2 {
		t.Fatalf("result=%+v", result)
	}
}

func pollRunGet(t *testing.T, connection net.Conn, reader *bufio.Reader, runID string, done func(application.RunGetResponse) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		writeRPCRequest(t, connection, 20, "run.get", map[string]any{"runId": runID})
		var response application.RunGetResponse
		decodeRPCResult(t, readRPCResponse(t, connection, reader), &response)
		if done(response) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("run.get polling timed out")
}

func pollResult(t *testing.T, connection net.Conn, reader *bufio.Reader, runID string, result *application.RunResultResponse) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		writeRPCRequest(t, connection, 21, "run.result", map[string]any{"runId": runID})
		response := readRPCResponse(t, connection, reader)
		if response.Error != nil {
			if response.Error.Code == -32011 {
				time.Sleep(25 * time.Millisecond)
				continue
			}
			t.Fatalf("run.result error=%+v", response.Error)
		}
		decodeRPCResult(t, response, result)
		return
	}
	t.Fatal("run.result polling timed out")
}

type rpcTestResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

func writeRPCRequest(t *testing.T, writer io.Writer, id int, method string, params any) {
	t.Helper()
	if _, err := io.WriteString(writer, request(id, method, params)); err != nil {
		t.Fatal(err)
	}
}

func readRPCResponse(t *testing.T, connection net.Conn, reader *bufio.Reader) rpcTestResponse {
	t.Helper()
	for {
		_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var response rpcTestResponse
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatalf("decode %q: %v", line, err)
		}
		if response.ID != 0 {
			return response
		}
	}
}

func decodeRPCResult(t *testing.T, response rpcTestResponse, target any) {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("RPC error=%+v", response.Error)
	}
	if err := json.Unmarshal(response.Result, target); err != nil {
		t.Fatalf("decode result %s: %v", response.Result, err)
	}
}

func (f *fakeBackend) Name() string {
	if f.name != "" {
		return f.name
	}
	return "codex"
}
func (*fakeBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"codex"}, Runtimes: []string{"local"}, SupportsOutput: true}
}
func (f *fakeBackend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: f.Name(), Ready: true}
}
func (f *fakeBackend) Start(context.Context, backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	f.mu.Lock()
	f.starts++
	start := f.starts
	f.mu.Unlock()
	return &backend.ExecutionHandle{Backend: f.Name(), SchemaVersion: 1, ID: fmt.Sprintf("session-%d", start)}, nil
}
func (f *fakeBackend) Observe(context.Context, backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	if f.wait != nil {
		<-f.wait
	}
	f.mu.Lock()
	index := f.starts - 1
	value := f.result
	if index >= 0 && index < len(f.results) {
		value = f.results[index]
	}
	f.mu.Unlock()
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
	if !strings.Contains(output.String(), `"engineVersion":"0.2.1-alpha.1"`) || !strings.Contains(output.String(), `"protocolVersion":2`) || !strings.Contains(output.String(), `"run.start"`) || strings.Contains(output.String(), `"run.startWorkflow"`) {
		t.Fatalf("response=%s", output.String())
	}
}

func TestHandshakeReportsRegistryAndDoctorsSelectedBackend(t *testing.T) {
	registry := backend.NewRegistry()
	for _, candidate := range []*fakeBackend{{name: "codex"}, {name: "fixture"}} {
		if err := registry.Register(candidate); err != nil {
			t.Fatal(err)
		}
	}
	output := &safeBuffer{}
	service := run.NewServiceWithRegistry(registry, "codex", store.New(t.TempDir()))
	server := NewServer(strings.NewReader(request(1, "engine.hello", map[string]any{"backend": "codex"})), output, service)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, `"supportedBackends":["codex","fixture"]`) || !strings.Contains(text, `"backendDiagnostic":"backend codex is ready"`) {
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
	workflowDocument := map[string]any{"apiVersion": "fishyume/v2", "name": "ad-hoc", "defaults": map[string]any{"agent": map[string]any{"driver": "codex", "target": "local"}}, "execution": map[string]any{"maxConcurrency": 1}, "nodes": map[string]any{"agent-1": map[string]any{"type": "agent", "task": "t"}}}
	output, service := serve(t, request(1, "run.start", map[string]any{"project": "p", "workflow": map[string]any{"document": workflowDocument}, "clientRequestId": "rpc-ordered-start"}), backendImpl)
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

func TestRetiredMutationMethodsAreNotAvailable(t *testing.T) {
	doc := "apiVersion: fishyume/v2\nname: approval\ndefaults: {agent: {driver: codex, target: local}}\nexecution: {maxConcurrency: 1}\nnodes: {approve: {type: approval, prompt: approve}}\n"
	input := request(1, "run.start", map[string]any{"project": "p", "workflow": map[string]any{"source": map[string]any{"filename": "x.yaml", "content": doc}}, "clientRequestId": "rpc-formal-start"}) +
		request(2, "run.startWorkflow", map[string]any{"project": "p", "filename": "x.yaml", "content": doc}) +
		request(3, "run.resume", map[string]any{"runId": "run-x"}) +
		request(4, "run.cancel", map[string]any{"runId": "run-x"}) +
		request(5, "run.detach", map[string]any{"runId": "run-x"})
	output, _ := serve(t, input, &fakeBackend{})
	text := output.String()
	if !strings.Contains(text, `"runId":"run-`) || strings.Count(text, `"code":-32601`) != 4 {
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
