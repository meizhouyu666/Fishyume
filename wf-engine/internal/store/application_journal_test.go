package store

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestApplicationJournalLifecycleAndConflict(t *testing.T) {
	state := New(t.TempDir())
	now := time.Unix(10, 0).UTC()
	request := json.RawMessage(`{"project":"p"}`)
	record, err := state.BeginApplicationJournal("start", "request-1", "hash-1", request, "run-planned", now)
	if err != nil || record.State != JournalIntent {
		t.Fatalf("begin = %+v, error = %v", record, err)
	}
	replayed, err := state.BeginApplicationJournal("start", "request-1", "hash-1", request, "ignored", now.Add(time.Second))
	if err != nil || replayed.Kind != record.Kind || replayed.ID != record.ID || replayed.RequestHash != record.RequestHash || replayed.PlannedRunID != record.PlannedRunID || replayed.State != record.State || !replayed.CreatedAt.Equal(record.CreatedAt) {
		t.Fatalf("begin replay = %+v, error = %v", replayed, err)
	}
	if _, err := state.BeginApplicationJournal("start", "request-1", "hash-2", request, "run-other", now); err == nil {
		t.Fatal("same id with different hash was accepted")
	} else {
		var conflict *JournalConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("conflict error = %T %v", err, err)
		}
	}
	response := json.RawMessage(`{"runId":"run-planned"}`)
	mutated, err := state.MarkApplicationJournalMutated("start", "request-1", "hash-1", response, now.Add(time.Second))
	if err != nil || mutated.State != JournalMutated {
		t.Fatalf("mutated = %+v, error = %v", mutated, err)
	}
	committed, err := state.CommitApplicationJournal("start", "request-1", "hash-1", now.Add(2*time.Second))
	if err != nil || committed.State != JournalCommitted || !equalJSON(committed.Response, response) {
		t.Fatalf("committed = %+v, error = %v", committed, err)
	}
	pending, err := state.ListPendingApplicationJournals()
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending = %+v, error = %v", pending, err)
	}
}

func TestApplicationJournalFaultStagesRemainRecoverable(t *testing.T) {
	for _, faultPoint := range []string{"journal_intent", "journal_mutation", "journal_commit"} {
		t.Run(faultPoint, func(t *testing.T) {
			state := New(t.TempDir())
			failed := false
			state.SetFaultInjectorForTest(func(operation, _ string) error {
				if operation == faultPoint && !failed {
					failed = true
					return errors.New("fixture failure")
				}
				return nil
			})
			now := time.Unix(20, 0).UTC()
			request := json.RawMessage(`{"actionId":"a"}`)
			record, beginErr := state.BeginApplicationJournal("action", "action-1", "hash", request, "run-1", now)
			if faultPoint == "journal_intent" {
				if beginErr == nil {
					t.Fatal("intent fault was not injected")
				}
				state.SetFaultInjectorForTest(nil)
				record, beginErr = state.BeginApplicationJournal("action", "action-1", "hash", request, "run-1", now)
			}
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			response := json.RawMessage(`{"actionId":"action-1"}`)
			mutated, mutationErr := state.MarkApplicationJournalMutated("action", "action-1", "hash", response, now.Add(time.Second))
			if faultPoint == "journal_mutation" {
				if mutationErr == nil {
					t.Fatal("mutation fault was not injected")
				}
				persisted, err := state.ReadApplicationJournal("action", "action-1")
				if err != nil || persisted.State != JournalIntent {
					t.Fatalf("persisted after mutation fault = %+v, error = %v", persisted, err)
				}
				state.SetFaultInjectorForTest(nil)
				mutated, mutationErr = state.MarkApplicationJournalMutated("action", "action-1", "hash", response, now.Add(time.Second))
			}
			if mutationErr != nil || mutated.State != JournalMutated || record.State != JournalIntent {
				t.Fatalf("mutated = %+v, error = %v", mutated, mutationErr)
			}
			committed, commitErr := state.CommitApplicationJournal("action", "action-1", "hash", now.Add(2*time.Second))
			if faultPoint == "journal_commit" {
				if commitErr == nil {
					t.Fatal("commit fault was not injected")
				}
				persisted, err := state.ReadApplicationJournal("action", "action-1")
				if err != nil || persisted.State != JournalMutated {
					t.Fatalf("persisted after commit fault = %+v, error = %v", persisted, err)
				}
				state.SetFaultInjectorForTest(nil)
				committed, commitErr = state.CommitApplicationJournal("action", "action-1", "hash", now.Add(2*time.Second))
			}
			if commitErr != nil || committed.State != JournalCommitted {
				t.Fatalf("committed = %+v, error = %v", committed, commitErr)
			}
		})
	}
}

func TestApplicationJournalFutureVersionFailsClosedWithoutMutation(t *testing.T) {
	state := New(t.TempDir())
	now := time.Unix(30, 0).UTC()
	request := json.RawMessage(`{"project":"p"}`)
	if _, err := state.BeginApplicationJournal("start", "future-version", "hash", request, "run-future", now); err != nil {
		t.Fatal(err)
	}
	path := state.applicationJournalPath("start", "future-version")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record["version"] = 2
	future, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, future, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReadApplicationJournal("start", "future-version"); err == nil {
		t.Fatal("future journal version was accepted")
	}
	if _, err := state.ListPendingApplicationJournals(); err == nil {
		t.Fatal("future journal version was accepted during recovery listing")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(future) {
		t.Fatal("future journal version failure rewrote the journal")
	}
}
