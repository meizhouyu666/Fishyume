package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"wf.local/wf-engine/internal/contextcompiler"
	"wf.local/wf-engine/internal/store"
)

func TestApplicationMemoryLifecycleFixesWriterAndMapsErrors(t *testing.T) {
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	state := store.New(t.TempDir())
	service := NewService(newFakeCore(), "codex", state)
	request := MemoryCreateRequest{Project: project, MutationID: "application-user", Type: contextcompiler.MemoryFact, Content: "user value", Sensitivity: contextcompiler.SensitivityProject, Reason: "explicit user reason"}
	created, appErr := service.MemoryCreate(context.Background(), request)
	if appErr != nil || created.Revision != 1 || created.Replayed {
		t.Fatalf("create = %+v, %v", created, appErr)
	}
	userRecord, appErr := service.MemoryGet(context.Background(), MemoryGetRequest{Project: project, RecordID: created.RecordID})
	if appErr != nil || userRecord.Record.Provenance.Writer != contextcompiler.MemoryWriterUser || userRecord.Record.Provenance.Source != "fishyume.cli" {
		t.Fatalf("user record = %+v, %v", userRecord, appErr)
	}
	host := request
	host.MutationID, host.Content, host.Reason = "application-host", "host value", "explicit host reason"
	hostCreated, appErr := service.MemoryCreateHost(context.Background(), host)
	if appErr != nil {
		t.Fatal(appErr)
	}
	hostRecord, appErr := service.MemoryGet(context.Background(), MemoryGetRequest{Project: project, RecordID: hostCreated.RecordID})
	if appErr != nil || hostRecord.Record.Provenance.Writer != contextcompiler.MemoryWriterHostAgent || hostRecord.Record.Provenance.Source != "fishyume.mcp" {
		t.Fatalf("host record = %+v, %v", hostRecord, appErr)
	}
	missingReason := host
	missingReason.MutationID, missingReason.Reason = "host-no-reason", ""
	if _, appErr := service.MemoryCreateHost(context.Background(), missingReason); appErr == nil || appErr.Code != CodeInvalidArgument {
		t.Fatalf("host reason error = %v", appErr)
	}
	sensitive := request
	sensitive.MutationID, sensitive.Sensitivity = "sensitive", contextcompiler.SensitivitySensitive
	if _, appErr := service.MemoryCreate(context.Background(), sensitive); appErr == nil || appErr.Code != CodeInvalidArgument {
		t.Fatalf("sensitive error = %v", appErr)
	}
	listed, appErr := service.MemoryList(context.Background(), MemoryListRequest{Project: project, Limit: 1})
	if appErr != nil || len(listed.Items) != 1 || listed.NextCursor == "" {
		t.Fatalf("list = %+v, %v", listed, appErr)
	}
	if _, appErr := service.MemoryList(context.Background(), MemoryListRequest{Project: project, Limit: 1, Cursor: listed.NextCursor, Filter: MemoryListFilter{State: contextcompiler.MemoryDeleted}}); appErr == nil || appErr.Code != CodeConflict {
		t.Fatalf("cursor conflict = %v", appErr)
	}
}
