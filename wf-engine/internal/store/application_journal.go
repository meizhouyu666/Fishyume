package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"
)

const (
	JournalIntent    = "intent"
	JournalMutated   = "mutated"
	JournalCommitted = "committed"
)

type ApplicationJournalRecord struct {
	Version      int             `json:"version"`
	Kind         string          `json:"kind"`
	ID           string          `json:"id"`
	RequestHash  string          `json:"requestHash"`
	Request      json.RawMessage `json:"request"`
	PlannedRunID string          `json:"plannedRunId"`
	State        string          `json:"state"`
	Response     json.RawMessage `json:"response,omitempty"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

type JournalConflictError struct {
	Kind string
	ID   string
}

func (e *JournalConflictError) Error() string {
	return fmt.Sprintf("%s id %q is already bound to a different canonical request", e.Kind, e.ID)
}

func (s *Store) BeginApplicationJournal(kind, id, requestHash string, request json.RawMessage, plannedRunID string, now time.Time) (ApplicationJournalRecord, error) {
	if err := validateJournalIdentity(kind, id, requestHash, request, plannedRunID); err != nil {
		return ApplicationJournalRecord{}, err
	}
	path := s.applicationJournalPath(kind, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ApplicationJournalRecord{}, fmt.Errorf("create application journal directory: %w", err)
	}
	var result ApplicationJournalRecord
	err := withLeaseGuard(path, func() error {
		var existing ApplicationJournalRecord
		if err := readJSON(path, &existing); err == nil {
			if err := validateJournalRecord(existing); err != nil {
				return err
			}
			if existing.Kind != kind || existing.ID != id || existing.RequestHash != requestHash {
				return &JournalConflictError{Kind: kind, ID: id}
			}
			result = existing
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := s.injectFault("journal_intent", path); err != nil {
			return err
		}
		result = ApplicationJournalRecord{Version: 1, Kind: kind, ID: id, RequestHash: requestHash, Request: append(json.RawMessage(nil), request...), PlannedRunID: plannedRunID, State: JournalIntent, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
		return s.writeJSON(path, result)
	})
	return result, err
}

func (s *Store) MarkApplicationJournalMutated(kind, id, requestHash string, response json.RawMessage, now time.Time) (ApplicationJournalRecord, error) {
	if len(response) == 0 || !json.Valid(response) {
		return ApplicationJournalRecord{}, fmt.Errorf("journal response must be valid JSON")
	}
	return s.updateApplicationJournal(kind, id, requestHash, "journal_mutation", func(record *ApplicationJournalRecord) error {
		if record.State == JournalCommitted || record.State == JournalMutated {
			if !equalJSON(record.Response, response) {
				return fmt.Errorf("journal %s %q already has a different response", kind, id)
			}
			return nil
		}
		record.State = JournalMutated
		record.Response = append(json.RawMessage(nil), response...)
		record.UpdatedAt = now.UTC()
		return nil
	})
}

func equalJSON(left, right json.RawMessage) bool {
	leftValue, leftOK := decodeSingleJSON(left)
	rightValue, rightOK := decodeSingleJSON(right)
	return leftOK && rightOK && reflect.DeepEqual(leftValue, rightValue)
}

func decodeSingleJSON(data []byte) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var extra any
	return value, decoder.Decode(&extra) == io.EOF
}

func (s *Store) CommitApplicationJournal(kind, id, requestHash string, now time.Time) (ApplicationJournalRecord, error) {
	return s.updateApplicationJournal(kind, id, requestHash, "journal_commit", func(record *ApplicationJournalRecord) error {
		if record.State == JournalCommitted {
			return nil
		}
		if record.State != JournalMutated || len(record.Response) == 0 {
			return fmt.Errorf("journal %s %q cannot commit before mutation response", kind, id)
		}
		record.State = JournalCommitted
		record.UpdatedAt = now.UTC()
		return nil
	})
}

func (s *Store) updateApplicationJournal(kind, id, requestHash, faultPoint string, update func(*ApplicationJournalRecord) error) (ApplicationJournalRecord, error) {
	path := s.applicationJournalPath(kind, id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return ApplicationJournalRecord{}, fmt.Errorf("create application journal directory: %w", err)
	}
	var result ApplicationJournalRecord
	err := withLeaseGuard(path, func() error {
		if err := readJSON(path, &result); err != nil {
			return err
		}
		if err := validateJournalRecord(result); err != nil {
			return err
		}
		if result.Kind != kind || result.ID != id || result.RequestHash != requestHash {
			return &JournalConflictError{Kind: kind, ID: id}
		}
		before, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err := update(&result); err != nil {
			return err
		}
		after, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if string(before) == string(after) {
			return nil
		}
		if err := s.injectFault(faultPoint, path); err != nil {
			return err
		}
		return s.writeJSON(path, result)
	})
	return result, err
}

func (s *Store) ListPendingApplicationJournals() ([]ApplicationJournalRecord, error) {
	root := filepath.Join(s.root, "application-journal")
	records := make([]ApplicationJournalRecord, 0)
	for _, kind := range []string{"action", "start"} {
		entries, err := os.ReadDir(filepath.Join(root, kind))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("list %s application journals: %w", kind, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			var record ApplicationJournalRecord
			if err := readJSON(filepath.Join(root, kind, entry.Name()), &record); err != nil {
				return nil, err
			}
			if err := validateJournalRecord(record); err != nil {
				return nil, err
			}
			if record.State != JournalCommitted {
				records = append(records, record)
			}
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].CreatedAt.Before(records[j].CreatedAt)
		}
		if records[i].Kind != records[j].Kind {
			return records[i].Kind < records[j].Kind
		}
		return records[i].ID < records[j].ID
	})
	return records, nil
}

func (s *Store) ReadApplicationJournal(kind, id string) (ApplicationJournalRecord, error) {
	var record ApplicationJournalRecord
	if err := readJSON(s.applicationJournalPath(kind, id), &record); err != nil {
		return ApplicationJournalRecord{}, err
	}
	if err := validateJournalRecord(record); err != nil {
		return ApplicationJournalRecord{}, err
	}
	if record.Kind != kind || record.ID != id {
		return ApplicationJournalRecord{}, fmt.Errorf("application journal identity mismatch")
	}
	return record, nil
}

func (s *Store) applicationJournalPath(kind, id string) string {
	digest := sha256.Sum256([]byte(id))
	return filepath.Join(s.root, "application-journal", kind, hex.EncodeToString(digest[:])+".json")
}

func validateJournalIdentity(kind, id, requestHash string, request json.RawMessage, plannedRunID string) error {
	if kind != "start" && kind != "action" {
		return fmt.Errorf("invalid application journal kind %q", kind)
	}
	if id == "" || requestHash == "" || plannedRunID == "" || len(request) == 0 || !json.Valid(request) {
		return fmt.Errorf("application journal identity is incomplete")
	}
	return nil
}

func validateJournalRecord(record ApplicationJournalRecord) error {
	if record.Version != 1 || record.Kind == "" || record.ID == "" || record.RequestHash == "" || record.PlannedRunID == "" || len(record.Request) == 0 || !json.Valid(record.Request) || record.CreatedAt.IsZero() || record.UpdatedAt.IsZero() {
		return fmt.Errorf("application journal record is incomplete")
	}
	if record.State != JournalIntent && record.State != JournalMutated && record.State != JournalCommitted {
		return fmt.Errorf("application journal record has invalid state %q", record.State)
	}
	if (record.State == JournalMutated || record.State == JournalCommitted) && (len(record.Response) == 0 || !json.Valid(record.Response)) {
		return fmt.Errorf("application journal response is missing")
	}
	return nil
}
