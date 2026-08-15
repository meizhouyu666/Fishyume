package contextcompiler

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

type contractFixtureV2 struct {
	SchemaVersion   string             `json:"schemaVersion"`
	Limits          ContractLimitsV2   `json:"limits"`
	ErrorCodes      []ContextErrorCode `json:"errorCodes"`
	ComponentOrder  []ComponentKind    `json:"componentOrder"`
	AttentionTiers  []AttentionTier    `json:"attentionTiers"`
	Sensitivities   []Sensitivity      `json:"sensitivities"`
	TruncationModes []TruncationMode   `json:"truncationModes"`
	OmissionReasons []OmissionReason   `json:"omissionReasons"`
	MemoryTypes     []MemoryType       `json:"memoryTypes"`
	MemoryStates    []MemoryState      `json:"memoryStates"`
	MemoryWriters   []MemoryWriter     `json:"memoryWriters"`
	Envelope        ContextEnvelopeV2  `json:"envelope"`
	EnvelopeHash    string             `json:"envelopeHash"`
	MemoryRecords   []MemoryRecordV1   `json:"memoryRecords"`
}

func TestContextContractV2GoldenFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/contracts-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture contractFixtureV2
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SchemaVersion != "fishyume.context-contract-fixture/v1" {
		t.Fatalf("fixture version = %q", fixture.SchemaVersion)
	}
	if !reflect.DeepEqual(fixture.Limits, StableContractLimitsV2()) {
		t.Fatalf("fixture limits drifted: %+v", fixture.Limits)
	}
	if !reflect.DeepEqual(fixture.ErrorCodes, StableContextErrorCodes) {
		t.Fatalf("fixture errors drifted: %v", fixture.ErrorCodes)
	}
	if !reflect.DeepEqual(fixture.ComponentOrder, SortedComponentKindsV2()) {
		t.Fatalf("fixture component order drifted: %v", fixture.ComponentOrder)
	}
	assertFixtureEnumsV2(t, fixture)
	if err := ValidateContextEnvelopeV2(fixture.Envelope); err != nil {
		t.Fatal(err)
	}
	hash, err := CanonicalEnvelopeHashV2(fixture.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	if hash != fixture.EnvelopeHash {
		t.Fatalf("envelope hash = %s, want %s", hash, fixture.EnvelopeHash)
	}
	for _, record := range fixture.MemoryRecords {
		if err := ValidateMemoryRecordV1(record); err != nil {
			t.Fatalf("Memory %s: %v", record.ID, err)
		}
	}
}

func assertFixtureEnumsV2(t *testing.T, fixture contractFixtureV2) {
	t.Helper()
	checks := []struct {
		name string
		got  any
		want any
	}{
		{name: "attention tiers", got: fixture.AttentionTiers, want: []AttentionTier{TierRequired, TierImportant, TierOptional}},
		{name: "sensitivities", got: fixture.Sensitivities, want: []Sensitivity{SensitivityPublic, SensitivityProject, SensitivitySensitive}},
		{name: "truncation modes", got: fixture.TruncationModes, want: []TruncationMode{TruncationNone, TruncationTail}},
		{name: "omission reasons", got: fixture.OmissionReasons, want: []OmissionReason{OmissionBudgetExhausted, OmissionSuperseded, OmissionExpired, OmissionIrrelevant, OmissionUnavailable, OmissionSensitivityPolicy, OmissionDuplicate}},
		{name: "Memory types", got: fixture.MemoryTypes, want: []MemoryType{MemoryDecision, MemoryConstraint, MemoryFact, MemoryProcedure, MemoryPreference}},
		{name: "Memory states", got: fixture.MemoryStates, want: []MemoryState{MemoryActive, MemorySuperseded, MemoryDeleted}},
		{name: "Memory writers", got: fixture.MemoryWriters, want: []MemoryWriter{MemoryWriterUser, MemoryWriterHostAgent, MemoryWriterMigration}},
	}
	for _, check := range checks {
		if !reflect.DeepEqual(check.got, check.want) {
			t.Fatalf("fixture %s drifted: %v", check.name, check.got)
		}
	}
}

func TestContextManifestV2PersistsMetadataWithoutContent(t *testing.T) {
	fixture := loadContractFixtureV2(t)
	manifest, err := BuildContextManifestV2(fixture.Envelope)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != ManifestV2Version || manifest.EnvelopeHash != fixture.EnvelopeHash {
		t.Fatalf("manifest identity is incomplete: %+v", manifest)
	}
	for _, component := range fixture.Envelope.Components {
		if strings.Contains(string(encoded), component.Content) {
			t.Fatalf("durable manifest leaked component %q content", component.ID)
		}
	}
	if manifest.Usage.TotalBytes != 228 || manifest.Usage.RequiredBytes != 138 || manifest.Usage.ImportantBytes != 61 || manifest.Usage.OptionalBytes != 29 {
		t.Fatalf("unexpected attention usage: %+v", manifest.Usage)
	}
}

func TestCanonicalEnvelopeV2UsesUnescapedCompactUTF8JSON(t *testing.T) {
	fixture := loadContractFixtureV2(t)
	candidate := cloneEnvelopeV2(t, fixture.Envelope)
	component := &candidate.Components[3]
	component.Content = "Implement <approved> 中文 contract."
	component.ContentHash = hashBytes([]byte(component.Content))
	component.Provenance.SourceHash = component.ContentHash
	component.OriginalBytes = len([]byte(component.Content))
	component.IncludedBytes = component.OriginalBytes
	encoded, err := canonicalJSON(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), "<approved>") || strings.Contains(string(encoded), `\u003c`) || strings.HasSuffix(string(encoded), "\n") {
		t.Fatalf("canonical JSON escaping drifted: %s", encoded)
	}
	if _, err := CanonicalEnvelopeHashV2(candidate); err != nil {
		t.Fatal(err)
	}
}

func TestContextEnvelopeV2RejectsMissingRequiredOrderHashAndBudgetDrift(t *testing.T) {
	fixture := loadContractFixtureV2(t)
	tests := []struct {
		name string
		edit func(*ContextEnvelopeV2)
		code ContextErrorCode
	}{
		{name: "required missing", edit: func(value *ContextEnvelopeV2) { value.Components = value.Components[1:] }, code: CodeContextRequiredMissing},
		{name: "canonical order", edit: func(value *ContextEnvelopeV2) {
			value.Components[0], value.Components[1] = value.Components[1], value.Components[0]
		}, code: CodeContextInvalidComponent},
		{name: "content hash", edit: func(value *ContextEnvelopeV2) { value.Components[0].ContentHash = strings.Repeat("0", 64) }, code: CodeContextHashMismatch},
		{name: "required truncation", edit: func(value *ContextEnvelopeV2) {
			value.Components[0].Truncation = TruncationTail
			value.Components[0].OriginalBytes++
		}, code: CodeContextInvalidComponent},
		{name: "tier budget", edit: func(value *ContextEnvelopeV2) { value.Budget.RequiredBytes = 100; value.Budget.OptionalBytes = 668 }, code: CodeContextBudgetUnsatisfiable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneEnvelopeV2(t, fixture.Envelope)
			test.edit(&candidate)
			err := ValidateContextEnvelopeV2(candidate)
			var contractErr *ContractError
			if !errors.As(err, &contractErr) || contractErr.Code != test.code {
				t.Fatalf("error = %v, want code %s", err, test.code)
			}
		})
	}
}

func TestMemoryRecordV1RequiresExplicitTrustedWriterAndRejectsSensitiveStorage(t *testing.T) {
	fixture := loadContractFixtureV2(t)
	active := fixture.MemoryRecords[0]
	if err := ValidateMemoryRecordV1(active); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*MemoryRecordV1){
		"node Agent writer":     func(value *MemoryRecordV1) { value.Provenance.Writer = MemoryWriter("node_agent") },
		"sensitive Memory":      func(value *MemoryRecordV1) { value.Sensitivity = SensitivitySensitive },
		"unsorted supersedes":   func(value *MemoryRecordV1) { value.Supersedes = []string{"memory-z", "memory-a"} },
		"content hash mismatch": func(value *MemoryRecordV1) { value.ContentHash = strings.Repeat("0", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := active
			candidate.Supersedes = append([]string(nil), active.Supersedes...)
			edit(&candidate)
			if err := ValidateMemoryRecordV1(candidate); err == nil {
				t.Fatal("invalid Memory record was accepted")
			}
		})
	}
	if err := ValidateMemoryRecordV1(fixture.MemoryRecords[1]); err != nil {
		t.Fatalf("explicit deletion tombstone was rejected: %v", err)
	}
}

func TestEvaluationFixturesDetectEveryM5RiskClass(t *testing.T) {
	data, err := os.ReadFile("testdata/evaluation-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite EvaluationSuiteV1
	if err := json.Unmarshal(data, &suite); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEvaluationSuiteV1(suite); err != nil {
		t.Fatal(err)
	}
	goldenHash := strings.Repeat("a", 64)
	for _, fixture := range suite.Fixtures {
		candidate := EvaluationCandidateV1{
			ComponentIDs:   append([]string(nil), fixture.ExpectedComponentOrder...),
			Omissions:      append([]ExpectedOmissionV1(nil), fixture.ExpectedOmissions...),
			Manifest:       json.RawMessage(`{"metadataOnly":true}`),
			EnvelopeHashes: []string{goldenHash, goldenHash},
		}
		if findings := EvaluateCandidateV1(fixture, candidate); len(findings) != 0 {
			t.Fatalf("golden fixture %s failed: %+v", fixture.ID, findings)
		}
		mutated := candidate
		mutated.ComponentIDs = append([]string(nil), candidate.ComponentIDs...)
		mutated.Omissions = append([]ExpectedOmissionV1(nil), candidate.Omissions...)
		mutated.EnvelopeHashes = append([]string(nil), candidate.EnvelopeHashes...)
		switch fixture.Detects {
		case DetectMissingRequired:
			mutated.ComponentIDs = mutated.ComponentIDs[1:]
		case DetectStaleMemory, DetectIrrelevantContext, DetectDependencyIsolation:
			mutated.ComponentIDs = append(mutated.ComponentIDs, fixture.ForbiddenComponentIDs[0])
		case DetectSensitiveLeakage:
			mutated.Manifest = json.RawMessage(`{"content":"relay-token-secret-marker"}`)
		case DetectNondeterminism:
			mutated.EnvelopeHashes[1] = strings.Repeat("b", 64)
		}
		if findings := EvaluateCandidateV1(fixture, mutated); len(findings) == 0 {
			t.Fatalf("fixture %s did not detect its target regression", fixture.ID)
		}
	}
}

func loadContractFixtureV2(t *testing.T) contractFixtureV2 {
	t.Helper()
	data, err := os.ReadFile("testdata/contracts-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture contractFixtureV2
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func cloneEnvelopeV2(t *testing.T, source ContextEnvelopeV2) ContextEnvelopeV2 {
	t.Helper()
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var result ContextEnvelopeV2
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
