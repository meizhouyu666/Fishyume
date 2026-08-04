package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Store struct{ root string }

func New(root string) *Store { return &Store{root: root} }

func NewDefault() (*Store, error) {
	root, err := StateRoot()
	if err != nil {
		return nil, err
	}
	return New(root), nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) RunDir(runID string) string { return filepath.Join(s.root, "runs", runID) }

func (s *Store) SnapshotPath(runID string) string { return filepath.Join(s.RunDir(runID), "run.json") }

func (s *Store) EventsPath(runID string) string {
	return filepath.Join(s.RunDir(runID), "events.jsonl")
}

func (s *Store) OutputPath(runID string) string {
	return filepath.Join(s.RunDir(runID), "nodes", "agent-1", "output.log")
}

func (s *Store) InitRun(runID string) error {
	dir := filepath.Dir(s.OutputPath(runID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create run directory %q: %w", dir, err)
	}
	file, err := os.OpenFile(s.OutputPath(runID), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("initialize node output %q: %w", s.OutputPath(runID), err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close node output %q: %w", s.OutputPath(runID), err)
	}
	return nil
}

func (s *Store) WriteSnapshot(runID string, snapshot any) error {
	path := s.SnapshotPath(runID)
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot for %q: %w", path, err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".run-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary snapshot beside %q: %w", path, err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("set temporary snapshot permissions %q: %w", tmpPath, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary snapshot %q: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary snapshot %q: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary snapshot %q: %w", tmpPath, err)
	}
	if err := replaceFile(tmpPath, path); err != nil {
		return fmt.Errorf("replace snapshot %q from %q: %w", path, tmpPath, err)
	}
	committed = true
	return nil
}

func (s *Store) AppendEvent(runID string, event any) error {
	path := s.EventsPath(runID)
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode event for %q: %w", path, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open event log %q: %w", path, err)
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("append event log %q: %w", path, err)
	}
	return nil
}

func (s *Store) WriteOutput(runID, output string) error {
	path := s.OutputPath(runID)
	if err := os.WriteFile(path, []byte(output), 0o600); err != nil {
		return fmt.Errorf("write node output %q: %w", path, err)
	}
	return nil
}
