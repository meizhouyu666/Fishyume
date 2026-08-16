package contextcompiler

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"wf.local/wf-engine/internal/workflow"
)

const MaxProjectInstructionFileBytes = MaxComponentContentBytes

// SourceDeclarationV2 is metadata supplied by the caller for one explicitly
// selected source. SourceHash and ContentHash are deliberately not accepted:
// resolution computes both from the bytes it actually resolved.
type SourceDeclarationV2 struct {
	ID            string        `json:"id"`
	SourceVersion string        `json:"sourceVersion"`
	Reason        string        `json:"reason"`
	Tier          AttentionTier `json:"tier"`
	Sensitivity   Sensitivity   `json:"sensitivity"`
	Unavailable   bool          `json:"unavailable,omitempty"`
}

type ProjectInstructionSourceV2 struct {
	Declaration  SourceDeclarationV2 `json:"declaration"`
	Content      string              `json:"content,omitempty"`
	RelativePath string              `json:"relativePath,omitempty"`
}

type WorkflowPolicySourceV2 struct {
	Declaration SourceDeclarationV2 `json:"declaration"`
	Content     string              `json:"content"`
}

type NodeTaskSourceV2 struct {
	Declaration SourceDeclarationV2 `json:"declaration"`
	NodeID      string              `json:"nodeId"`
	Content     string              `json:"content"`
}

type DependencyResultSourceV2 struct {
	Declaration  SourceDeclarationV2 `json:"declaration"`
	UpstreamNode string              `json:"upstreamNode"`
	Result       *workflow.Result    `json:"result"`
}

type UserAnswerSourceV2 struct {
	Declaration SourceDeclarationV2 `json:"declaration"`
	Answer      json.RawMessage     `json:"answer"`
}

type SelectedMemorySourceV2 struct {
	Record MemoryRecordV1 `json:"record"`
	Reason string         `json:"reason"`
}

type ContextSourceResolutionInputV2 struct {
	ProjectRoot         string                       `json:"projectRoot"`
	AsOf                time.Time                    `json:"asOf"`
	ProjectInstructions []ProjectInstructionSourceV2 `json:"projectInstructions"`
	WorkflowPolicies    []WorkflowPolicySourceV2     `json:"workflowPolicies"`
	NodeTasks           []NodeTaskSourceV2           `json:"nodeTasks"`
	DependencyResults   []DependencyResultSourceV2   `json:"dependencyResults"`
	UserAnswers         []UserAnswerSourceV2         `json:"userAnswers"`
	SelectedMemory      []SelectedMemorySourceV2     `json:"selectedMemory"`
}

type ContextSourceResolutionV2 struct {
	Components []ContextComponentV2 `json:"components"`
	Omissions  []ContextOmissionV2  `json:"omissions"`
}

// ContextSourceRegistryV2 has no mutation or registration method. The only
// supported registry is the built-in ordered set returned by the constructor.
type ContextSourceRegistryV2 struct {
	orderedKinds [6]ComponentKind
}

var contextSourceNodeIDPatternV2 = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func BuiltinContextSourceRegistryV2() ContextSourceRegistryV2 {
	return ContextSourceRegistryV2{orderedKinds: [6]ComponentKind{
		KindProjectInstructions,
		KindWorkflowPolicy,
		KindNodeTask,
		KindUserAnswer,
		KindDependencyResult,
		KindMemory,
	}}
}

func (registry ContextSourceRegistryV2) Kinds() []ComponentKind {
	result := make([]ComponentKind, len(registry.orderedKinds))
	copy(result, registry.orderedKinds[:])
	return result
}

func (registry ContextSourceRegistryV2) Resolve(input ContextSourceResolutionInputV2) (ContextSourceResolutionV2, error) {
	if registry != BuiltinContextSourceRegistryV2() {
		return ContextSourceResolutionV2{}, contractError(CodeContextVersionUnsupported, "unsupported Context source registry", "")
	}
	if len(input.NodeTasks) == 0 {
		return ContextSourceResolutionV2{}, contractError(CodeContextRequiredMissing, "current Node task source is missing", "")
	}
	if len(input.NodeTasks) != 1 {
		return ContextSourceResolutionV2{}, contractError(CodeContextInvalidComponent, "exactly one current Node task source is required", "")
	}
	if len(input.SelectedMemory) > MaxSelectedMemoryRecords {
		return ContextSourceResolutionV2{}, contractError(CodeContextBudgetUnsatisfiable, "selected Memory record count exceeds its bound", "")
	}
	if sourceDeclarationCount(input)-len(input.SelectedMemory) > MaxContextComponents {
		return ContextSourceResolutionV2{}, contractError(CodeContextInvalidComponent, "declared non-Memory Context source count exceeds its bound", "")
	}
	if len(input.SelectedMemory) > 0 && input.AsOf.IsZero() {
		return ContextSourceResolutionV2{}, contractError(CodeContextInvalidComponent, "Memory resolution requires an explicit reference time", "")
	}
	if err := validateSourceIdentitiesV2(input); err != nil {
		return ContextSourceResolutionV2{}, err
	}

	projectRoot, err := resolutionProjectRoot(input)
	if err != nil {
		return ContextSourceResolutionV2{}, err
	}
	components := make([]ContextComponentV2, 0, sourceDeclarationCount(input))
	omissions := make([]ContextOmissionV2, 0, len(input.SelectedMemory))

	projectSources := append([]ProjectInstructionSourceV2(nil), input.ProjectInstructions...)
	sort.Slice(projectSources, func(i, j int) bool { return projectSources[i].Declaration.ID < projectSources[j].Declaration.ID })
	for _, source := range projectSources {
		declaration := source.Declaration
		if err := validateDeclaredAvailability(declaration); err != nil {
			return ContextSourceResolutionV2{}, err
		}
		if declaration.Tier != TierRequired {
			return ContextSourceResolutionV2{}, contractError(CodeContextInvalidComponent, "project instructions must use the required tier", declaration.ID)
		}
		content, sourceRef, resolveErr := resolveProjectInstructionsV2(projectRoot, source)
		if resolveErr != nil {
			return ContextSourceResolutionV2{}, resolveErr
		}
		component, buildErr := resolvedComponentV2(declaration, KindProjectInstructions, sourceRef, content)
		if buildErr != nil {
			return ContextSourceResolutionV2{}, buildErr
		}
		components = append(components, component)
	}

	workflowSources := append([]WorkflowPolicySourceV2(nil), input.WorkflowPolicies...)
	sort.Slice(workflowSources, func(i, j int) bool { return workflowSources[i].Declaration.ID < workflowSources[j].Declaration.ID })
	for _, source := range workflowSources {
		if err := validateDeclaredAvailability(source.Declaration); err != nil {
			return ContextSourceResolutionV2{}, err
		}
		component, buildErr := resolvedComponentV2(source.Declaration, KindWorkflowPolicy, "workflow:policy/"+source.Declaration.ID, source.Content)
		if buildErr != nil {
			return ContextSourceResolutionV2{}, buildErr
		}
		components = append(components, component)
	}

	nodeSource := input.NodeTasks[0]
	if err := validateDeclaredAvailability(nodeSource.Declaration); err != nil {
		return ContextSourceResolutionV2{}, err
	}
	if nodeSource.Declaration.Tier != TierRequired {
		return ContextSourceResolutionV2{}, contractError(CodeContextInvalidComponent, "current Node task must use the required tier", nodeSource.Declaration.ID)
	}
	if !contextSourceNodeIDPatternV2.MatchString(nodeSource.NodeID) {
		return ContextSourceResolutionV2{}, contractError(CodeContextInvalidComponent, "current Node task source has an invalid Workflow Node ID", nodeSource.Declaration.ID)
	}
	nodeComponent, err := resolvedComponentV2(nodeSource.Declaration, KindNodeTask, "workflow:node/"+nodeSource.NodeID, nodeSource.Content)
	if err != nil {
		return ContextSourceResolutionV2{}, err
	}
	components = append(components, nodeComponent)

	answerSources := append([]UserAnswerSourceV2(nil), input.UserAnswers...)
	sort.Slice(answerSources, func(i, j int) bool { return answerSources[i].Declaration.ID < answerSources[j].Declaration.ID })
	for _, source := range answerSources {
		declaration := source.Declaration
		if err := validateDeclaredAvailability(declaration); err != nil {
			return ContextSourceResolutionV2{}, err
		}
		if declaration.Tier != TierRequired {
			return ContextSourceResolutionV2{}, contractError(CodeContextInvalidComponent, "user answers must use the required tier", declaration.ID)
		}
		content, canonicalErr := canonicalAnswerV2(source.Answer, declaration.ID)
		if canonicalErr != nil {
			return ContextSourceResolutionV2{}, canonicalErr
		}
		component, buildErr := resolvedComponentV2(declaration, KindUserAnswer, "run:answer/"+declaration.ID, content)
		if buildErr != nil {
			return ContextSourceResolutionV2{}, buildErr
		}
		components = append(components, component)
	}

	dependencySources := append([]DependencyResultSourceV2(nil), input.DependencyResults...)
	sort.Slice(dependencySources, func(i, j int) bool { return dependencySources[i].Declaration.ID < dependencySources[j].Declaration.ID })
	for _, source := range dependencySources {
		declaration := source.Declaration
		if err := validateDeclaredAvailability(declaration); err != nil {
			return ContextSourceResolutionV2{}, err
		}
		if !contextSourceNodeIDPatternV2.MatchString(source.UpstreamNode) || source.Result == nil {
			return ContextSourceResolutionV2{}, missingSourceError(declaration, "declared dependency Result is missing")
		}
		if err := workflow.ValidateResult(*source.Result); err != nil {
			return ContextSourceResolutionV2{}, contractError(CodeContextInvalidComponent, "declared dependency Result is invalid", declaration.ID)
		}
		encoded, encodeErr := canonicalJSON(source.Result)
		if encodeErr != nil {
			return ContextSourceResolutionV2{}, contractError(CodeContextInvalidComponent, "declared dependency Result cannot be encoded", declaration.ID)
		}
		component, buildErr := resolvedComponentV2(declaration, KindDependencyResult, "run:node/"+source.UpstreamNode+"/result", string(encoded))
		if buildErr != nil {
			return ContextSourceResolutionV2{}, buildErr
		}
		components = append(components, component)
	}

	memorySources := append([]SelectedMemorySourceV2(nil), input.SelectedMemory...)
	sort.Slice(memorySources, func(i, j int) bool { return memorySources[i].Record.ID < memorySources[j].Record.ID })
	for _, selected := range memorySources {
		record := selected.Record
		if err := ValidateMemoryRecordV1(record); err != nil {
			return ContextSourceResolutionV2{}, err
		}
		if strings.TrimSpace(selected.Reason) == "" || selected.Reason != strings.TrimSpace(selected.Reason) || len(selected.Reason) > 1024 {
			return ContextSourceResolutionV2{}, contractError(CodeMemoryInvalidRecord, "Memory selection reason is invalid", record.ID)
		}
		if projectRoot == "" || !sameCanonicalProjectV2(projectRoot, record.Project) {
			return ContextSourceResolutionV2{}, contractError(CodeMemoryConflict, "selected Memory belongs to a different project", record.ID)
		}
		if record.State != MemoryActive {
			reason := OmissionSuperseded
			if record.State == MemoryDeleted {
				reason = OmissionUnavailable
			}
			omissions = append(omissions, memoryOmissionV2(record, reason))
			continue
		}
		if memoryExpiredV2(record, input.AsOf) {
			omissions = append(omissions, memoryOmissionV2(record, OmissionExpired))
			continue
		}
		declaration := SourceDeclarationV2{ID: record.ID, SourceVersion: record.SchemaVersion, Reason: selected.Reason, Tier: TierOptional, Sensitivity: record.Sensitivity}
		component, buildErr := resolvedComponentV2(declaration, KindMemory, "memory:"+record.ID, record.Content)
		if buildErr != nil {
			return ContextSourceResolutionV2{}, buildErr
		}
		if component.Provenance.SourceHash != record.ContentHash {
			return ContextSourceResolutionV2{}, contractError(CodeContextHashMismatch, "selected Memory source hash does not match its record", record.ID)
		}
		components = append(components, component)
	}

	sort.Slice(components, func(i, j int) bool {
		left, right := componentRank(components[i].Kind), componentRank(components[j].Kind)
		if left != right {
			return left < right
		}
		return components[i].ID < components[j].ID
	})
	sort.Slice(omissions, func(i, j int) bool { return omissions[i].ComponentID < omissions[j].ComponentID })
	if len(components) > MaxContextComponents || len(omissions) > MaxContextOmissions {
		return ContextSourceResolutionV2{}, contractError(CodeContextInvalidComponent, "resolved Context source count exceeds its bound", "")
	}
	for _, omission := range omissions {
		if err := validateOmissionV2(omission); err != nil {
			return ContextSourceResolutionV2{}, err
		}
	}
	return ContextSourceResolutionV2{Components: components, Omissions: omissions}, nil
}

func validateSourceIdentitiesV2(input ContextSourceResolutionInputV2) error {
	type identity struct {
		id   string
		kind ComponentKind
	}
	identities := make([]identity, 0, sourceDeclarationCount(input)+len(input.SelectedMemory))
	add := func(declaration SourceDeclarationV2, kind ComponentKind) error {
		if !contractIDPattern.MatchString(declaration.ID) {
			return contractError(CodeContextInvalidComponent, "Context source ID is invalid", declaration.ID)
		}
		identities = append(identities, identity{id: declaration.ID, kind: kind})
		return nil
	}
	for _, source := range input.ProjectInstructions {
		if err := add(source.Declaration, KindProjectInstructions); err != nil {
			return err
		}
	}
	for _, source := range input.WorkflowPolicies {
		if err := add(source.Declaration, KindWorkflowPolicy); err != nil {
			return err
		}
	}
	for _, source := range input.NodeTasks {
		if err := add(source.Declaration, KindNodeTask); err != nil {
			return err
		}
	}
	for _, source := range input.DependencyResults {
		if err := add(source.Declaration, KindDependencyResult); err != nil {
			return err
		}
	}
	for _, source := range input.UserAnswers {
		if err := add(source.Declaration, KindUserAnswer); err != nil {
			return err
		}
	}
	for _, source := range input.SelectedMemory {
		if !contractIDPattern.MatchString(source.Record.ID) {
			return contractError(CodeMemoryInvalidRecord, "Memory identity is invalid", source.Record.ID)
		}
		identities = append(identities, identity{id: source.Record.ID, kind: KindMemory})
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].id != identities[j].id {
			return identities[i].id < identities[j].id
		}
		return componentRank(identities[i].kind) < componentRank(identities[j].kind)
	})
	for index := 1; index < len(identities); index++ {
		if identities[index-1].id == identities[index].id {
			return contractError(CodeContextInvalidComponent, "Context source ID is duplicated or conflicts across source kinds", identities[index].id)
		}
	}
	upstreamNodes := make([]string, 0, len(input.DependencyResults))
	for _, source := range input.DependencyResults {
		if strings.TrimSpace(source.UpstreamNode) != "" {
			upstreamNodes = append(upstreamNodes, source.UpstreamNode)
		}
	}
	sort.Strings(upstreamNodes)
	for index := 1; index < len(upstreamNodes); index++ {
		if upstreamNodes[index-1] == upstreamNodes[index] {
			return contractError(CodeContextInvalidComponent, "upstream dependency Result is declared more than once", upstreamNodes[index])
		}
	}
	return nil
}

func sourceDeclarationCount(input ContextSourceResolutionInputV2) int {
	return len(input.ProjectInstructions) + len(input.WorkflowPolicies) + len(input.NodeTasks) + len(input.DependencyResults) + len(input.UserAnswers) + len(input.SelectedMemory)
}

func validateDeclaredAvailability(declaration SourceDeclarationV2) error {
	if declaration.Unavailable {
		return contractError(CodeContextSourceUnavailable, "declared Context source is unavailable", declaration.ID)
	}
	return nil
}

func missingSourceError(declaration SourceDeclarationV2, message string) error {
	if declaration.Tier == TierRequired {
		return contractError(CodeContextRequiredMissing, message, declaration.ID)
	}
	return contractError(CodeContextInvalidComponent, message, declaration.ID)
}

func resolvedComponentV2(declaration SourceDeclarationV2, kind ComponentKind, sourceRef, content string) (ContextComponentV2, error) {
	if strings.TrimSpace(content) == "" {
		return ContextComponentV2{}, missingSourceError(declaration, "resolved Context source content is missing")
	}
	contentBytes := []byte(content)
	hash := hashBytes(contentBytes)
	component := ContextComponentV2{
		ID: declaration.ID, Kind: kind, Tier: declaration.Tier, Sensitivity: declaration.Sensitivity,
		Provenance: ComponentProvenanceV2{Source: sourceRef, SourceVersion: declaration.SourceVersion, SourceHash: hash, Reason: declaration.Reason},
		Content:    content, ContentHash: hash, OriginalBytes: len(contentBytes), IncludedBytes: len(contentBytes), Truncation: TruncationNone,
	}
	if err := validateComponentV2(component); err != nil {
		return ContextComponentV2{}, err
	}
	return component, nil
}

func resolutionProjectRoot(input ContextSourceResolutionInputV2) (string, error) {
	needsRoot := len(input.SelectedMemory) > 0
	for _, source := range input.ProjectInstructions {
		needsRoot = needsRoot || source.RelativePath != ""
	}
	if !needsRoot {
		return "", nil
	}
	if strings.TrimSpace(input.ProjectRoot) == "" {
		return "", contractError(CodeContextSourceUnavailable, "canonical project root is required", "")
	}
	absolute, err := filepath.Abs(input.ProjectRoot)
	if err != nil {
		return "", contractError(CodeContextSourceUnavailable, "canonical project root cannot be resolved", "")
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", contractError(CodeContextSourceUnavailable, "canonical project root cannot be resolved", "")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", contractError(CodeContextSourceUnavailable, "canonical project root is not a directory", "")
	}
	return filepath.Clean(canonical), nil
}

func resolveProjectInstructionsV2(projectRoot string, source ProjectInstructionSourceV2) (string, string, error) {
	declaration := source.Declaration
	hasContent, hasPath := source.Content != "", source.RelativePath != ""
	if hasContent == hasPath {
		if !hasContent {
			return "", "", missingSourceError(declaration, "project instruction content or file is missing")
		}
		return "", "", contractError(CodeContextInvalidComponent, "project instruction must select exactly one inline or file source", declaration.ID)
	}
	if hasContent {
		return source.Content, "project:instructions/" + declaration.ID, nil
	}
	if filepath.IsAbs(source.RelativePath) || filepath.Clean(source.RelativePath) == "." || len([]byte(filepath.ToSlash(source.RelativePath))) > MaxProvenanceRefBytes-len("project:file/") {
		return "", "", contractError(CodeContextInvalidComponent, "project instruction file must be a relative file path", declaration.ID)
	}
	candidate, err := filepath.EvalSymlinks(filepath.Join(projectRoot, source.RelativePath))
	if err != nil {
		return "", "", contractError(CodeContextSourceUnavailable, "project instruction file is unavailable", declaration.ID)
	}
	candidate = filepath.Clean(candidate)
	if !pathWithinRootV2(projectRoot, candidate) {
		return "", "", contractError(CodeContextSourceUnavailable, "project instruction file resolves outside the canonical project root", declaration.ID)
	}
	file, err := os.Open(candidate)
	if err != nil {
		return "", "", contractError(CodeContextSourceUnavailable, "project instruction file is unavailable", declaration.ID)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxProjectInstructionFileBytes {
		return "", "", contractError(CodeContextSourceUnavailable, "project instruction file is not a bounded regular file", declaration.ID)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxProjectInstructionFileBytes+1))
	if err != nil || len(data) > MaxProjectInstructionFileBytes {
		return "", "", contractError(CodeContextSourceUnavailable, "project instruction file cannot be read within its bound", declaration.ID)
	}
	relative, err := filepath.Rel(projectRoot, candidate)
	if err != nil {
		return "", "", contractError(CodeContextSourceUnavailable, "project instruction file provenance cannot be resolved", declaration.ID)
	}
	return string(data), "project:file/" + filepath.ToSlash(relative), nil
}

func pathWithinRootV2(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return false
	}
	return true
}

func canonicalAnswerV2(answer json.RawMessage, subjectID string) (string, error) {
	if len(answer) == 0 {
		return "", contractError(CodeContextRequiredMissing, "user answer content is missing", subjectID)
	}
	if len(answer) > MaxComponentContentBytes {
		return "", contractError(CodeContextInvalidComponent, "user answer exceeds its source bound", subjectID)
	}
	decoder := json.NewDecoder(bytes.NewReader(answer))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", contractError(CodeContextInvalidComponent, "user answer is not valid JSON", subjectID)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", contractError(CodeContextInvalidComponent, "user answer contains trailing JSON data", subjectID)
	}
	encoded, err := canonicalJSON(value)
	if err != nil || len(encoded) > MaxComponentContentBytes {
		return "", contractError(CodeContextInvalidComponent, "user answer cannot be canonically encoded within its bound", subjectID)
	}
	return string(encoded), nil
}

func memoryExpiredV2(record MemoryRecordV1, asOf time.Time) bool {
	if record.Retention.MaxUses > 0 && record.UseCount >= record.Retention.MaxUses {
		return true
	}
	if record.Retention.ExpiresAt == "" {
		return false
	}
	expiresAt, _ := time.Parse(time.RFC3339, record.Retention.ExpiresAt)
	return !asOf.Before(expiresAt)
}

func memoryOmissionV2(record MemoryRecordV1, reason OmissionReason) ContextOmissionV2 {
	originalBytes := len([]byte(record.Content))
	return ContextOmissionV2{ComponentID: record.ID, Kind: KindMemory, Tier: TierOptional, Reason: reason, SourceHash: record.ContentHash, OriginalBytes: originalBytes}
}

func sameCanonicalProjectV2(canonicalRoot, recordProject string) bool {
	absolute, err := filepath.Abs(recordProject)
	if err != nil {
		return false
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return false
	}
	left, right := filepath.Clean(canonicalRoot), filepath.Clean(canonical)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}
