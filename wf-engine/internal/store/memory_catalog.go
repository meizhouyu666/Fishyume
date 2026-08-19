package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"wf.local/wf-engine/internal/contextcompiler"
)

const (
	MemoryCatalogVersion    = "fishyume.memory-catalog/v1"
	MaxMemoryReceipts       = 4096
	MaxMemoryCatalogBytes   = 64 * 1024 * 1024
	DefaultMemoryListLimit  = 50
	MaxMemoryListLimit      = 100
	memorySourceVersion     = "fishyume.application/v1"
	memoryCatalogFile       = "catalog-v1.json"
	memoryCatalogLock       = "catalog.lock"
	memoryCatalogTempPrefix = ".catalog-v1-"
	memoryCatalogTempSuffix = ".tmp"
)

type MemoryStoreErrorCode string

const (
	MemoryStoreInvalid     MemoryStoreErrorCode = "memory_invalid_record"
	MemoryStoreConflict    MemoryStoreErrorCode = "memory_conflict"
	MemoryStoreNotFound    MemoryStoreErrorCode = "memory_not_found"
	MemoryStoreCorrupt     MemoryStoreErrorCode = "memory_catalog_corrupt"
	MemoryStoreUnsupported MemoryStoreErrorCode = "context_version_unsupported"
	MemoryStoreUnavailable MemoryStoreErrorCode = "memory_store_unavailable"
)

type MemoryStoreError struct {
	Code    MemoryStoreErrorCode
	Message string
}

func (e *MemoryStoreError) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Code) + ": " + e.Message
}

func memoryError(code MemoryStoreErrorCode, message string) error {
	return &MemoryStoreError{Code: code, Message: message}
}

type MemoryMutationReceiptV1 struct {
	MutationID  string                       `json:"mutationId"`
	RequestHash string                       `json:"requestHash"`
	Operation   string                       `json:"operation"`
	Writer      contextcompiler.MemoryWriter `json:"writer"`
	Reason      string                       `json:"reason"`
	Revision    uint64                       `json:"revision"`
	RecordID    string                       `json:"recordId"`
	AffectedIDs []string                     `json:"affectedIds"`
	CreatedAt   string                       `json:"createdAt"`
}

type MemoryCatalogV1 struct {
	SchemaVersion string                           `json:"schemaVersion"`
	Project       string                           `json:"project"`
	ProjectHash   string                           `json:"projectHash"`
	Revision      uint64                           `json:"revision"`
	Records       []contextcompiler.MemoryRecordV1 `json:"records"`
	Receipts      []MemoryMutationReceiptV1        `json:"receipts"`
}

type MemoryCreateInput struct {
	Project     string
	MutationID  string
	Type        contextcompiler.MemoryType
	Content     string
	Sensitivity contextcompiler.Sensitivity
	Writer      contextcompiler.MemoryWriter
	Reason      string
	ExpiresAt   string
	MaxUses     int
}

type MemorySupersedeInput struct {
	Project     string
	MutationID  string
	Supersedes  []string
	Type        contextcompiler.MemoryType
	Content     string
	Sensitivity contextcompiler.Sensitivity
	Writer      contextcompiler.MemoryWriter
	Reason      string
	ExpiresAt   string
	MaxUses     int
}

type MemoryDeleteInput struct {
	Project    string
	MutationID string
	RecordID   string
	Writer     contextcompiler.MemoryWriter
	Reason     string
}

type MemoryConsumeInput struct {
	Project    string
	MutationID string
	RecordIDs  []string
	Reason     string
}

type MemoryMutationResult struct {
	Revision    uint64   `json:"revision"`
	RecordID    string   `json:"recordId"`
	AffectedIDs []string `json:"affectedIds"`
	Replayed    bool     `json:"replayed"`
}

type MemoryListFilter struct {
	Type        contextcompiler.MemoryType   `json:"type,omitempty"`
	State       contextcompiler.MemoryState  `json:"state,omitempty"`
	Sensitivity contextcompiler.Sensitivity  `json:"sensitivity,omitempty"`
	Writer      contextcompiler.MemoryWriter `json:"writer,omitempty"`
}

type MemoryListInput struct {
	Project string
	Filter  MemoryListFilter
	Cursor  string
	Limit   int
}

type MemoryRecordMetadata struct {
	SchemaVersion string                             `json:"schemaVersion"`
	ID            string                             `json:"id"`
	Project       string                             `json:"project"`
	Type          contextcompiler.MemoryType         `json:"type"`
	Scope         string                             `json:"scope"`
	ContentHash   string                             `json:"contentHash"`
	Sensitivity   contextcompiler.Sensitivity        `json:"sensitivity"`
	Provenance    contextcompiler.MemoryProvenanceV1 `json:"provenance"`
	CreatedAt     string                             `json:"createdAt"`
	UpdatedAt     string                             `json:"updatedAt"`
	Supersedes    []string                           `json:"supersedes"`
	State         contextcompiler.MemoryState        `json:"state"`
	StateReason   string                             `json:"stateReason,omitempty"`
	UseCount      int                                `json:"useCount"`
	Retention     contextcompiler.MemoryRetentionV1  `json:"retention"`
}

type MemoryListResult struct {
	Revision   uint64                 `json:"revision"`
	Items      []MemoryRecordMetadata `json:"items"`
	NextCursor string                 `json:"nextCursor,omitempty"`
}

type memoryProjectIdentity struct {
	canonical  string
	normalized string
	hash       string
	directory  string
	catalog    string
	lock       string
}

type memoryListCursor struct {
	Version    int    `json:"version"`
	Revision   uint64 `json:"revision"`
	LastID     string `json:"lastId"`
	FilterHash string `json:"filterHash"`
}

type canonicalCreateRequest struct {
	Type        contextcompiler.MemoryType   `json:"type"`
	Content     string                       `json:"content"`
	Sensitivity contextcompiler.Sensitivity  `json:"sensitivity"`
	Writer      contextcompiler.MemoryWriter `json:"writer"`
	Reason      string                       `json:"reason"`
	ExpiresAt   string                       `json:"expiresAt,omitempty"`
	MaxUses     int                          `json:"maxUses,omitempty"`
}

type canonicalSupersedeRequest struct {
	Supersedes []string               `json:"supersedes"`
	Create     canonicalCreateRequest `json:"create"`
}

type canonicalDeleteRequest struct {
	RecordID string                       `json:"recordId"`
	Writer   contextcompiler.MemoryWriter `json:"writer"`
	Reason   string                       `json:"reason"`
}

func (s *Store) SetMemoryClockForTest(clock func() time.Time) {
	s.memoryClockMu.Lock()
	defer s.memoryClockMu.Unlock()
	if clock == nil {
		s.memoryClock = time.Now
		return
	}
	s.memoryClock = clock
}

func (s *Store) memoryNow() time.Time {
	s.memoryClockMu.RLock()
	clock := s.memoryClock
	s.memoryClockMu.RUnlock()
	return clock().UTC()
}

func (s *Store) MemoryCatalogPath(project string) (string, error) {
	identity, err := s.resolveMemoryProject(project)
	if err != nil {
		return "", err
	}
	return identity.catalog, nil
}

func (s *Store) CreateMemory(input MemoryCreateInput) (MemoryMutationResult, error) {
	request := canonicalCreateRequest{Type: input.Type, Content: input.Content, Sensitivity: input.Sensitivity, Writer: input.Writer, Reason: input.Reason, ExpiresAt: input.ExpiresAt, MaxUses: input.MaxUses}
	if err := validateMemoryCreateRequest(input.MutationID, request); err != nil {
		return MemoryMutationResult{}, err
	}
	return s.mutateMemory(input.Project, input.MutationID, "create", input.Writer, input.Reason, request, func(catalog *MemoryCatalogV1, identity memoryProjectIdentity, now time.Time) (string, []string, error) {
		if len(catalog.Records) >= contextcompiler.MaxProjectMemoryRecords {
			return "", nil, memoryError(MemoryStoreConflict, "project Memory record limit reached")
		}
		id := memoryRecordID(identity.hash, "create", input.MutationID)
		if findMemoryRecord(catalog.Records, id) >= 0 {
			return "", nil, memoryError(MemoryStoreConflict, "computed Memory record identity already exists")
		}
		record := buildMemoryRecord(catalog.Project, id, nil, request, now)
		if err := contextcompiler.ValidateMemoryRecordV1(record); err != nil {
			return "", nil, memoryError(MemoryStoreInvalid, err.Error())
		}
		catalog.Records = append(catalog.Records, record)
		sort.Slice(catalog.Records, func(i, j int) bool { return catalog.Records[i].ID < catalog.Records[j].ID })
		return id, []string{}, nil
	})
}

func (s *Store) SupersedeMemory(input MemorySupersedeInput) (MemoryMutationResult, error) {
	targets := append([]string(nil), input.Supersedes...)
	sort.Strings(targets)
	request := canonicalSupersedeRequest{Supersedes: targets, Create: canonicalCreateRequest{Type: input.Type, Content: input.Content, Sensitivity: input.Sensitivity, Writer: input.Writer, Reason: input.Reason, ExpiresAt: input.ExpiresAt, MaxUses: input.MaxUses}}
	if err := validateMemoryCreateRequest(input.MutationID, request.Create); err != nil {
		return MemoryMutationResult{}, err
	}
	if len(targets) < 1 || len(targets) > contextcompiler.MaxMemorySupersedes || hasDuplicateStrings(targets) {
		return MemoryMutationResult{}, memoryError(MemoryStoreInvalid, "supersedes must contain 1..16 unique record IDs")
	}
	for _, id := range targets {
		if !validMemoryRecordID(id) {
			return MemoryMutationResult{}, memoryError(MemoryStoreInvalid, "supersedes contains an invalid record ID")
		}
	}
	return s.mutateMemory(input.Project, input.MutationID, "supersede", input.Writer, input.Reason, request, func(catalog *MemoryCatalogV1, identity memoryProjectIdentity, now time.Time) (string, []string, error) {
		if len(catalog.Records) >= contextcompiler.MaxProjectMemoryRecords {
			return "", nil, memoryError(MemoryStoreConflict, "project Memory record limit reached")
		}
		indexes := make([]int, 0, len(targets))
		for _, id := range targets {
			index := findMemoryRecord(catalog.Records, id)
			if index < 0 {
				return "", nil, memoryError(MemoryStoreNotFound, "Memory record to supersede was not found")
			}
			if catalog.Records[index].State != contextcompiler.MemoryActive {
				return "", nil, memoryError(MemoryStoreConflict, "only active Memory records may be superseded")
			}
			indexes = append(indexes, index)
		}
		id := memoryRecordID(identity.hash, "supersede", input.MutationID)
		if findMemoryRecord(catalog.Records, id) >= 0 {
			return "", nil, memoryError(MemoryStoreConflict, "computed Memory record identity already exists")
		}
		replacement := buildMemoryRecord(catalog.Project, id, targets, request.Create, now)
		if err := contextcompiler.ValidateMemoryRecordV1(replacement); err != nil {
			return "", nil, memoryError(MemoryStoreInvalid, err.Error())
		}
		stamp := now.Format(time.RFC3339Nano)
		for _, index := range indexes {
			catalog.Records[index].State = contextcompiler.MemorySuperseded
			catalog.Records[index].StateReason = request.Create.Reason
			catalog.Records[index].UpdatedAt = stamp
			if err := contextcompiler.ValidateMemoryRecordV1(catalog.Records[index]); err != nil {
				return "", nil, memoryError(MemoryStoreInvalid, err.Error())
			}
		}
		catalog.Records = append(catalog.Records, replacement)
		sort.Slice(catalog.Records, func(i, j int) bool { return catalog.Records[i].ID < catalog.Records[j].ID })
		return id, targets, nil
	})
}

func (s *Store) DeleteMemory(input MemoryDeleteInput) (MemoryMutationResult, error) {
	request := canonicalDeleteRequest{RecordID: input.RecordID, Writer: input.Writer, Reason: input.Reason}
	if err := validateMutationIdentity(input.MutationID); err != nil {
		return MemoryMutationResult{}, err
	}
	if !validMemoryRecordID(request.RecordID) || !validMemoryWriter(request.Writer) || !validAuditReason(request.Reason) {
		return MemoryMutationResult{}, memoryError(MemoryStoreInvalid, "delete request is invalid")
	}
	return s.mutateMemory(input.Project, input.MutationID, "delete", input.Writer, input.Reason, request, func(catalog *MemoryCatalogV1, _ memoryProjectIdentity, now time.Time) (string, []string, error) {
		index := findMemoryRecord(catalog.Records, request.RecordID)
		if index < 0 {
			return "", nil, memoryError(MemoryStoreNotFound, "Memory record was not found")
		}
		if catalog.Records[index].State == contextcompiler.MemoryDeleted {
			return "", nil, memoryError(MemoryStoreConflict, "Memory record is already deleted")
		}
		catalog.Records[index].Content = ""
		catalog.Records[index].State = contextcompiler.MemoryDeleted
		catalog.Records[index].StateReason = request.Reason
		catalog.Records[index].UpdatedAt = now.Format(time.RFC3339Nano)
		if err := contextcompiler.ValidateMemoryRecordV1(catalog.Records[index]); err != nil {
			return "", nil, memoryError(MemoryStoreInvalid, err.Error())
		}
		return request.RecordID, []string{request.RecordID}, nil
	})
}

// ConsumeMemory reserves one use of each selected record under an idempotent
// receipt. It is engine-owned and only increments records that actually fit in
// the compiled Context Envelope.
func (s *Store) ConsumeMemory(input MemoryConsumeInput) (MemoryMutationResult, error) {
	ids := append([]string(nil), input.RecordIDs...)
	sort.Strings(ids)
	if len(ids) == 0 || len(ids) > contextcompiler.MaxSelectedMemoryRecords || hasDuplicateStrings(ids) || validateMutationIdentity(input.MutationID) != nil || !validAuditReason(input.Reason) {
		return MemoryMutationResult{}, memoryError(MemoryStoreInvalid, "Memory consume request is invalid")
	}
	for _, id := range ids {
		if !validMemoryRecordID(id) {
			return MemoryMutationResult{}, memoryError(MemoryStoreInvalid, "Memory consume contains an invalid record ID")
		}
	}
	request := struct {
		RecordIDs []string `json:"recordIds"`
		Reason    string   `json:"reason"`
	}{ids, input.Reason}
	return s.mutateMemory(input.Project, input.MutationID, "consume", contextcompiler.MemoryWriterEngine, input.Reason, request, func(catalog *MemoryCatalogV1, _ memoryProjectIdentity, now time.Time) (string, []string, error) {
		stamp := now.UTC().Format(time.RFC3339Nano)
		for _, id := range ids {
			index := findMemoryRecord(catalog.Records, id)
			if index < 0 {
				return "", nil, memoryError(MemoryStoreNotFound, "Memory record was not found")
			}
			record := &catalog.Records[index]
			if record.State != contextcompiler.MemoryActive {
				return "", nil, memoryError(MemoryStoreConflict, "selected Memory record is not active")
			}
			if record.Retention.ExpiresAt != "" {
				if expiry, err := time.Parse(time.RFC3339, record.Retention.ExpiresAt); err != nil || !expiry.After(now) {
					return "", nil, memoryError(MemoryStoreConflict, "selected Memory record is expired")
				}
			}
			if record.Retention.MaxUses > 0 && record.UseCount >= record.Retention.MaxUses {
				return "", nil, memoryError(MemoryStoreConflict, "selected Memory record has reached maxUses")
			}
		}
		for _, id := range ids {
			index := findMemoryRecord(catalog.Records, id)
			catalog.Records[index].UseCount++
			catalog.Records[index].UpdatedAt = stamp
		}
		return ids[0], ids[1:], nil
	})
}

func (s *Store) GetMemory(project, recordID string) (contextcompiler.MemoryRecordV1, uint64, error) {
	if !validMemoryRecordID(recordID) {
		return contextcompiler.MemoryRecordV1{}, 0, memoryError(MemoryStoreInvalid, "Memory record ID is invalid")
	}
	identity, err := s.resolveMemoryProject(project)
	if err != nil {
		return contextcompiler.MemoryRecordV1{}, 0, err
	}
	var record contextcompiler.MemoryRecordV1
	var revision uint64
	err = s.withMemoryCatalog(identity, func(catalog *MemoryCatalogV1) error {
		index := findMemoryRecord(catalog.Records, recordID)
		if index < 0 {
			return memoryError(MemoryStoreNotFound, "Memory record was not found")
		}
		record = cloneMemoryRecord(catalog.Records[index])
		revision = catalog.Revision
		return nil
	})
	return record, revision, err
}

func (s *Store) ListMemory(input MemoryListInput) (MemoryListResult, error) {
	limit := input.Limit
	if limit == 0 {
		limit = DefaultMemoryListLimit
	}
	if limit < 1 || limit > MaxMemoryListLimit {
		return MemoryListResult{}, memoryError(MemoryStoreInvalid, "Memory list limit must be between 1 and 100")
	}
	if err := validateMemoryFilter(input.Filter); err != nil {
		return MemoryListResult{}, err
	}
	filterHash, err := canonicalHash(input.Filter)
	if err != nil {
		return MemoryListResult{}, memoryError(MemoryStoreInvalid, "Memory filter cannot be encoded")
	}
	cursor, err := decodeMemoryCursor(input.Cursor)
	if err != nil {
		return MemoryListResult{}, err
	}
	identity, err := s.resolveMemoryProject(input.Project)
	if err != nil {
		return MemoryListResult{}, err
	}
	result := MemoryListResult{Items: []MemoryRecordMetadata{}}
	err = s.withMemoryCatalog(identity, func(catalog *MemoryCatalogV1) error {
		if input.Cursor != "" && (cursor.Revision != catalog.Revision || cursor.FilterHash != filterHash) {
			return memoryError(MemoryStoreConflict, "Memory list cursor no longer matches the catalog revision or filter")
		}
		result.Revision = catalog.Revision
		matches := make([]contextcompiler.MemoryRecordV1, 0, len(catalog.Records))
		for _, record := range catalog.Records {
			if record.ID > cursor.LastID && memoryMatches(record, input.Filter) {
				matches = append(matches, record)
			}
		}
		count := min(limit, len(matches))
		for _, record := range matches[:count] {
			result.Items = append(result.Items, memoryMetadata(record))
		}
		if len(matches) > count {
			encoded, encodeErr := encodeMemoryCursor(memoryListCursor{Version: 1, Revision: catalog.Revision, LastID: matches[count-1].ID, FilterHash: filterHash})
			if encodeErr != nil {
				return encodeErr
			}
			result.NextCursor = encoded
		}
		return nil
	})
	return result, err
}

func (s *Store) mutateMemory(project, mutationID, operation string, writer contextcompiler.MemoryWriter, reason string, request any, apply func(*MemoryCatalogV1, memoryProjectIdentity, time.Time) (string, []string, error)) (MemoryMutationResult, error) {
	identity, err := s.resolveMemoryProject(project)
	if err != nil {
		return MemoryMutationResult{}, err
	}
	requestHash, err := canonicalHash(request)
	if err != nil {
		return MemoryMutationResult{}, memoryError(MemoryStoreInvalid, "Memory mutation request cannot be encoded")
	}
	var result MemoryMutationResult
	err = s.withMemoryCatalog(identity, func(catalog *MemoryCatalogV1) error {
		if receipt, found := findMemoryReceipt(catalog.Receipts, mutationID); found {
			if receipt.Operation != operation || receipt.RequestHash != requestHash {
				return memoryError(MemoryStoreConflict, "mutationId is already bound to a different canonical request")
			}
			result = MemoryMutationResult{Revision: receipt.Revision, RecordID: receipt.RecordID, AffectedIDs: append([]string{}, receipt.AffectedIDs...), Replayed: true}
			return nil
		}
		now := s.memoryNow()
		recordID, affectedIDs, applyErr := apply(catalog, identity, now)
		if applyErr != nil {
			return applyErr
		}
		catalog.Revision++
		receipt := MemoryMutationReceiptV1{MutationID: mutationID, RequestHash: requestHash, Operation: operation, Writer: writer, Reason: reason, Revision: catalog.Revision, RecordID: recordID, AffectedIDs: append([]string{}, affectedIDs...), CreatedAt: now.Format(time.RFC3339Nano)}
		catalog.Receipts = append(catalog.Receipts, receipt)
		if len(catalog.Receipts) > MaxMemoryReceipts {
			catalog.Receipts = append([]MemoryMutationReceiptV1(nil), catalog.Receipts[len(catalog.Receipts)-MaxMemoryReceipts:]...)
		}
		if err := validateMemoryCatalog(*catalog, identity); err != nil {
			return err
		}
		if err := s.writeMemoryCatalog(identity, *catalog); err != nil {
			return err
		}
		result = MemoryMutationResult{Revision: catalog.Revision, RecordID: recordID, AffectedIDs: append([]string{}, affectedIDs...), Replayed: false}
		return nil
	})
	return result, err
}

func (s *Store) resolveMemoryProject(project string) (memoryProjectIdentity, error) {
	if !utf8.ValidString(project) || strings.TrimSpace(project) == "" || project != strings.TrimSpace(project) || len([]byte(project)) > 4096 {
		return memoryProjectIdentity{}, memoryError(MemoryStoreInvalid, "project must be a bounded canonicalizable directory path")
	}
	absolute, err := filepath.Abs(project)
	if err != nil {
		return memoryProjectIdentity{}, memoryError(MemoryStoreInvalid, "project path cannot be made absolute")
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return memoryProjectIdentity{}, memoryError(MemoryStoreInvalid, "project path cannot be resolved")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return memoryProjectIdentity{}, memoryError(MemoryStoreInvalid, "project path is not a directory")
	}
	canonical = filepath.Clean(canonical)
	normalized := normalizeMemoryProject(canonical)
	digest := sha256.Sum256([]byte(normalized))
	hash := hex.EncodeToString(digest[:])
	directory := filepath.Join(s.root, "memory", "projects", hash)
	return memoryProjectIdentity{canonical: canonical, normalized: normalized, hash: hash, directory: directory, catalog: filepath.Join(directory, memoryCatalogFile), lock: filepath.Join(directory, memoryCatalogLock)}, nil
}

func normalizeMemoryProject(project string) string {
	normalized := filepath.ToSlash(filepath.Clean(project))
	if runtime.GOOS == "windows" {
		normalized = strings.ToLower(normalized)
	}
	return normalized
}

func (s *Store) withMemoryCatalog(identity memoryProjectIdentity, action func(*MemoryCatalogV1) error) error {
	if err := os.MkdirAll(identity.directory, 0o700); err != nil {
		return memoryError(MemoryStoreUnavailable, "Memory project directory cannot be created")
	}
	err := withFileLock(identity.lock, func() error {
		if err := cleanupMemoryTemps(identity.directory); err != nil {
			return memoryError(MemoryStoreUnavailable, "stale Memory catalog temporary files cannot be removed")
		}
		catalog, err := readMemoryCatalog(identity)
		if err != nil {
			return err
		}
		return action(&catalog)
	})
	if err == nil {
		return nil
	}
	var storeErr *MemoryStoreError
	if errors.As(err, &storeErr) {
		return err
	}
	return memoryError(MemoryStoreUnavailable, "Memory catalog lock cannot be acquired")
}

func readMemoryCatalog(identity memoryProjectIdentity) (MemoryCatalogV1, error) {
	file, err := os.Open(identity.catalog)
	if errors.Is(err, os.ErrNotExist) {
		return MemoryCatalogV1{SchemaVersion: MemoryCatalogVersion, Project: identity.canonical, ProjectHash: identity.hash, Records: []contextcompiler.MemoryRecordV1{}, Receipts: []MemoryMutationReceiptV1{}}, nil
	}
	if err != nil {
		return MemoryCatalogV1{}, memoryError(MemoryStoreUnavailable, "Memory catalog cannot be read")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return MemoryCatalogV1{}, memoryError(MemoryStoreUnavailable, "Memory catalog metadata cannot be read")
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > MaxMemoryCatalogBytes {
		return MemoryCatalogV1{}, memoryError(MemoryStoreCorrupt, "Memory catalog is not a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxMemoryCatalogBytes+1))
	if err != nil {
		return MemoryCatalogV1{}, memoryError(MemoryStoreUnavailable, "Memory catalog cannot be read within its bound")
	}
	if len(data) == 0 || len(data) > MaxMemoryCatalogBytes || !utf8.Valid(data) || !json.Valid(data) {
		return MemoryCatalogV1{}, memoryError(MemoryStoreCorrupt, "Memory catalog is empty, oversized, invalid UTF-8, or malformed JSON")
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return MemoryCatalogV1{}, memoryError(MemoryStoreCorrupt, "Memory catalog contains duplicate JSON keys")
	}
	var catalog MemoryCatalogV1
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return MemoryCatalogV1{}, memoryError(MemoryStoreCorrupt, "Memory catalog does not match its strict schema")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return MemoryCatalogV1{}, memoryError(MemoryStoreCorrupt, "Memory catalog contains trailing JSON data")
	}
	if err := validateMemoryCatalog(catalog, identity); err != nil {
		return MemoryCatalogV1{}, err
	}
	return catalog, nil
}

func validateMemoryCatalog(catalog MemoryCatalogV1, identity memoryProjectIdentity) error {
	if catalog.SchemaVersion != MemoryCatalogVersion {
		return memoryError(MemoryStoreUnsupported, "Memory catalog schema version is unsupported")
	}
	if !utf8.ValidString(catalog.Project) || normalizeMemoryProject(catalog.Project) != identity.normalized || catalog.ProjectHash != identity.hash {
		return memoryError(MemoryStoreCorrupt, "Memory catalog project identity does not match its directory")
	}
	if catalog.Records == nil || catalog.Receipts == nil || len(catalog.Records) > contextcompiler.MaxProjectMemoryRecords || len(catalog.Receipts) > MaxMemoryReceipts {
		return memoryError(MemoryStoreCorrupt, "Memory catalog collections are missing or exceed their bounds")
	}
	recordIDs := make(map[string]struct{}, len(catalog.Records))
	lastID := ""
	for _, record := range catalog.Records {
		if record.ID <= lastID {
			return memoryError(MemoryStoreCorrupt, "Memory catalog records are duplicated or not in canonical order")
		}
		if normalizeMemoryProject(record.Project) != identity.normalized || record.Scope != "project" || !utf8.ValidString(record.Content) || record.Supersedes == nil {
			return memoryError(MemoryStoreCorrupt, "Memory record project, scope, or content encoding is invalid")
		}
		if err := contextcompiler.ValidateMemoryRecordV1(record); err != nil {
			return memoryError(MemoryStoreCorrupt, "Memory catalog contains an invalid record")
		}
		recordIDs[record.ID] = struct{}{}
		lastID = record.ID
	}
	receiptIDs := make(map[string]struct{}, len(catalog.Receipts))
	var lastRevision uint64
	for _, receipt := range catalog.Receipts {
		if err := validateMutationIdentity(receipt.MutationID); err != nil || !validHashString(receipt.RequestHash) || !validMemoryReceiptWriter(receipt.Writer) || !validAuditReason(receipt.Reason) || receipt.Revision == 0 || receipt.Revision > catalog.Revision || receipt.Revision <= lastRevision || !validReceiptOperation(receipt.Operation) || !validMemoryRecordID(receipt.RecordID) || receipt.AffectedIDs == nil || !sort.StringsAreSorted(receipt.AffectedIDs) || hasDuplicateStrings(receipt.AffectedIDs) {
			return memoryError(MemoryStoreCorrupt, "Memory catalog contains an invalid receipt")
		}
		if _, exists := receiptIDs[receipt.MutationID]; exists {
			return memoryError(MemoryStoreCorrupt, "Memory catalog contains duplicate mutation receipts")
		}
		if _, exists := recordIDs[receipt.RecordID]; !exists {
			return memoryError(MemoryStoreCorrupt, "Memory receipt references a missing record")
		}
		for _, affectedID := range receipt.AffectedIDs {
			if !validMemoryRecordID(affectedID) {
				return memoryError(MemoryStoreCorrupt, "Memory receipt contains an invalid affected record ID")
			}
			if _, exists := recordIDs[affectedID]; !exists {
				return memoryError(MemoryStoreCorrupt, "Memory receipt references a missing affected record")
			}
		}
		switch receipt.Operation {
		case "create":
			if len(receipt.AffectedIDs) != 0 {
				return memoryError(MemoryStoreCorrupt, "Memory create receipt has affected records")
			}
		case "supersede":
			record := catalog.Records[findMemoryRecord(catalog.Records, receipt.RecordID)]
			if len(receipt.AffectedIDs) < 1 || len(receipt.AffectedIDs) > contextcompiler.MaxMemorySupersedes || !equalStrings(record.Supersedes, receipt.AffectedIDs) {
				return memoryError(MemoryStoreCorrupt, "Memory supersede receipt does not match its replacement")
			}
		case "delete":
			if len(receipt.AffectedIDs) != 1 || receipt.AffectedIDs[0] != receipt.RecordID {
				return memoryError(MemoryStoreCorrupt, "Memory delete receipt does not match its tombstone")
			}
		case "consume":
			if receipt.Writer != contextcompiler.MemoryWriterEngine || len(receipt.AffectedIDs) > contextcompiler.MaxSelectedMemoryRecords-1 || (len(receipt.AffectedIDs) > 0 && receipt.RecordID >= receipt.AffectedIDs[0]) {
				return memoryError(MemoryStoreCorrupt, "Memory consume receipt exceeds its bound")
			}
		default:
			if receipt.Writer == contextcompiler.MemoryWriterEngine {
				return memoryError(MemoryStoreCorrupt, "Memory engine writer is only valid for consume receipts")
			}
		}
		if _, err := time.Parse(time.RFC3339, receipt.CreatedAt); err != nil {
			return memoryError(MemoryStoreCorrupt, "Memory receipt timestamp is invalid")
		}
		receiptIDs[receipt.MutationID] = struct{}{}
		lastRevision = receipt.Revision
	}
	if (catalog.Revision == 0 && (len(catalog.Records) != 0 || len(catalog.Receipts) != 0)) || (catalog.Revision > 0 && (len(catalog.Receipts) == 0 || lastRevision != catalog.Revision)) {
		return memoryError(MemoryStoreCorrupt, "Memory catalog revision does not match its durable receipts")
	}
	return nil
}

func (s *Store) writeMemoryCatalog(identity memoryProjectIdentity, catalog MemoryCatalogV1) error {
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil || len(data)+1 > MaxMemoryCatalogBytes {
		return memoryError(MemoryStoreUnavailable, "Memory catalog cannot be encoded within its bound")
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(identity.directory, memoryCatalogTempPrefix+"*"+memoryCatalogTempSuffix)
	if err != nil {
		return memoryError(MemoryStoreUnavailable, "Memory catalog temporary file cannot be created")
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return memoryError(MemoryStoreUnavailable, "Memory catalog temporary permissions cannot be set")
	}
	if _, err := temporary.Write(data); err != nil {
		return memoryError(MemoryStoreUnavailable, "Memory catalog temporary file cannot be written")
	}
	if err := s.injectFault("memory_after_temp_write", temporaryPath); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return memoryError(MemoryStoreUnavailable, "Memory catalog temporary file cannot be synchronized")
	}
	if err := s.injectFault("memory_after_temp_sync", temporaryPath); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return memoryError(MemoryStoreUnavailable, "Memory catalog temporary file cannot be closed")
	}
	if err := s.injectFault("memory_before_replace", identity.catalog); err != nil {
		return err
	}
	if err := replaceFile(temporaryPath, identity.catalog); err != nil {
		return memoryError(MemoryStoreUnavailable, "Memory catalog cannot be atomically replaced")
	}
	committed = true
	if err := s.injectFault("memory_after_replace_before_directory_sync", identity.catalog); err != nil {
		return err
	}
	if err := syncDirectory(identity.directory); err != nil {
		return memoryError(MemoryStoreUnavailable, "Memory catalog directory cannot be synchronized")
	}
	if err := s.injectFault("memory_after_replace", identity.catalog); err != nil {
		return err
	}
	return nil
}

func cleanupMemoryTemps(directory string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), memoryCatalogTempPrefix) || !strings.HasSuffix(entry.Name(), memoryCatalogTempSuffix) {
			continue
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func buildMemoryRecord(project, id string, supersedes []string, request canonicalCreateRequest, now time.Time) contextcompiler.MemoryRecordV1 {
	source := "fishyume.cli"
	if request.Writer == contextcompiler.MemoryWriterHostAgent {
		source = "fishyume.mcp"
	} else if request.Writer == contextcompiler.MemoryWriterMigration {
		source = "fishyume.migration"
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	return contextcompiler.MemoryRecordV1{
		SchemaVersion: contextcompiler.MemoryRecordV1Version,
		ID:            id, Project: project, Type: request.Type, Scope: "project", Content: request.Content,
		ContentHash: hashString(request.Content), Sensitivity: request.Sensitivity,
		Provenance: contextcompiler.MemoryProvenanceV1{Writer: request.Writer, Source: source, SourceVersion: memorySourceVersion, SourceHash: hashString(source + "\x00" + memorySourceVersion), Reason: request.Reason},
		CreatedAt:  stamp, UpdatedAt: stamp, Supersedes: append([]string{}, supersedes...), State: contextcompiler.MemoryActive, UseCount: 0,
		Retention: contextcompiler.MemoryRetentionV1{ExpiresAt: request.ExpiresAt, MaxUses: request.MaxUses},
	}
}

func validateMemoryCreateRequest(mutationID string, request canonicalCreateRequest) error {
	if err := validateMutationIdentity(mutationID); err != nil {
		return err
	}
	if !utf8.ValidString(request.Content) || strings.TrimSpace(request.Content) == "" || len([]byte(request.Content)) > contextcompiler.MaxMemoryContentBytes {
		return memoryError(MemoryStoreInvalid, "Memory content must be non-empty valid UTF-8 within 16 KiB")
	}
	if !validMemoryType(request.Type) || (request.Sensitivity != contextcompiler.SensitivityPublic && request.Sensitivity != contextcompiler.SensitivityProject) || !validMemoryWriter(request.Writer) || !validAuditReason(request.Reason) || request.MaxUses < 0 || request.MaxUses > 10000 {
		return memoryError(MemoryStoreInvalid, "Memory type, sensitivity, writer, reason, or retention is invalid")
	}
	if request.ExpiresAt != "" {
		if _, err := time.Parse(time.RFC3339, request.ExpiresAt); err != nil {
			return memoryError(MemoryStoreInvalid, "Memory expiry must be RFC3339")
		}
	}
	return nil
}

func validateMutationIdentity(value string) error {
	if !utf8.ValidString(value) || strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || len([]byte(value)) > 256 {
		return memoryError(MemoryStoreInvalid, "mutationId must be non-empty valid UTF-8 within 256 bytes")
	}
	return nil
}

func validAuditReason(value string) bool {
	return utf8.ValidString(value) && strings.TrimSpace(value) != "" && value == strings.TrimSpace(value) && len([]byte(value)) <= 1024
}

func validMemoryWriter(value contextcompiler.MemoryWriter) bool {
	return value == contextcompiler.MemoryWriterUser || value == contextcompiler.MemoryWriterHostAgent || value == contextcompiler.MemoryWriterMigration
}

func validMemoryReceiptWriter(value contextcompiler.MemoryWriter) bool {
	return validMemoryWriter(value) || value == contextcompiler.MemoryWriterEngine
}

func validMemoryType(value contextcompiler.MemoryType) bool {
	switch value {
	case contextcompiler.MemoryDecision, contextcompiler.MemoryConstraint, contextcompiler.MemoryFact, contextcompiler.MemoryProcedure, contextcompiler.MemoryPreference:
		return true
	default:
		return false
	}
}

func validateMemoryFilter(filter MemoryListFilter) error {
	if filter.Type != "" && !validMemoryType(filter.Type) {
		return memoryError(MemoryStoreInvalid, "Memory type filter is invalid")
	}
	if filter.State != "" && filter.State != contextcompiler.MemoryActive && filter.State != contextcompiler.MemorySuperseded && filter.State != contextcompiler.MemoryDeleted {
		return memoryError(MemoryStoreInvalid, "Memory state filter is invalid")
	}
	if filter.Sensitivity != "" && filter.Sensitivity != contextcompiler.SensitivityPublic && filter.Sensitivity != contextcompiler.SensitivityProject {
		return memoryError(MemoryStoreInvalid, "Memory sensitivity filter is invalid")
	}
	if filter.Writer != "" && !validMemoryWriter(filter.Writer) {
		return memoryError(MemoryStoreInvalid, "Memory writer filter is invalid")
	}
	return nil
}

func memoryMatches(record contextcompiler.MemoryRecordV1, filter MemoryListFilter) bool {
	return (filter.Type == "" || record.Type == filter.Type) && (filter.State == "" || record.State == filter.State) && (filter.Sensitivity == "" || record.Sensitivity == filter.Sensitivity) && (filter.Writer == "" || record.Provenance.Writer == filter.Writer)
}

func memoryMetadata(record contextcompiler.MemoryRecordV1) MemoryRecordMetadata {
	return MemoryRecordMetadata{SchemaVersion: record.SchemaVersion, ID: record.ID, Project: record.Project, Type: record.Type, Scope: record.Scope, ContentHash: record.ContentHash, Sensitivity: record.Sensitivity, Provenance: record.Provenance, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, Supersedes: append([]string{}, record.Supersedes...), State: record.State, StateReason: record.StateReason, UseCount: record.UseCount, Retention: record.Retention}
}

func cloneMemoryRecord(record contextcompiler.MemoryRecordV1) contextcompiler.MemoryRecordV1 {
	record.Supersedes = append([]string{}, record.Supersedes...)
	return record
}

func findMemoryRecord(records []contextcompiler.MemoryRecordV1, id string) int {
	index := sort.Search(len(records), func(index int) bool { return records[index].ID >= id })
	if index < len(records) && records[index].ID == id {
		return index
	}
	return -1
}

func findMemoryReceipt(receipts []MemoryMutationReceiptV1, mutationID string) (MemoryMutationReceiptV1, bool) {
	for _, receipt := range receipts {
		if receipt.MutationID == mutationID {
			return receipt, true
		}
	}
	return MemoryMutationReceiptV1{}, false
}

func memoryRecordID(projectHash, operation, mutationID string) string {
	digest := sha256.Sum256([]byte(projectHash + "\x00" + operation + "\x00" + mutationID))
	return "memory-" + hex.EncodeToString(digest[:16])
}

func validMemoryRecordID(value string) bool {
	if len(value) < 1 || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}

func validReceiptOperation(value string) bool {
	return value == "create" || value == "supersede" || value == "delete" || value == "consume"
}

func hasDuplicateStrings(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return true
		}
	}
	return false
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func hashString(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func validHashString(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func canonicalHash(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return hashString(string(data)), nil
}

func encodeMemoryCursor(cursor memoryListCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeMemoryCursor(value string) (memoryListCursor, error) {
	if value == "" {
		return memoryListCursor{Version: 1}, nil
	}
	if len(value) > 1024 {
		return memoryListCursor{}, memoryError(MemoryStoreInvalid, "Memory list cursor is invalid")
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || !utf8.Valid(data) || !json.Valid(data) || rejectDuplicateJSONKeys(data) != nil {
		return memoryListCursor{}, memoryError(MemoryStoreInvalid, "Memory list cursor is invalid")
	}
	var cursor memoryListCursor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil || ensureJSONEOF(decoder) != nil || cursor.Version != 1 || cursor.Revision == 0 || !validMemoryRecordID(cursor.LastID) || !validHashString(cursor.FilterHash) {
		return memoryListCursor{}, memoryError(MemoryStoreInvalid, "Memory list cursor is invalid")
	}
	return cursor, nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := readUniqueJSONValue(decoder); err != nil {
		return err
	}
	return ensureJSONEOF(decoder)
}

func readUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate key %q", key)
			}
			seen[key] = struct{}{}
			if err := readUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := readUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}
