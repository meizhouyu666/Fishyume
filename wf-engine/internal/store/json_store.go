package store

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

type FaultInjector func(operation, path string) error

type Store struct {
	root string

	faultMu       sync.RWMutex
	faultInjector FaultInjector
	memoryClockMu sync.RWMutex
	memoryClock   func() time.Time
}

var safeID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,127}$`)

func New(root string) *Store { return &Store{root: root, memoryClock: time.Now} }

// LegacyFallback returns the former default store for read-only compatibility
// lookup. Callers must not use it for new writes or migration.
func (s *Store) LegacyFallback() *Store {
	legacyRoot, err := LegacyStateRoot()
	if err != nil || filepath.Clean(legacyRoot) == filepath.Clean(s.root) {
		return nil
	}
	return New(legacyRoot)
}

func NewDefault() (*Store, error) {
	root, err := StateRoot()
	if err != nil {
		return nil, err
	}
	return New(root), nil
}

func (s *Store) Root() string { return s.root }

func (s *Store) SetFaultInjectorForTest(injector FaultInjector) {
	s.faultMu.Lock()
	defer s.faultMu.Unlock()
	s.faultInjector = injector
}

func (s *Store) injectFault(operation, path string) error {
	s.faultMu.RLock()
	injector := s.faultInjector
	s.faultMu.RUnlock()
	if injector == nil {
		return nil
	}
	if err := injector(operation, path); err != nil {
		return fmt.Errorf("injected %s failure for %q: %w", operation, path, err)
	}
	return nil
}

func (s *Store) RunDir(runID string) string { return filepath.Join(s.root, "runs", runID) }

func (s *Store) SnapshotPath(runID string) string { return filepath.Join(s.RunDir(runID), "run.json") }

func (s *Store) EventsPath(runID string) string {
	return filepath.Join(s.RunDir(runID), "events.jsonl")
}

func (s *Store) OutputPath(runID string) string {
	return filepath.Join(s.RunDir(runID), "nodes", "agent-1", "output.log")
}

func (s *Store) WorkflowPath(runID string) string {
	return filepath.Join(s.RunDir(runID), "workflow.json")
}

func (s *Store) NodePath(runID, nodeID string) string {
	return filepath.Join(s.RunDir(runID), "nodes", nodeID, "node.json")
}

func (s *Store) ActionIntentPath(runID, actionID string) string {
	digest := sha256.Sum256([]byte(actionID))
	return filepath.Join(s.RunDir(runID), "action-intents", hex.EncodeToString(digest[:])+".json")
}

func (s *Store) WriteActionIntent(runID, actionID string, intent any) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	if actionID == "" {
		return fmt.Errorf("action id is required")
	}
	return s.writeJSON(s.ActionIntentPath(runID, actionID), intent)
}

func (s *Store) ReadActionIntent(runID, actionID string, target any) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	if actionID == "" {
		return fmt.Errorf("action id is required")
	}
	return readJSON(s.ActionIntentPath(runID, actionID), target)
}

func (s *Store) RemoveActionIntent(runID, actionID string) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	if actionID == "" {
		return fmt.Errorf("action id is required")
	}
	if err := os.Remove(s.ActionIntentPath(runID, actionID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove action intent %q: %w", actionID, err)
	}
	return nil
}

func (s *Store) ListActionIntentIDs(runID string) ([]string, error) {
	if err := validateID("run", runID); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.RunDir(runID), "action-intents")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list action intents for run %q: %w", runID, err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var header struct {
			ActionID string `json:"actionId"`
		}
		if err := readJSON(filepath.Join(dir, entry.Name()), &header); err != nil {
			return nil, err
		}
		if header.ActionID == "" {
			return nil, fmt.Errorf("action intent %q has no actionId", entry.Name())
		}
		ids = append(ids, header.ActionID)
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Store) AttemptDir(runID, nodeID string, number int) string {
	return filepath.Join(s.RunDir(runID), "nodes", nodeID, "attempts", strconv.Itoa(number))
}

func (s *Store) AttemptPath(runID, nodeID string, number int) string {
	return filepath.Join(s.AttemptDir(runID, nodeID, number), "attempt.json")
}

func (s *Store) ResultPath(runID, nodeID string, number int) string {
	return filepath.Join(s.AttemptDir(runID, nodeID, number), "result.json")
}

func (s *Store) NodeOutputPath(runID, nodeID string, number int) string {
	return filepath.Join(s.AttemptDir(runID, nodeID, number), "output.log")
}

func (s *Store) InitRun(runID string) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
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

func (s *Store) InitWorkflowRun(runID string) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	dir := filepath.Join(s.RunDir(runID), "nodes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create workflow run directory %q: %w", dir, err)
	}
	return nil
}

func (s *Store) WriteSnapshot(runID string, snapshot any) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	path := s.SnapshotPath(runID)
	return s.writeJSON(path, snapshot)
}

func (s *Store) WriteWorkflow(runID string, document any) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	path := s.WorkflowPath(runID)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("normalized workflow for run %q already exists", runID)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect workflow snapshot %q: %w", path, err)
	}
	return s.writeJSON(path, document)
}

// EnsureWorkflow creates the normalized workflow once and otherwise verifies
// that a retry is bound to the same durable initialization.
func (s *Store) EnsureWorkflow(runID string, document any) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	return s.ensureJSON(s.WorkflowPath(runID), document, fmt.Sprintf("normalized workflow for run %q", runID))
}

func (s *Store) WriteNode(runID, nodeID string, snapshot any) error {
	if err := validateRunNode(runID, nodeID); err != nil {
		return err
	}
	return s.writeJSON(s.NodePath(runID, nodeID), snapshot)
}

// EnsureNode creates an initial Node snapshot once and never overwrites
// residual state from an interrupted start.
func (s *Store) EnsureNode(runID, nodeID string, snapshot any) error {
	if err := validateRunNode(runID, nodeID); err != nil {
		return err
	}
	return s.ensureJSON(s.NodePath(runID, nodeID), snapshot, fmt.Sprintf("initial snapshot for node %q", nodeID))
}

// EnsureSnapshot creates an initial Run snapshot once and otherwise verifies
// exact initial content. Callers must only use this before the Run can advance.
func (s *Store) EnsureSnapshot(runID string, snapshot any) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	return s.ensureJSON(s.SnapshotPath(runID), snapshot, fmt.Sprintf("initial snapshot for run %q", runID))
}

func (s *Store) ensureJSON(path string, value any, description string) error {
	expected, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", description, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create snapshot directory %q: %w", filepath.Dir(path), err)
	}
	return withLeaseGuard(path, func() error {
		existing, readErr := os.ReadFile(path)
		if readErr == nil {
			if !equalJSON(existing, expected) {
				return fmt.Errorf("%s does not match requested initialization", description)
			}
			return nil
		}
		if !os.IsNotExist(readErr) {
			return fmt.Errorf("inspect %s: %w", description, readErr)
		}
		return s.writeJSON(path, value)
	})
}

func (s *Store) WriteAttempt(runID, nodeID string, number int, snapshot any) error {
	if err := validateAttempt(runID, nodeID, number); err != nil {
		return err
	}
	path := s.AttemptPath(runID, nodeID, number)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("attempt %d for node %q already exists", number, nodeID)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect attempt %q: %w", path, err)
	}
	if err := s.writeJSON(path, snapshot); err != nil {
		return err
	}
	return os.WriteFile(s.NodeOutputPath(runID, nodeID, number), nil, 0o600)
}

func (s *Store) UpdateAttempt(runID, nodeID string, number int, snapshot any) error {
	if err := validateAttempt(runID, nodeID, number); err != nil {
		return err
	}
	return s.writeJSON(s.AttemptPath(runID, nodeID, number), snapshot)
}

func (s *Store) WriteResult(runID, nodeID string, number int, result any) error {
	if err := validateAttempt(runID, nodeID, number); err != nil {
		return err
	}
	return s.writeJSON(s.ResultPath(runID, nodeID, number), result)
}

func (s *Store) writeJSON(path string, value any) error {
	if err := s.injectFault("write_json", path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create snapshot directory %q: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
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
	if err := validateID("run", runID); err != nil {
		return err
	}
	path := s.EventsPath(runID)
	if err := s.injectFault("append_event", path); err != nil {
		return err
	}
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

// EnsureInitialEvent appends the first event exactly once. Existing event
// history is never rewritten; its first record must match the requested start.
func (s *Store) EnsureInitialEvent(runID string, event any) (bool, error) {
	if err := validateID("run", runID); err != nil {
		return false, err
	}
	path := s.EventsPath(runID)
	expected, err := json.Marshal(event)
	if err != nil {
		return false, fmt.Errorf("encode initial event for %q: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("create event directory %q: %w", filepath.Dir(path), err)
	}
	created := false
	err = withLeaseGuard(path, func() error {
		file, openErr := os.Open(path)
		if openErr == nil {
			scanner := bufio.NewScanner(file)
			hasFirst := scanner.Scan()
			first := append([]byte(nil), scanner.Bytes()...)
			scanErr := scanner.Err()
			closeErr := file.Close()
			if scanErr != nil {
				return fmt.Errorf("read initial event for run %q: %w", runID, scanErr)
			}
			if closeErr != nil {
				return fmt.Errorf("close event log for run %q: %w", runID, closeErr)
			}
			if hasFirst {
				if !equalJSON(first, expected) {
					return fmt.Errorf("initial event for run %q does not match requested initialization", runID)
				}
				return nil
			}
		} else if !os.IsNotExist(openErr) {
			return fmt.Errorf("open events for run %q: %w", runID, openErr)
		}
		if err := s.AppendEvent(runID, event); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

func (s *Store) WriteOutput(runID, output string) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	path := s.OutputPath(runID)
	if err := s.injectFault("write_output", path); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(output), 0o600); err != nil {
		return fmt.Errorf("write node output %q: %w", path, err)
	}
	return nil
}

func (s *Store) WriteNodeOutput(runID, nodeID string, number int, output string) error {
	if err := validateAttempt(runID, nodeID, number); err != nil {
		return err
	}
	path := s.NodeOutputPath(runID, nodeID, number)
	if err := s.injectFault("write_output", path); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(output), 0o600); err != nil {
		return fmt.Errorf("write node output %q: %w", path, err)
	}
	return nil
}

func (s *Store) ReadSnapshot(runID string, target any) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	return readJSON(s.SnapshotPath(runID), target)
}

func (s *Store) ReadWorkflow(runID string, target any) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	return readJSON(s.WorkflowPath(runID), target)
}

func (s *Store) ReadNode(runID, nodeID string, target any) error {
	if err := validateRunNode(runID, nodeID); err != nil {
		return err
	}
	return readJSON(s.NodePath(runID, nodeID), target)
}

func (s *Store) ReadAttempt(runID, nodeID string, number int, target any) error {
	if err := validateAttempt(runID, nodeID, number); err != nil {
		return err
	}
	return readJSON(s.AttemptPath(runID, nodeID, number), target)
}

func (s *Store) ReadResult(runID, nodeID string, number int, target any) error {
	if err := validateAttempt(runID, nodeID, number); err != nil {
		return err
	}
	return readJSON(s.ResultPath(runID, nodeID, number), target)
}

// ReadNodeOutput returns at most maxBytes from the tail of an Attempt output
// log. Output is observational evidence, not durable workflow state, so reads
// must remain bounded even if a legacy store contains a larger file.
func (s *Store) ReadNodeOutput(runID, nodeID string, number, maxBytes int) (string, error) {
	if err := validateAttempt(runID, nodeID, number); err != nil {
		return "", err
	}
	if maxBytes <= 0 {
		maxBytes = 32 * 1024
	}
	if maxBytes > 128*1024 {
		maxBytes = 128 * 1024
	}
	file, err := os.Open(s.NodeOutputPath(runID, nodeID, number))
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	start := info.Size() - int64(maxBytes)
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)))
	if err != nil {
		return "", err
	}
	if start > 0 {
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 && newline+1 < len(data) {
			data = data[newline+1:]
		}
	}
	return string(data), nil
}

func (s *Store) ListNodeIDs(runID string) ([]string, error) {
	if err := validateID("run", runID); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(s.RunDir(runID), "nodes"))
	if err != nil {
		return nil, fmt.Errorf("list nodes for run %q: %w", runID, err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && safeID.MatchString(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Store) ListRunIDs() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "runs"))
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list workflow runs: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && safeID.MatchString(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *Store) ListAttempts(runID, nodeID string) ([]int, error) {
	if err := validateRunNode(runID, nodeID); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.RunDir(runID), "nodes", nodeID, "attempts")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []int{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list attempts for node %q: %w", nodeID, err)
	}
	numbers := make([]int, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		number, err := strconv.Atoi(entry.Name())
		if err == nil && number > 0 {
			numbers = append(numbers, number)
		}
	}
	sort.Ints(numbers)
	return numbers, nil
}

func (s *Store) ReadEvents(runID string, target func(json.RawMessage) error) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	file, err := os.Open(s.EventsPath(runID))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open events for run %q: %w", runID, err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := append(json.RawMessage(nil), scanner.Bytes()...)
		if !json.Valid(line) {
			return fmt.Errorf("corrupted event log for run %q", runID)
		}
		if err := target(line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read events for run %q: %w", runID, err)
	}
	return nil
}

type SnapshotKind string

const (
	SnapshotM2       SnapshotKind = "m2"
	SnapshotLegacyM1 SnapshotKind = "legacy-m1"
)

func (s *Store) DetectSnapshot(runID string) (SnapshotKind, error) {
	var header struct {
		ProtocolVersion int    `json:"protocolVersion"`
		Phase           string `json:"phase"`
		Status          string `json:"status"`
	}
	if err := s.ReadSnapshot(runID, &header); err != nil {
		return "", err
	}
	if header.ProtocolVersion == 1 || (header.Status != "" && header.Phase == "") {
		return SnapshotLegacyM1, nil
	}
	if header.ProtocolVersion == 2 && header.Phase != "" {
		return SnapshotM2, nil
	}
	return "", fmt.Errorf("run %q has an unrecognized snapshot format", runID)
}

func readJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open snapshot %q: %w", path, err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 4*1024*1024))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode snapshot %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("snapshot %q contains multiple JSON values", path)
		}
		return fmt.Errorf("decode trailing data in %q: %w", path, err)
	}
	return nil
}

func validateID(kind, value string) error {
	if !safeID.MatchString(value) {
		return fmt.Errorf("invalid %s id %q", kind, value)
	}
	return nil
}

func validateRunNode(runID, nodeID string) error {
	if err := validateID("run", runID); err != nil {
		return err
	}
	return validateID("node", nodeID)
}

func validateAttempt(runID, nodeID string, number int) error {
	if err := validateRunNode(runID, nodeID); err != nil {
		return err
	}
	if number < 1 {
		return fmt.Errorf("attempt number must be positive")
	}
	return nil
}
