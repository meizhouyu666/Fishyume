package contextcompiler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"wf.local/wf-engine/internal/agent"
)

const (
	EnvelopeV2Version        = "fishyume.context/v2"
	CompilerV2Version        = "context-compiler/v2"
	ManifestV2Version        = "fishyume.context-manifest/v2"
	MemoryRecordV1Version    = "fishyume.memory/v1"
	MaxContextPayloadBytes   = 128 * 1024
	MaxContextComponents     = 128
	MaxContextOmissions      = 256
	MaxComponentContentBytes = 64 * 1024
	MaxMemoryContentBytes    = 16 * 1024
	MaxSelectedMemoryRecords = 32
	MaxProjectMemoryRecords  = 2048
	MaxProvenanceRefBytes    = 1024
	MaxMemorySupersedes      = 16
)

type ContextErrorCode string

const (
	CodeContextInvalidComponent    ContextErrorCode = "context_invalid_component"
	CodeContextRequiredMissing     ContextErrorCode = "context_required_missing"
	CodeContextBudgetUnsatisfiable ContextErrorCode = "context_budget_unsatisfiable"
	CodeContextSourceUnavailable   ContextErrorCode = "context_source_unavailable"
	CodeContextHashMismatch        ContextErrorCode = "context_hash_mismatch"
	CodeContextVersionUnsupported  ContextErrorCode = "context_version_unsupported"
	CodeMemoryInvalidRecord        ContextErrorCode = "memory_invalid_record"
	CodeMemoryConflict             ContextErrorCode = "memory_conflict"
	CodeMemoryNotFound             ContextErrorCode = "memory_not_found"
)

var StableContextErrorCodes = []ContextErrorCode{
	CodeContextInvalidComponent,
	CodeContextRequiredMissing,
	CodeContextBudgetUnsatisfiable,
	CodeContextSourceUnavailable,
	CodeContextHashMismatch,
	CodeContextVersionUnsupported,
	CodeMemoryInvalidRecord,
	CodeMemoryConflict,
	CodeMemoryNotFound,
}

type ContractError struct {
	Code      ContextErrorCode `json:"code"`
	Message   string           `json:"message"`
	SubjectID string           `json:"subjectId,omitempty"`
}

func (e *ContractError) Error() string {
	if e == nil {
		return ""
	}
	if e.SubjectID != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.SubjectID)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func contractError(code ContextErrorCode, message, subjectID string) error {
	return &ContractError{Code: code, Message: message, SubjectID: subjectID}
}

type AttentionTier string

const (
	TierRequired  AttentionTier = "required"
	TierImportant AttentionTier = "important"
	TierOptional  AttentionTier = "optional"
)

type Sensitivity string

const (
	SensitivityPublic    Sensitivity = "public"
	SensitivityProject   Sensitivity = "project"
	SensitivitySensitive Sensitivity = "sensitive"
)

type ComponentKind string

const (
	KindExecutionContract   ComponentKind = "execution_contract"
	KindProjectInstructions ComponentKind = "project_instructions"
	KindWorkflowPolicy      ComponentKind = "workflow_policy"
	KindNodeTask            ComponentKind = "node_task"
	KindUserAnswer          ComponentKind = "user_answer"
	KindDependencyResult    ComponentKind = "dependency_result"
	KindSkillInstructions   ComponentKind = "skill_instructions"
	KindMemory              ComponentKind = "memory"
	KindOutputContract      ComponentKind = "output_contract"
)

type TruncationMode string

const (
	TruncationNone TruncationMode = "none"
	TruncationTail TruncationMode = "tail"
)

type OmissionReason string

const (
	OmissionBudgetExhausted   OmissionReason = "budget_exhausted"
	OmissionSuperseded        OmissionReason = "superseded"
	OmissionExpired           OmissionReason = "expired"
	OmissionIrrelevant        OmissionReason = "irrelevant"
	OmissionUnavailable       OmissionReason = "unavailable"
	OmissionSensitivityPolicy OmissionReason = "sensitivity_policy"
	OmissionDuplicate         OmissionReason = "duplicate"
)

type ComponentProvenanceV2 struct {
	Source        string `json:"source"`
	SourceVersion string `json:"sourceVersion"`
	SourceHash    string `json:"sourceHash"`
	Reason        string `json:"reason"`
}

type ContextComponentV2 struct {
	ID            string                `json:"id"`
	Kind          ComponentKind         `json:"kind"`
	Tier          AttentionTier         `json:"tier"`
	Sensitivity   Sensitivity           `json:"sensitivity"`
	Provenance    ComponentProvenanceV2 `json:"provenance"`
	Content       string                `json:"content"`
	ContentHash   string                `json:"contentHash"`
	OriginalBytes int                   `json:"originalBytes"`
	IncludedBytes int                   `json:"includedBytes"`
	Truncation    TruncationMode        `json:"truncation"`
}

type ContextOmissionV2 struct {
	ComponentID   string         `json:"componentId"`
	Kind          ComponentKind  `json:"kind"`
	Tier          AttentionTier  `json:"tier"`
	Reason        OmissionReason `json:"reason"`
	SourceHash    string         `json:"sourceHash"`
	OriginalBytes int            `json:"originalBytes"`
}

type AttentionBudgetV2 struct {
	TotalBytes     int `json:"totalBytes"`
	RequiredBytes  int `json:"requiredBytes"`
	ImportantBytes int `json:"importantBytes"`
	OptionalBytes  int `json:"optionalBytes"`
}

type ContextEnvelopeV2 struct {
	SchemaVersion   string                `json:"schemaVersion"`
	CompilerVersion string                `json:"compilerVersion"`
	Identity        agent.AttemptIdentity `json:"identity"`
	Budget          AttentionBudgetV2     `json:"budget"`
	Components      []ContextComponentV2  `json:"components"`
	Omissions       []ContextOmissionV2   `json:"omissions"`
}

type ContextComponentManifestV2 struct {
	ID            string                `json:"id"`
	Kind          ComponentKind         `json:"kind"`
	Tier          AttentionTier         `json:"tier"`
	Sensitivity   Sensitivity           `json:"sensitivity"`
	Provenance    ComponentProvenanceV2 `json:"provenance"`
	ContentHash   string                `json:"contentHash"`
	OriginalBytes int                   `json:"originalBytes"`
	IncludedBytes int                   `json:"includedBytes"`
	Truncation    TruncationMode        `json:"truncation"`
}

type AttentionUsageV2 struct {
	TotalBytes     int `json:"totalBytes"`
	RequiredBytes  int `json:"requiredBytes"`
	ImportantBytes int `json:"importantBytes"`
	OptionalBytes  int `json:"optionalBytes"`
}

type ContextManifestV2 struct {
	SchemaVersion   string                       `json:"schemaVersion"`
	CompilerVersion string                       `json:"compilerVersion"`
	EnvelopeHash    string                       `json:"envelopeHash"`
	Budget          AttentionBudgetV2            `json:"budget"`
	Usage           AttentionUsageV2             `json:"usage"`
	Components      []ContextComponentManifestV2 `json:"components"`
	Omissions       []ContextOmissionV2          `json:"omissions"`
}

type MemoryType string

const (
	MemoryDecision   MemoryType = "decision"
	MemoryConstraint MemoryType = "constraint"
	MemoryFact       MemoryType = "fact"
	MemoryProcedure  MemoryType = "procedure"
	MemoryPreference MemoryType = "preference"
)

type MemoryState string

const (
	MemoryActive     MemoryState = "active"
	MemorySuperseded MemoryState = "superseded"
	MemoryDeleted    MemoryState = "deleted"
)

type MemoryWriter string

const (
	MemoryWriterUser      MemoryWriter = "user"
	MemoryWriterHostAgent MemoryWriter = "host_agent"
	MemoryWriterMigration MemoryWriter = "migration"
)

type MemoryProvenanceV1 struct {
	Writer        MemoryWriter `json:"writer"`
	Source        string       `json:"source"`
	SourceVersion string       `json:"sourceVersion"`
	SourceHash    string       `json:"sourceHash"`
	Reason        string       `json:"reason"`
}

type MemoryRetentionV1 struct {
	ExpiresAt string `json:"expiresAt,omitempty"`
	MaxUses   int    `json:"maxUses,omitempty"`
}

type MemoryRecordV1 struct {
	SchemaVersion string             `json:"schemaVersion"`
	ID            string             `json:"id"`
	Project       string             `json:"project"`
	Type          MemoryType         `json:"type"`
	Scope         string             `json:"scope"`
	Content       string             `json:"content,omitempty"`
	ContentHash   string             `json:"contentHash"`
	Sensitivity   Sensitivity        `json:"sensitivity"`
	Provenance    MemoryProvenanceV1 `json:"provenance"`
	CreatedAt     string             `json:"createdAt"`
	UpdatedAt     string             `json:"updatedAt"`
	Supersedes    []string           `json:"supersedes"`
	State         MemoryState        `json:"state"`
	StateReason   string             `json:"stateReason,omitempty"`
	UseCount      int                `json:"useCount"`
	Retention     MemoryRetentionV1  `json:"retention"`
}

type ContractLimitsV2 struct {
	MaxContextPayloadBytes   int `json:"maxContextPayloadBytes"`
	MaxContextComponents     int `json:"maxContextComponents"`
	MaxContextOmissions      int `json:"maxContextOmissions"`
	MaxComponentContentBytes int `json:"maxComponentContentBytes"`
	MaxMemoryContentBytes    int `json:"maxMemoryContentBytes"`
	MaxSelectedMemoryRecords int `json:"maxSelectedMemoryRecords"`
	MaxProjectMemoryRecords  int `json:"maxProjectMemoryRecords"`
	MaxProvenanceRefBytes    int `json:"maxProvenanceRefBytes"`
	MaxMemorySupersedes      int `json:"maxMemorySupersedes"`
}

func StableContractLimitsV2() ContractLimitsV2 {
	return ContractLimitsV2{
		MaxContextPayloadBytes: MaxContextPayloadBytes, MaxContextComponents: MaxContextComponents,
		MaxContextOmissions: MaxContextOmissions, MaxComponentContentBytes: MaxComponentContentBytes,
		MaxMemoryContentBytes: MaxMemoryContentBytes, MaxSelectedMemoryRecords: MaxSelectedMemoryRecords,
		MaxProjectMemoryRecords: MaxProjectMemoryRecords, MaxProvenanceRefBytes: MaxProvenanceRefBytes,
		MaxMemorySupersedes: MaxMemorySupersedes,
	}
}

var contractIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)

func ValidateContextEnvelopeV2(envelope ContextEnvelopeV2) error {
	if envelope.SchemaVersion != EnvelopeV2Version || envelope.CompilerVersion != CompilerV2Version {
		return contractError(CodeContextVersionUnsupported, "unsupported Context Envelope or compiler version", "")
	}
	if strings.TrimSpace(envelope.Identity.RunID) == "" || strings.TrimSpace(envelope.Identity.NodeID) == "" || envelope.Identity.Attempt < 1 {
		return contractError(CodeContextInvalidComponent, "Attempt identity is incomplete", "")
	}
	if err := validateBudgetV2(envelope.Budget); err != nil {
		return err
	}
	if len(envelope.Components) == 0 || len(envelope.Components) > MaxContextComponents {
		return contractError(CodeContextInvalidComponent, "Context component count is outside its bounds", "")
	}
	if len(envelope.Omissions) > MaxContextOmissions {
		return contractError(CodeContextInvalidComponent, "Context omission count exceeds its bound", "")
	}
	seen := make(map[string]struct{}, len(envelope.Components)+len(envelope.Omissions))
	kinds := make(map[ComponentKind]bool)
	usage := AttentionUsageV2{}
	lastRank, lastID := -1, ""
	memoryCount := 0
	for _, component := range envelope.Components {
		if err := validateComponentV2(component); err != nil {
			return err
		}
		if _, exists := seen[component.ID]; exists {
			return contractError(CodeContextInvalidComponent, "Context component ID is duplicated", component.ID)
		}
		seen[component.ID] = struct{}{}
		rank := componentRank(component.Kind)
		if rank < lastRank || (rank == lastRank && component.ID <= lastID) {
			return contractError(CodeContextInvalidComponent, "Context components are not in canonical order", component.ID)
		}
		lastRank, lastID = rank, component.ID
		kinds[component.Kind] = true
		addUsage(&usage, component.Tier, component.IncludedBytes)
		if component.Kind == KindMemory {
			memoryCount++
		}
	}
	for _, required := range []ComponentKind{KindExecutionContract, KindNodeTask, KindOutputContract} {
		if !kinds[required] {
			return contractError(CodeContextRequiredMissing, fmt.Sprintf("required component kind %q is missing", required), "")
		}
	}
	if memoryCount > MaxSelectedMemoryRecords {
		return contractError(CodeContextBudgetUnsatisfiable, "selected Memory record count exceeds its bound", "")
	}
	if usage.TotalBytes > envelope.Budget.TotalBytes || usage.RequiredBytes > envelope.Budget.RequiredBytes || usage.ImportantBytes > envelope.Budget.ImportantBytes || usage.OptionalBytes > envelope.Budget.OptionalBytes {
		return contractError(CodeContextBudgetUnsatisfiable, "Context components exceed an attention tier budget", "")
	}
	lastOmissionID := ""
	for _, omission := range envelope.Omissions {
		if err := validateOmissionV2(omission); err != nil {
			return err
		}
		if _, exists := seen[omission.ComponentID]; exists {
			return contractError(CodeContextInvalidComponent, "included and omitted component IDs overlap", omission.ComponentID)
		}
		if omission.ComponentID <= lastOmissionID {
			return contractError(CodeContextInvalidComponent, "Context omissions are not in canonical ID order", omission.ComponentID)
		}
		seen[omission.ComponentID] = struct{}{}
		lastOmissionID = omission.ComponentID
	}
	return nil
}

func CanonicalEnvelopeHashV2(envelope ContextEnvelopeV2) (string, error) {
	if err := ValidateContextEnvelopeV2(envelope); err != nil {
		return "", err
	}
	encoded, err := canonicalJSON(envelope)
	if err != nil {
		return "", err
	}
	return hashBytes(encoded), nil
}

func BuildContextManifestV2(envelope ContextEnvelopeV2) (ContextManifestV2, error) {
	hash, err := CanonicalEnvelopeHashV2(envelope)
	if err != nil {
		return ContextManifestV2{}, err
	}
	manifest := ContextManifestV2{
		SchemaVersion: ManifestV2Version, CompilerVersion: envelope.CompilerVersion, EnvelopeHash: hash,
		Budget: envelope.Budget, Components: make([]ContextComponentManifestV2, 0, len(envelope.Components)),
		Omissions: append([]ContextOmissionV2(nil), envelope.Omissions...),
	}
	for _, component := range envelope.Components {
		manifest.Components = append(manifest.Components, ContextComponentManifestV2{
			ID: component.ID, Kind: component.Kind, Tier: component.Tier, Sensitivity: component.Sensitivity,
			Provenance: component.Provenance, ContentHash: component.ContentHash,
			OriginalBytes: component.OriginalBytes, IncludedBytes: component.IncludedBytes, Truncation: component.Truncation,
		})
		addUsage(&manifest.Usage, component.Tier, component.IncludedBytes)
	}
	return manifest, nil
}

func ValidateMemoryRecordV1(record MemoryRecordV1) error {
	if record.SchemaVersion != MemoryRecordV1Version {
		return contractError(CodeContextVersionUnsupported, "unsupported Memory record version", record.ID)
	}
	if !contractIDPattern.MatchString(record.ID) || strings.TrimSpace(record.Project) == "" || record.Project != strings.TrimSpace(record.Project) || len(record.Project) > 4096 || strings.TrimSpace(record.Scope) == "" || record.Scope != strings.TrimSpace(record.Scope) || len(record.Scope) > 256 {
		return contractError(CodeMemoryInvalidRecord, "Memory identity, project, or scope is invalid", record.ID)
	}
	if !validMemoryType(record.Type) || !validMemoryState(record.State) || !validMemoryWriter(record.Provenance.Writer) {
		return contractError(CodeMemoryInvalidRecord, "Memory type, state, or writer is invalid", record.ID)
	}
	if record.Sensitivity != SensitivityPublic && record.Sensitivity != SensitivityProject {
		return contractError(CodeMemoryInvalidRecord, "sensitive content cannot be stored as long-term Memory", record.ID)
	}
	if err := validateProvenance(record.Provenance.Source, record.Provenance.SourceVersion, record.Provenance.SourceHash); err != nil || strings.TrimSpace(record.Provenance.Reason) == "" || record.Provenance.Reason != strings.TrimSpace(record.Provenance.Reason) || len(record.Provenance.Reason) > 1024 {
		return contractError(CodeMemoryInvalidRecord, "Memory provenance is invalid", record.ID)
	}
	createdAt, err := time.Parse(time.RFC3339, record.CreatedAt)
	if err != nil {
		return contractError(CodeMemoryInvalidRecord, "Memory creation time must be RFC3339", record.ID)
	}
	updatedAt, err := time.Parse(time.RFC3339, record.UpdatedAt)
	if err != nil || updatedAt.Before(createdAt) {
		return contractError(CodeMemoryInvalidRecord, "Memory update time must be RFC3339 and not precede creation", record.ID)
	}
	if record.State != MemoryActive && strings.TrimSpace(record.StateReason) == "" {
		return contractError(CodeMemoryInvalidRecord, "non-active Memory requires a state reason", record.ID)
	}
	if record.StateReason != strings.TrimSpace(record.StateReason) || len(record.StateReason) > 1024 {
		return contractError(CodeMemoryInvalidRecord, "Memory state reason exceeds its bound", record.ID)
	}
	if record.State == MemoryDeleted {
		if record.Content != "" || !validHash(record.ContentHash) {
			return contractError(CodeMemoryInvalidRecord, "deleted Memory must retain only a valid content hash", record.ID)
		}
	} else if strings.TrimSpace(record.Content) == "" || len([]byte(record.Content)) > MaxMemoryContentBytes || record.ContentHash != hashBytes([]byte(record.Content)) {
		return contractError(CodeContextHashMismatch, "Memory content is empty, oversized, or does not match its hash", record.ID)
	}
	if len(record.Supersedes) > MaxMemorySupersedes || !sort.StringsAreSorted(record.Supersedes) {
		return contractError(CodeMemoryInvalidRecord, "Memory supersedes must be bounded and sorted", record.ID)
	}
	seen := make(map[string]struct{}, len(record.Supersedes))
	for _, superseded := range record.Supersedes {
		if !contractIDPattern.MatchString(superseded) || superseded == record.ID {
			return contractError(CodeMemoryConflict, "Memory supersedes contains an invalid identity", record.ID)
		}
		if _, exists := seen[superseded]; exists {
			return contractError(CodeMemoryConflict, "Memory supersedes contains a duplicate identity", record.ID)
		}
		seen[superseded] = struct{}{}
	}
	if record.Retention.MaxUses < 0 || record.Retention.MaxUses > 10000 || record.UseCount < 0 || (record.Retention.MaxUses > 0 && record.UseCount > record.Retention.MaxUses) {
		return contractError(CodeMemoryInvalidRecord, "Memory maxUses is outside its bound", record.ID)
	}
	if record.Retention.ExpiresAt != "" {
		expiresAt, parseErr := time.Parse(time.RFC3339, record.Retention.ExpiresAt)
		if parseErr != nil || !expiresAt.After(createdAt) {
			return contractError(CodeMemoryInvalidRecord, "Memory expiry must be RFC3339 and after creation", record.ID)
		}
	}
	return nil
}

func validateBudgetV2(budget AttentionBudgetV2) error {
	if budget.TotalBytes < 1 || budget.TotalBytes > MaxContextPayloadBytes || budget.RequiredBytes < 1 || budget.ImportantBytes < 0 || budget.OptionalBytes < 0 || budget.RequiredBytes > budget.TotalBytes || budget.ImportantBytes > budget.TotalBytes || budget.OptionalBytes > budget.TotalBytes {
		return contractError(CodeContextBudgetUnsatisfiable, "attention tier budgets must be non-negative, bounded, and sum to totalBytes", "")
	}
	if budget.RequiredBytes > budget.TotalBytes-budget.ImportantBytes || budget.RequiredBytes+budget.ImportantBytes > budget.TotalBytes-budget.OptionalBytes || budget.RequiredBytes+budget.ImportantBytes+budget.OptionalBytes != budget.TotalBytes {
		return contractError(CodeContextBudgetUnsatisfiable, "attention tier budgets must be non-negative, bounded, and sum to totalBytes", "")
	}
	return nil
}

func validateComponentV2(component ContextComponentV2) error {
	if !contractIDPattern.MatchString(component.ID) || componentRank(component.Kind) < 0 || !validTier(component.Tier) || !validSensitivity(component.Sensitivity) {
		return contractError(CodeContextInvalidComponent, "Context component identity or enum is invalid", component.ID)
	}
	if !tierAllowed(component.Kind, component.Tier) {
		return contractError(CodeContextInvalidComponent, "Context component kind cannot use this attention tier", component.ID)
	}
	if err := validateProvenance(component.Provenance.Source, component.Provenance.SourceVersion, component.Provenance.SourceHash); err != nil || strings.TrimSpace(component.Provenance.Reason) == "" || component.Provenance.Reason != strings.TrimSpace(component.Provenance.Reason) || len(component.Provenance.Reason) > 1024 {
		return contractError(CodeContextInvalidComponent, "Context component provenance is invalid", component.ID)
	}
	if !utf8.ValidString(component.Content) {
		return contractError(CodeContextInvalidComponent, "Context component content is not valid UTF-8", component.ID)
	}
	included := len([]byte(component.Content))
	if included == 0 || included > MaxComponentContentBytes || component.IncludedBytes != included || component.OriginalBytes < included {
		return contractError(CodeContextInvalidComponent, "Context component byte accounting is invalid", component.ID)
	}
	if component.ContentHash != hashBytes([]byte(component.Content)) {
		return contractError(CodeContextHashMismatch, "Context component content hash does not match", component.ID)
	}
	switch component.Truncation {
	case TruncationNone:
		if component.OriginalBytes != component.IncludedBytes {
			return contractError(CodeContextInvalidComponent, "untruncated component byte counts differ", component.ID)
		}
	case TruncationTail:
		if component.Tier == TierRequired || component.OriginalBytes <= component.IncludedBytes {
			return contractError(CodeContextInvalidComponent, "required components cannot be truncated and tail truncation must remove bytes", component.ID)
		}
	default:
		return contractError(CodeContextInvalidComponent, "unsupported truncation mode", component.ID)
	}
	return nil
}

func validateOmissionV2(omission ContextOmissionV2) error {
	if !contractIDPattern.MatchString(omission.ComponentID) || componentRank(omission.Kind) < 0 || !validTier(omission.Tier) || !tierAllowed(omission.Kind, omission.Tier) || !validOmissionReason(omission.Reason) || !validHash(omission.SourceHash) || omission.OriginalBytes < 0 {
		return contractError(CodeContextInvalidComponent, "Context omission is invalid", omission.ComponentID)
	}
	if omission.Tier == TierRequired {
		return contractError(CodeContextRequiredMissing, "required context cannot be represented as an omission", omission.ComponentID)
	}
	return nil
}

func validateProvenance(source, version, sourceHash string) error {
	if strings.TrimSpace(source) == "" || source != strings.TrimSpace(source) || len([]byte(source)) > MaxProvenanceRefBytes || strings.TrimSpace(version) == "" || version != strings.TrimSpace(version) || len(version) > 128 || !validHash(sourceHash) {
		return fmt.Errorf("invalid provenance")
	}
	return nil
}

func addUsage(usage *AttentionUsageV2, tier AttentionTier, bytes int) {
	usage.TotalBytes += bytes
	switch tier {
	case TierRequired:
		usage.RequiredBytes += bytes
	case TierImportant:
		usage.ImportantBytes += bytes
	case TierOptional:
		usage.OptionalBytes += bytes
	}
}

func componentRank(kind ComponentKind) int {
	switch kind {
	case KindExecutionContract:
		return 0
	case KindProjectInstructions:
		return 1
	case KindWorkflowPolicy:
		return 2
	case KindNodeTask:
		return 3
	case KindUserAnswer:
		return 4
	case KindDependencyResult:
		return 5
	case KindSkillInstructions:
		return 6
	case KindMemory:
		return 7
	case KindOutputContract:
		return 8
	default:
		return -1
	}
}

func tierAllowed(kind ComponentKind, tier AttentionTier) bool {
	switch kind {
	case KindExecutionContract, KindProjectInstructions, KindNodeTask, KindUserAnswer, KindOutputContract:
		return tier == TierRequired
	case KindWorkflowPolicy, KindDependencyResult, KindSkillInstructions:
		return tier == TierRequired || tier == TierImportant
	case KindMemory:
		return tier == TierOptional
	default:
		return false
	}
}

func validTier(value AttentionTier) bool {
	return value == TierRequired || value == TierImportant || value == TierOptional
}
func validSensitivity(value Sensitivity) bool {
	return value == SensitivityPublic || value == SensitivityProject || value == SensitivitySensitive
}
func validHash(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func hashBytes(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func canonicalJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}
func validMemoryType(value MemoryType) bool {
	return value == MemoryDecision || value == MemoryConstraint || value == MemoryFact || value == MemoryProcedure || value == MemoryPreference
}
func validMemoryState(value MemoryState) bool {
	return value == MemoryActive || value == MemorySuperseded || value == MemoryDeleted
}
func validMemoryWriter(value MemoryWriter) bool {
	return value == MemoryWriterUser || value == MemoryWriterHostAgent || value == MemoryWriterMigration
}
func validOmissionReason(value OmissionReason) bool {
	switch value {
	case OmissionBudgetExhausted, OmissionSuperseded, OmissionExpired, OmissionIrrelevant, OmissionUnavailable, OmissionSensitivityPolicy, OmissionDuplicate:
		return true
	default:
		return false
	}
}

func SortedComponentKindsV2() []ComponentKind {
	result := []ComponentKind{KindExecutionContract, KindProjectInstructions, KindWorkflowPolicy, KindNodeTask, KindUserAnswer, KindDependencyResult, KindSkillInstructions, KindMemory, KindOutputContract}
	sort.SliceStable(result, func(i, j int) bool { return componentRank(result[i]) < componentRank(result[j]) })
	return result
}
