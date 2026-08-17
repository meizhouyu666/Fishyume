package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wf.local/wf-engine/internal/application"
	"wf.local/wf-engine/internal/contextcompiler"
	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/store"
)

func TestMemoryJSONRPCPublicAndHostWriterFacades(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	state := store.New(t.TempDir())
	runService := run.NewService(&fakeBackend{}, state)
	applicationService := application.NewService(runService, "codex", state)
	forged := callMemoryRPC(t, runService, applicationService, "memory.create", map[string]any{"project": project, "mutationId": "rpc-forged", "type": "fact", "content": "user", "sensitivity": "project", "reason": "rpc user", "writer": "host_agent"})
	if forged.Error == nil || forged.Error.Code != -32602 {
		t.Fatalf("caller-selectable writer was accepted: %+v", forged)
	}
	public := callMemoryRPC(t, runService, applicationService, "memory.create", map[string]any{"project": project, "mutationId": "rpc-user", "type": "fact", "content": "user", "sensitivity": "project", "reason": "rpc user"})
	var publicResult application.MemoryMutationResponse
	decodeMemoryRPCResult(t, public, &publicResult)
	host := callMemoryRPC(t, runService, applicationService, "memory.host.create", map[string]any{"project": project, "mutationId": "rpc-host", "type": "fact", "content": "host", "sensitivity": "project", "reason": "rpc host"})
	var hostResult application.MemoryMutationResponse
	decodeMemoryRPCResult(t, host, &hostResult)
	publicRecord, _, _ := state.GetMemory(project, publicResult.RecordID)
	hostRecord, _, _ := state.GetMemory(project, hostResult.RecordID)
	if publicRecord.Provenance.Writer != contextcompiler.MemoryWriterUser || hostRecord.Provenance.Writer != contextcompiler.MemoryWriterHostAgent {
		t.Fatalf("writers: public=%s host=%s", publicRecord.Provenance.Writer, hostRecord.Provenance.Writer)
	}
	if !containsMethod(supportedMethods, "memory.create") || containsMethod(supportedMethods, "memory.host.create") {
		t.Fatalf("supported methods expose wrong Memory facade: %v", supportedMethods)
	}
}

func callMemoryRPC(t *testing.T, runService *run.Service, applicationService *application.Service, method string, params any) Response {
	t.Helper()
	var output bytes.Buffer
	server := NewServer(strings.NewReader(request(1, method, params)), &output, runService, applicationService)
	if err := server.Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	var response Response
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func decodeMemoryRPCResult(t *testing.T, response Response, target any) {
	t.Helper()
	data, err := json.Marshal(response.Result)
	if err != nil || json.Unmarshal(data, target) != nil {
		t.Fatalf("decode RPC result: %v", err)
	}
}

func containsMethod(methods []string, wanted string) bool {
	for _, method := range methods {
		if method == wanted {
			return true
		}
	}
	return false
}
