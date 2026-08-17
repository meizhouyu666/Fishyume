package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"wf.local/wf-engine/internal/contextcompiler"
)

var fixedMemoryTime = time.Date(2026, 8, 17, 8, 30, 0, 123456789, time.UTC)

func TestMemoryCatalogLifecycleIdempotencyIsolationAndTombstone(t *testing.T) {
	state := New(t.TempDir())
	state.SetMemoryClockForTest(func() time.Time { return fixedMemoryTime })
	project := mkdirProject(t, "project-a")
	other := mkdirProject(t, "project-b")
	create := validCreate(project, "mutation-create", "plaintext-marker-that-must-disappear")
	created, err := state.CreateMemory(create)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.Replayed || created.RecordID == "" || created.AffectedIDs == nil || len(created.AffectedIDs) != 0 {
		t.Fatalf("unexpected create result: %+v", created)
	}
	replayed, err := state.CreateMemory(create)
	if err != nil || !replayed.Replayed || replayed.Revision != created.Revision || replayed.RecordID != created.RecordID {
		t.Fatalf("create replay = %+v, %v", replayed, err)
	}
	conflict := create
	conflict.Content = "different"
	assertMemoryError(t, mustCreateMemory(state, conflict), MemoryStoreConflict)
	if _, _, err := state.GetMemory(other, created.RecordID); err == nil {
		t.Fatal("cross-project get unexpectedly found a record")
	} else {
		assertMemoryError(t, err, MemoryStoreNotFound)
	}

	supersede := MemorySupersedeInput{Project: project, MutationID: "mutation-supersede", Supersedes: []string{created.RecordID}, Type: contextcompiler.MemoryDecision, Content: "replacement", Sensitivity: contextcompiler.SensitivityProject, Writer: contextcompiler.MemoryWriterUser, Reason: "replace stale fact"}
	replacement, err := state.SupersedeMemory(supersede)
	if err != nil {
		t.Fatal(err)
	}
	if replay, replayErr := state.SupersedeMemory(supersede); replayErr != nil || !replay.Replayed || replay.RecordID != replacement.RecordID {
		t.Fatalf("supersede replay = %+v, %v", replay, replayErr)
	}
	supersedeConflict := supersede
	supersedeConflict.Content = "different replacement"
	if _, conflictErr := state.SupersedeMemory(supersedeConflict); conflictErr == nil {
		t.Fatal("supersede mutationId conflict was accepted")
	} else {
		assertMemoryError(t, conflictErr, MemoryStoreConflict)
	}
	old, _, err := state.GetMemory(project, created.RecordID)
	if err != nil || old.State != contextcompiler.MemorySuperseded || old.Content != create.Content {
		t.Fatalf("superseded record = %+v, %v", old, err)
	}

	deleted, err := state.DeleteMemory(MemoryDeleteInput{Project: project, MutationID: "mutation-delete", RecordID: created.RecordID, Writer: contextcompiler.MemoryWriterUser, Reason: "remove obsolete plaintext"})
	if err != nil {
		t.Fatal(err)
	}
	if replay, replayErr := state.DeleteMemory(MemoryDeleteInput{Project: project, MutationID: "mutation-delete", RecordID: created.RecordID, Writer: contextcompiler.MemoryWriterUser, Reason: "remove obsolete plaintext"}); replayErr != nil || !replay.Replayed || replay.Revision != deleted.Revision {
		t.Fatalf("delete replay = %+v, %v", replay, replayErr)
	}
	if _, conflictErr := state.DeleteMemory(MemoryDeleteInput{Project: project, MutationID: "mutation-delete", RecordID: created.RecordID, Writer: contextcompiler.MemoryWriterUser, Reason: "different reason"}); conflictErr == nil {
		t.Fatal("delete mutationId conflict was accepted")
	} else {
		assertMemoryError(t, conflictErr, MemoryStoreConflict)
	}
	tombstone, _, err := state.GetMemory(project, created.RecordID)
	if err != nil || tombstone.State != contextcompiler.MemoryDeleted || tombstone.Content != "" || tombstone.ContentHash != old.ContentHash || tombstone.Provenance != old.Provenance {
		t.Fatalf("tombstone = %+v, %v", tombstone, err)
	}
	catalogPath, _ := state.MemoryCatalogPath(project)
	catalogBytes, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(catalogBytes, []byte("plaintext-marker-that-must-disappear")) {
		t.Fatal("deleted plaintext remains in the live catalog")
	}
	if bytes.Contains(catalogBytes, []byte(`"supersedes": null`)) || bytes.Contains(catalogBytes, []byte(`"affectedIds": null`)) {
		t.Fatal("catalog encoded a required collection as null")
	}
	assertNoMemoryTemps(t, filepath.Dir(catalogPath))
}

func TestMemoryCatalogCanonicalSymlinkIdentityAndCrossProjectHash(t *testing.T) {
	state := New(t.TempDir())
	project := mkdirProject(t, "canonical")
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Skipf("directory symlink unavailable on this platform: %v", err)
	}
	canonicalPath, err := state.MemoryCatalogPath(project)
	if err != nil {
		t.Fatal(err)
	}
	aliasPath, err := state.MemoryCatalogPath(alias)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalPath != aliasPath {
		t.Fatalf("symlink catalog path %q != canonical %q", aliasPath, canonicalPath)
	}
	created, err := state.CreateMemory(validCreate(alias, "alias-create", "same identity"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.GetMemory(project, created.RecordID); err != nil {
		t.Fatal(err)
	}
	otherPath, err := state.MemoryCatalogPath(mkdirProject(t, "other"))
	if err != nil {
		t.Fatal(err)
	}
	if otherPath == canonicalPath {
		t.Fatal("distinct canonical projects share a catalog")
	}
}

func TestMemoryCatalogSupersedeIsAllOrNothing(t *testing.T) {
	state := New(t.TempDir())
	project := mkdirProject(t, "project")
	first, _ := state.CreateMemory(validCreate(project, "first", "first"))
	second, _ := state.CreateMemory(validCreate(project, "second", "second"))
	if _, err := state.DeleteMemory(MemoryDeleteInput{Project: project, MutationID: "delete-second", RecordID: second.RecordID, Writer: contextcompiler.MemoryWriterUser, Reason: "delete target"}); err != nil {
		t.Fatal(err)
	}
	before, _, _ := state.GetMemory(project, first.RecordID)
	_, err := state.SupersedeMemory(MemorySupersedeInput{Project: project, MutationID: "atomic-supersede", Supersedes: []string{second.RecordID, first.RecordID}, Type: contextcompiler.MemoryFact, Content: "replacement", Sensitivity: contextcompiler.SensitivityProject, Writer: contextcompiler.MemoryWriterHostAgent, Reason: "atomic replacement"})
	assertMemoryError(t, err, MemoryStoreConflict)
	after, revision, getErr := state.GetMemory(project, first.RecordID)
	if getErr != nil || after.State != before.State || revision != 3 {
		t.Fatalf("partial supersede: before=%+v after=%+v revision=%d err=%v", before, after, revision, getErr)
	}
}

func TestMemoryCatalogFaultStagesCleanupAndCrashReplay(t *testing.T) {
	for _, operation := range []string{"memory_after_temp_write", "memory_after_temp_sync", "memory_before_replace"} {
		t.Run(operation, func(t *testing.T) {
			state := New(t.TempDir())
			project := mkdirProject(t, operation)
			state.SetFaultInjectorForTest(func(candidate, _ string) error {
				if candidate == operation {
					return errors.New("fault")
				}
				return nil
			})
			if _, err := state.CreateMemory(validCreate(project, "faulted", "not committed")); err == nil {
				t.Fatal("faulted mutation unexpectedly succeeded")
			}
			state.SetFaultInjectorForTest(nil)
			listed, err := state.ListMemory(MemoryListInput{Project: project})
			if err != nil || listed.Revision != 0 || len(listed.Items) != 0 {
				t.Fatalf("pre-commit fault changed catalog: %+v, %v", listed, err)
			}
			catalogPath, _ := state.MemoryCatalogPath(project)
			assertNoMemoryTemps(t, filepath.Dir(catalogPath))
		})
	}
	for _, operation := range []string{"memory_after_replace_before_directory_sync", "memory_after_replace"} {
		t.Run(operation, func(t *testing.T) {
			state := New(t.TempDir())
			project := mkdirProject(t, operation)
			request := validCreate(project, "crash-window", "committed")
			state.SetFaultInjectorForTest(func(candidate, _ string) error {
				if candidate == operation {
					return errors.New("response lost")
				}
				return nil
			})
			if _, err := state.CreateMemory(request); err == nil {
				t.Fatal("post-commit response fault unexpectedly succeeded")
			}
			state.SetFaultInjectorForTest(nil)
			replayed, err := state.CreateMemory(request)
			if err != nil || !replayed.Replayed || replayed.Revision != 1 {
				t.Fatalf("crash replay = %+v, %v", replayed, err)
			}
		})
	}
}

func TestMemoryCatalogStaleTempCleanupAndCorruptionFailClosed(t *testing.T) {
	state := New(t.TempDir())
	project := mkdirProject(t, "project")
	catalogPath, err := state.MemoryCatalogPath(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(catalogPath), 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(filepath.Dir(catalogPath), memoryCatalogTempPrefix+"stale"+memoryCatalogTempSuffix)
	if err := os.WriteFile(stale, []byte("abandoned-plaintext"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ListMemory(MemoryListInput{Project: project}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale temp still exists: %v", err)
	}

	identity, _ := state.resolveMemoryProject(project)
	valid := fmt.Sprintf(`{"schemaVersion":%q,"project":%q,"projectHash":%q,"revision":0,"records":[],"receipts":[]}`, MemoryCatalogVersion, identity.canonical, identity.hash)
	cases := map[string]string{
		"truncated":                valid[:len(valid)-1],
		"duplicate":                strings.Replace(valid, `"revision":0`, `"revision":0,"revision":0`, 1),
		"unknown":                  strings.Replace(valid, `"revision":0`, `"unknown":true,"revision":0`, 1),
		"unsupported":              strings.Replace(valid, MemoryCatalogVersion, "fishyume.memory-catalog/v99", 1),
		"revision_without_receipt": strings.Replace(valid, `"revision":0`, `"revision":1`, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(catalogPath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, listErr := state.ListMemory(MemoryListInput{Project: project})
			if listErr == nil {
				t.Fatal("corrupt catalog was accepted")
			}
			_, createErr := state.CreateMemory(validCreate(project, "must-not-overwrite-"+name, "safe"))
			if createErr == nil {
				t.Fatal("mutation overwrote a corrupt catalog")
			}
			unchanged, _ := os.ReadFile(catalogPath)
			if string(unchanged) != body {
				t.Fatal("fail-closed operation changed corrupt catalog")
			}
		})
	}
	invalidUTF8 := append([]byte(valid[:len(valid)-1]), 0xff, '}')
	if err := os.WriteFile(catalogPath, invalidUTF8, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ListMemory(MemoryListInput{Project: project}); err == nil {
		t.Fatal("invalid UTF-8 catalog was accepted")
	}
	record := buildMemoryRecord(identity.canonical, "memory-duplicate", nil, canonicalCreateRequest{Type: contextcompiler.MemoryFact, Content: "x", Sensitivity: contextcompiler.SensitivityProject, Writer: contextcompiler.MemoryWriterUser, Reason: "duplicate fixture"}, fixedMemoryTime)
	duplicateCatalog := MemoryCatalogV1{SchemaVersion: MemoryCatalogVersion, Project: identity.canonical, ProjectHash: identity.hash, Revision: 1, Records: []contextcompiler.MemoryRecordV1{record, record}, Receipts: []MemoryMutationReceiptV1{}}
	duplicateBytes, _ := json.Marshal(duplicateCatalog)
	if err := os.WriteFile(catalogPath, duplicateBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ListMemory(MemoryListInput{Project: project}); err == nil {
		t.Fatal("duplicate Memory records were accepted")
	}
	if err := os.Truncate(catalogPath, MaxMemoryCatalogBytes+1); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ListMemory(MemoryListInput{Project: project}); err == nil {
		t.Fatal("oversized catalog was accepted")
	}
}

func TestMemoryCatalogLockFailureUsesStableUnavailableError(t *testing.T) {
	state := New(t.TempDir())
	project := mkdirProject(t, "project")
	identity, err := state.resolveMemoryProject(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(identity.lock, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = state.ListMemory(MemoryListInput{Project: project})
	assertMemoryError(t, err, MemoryStoreUnavailable)
}

func TestMemoryCatalogBoundsUTF8SensitiveReceiptsAndCapacity(t *testing.T) {
	state := New(t.TempDir())
	project := mkdirProject(t, "project")
	request := validCreate(project, "oversized", strings.Repeat("x", contextcompiler.MaxMemoryContentBytes+1))
	assertMemoryError(t, mustCreateMemory(state, request), MemoryStoreInvalid)
	request = validCreate(project, "invalid-utf8", string([]byte{0xff, 0xfe}))
	assertMemoryError(t, mustCreateMemory(state, request), MemoryStoreInvalid)
	request = validCreate(project, "sensitive", "secret")
	request.Sensitivity = contextcompiler.SensitivitySensitive
	assertMemoryError(t, mustCreateMemory(state, request), MemoryStoreInvalid)
	tooMany := make([]string, contextcompiler.MaxMemorySupersedes+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("memory-%02d", index)
	}
	if _, err := state.SupersedeMemory(MemorySupersedeInput{Project: project, MutationID: "too-many", Supersedes: tooMany, Type: contextcompiler.MemoryFact, Content: "x", Sensitivity: contextcompiler.SensitivityProject, Writer: contextcompiler.MemoryWriterUser, Reason: "too many"}); err == nil {
		t.Fatal("supersede accepted more than 16 targets")
	} else {
		assertMemoryError(t, err, MemoryStoreInvalid)
	}

	for index := 0; index < 17; index++ {
		if _, err := state.CreateMemory(validCreate(project, fmt.Sprintf("receipt-%03d", index), fmt.Sprintf("value-%03d", index))); err != nil {
			t.Fatal(err)
		}
	}
	identity, _ := state.resolveMemoryProject(project)
	catalog, err := readMemoryCatalog(identity)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Revision != 17 || len(catalog.Receipts) != 17 || MaxMemoryReceipts != 2*contextcompiler.MaxProjectMemoryRecords {
		t.Fatalf("receipt retention is not lifetime-bounded: revision=%d receipts=%d maximum=%d", catalog.Revision, len(catalog.Receipts), MaxMemoryReceipts)
	}
	overflow := catalog
	overflow.Receipts = make([]MemoryMutationReceiptV1, MaxMemoryReceipts+1)
	if err := validateMemoryCatalog(overflow, identity); err == nil {
		t.Fatal("catalog accepted receipts beyond the lifetime bound")
	}
	revisionMismatch := catalog
	revisionMismatch.Revision++
	if err := validateMemoryCatalog(revisionMismatch, identity); err == nil {
		t.Fatal("catalog accepted a revision without its durable receipt")
	}
	nilAffected := catalog
	nilAffected.Receipts = append([]MemoryMutationReceiptV1{}, catalog.Receipts...)
	nilAffected.Receipts[0].AffectedIDs = nil
	if err := validateMemoryCatalog(nilAffected, identity); err == nil {
		t.Fatal("catalog accepted a null affectedIds collection")
	}
	upperHash := catalog
	upperHash.Receipts = append([]MemoryMutationReceiptV1{}, catalog.Receipts...)
	upperHash.Receipts[0].RequestHash = strings.ToUpper(upperHash.Receipts[0].RequestHash)
	if err := validateMemoryCatalog(upperHash, identity); err == nil {
		t.Fatal("catalog accepted a non-canonical request hash")
	}

	capacityState := New(t.TempDir())
	capacityProject := mkdirProject(t, "capacity")
	capacityIdentity, _ := capacityState.resolveMemoryProject(capacityProject)
	records := make([]contextcompiler.MemoryRecordV1, 0, contextcompiler.MaxProjectMemoryRecords)
	receipts := make([]MemoryMutationReceiptV1, 0, contextcompiler.MaxProjectMemoryRecords)
	for index := 0; index < contextcompiler.MaxProjectMemoryRecords; index++ {
		id := fmt.Sprintf("memory-capacity-%04d", index)
		records = append(records, buildMemoryRecord(capacityIdentity.canonical, id, nil, canonicalCreateRequest{Type: contextcompiler.MemoryFact, Content: "x", Sensitivity: contextcompiler.SensitivityProject, Writer: contextcompiler.MemoryWriterUser, Reason: "capacity fixture"}, fixedMemoryTime))
		receipts = append(receipts, MemoryMutationReceiptV1{MutationID: fmt.Sprintf("capacity-%04d", index), RequestHash: hashString(id), Operation: "create", Writer: contextcompiler.MemoryWriterUser, Reason: "capacity fixture", Revision: uint64(index + 1), RecordID: id, AffectedIDs: []string{}, CreatedAt: fixedMemoryTime.Format(time.RFC3339Nano)})
	}
	catalog = MemoryCatalogV1{SchemaVersion: MemoryCatalogVersion, Project: capacityIdentity.canonical, ProjectHash: capacityIdentity.hash, Revision: uint64(len(receipts)), Records: records, Receipts: receipts}
	if err := os.MkdirAll(capacityIdentity.directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := capacityState.writeMemoryCatalog(capacityIdentity, catalog); err != nil {
		t.Fatal(err)
	}
	assertMemoryError(t, mustCreateMemory(capacityState, validCreate(capacityProject, "over-capacity", "x")), MemoryStoreConflict)
}

func TestMemoryCatalogStablePaginationAndRevisionConflict(t *testing.T) {
	state := New(t.TempDir())
	project := mkdirProject(t, "project")
	for _, mutation := range []string{"z", "a", "m", "b", "q"} {
		if _, err := state.CreateMemory(validCreate(project, mutation, mutation)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := state.ListMemory(MemoryListInput{Project: project, Limit: 2, Filter: MemoryListFilter{State: contextcompiler.MemoryActive}})
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	second, err := state.ListMemory(MemoryListInput{Project: project, Limit: 2, Cursor: first.NextCursor, Filter: MemoryListFilter{State: contextcompiler.MemoryActive}})
	if err != nil || len(second.Items) != 2 {
		t.Fatalf("second page = %+v, %v", second, err)
	}
	ids := []string{first.Items[0].ID, first.Items[1].ID, second.Items[0].ID, second.Items[1].ID}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("pages are not in stable ID order: %v", ids)
	}
	if _, err := state.ListMemory(MemoryListInput{Project: project, Limit: 2, Cursor: first.NextCursor, Filter: MemoryListFilter{State: contextcompiler.MemoryDeleted}}); err == nil {
		t.Fatal("cursor was accepted with a different filter")
	} else {
		assertMemoryError(t, err, MemoryStoreConflict)
	}
	if _, err := state.CreateMemory(validCreate(project, "new-revision", "new")); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ListMemory(MemoryListInput{Project: project, Limit: 2, Cursor: first.NextCursor, Filter: MemoryListFilter{State: contextcompiler.MemoryActive}}); err == nil {
		t.Fatal("stale revision cursor was accepted")
	} else {
		assertMemoryError(t, err, MemoryStoreConflict)
	}
	encoded, _ := json.Marshal(first)
	if bytes.Contains(encoded, []byte(`"content"`)) {
		t.Fatal("list metadata leaked content")
	}
}

func TestMemoryRetentionDoesNotMutateLifecycleOrUseCount(t *testing.T) {
	state := New(t.TempDir())
	project := mkdirProject(t, "retention")
	clock := fixedMemoryTime
	state.SetMemoryClockForTest(func() time.Time { return clock })
	request := validCreate(project, "retained", "bounded retention")
	request.ExpiresAt = fixedMemoryTime.Add(time.Hour).Format(time.RFC3339)
	request.MaxUses = 1
	created, err := state.CreateMemory(request)
	if err != nil {
		t.Fatal(err)
	}
	clock = fixedMemoryTime.Add(24 * time.Hour)
	record, revision, err := state.GetMemory(project, created.RecordID)
	if err != nil || record.State != contextcompiler.MemoryActive || record.UseCount != 0 || revision != 1 {
		t.Fatalf("retention caused background mutation: record=%+v revision=%d err=%v", record, revision, err)
	}
}

func TestMemoryCatalogConcurrentStoresAndProcessesHaveNoLostUpdates(t *testing.T) {
	stateRoot := t.TempDir()
	project := mkdirProject(t, "project")
	const writers = 24
	var wait sync.WaitGroup
	errorsCh := make(chan error, writers)
	for index := 0; index < writers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, err := New(stateRoot).CreateMemory(validCreate(project, fmt.Sprintf("goroutine-%02d", index), fmt.Sprintf("content-%02d", index)))
			errorsCh <- err
		}(index)
	}
	wait.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	listed, err := New(stateRoot).ListMemory(MemoryListInput{Project: project, Limit: MaxMemoryListLimit})
	if err != nil || len(listed.Items) != writers || listed.Revision != writers {
		t.Fatalf("goroutine writers lost updates: items=%d revision=%d err=%v", len(listed.Items), listed.Revision, err)
	}

	commands := make([]*exec.Cmd, 8)
	for index := range commands {
		command := exec.Command(os.Args[0], "-test.run=^TestMemoryCatalogProcessWriter$", "-test.v=false")
		command.Env = append(os.Environ(), "FISHYUME_MEMORY_HELPER=1", "FISHYUME_MEMORY_STATE="+stateRoot, "FISHYUME_MEMORY_PROJECT="+project, fmt.Sprintf("FISHYUME_MEMORY_MUTATION=process-%02d", index))
		commands[index] = command
		if startErr := command.Start(); startErr != nil {
			t.Fatal(startErr)
		}
	}
	for _, command := range commands {
		if waitErr := command.Wait(); waitErr != nil {
			t.Fatal(waitErr)
		}
	}
	listed, err = New(stateRoot).ListMemory(MemoryListInput{Project: project, Limit: MaxMemoryListLimit})
	if err != nil || len(listed.Items) != writers+len(commands) || listed.Revision != writers+uint64(len(commands)) {
		t.Fatalf("process writers lost updates: items=%d revision=%d err=%v", len(listed.Items), listed.Revision, err)
	}
}

func TestMemoryCatalogProcessWriter(t *testing.T) {
	if os.Getenv("FISHYUME_MEMORY_HELPER") != "1" {
		t.Skip("helper process only")
	}
	_, err := New(os.Getenv("FISHYUME_MEMORY_STATE")).CreateMemory(validCreate(os.Getenv("FISHYUME_MEMORY_PROJECT"), os.Getenv("FISHYUME_MEMORY_MUTATION"), "process content"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestMemoryCatalogGoldenFixtureIsStrictAndDeterministic(t *testing.T) {
	data, err := os.ReadFile("testdata/memory-catalog-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		t.Fatal(err)
	}
	var catalog MemoryCatalogV1
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	if !bytes.Equal(data, encoded) {
		t.Fatal("golden catalog is not canonical indented JSON")
	}
	if catalog.SchemaVersion != MemoryCatalogVersion || len(catalog.Records) != 2 || catalog.Records[1].State != contextcompiler.MemoryDeleted || strings.Contains(string(data), "deleted fixture plaintext") {
		t.Fatalf("unexpected golden catalog: %+v", catalog)
	}
	identity := memoryProjectIdentity{canonical: catalog.Project, normalized: normalizeMemoryProject(catalog.Project), hash: catalog.ProjectHash}
	if hashString(identity.normalized) != identity.hash {
		t.Fatal("golden catalog project hash is not canonical")
	}
	if err := validateMemoryCatalog(catalog, identity); err != nil {
		t.Fatalf("golden catalog validation: %v", err)
	}
	for _, record := range catalog.Records {
		if err := contextcompiler.ValidateMemoryRecordV1(record); err != nil {
			t.Fatalf("golden record %s: %v", record.ID, err)
		}
	}
}

func validCreate(project, mutationID, content string) MemoryCreateInput {
	return MemoryCreateInput{Project: project, MutationID: mutationID, Type: contextcompiler.MemoryFact, Content: content, Sensitivity: contextcompiler.SensitivityProject, Writer: contextcompiler.MemoryWriterUser, Reason: "explicit test reason"}
}

func mkdirProject(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustCreateMemory(state *Store, input MemoryCreateInput) error {
	_, err := state.CreateMemory(input)
	return err
}

func assertMemoryError(t *testing.T, err error, code MemoryStoreErrorCode) {
	t.Helper()
	var storeErr *MemoryStoreError
	if !errors.As(err, &storeErr) || storeErr.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func assertNoMemoryTemps(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), memoryCatalogTempPrefix) && strings.HasSuffix(entry.Name(), memoryCatalogTempSuffix) {
			t.Fatalf("abandoned Memory temp remains: %s", entry.Name())
		}
	}
}
