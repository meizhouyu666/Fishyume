package contextcompiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

const EvaluationV1Version = "fishyume.context-evaluation/v1"

type EvaluationDetection string

const (
	DetectMissingRequired     EvaluationDetection = "missing_required_instruction"
	DetectStaleMemory         EvaluationDetection = "stale_memory"
	DetectIrrelevantContext   EvaluationDetection = "irrelevant_context"
	DetectSensitiveLeakage    EvaluationDetection = "sensitive_leakage"
	DetectNondeterminism      EvaluationDetection = "nondeterminism"
	DetectDependencyIsolation EvaluationDetection = "dependency_isolation"
)

type ExpectedOmissionV1 struct {
	ComponentID string         `json:"componentId"`
	Reason      OmissionReason `json:"reason"`
}

type EvaluationFixtureV1 struct {
	ID                       string               `json:"id"`
	Detects                  EvaluationDetection  `json:"detects"`
	Description              string               `json:"description"`
	ExpectedComponentOrder   []string             `json:"expectedComponentOrder"`
	RequiredComponentIDs     []string             `json:"requiredComponentIds"`
	ForbiddenComponentIDs    []string             `json:"forbiddenComponentIds"`
	ExpectedOmissions        []ExpectedOmissionV1 `json:"expectedOmissions"`
	ForbiddenPersistedText   []string             `json:"forbiddenPersistedText"`
	RequireDeterministicHash bool                 `json:"requireDeterministicHash"`
}

type EvaluationSuiteV1 struct {
	SchemaVersion string                `json:"schemaVersion"`
	Fixtures      []EvaluationFixtureV1 `json:"fixtures"`
}

type EvaluationCandidateV1 struct {
	ComponentIDs   []string             `json:"componentIds"`
	Omissions      []ExpectedOmissionV1 `json:"omissions"`
	Manifest       json.RawMessage      `json:"manifest"`
	EnvelopeHashes []string             `json:"envelopeHashes"`
}

type EvaluationFindingV1 struct {
	Code        string `json:"code"`
	ComponentID string `json:"componentId,omitempty"`
	Message     string `json:"message"`
}

func ValidateEvaluationSuiteV1(suite EvaluationSuiteV1) error {
	if suite.SchemaVersion != EvaluationV1Version {
		return fmt.Errorf("unsupported evaluation fixture version %q", suite.SchemaVersion)
	}
	if len(suite.Fixtures) == 0 || len(suite.Fixtures) > 64 {
		return fmt.Errorf("evaluation fixture count is outside its bound")
	}
	requiredDetections := map[EvaluationDetection]bool{
		DetectMissingRequired: false, DetectStaleMemory: false, DetectIrrelevantContext: false,
		DetectSensitiveLeakage: false, DetectNondeterminism: false, DetectDependencyIsolation: false,
	}
	seen := make(map[string]struct{}, len(suite.Fixtures))
	for _, fixture := range suite.Fixtures {
		if !contractIDPattern.MatchString(fixture.ID) || fixture.Description == "" || len(fixture.Description) > 1024 {
			return fmt.Errorf("evaluation fixture identity is invalid: %q", fixture.ID)
		}
		if _, exists := seen[fixture.ID]; exists {
			return fmt.Errorf("evaluation fixture ID %q is duplicated", fixture.ID)
		}
		seen[fixture.ID] = struct{}{}
		if _, exists := requiredDetections[fixture.Detects]; !exists {
			return fmt.Errorf("unsupported evaluation detection %q", fixture.Detects)
		}
		requiredDetections[fixture.Detects] = true
		if err := validateFixtureIDs(fixture); err != nil {
			return fmt.Errorf("fixture %q: %w", fixture.ID, err)
		}
		if fixture.Detects == DetectSensitiveLeakage && len(fixture.ForbiddenPersistedText) == 0 {
			return fmt.Errorf("fixture %q must declare sensitive leakage markers", fixture.ID)
		}
		if fixture.Detects == DetectNondeterminism && !fixture.RequireDeterministicHash {
			return fmt.Errorf("fixture %q must require deterministic hashes", fixture.ID)
		}
		orderIDs := make(map[string]struct{}, len(fixture.ExpectedComponentOrder))
		for _, id := range fixture.ExpectedComponentOrder {
			orderIDs[id] = struct{}{}
		}
		for _, id := range fixture.RequiredComponentIDs {
			if _, exists := orderIDs[id]; !exists {
				return fmt.Errorf("fixture %q requires %q outside its golden order", fixture.ID, id)
			}
		}
		for _, id := range fixture.ForbiddenComponentIDs {
			if _, exists := orderIDs[id]; exists {
				return fmt.Errorf("fixture %q includes forbidden component %q in its golden order", fixture.ID, id)
			}
		}
	}
	for detection, present := range requiredDetections {
		if !present {
			return fmt.Errorf("evaluation suite does not cover %q", detection)
		}
	}
	return nil
}

func EvaluateCandidateV1(fixture EvaluationFixtureV1, candidate EvaluationCandidateV1) []EvaluationFindingV1 {
	findings := make([]EvaluationFindingV1, 0)
	positions := make(map[string]int, len(candidate.ComponentIDs))
	for index, id := range candidate.ComponentIDs {
		if _, exists := positions[id]; exists {
			findings = append(findings, EvaluationFindingV1{Code: "duplicate_component", ComponentID: id, Message: "candidate contains a duplicate component"})
		}
		positions[id] = index
	}
	for _, id := range fixture.RequiredComponentIDs {
		if _, exists := positions[id]; !exists {
			findings = append(findings, EvaluationFindingV1{Code: "required_missing", ComponentID: id, Message: "required component is absent"})
		}
	}
	for _, id := range fixture.ForbiddenComponentIDs {
		if _, exists := positions[id]; exists {
			findings = append(findings, EvaluationFindingV1{Code: "forbidden_included", ComponentID: id, Message: "forbidden component is present"})
		}
	}
	if len(fixture.ExpectedComponentOrder) > 0 && !equalStrings(fixture.ExpectedComponentOrder, candidate.ComponentIDs) {
		findings = append(findings, EvaluationFindingV1{Code: "order_mismatch", Message: "candidate component order differs from the golden order"})
	}
	omissions := make(map[string]OmissionReason, len(candidate.Omissions))
	for _, omission := range candidate.Omissions {
		if _, exists := omissions[omission.ComponentID]; exists {
			findings = append(findings, EvaluationFindingV1{Code: "duplicate_omission", ComponentID: omission.ComponentID, Message: "candidate contains a duplicate omission"})
		}
		omissions[omission.ComponentID] = omission.Reason
	}
	if len(candidate.Omissions) != len(fixture.ExpectedOmissions) {
		findings = append(findings, EvaluationFindingV1{Code: "omission_count_mismatch", Message: "candidate omission count differs from the golden count"})
	}
	for _, expected := range fixture.ExpectedOmissions {
		if omissions[expected.ComponentID] != expected.Reason {
			findings = append(findings, EvaluationFindingV1{Code: "omission_mismatch", ComponentID: expected.ComponentID, Message: "candidate omission reason differs from the golden reason"})
		}
	}
	for _, marker := range fixture.ForbiddenPersistedText {
		if marker != "" && bytes.Contains(candidate.Manifest, []byte(marker)) {
			findings = append(findings, EvaluationFindingV1{Code: "sensitive_leakage", Message: "durable manifest contains forbidden sensitive text"})
		}
	}
	if !json.Valid(candidate.Manifest) {
		findings = append(findings, EvaluationFindingV1{Code: "manifest_invalid", Message: "candidate durable manifest is not valid JSON"})
	}
	if fixture.RequireDeterministicHash {
		if len(candidate.EnvelopeHashes) < 2 {
			findings = append(findings, EvaluationFindingV1{Code: "hash_evidence_missing", Message: "at least two envelope hashes are required"})
		} else {
			first := candidate.EnvelopeHashes[0]
			if !validHash(first) {
				findings = append(findings, EvaluationFindingV1{Code: "hash_invalid", Message: "candidate envelope hash is invalid"})
			}
			for _, hash := range candidate.EnvelopeHashes[1:] {
				if hash != first {
					findings = append(findings, EvaluationFindingV1{Code: "nondeterministic_hash", Message: "identical approved inputs produced different hashes"})
					break
				}
			}
		}
	}
	return findings
}

func validateFixtureIDs(fixture EvaluationFixtureV1) error {
	all := append([]string{}, fixture.ExpectedComponentOrder...)
	all = append(all, fixture.RequiredComponentIDs...)
	all = append(all, fixture.ForbiddenComponentIDs...)
	for _, omission := range fixture.ExpectedOmissions {
		all = append(all, omission.ComponentID)
		if !validOmissionReason(omission.Reason) {
			return fmt.Errorf("unsupported omission reason %q", omission.Reason)
		}
	}
	for _, id := range all {
		if !contractIDPattern.MatchString(id) {
			return fmt.Errorf("invalid component ID %q", id)
		}
	}
	for _, values := range [][]string{fixture.ExpectedComponentOrder, fixture.RequiredComponentIDs, fixture.ForbiddenComponentIDs} {
		copyOfValues := append([]string(nil), values...)
		sort.Strings(copyOfValues)
		for index := 1; index < len(copyOfValues); index++ {
			if copyOfValues[index] == copyOfValues[index-1] {
				return fmt.Errorf("component ID %q is duplicated within one fixture field", copyOfValues[index])
			}
		}
	}
	return nil
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
