package store

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStateRootOverrideAndDurableFiles(t *testing.T) {
	root := t.TempDir()
	t.Setenv(StateDirEnv, root)
	resolved, err := StateRoot()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(root)
	if resolved != want {
		t.Fatalf("StateRoot() = %q, want %q", resolved, want)
	}
	s := New(resolved)
	if err := s.InitRun("run-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSnapshot("run-1", map[string]any{"status": "created"}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteSnapshot("run-1", map[string]any{"status": "running"}); err != nil {
		t.Fatal(err)
	}
	for sequence := 1; sequence <= 2; sequence++ {
		if err := s.AppendEvent("run-1", map[string]any{"sequence": sequence}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.WriteOutput("run-1", "fixture output\n"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(s.SnapshotPath("run-1"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot["status"] != "running" {
		t.Fatalf("unexpected snapshot: %s", data)
	}
	file, err := os.Open(s.EventsPath("run-1"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("event count = %d, want 2", count)
	}
}
