package controlplane

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"wf.local/wf-engine/internal/application"
	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/rpc"
	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/store"
)

type fixtureBackend struct{}

func (*fixtureBackend) Name() string { return "fixture" }
func (*fixtureBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{Tools: []string{"fixture"}, Runtimes: []string{"local"}, SupportsOutput: true}
}
func (*fixtureBackend) Doctor(context.Context, backend.DoctorRequest) backend.DoctorReport {
	return backend.DoctorReport{Backend: "fixture", Ready: true}
}
func (*fixtureBackend) Start(context.Context, backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	return &backend.ExecutionHandle{Backend: "fixture", SchemaVersion: 1, ID: "fixture-1"}, nil
}
func (*fixtureBackend) Observe(context.Context, backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	return &backend.ExecutionObservation{State: backend.ObservationTerminal, Result: &backend.AgentResult{Status: "succeeded", Summary: "done"}}, nil
}
func (*fixtureBackend) Output(context.Context, backend.ExecutionHandle, int) (string, error) {
	return "", nil
}
func (*fixtureBackend) Cancel(context.Context, backend.ExecutionHandle) (*backend.CancelResult, error) {
	return &backend.CancelResult{State: backend.CancelConfirmed}, nil
}

func TestPlatformTransportOwnerAndHandshake(t *testing.T) {
	state := store.New(t.TempDir())
	owner, err := AcquireOwner(state.Root(), rpc.EngineVersion, rpc.ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	service := run.NewService(&fixtureBackend{}, state)
	server, err := NewServer(owner, service)
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		<-done
	})

	if _, err := AcquireOwner(state.Root(), "incompatible", rpc.ProtocolVersion); !errors.Is(err, ErrOwnerActive) {
		t.Fatalf("second owner error=%v", err)
	}
	record := owner.Record()
	wrong := record
	wrong.OwnerID = "wrong-owner"
	if connection, err := Dial(wrong, time.Second); err == nil {
		connection.Close()
		t.Fatal("handshake accepted the wrong owner identity")
	}
	futureSchema := record
	futureSchema.StateSchema++
	if connection, err := Dial(futureSchema, time.Second); err == nil {
		connection.Close()
		t.Fatal("handshake accepted a future state schema")
	}
	wrongProtocol := record
	wrongProtocol.RPCProtocolVersion++
	if connection, err := Dial(wrongProtocol, time.Second); err == nil {
		connection.Close()
		t.Fatal("handshake accepted an incompatible RPC protocol")
	}

	connection, err := Dial(record, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	response := rpcCall(t, connection, 1, "engine.hello", map[string]any{"driver": "fixture"})
	if response.Error != nil {
		t.Fatalf("hello error=%+v", response.Error)
	}
	encoded, _ := json.Marshal(response.Result)
	if string(encoded) == "null" {
		t.Fatal("hello returned no result")
	}
}

func TestConcurrentExpectedStateMutationHasSingleWinner(t *testing.T) {
	state := store.New(t.TempDir())
	owner, err := AcquireOwner(state.Root(), rpc.EngineVersion, rpc.ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	service := run.NewService(&fixtureBackend{}, state)
	server, err := NewServer(owner, service)
	if err != nil {
		owner.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	defer func() { cancel(); _ = server.Close(); <-done }()

	first, err := Dial(owner.Record(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Dial(owner.Record(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	document := "apiVersion: fishyume/v2\nname: approval\ndefaults: {agent: {driver: codex, target: local}}\nexecution: {maxConcurrency: 1}\nnodes: {approve: {type: approval, prompt: approve}}\n"
	started := rpcCall(t, first, 1, "run.start", map[string]any{"project": "p", "workflow": map[string]any{"source": map[string]any{"filename": "workflow.yaml", "content": document}}, "clientRequestId": "control-plane-concurrent-start"})
	var startResult application.RunStartResponse
	decodeResult(t, started, &startResult)
	var snapshot run.WorkflowSnapshot
	deadline := time.Now().Add(3 * time.Second)
	for {
		view, viewErr := service.Status(startResult.RunID)
		if viewErr == nil && view.Run != nil && view.Run.Reason == run.ReasonApprovalRequired {
			snapshot = *view.Run
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("approval did not become actionable: view=%+v err=%v", view, viewErr)
		}
		time.Sleep(time.Millisecond)
	}

	requests := []struct {
		connection net.Conn
		id         int
		action     string
	}{{first, 10, "approve"}, {second, 20, "reject"}}
	responses := make(chan rpc.Response, 2)
	var wait sync.WaitGroup
	for _, request := range requests {
		wait.Add(1)
		go func(item struct {
			connection net.Conn
			id         int
			action     string
		}) {
			defer wait.Done()
			responses <- rpcCall(t, item.connection, item.id, "run.action", map[string]any{
				"actionId": fmt.Sprintf("concurrent-%d", item.id), "runId": startResult.RunID,
				"type": item.action, "nodeId": "approve", "expectedStateVersion": snapshot.StateVersion,
			})
		}(request)
	}
	wait.Wait()
	close(responses)
	winners := 0
	for response := range responses {
		if response.Error == nil {
			winners++
		} else if response.Error.Code != -32009 {
			t.Fatalf("unexpected conflict response: %+v", response.Error)
		}
	}
	if winners != 1 {
		t.Fatalf("successful mutations=%d, want 1", winners)
	}
}

func TestReleasedOwnerAllowsSafeStaleReplacement(t *testing.T) {
	root := t.TempDir()
	first, err := AcquireOwner(root, "old", rpc.ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	old := first.Record()
	if err := first.Publish(); err != nil {
		t.Fatal(err)
	}
	// Simulate abrupt process exit: the OS lock is released while metadata and
	// any endpoint artifact remain stale.
	if err := first.lock.Close(); err != nil {
		t.Fatal(err)
	}
	first.lock = nil
	second, err := AcquireOwner(root, "new", rpc.ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	server, err := NewServer(second, run.NewService(&fixtureBackend{}, store.New(root)))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	current, err := readOwner(second.recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if current.OwnerID == old.OwnerID || current.EngineVersion != "new" {
		t.Fatalf("stale owner was not replaced: old=%+v current=%+v", old, current)
	}
}

func rpcCall(t *testing.T, connection net.Conn, id int, method string, params any) rpc.Response {
	t.Helper()
	request := map[string]any{"jsonrpc": "2.0", "protocolVersion": rpc.ProtocolVersion, "id": id, "method": method, "params": params}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(connection)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var response rpc.Response
		if err := json.Unmarshal(line, &response); err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(response.ID) == fmt.Sprint(id) {
			return response
		}
	}
}

func decodeResult(t *testing.T, response rpc.Response, target any) {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("RPC error=%+v", response.Error)
	}
	data, err := json.Marshal(response.Result)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}
