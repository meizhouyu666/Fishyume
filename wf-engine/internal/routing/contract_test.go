package routing

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

type catalogFixtureV1 struct {
	SchemaVersion string               `json:"schemaVersion"`
	Catalog       CapabilityCatalogV1  `json:"catalog"`
	CatalogHash   string               `json:"catalogHash"`
	Requirement   RoutingRequirementV1 `json:"requirement"`
	PromptProfile PromptProfileV1      `json:"promptProfile"`
}

func TestRoutingContractV1GoldenFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/contracts-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture catalogFixtureV1
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SchemaVersion != "fishyume.routing-contract-fixture/v1" {
		t.Fatalf("fixture version = %q", fixture.SchemaVersion)
	}
	if err := ValidateCatalog(fixture.Catalog); err != nil {
		t.Fatal(err)
	}
	hash, err := CatalogHash(fixture.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	if hash != fixture.CatalogHash {
		t.Fatalf("catalog hash = %s, want %s", hash, fixture.CatalogHash)
	}
	if err := ValidateRequirement(fixture.Requirement); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePromptProfile(fixture.PromptProfile); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(SortCapabilities(fixture.Catalog.Models[0].Capabilities), []Capability{CapabilityNeedsInput, CapabilityRepoEdit, CapabilityRepoRead, CapabilityStreaming, CapabilityStructuredOutput, CapabilityToolUse}) {
		t.Fatal("capability vocabulary drifted")
	}
}

func TestRoutingContractsRejectAmbiguityAndUnsafeFallback(t *testing.T) {
	fixture := loadFixture(t)
	invalidCatalog := fixture.Catalog
	invalidCatalog.Models = append(invalidCatalog.Models, invalidCatalog.Models[1])
	assertCode(t, ValidateCatalog(invalidCatalog), CodeDuplicateIdentity)

	invalidRequirement := fixture.Requirement
	invalidRequirement.Capabilities = append(invalidRequirement.Capabilities, invalidRequirement.Capabilities[0])
	assertCode(t, ValidateRequirement(invalidRequirement), CodeInvalidCapability)

	unsafe := FallbackPolicyV1{Mode: FallbackEligible, MaxAttempts: 2}
	assertCode(t, ValidateFallbackPolicy(unsafe), CodeInvalidFallback)

	badProfile := fixture.PromptProfile
	badProfile.Components = append(badProfile.Components, badProfile.Components[0])
	assertCode(t, ValidatePromptProfile(badProfile), CodeInvalidPromptProfile)
}

func TestRoutingDecisionIsBoundedAndCatalogHashIsAuditable(t *testing.T) {
	fixture := loadFixture(t)
	hash, err := CatalogHash(fixture.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	decision := RoutingDecisionV1{
		SchemaVersion:  RoutingDecisionV1Version,
		CatalogHash:    hash,
		Requirement:    fixture.Requirement,
		Selected:       fixture.Catalog.Models[0].Target,
		ReasonCodes:    []string{"capability_match", "complexity_standard", "cost_preference_low"},
		Budget:         BudgetGrantV1{MaxCostUnits: 20, ContextBytes: 131072, OutputBytes: 32768},
		FallbackPolicy: FallbackPolicyV1{Mode: FallbackEligible, MaxAttempts: 2, RequireNoSideEffect: true, RequireApproval: true},
		Fallback:       []Target{fixture.Catalog.Models[1].Target},
		PromptProfile:  fixture.PromptProfile.ID,
	}
	if err := ValidateDecision(decision); err != nil {
		t.Fatal(err)
	}
	encoded, err := CanonicalJSON(decision)
	if err != nil || strings.Contains(string(encoded), "prompt") && !strings.Contains(string(encoded), fixture.PromptProfile.ID) {
		t.Fatalf("decision canonicalization failed: %v %s", err, encoded)
	}
	decision.CatalogHash = strings.Repeat("0", 64)
	if err := ValidateDecision(decision); err != nil {
		t.Fatal("hash is syntactically valid and should remain auditable", err)
	}
}

func TestFallbackNoneCannotCarryTargets(t *testing.T) {
	fixture := loadFixture(t)
	hash, err := CatalogHash(fixture.Catalog)
	if err != nil {
		t.Fatal(err)
	}
	decision := RoutingDecisionV1{
		SchemaVersion: RoutingDecisionV1Version, CatalogHash: hash, Requirement: fixture.Requirement,
		Selected: fixture.Catalog.Models[0].Target, ReasonCodes: []string{"capability_match"},
		Budget:         BudgetGrantV1{MaxCostUnits: 1, ContextBytes: 1, OutputBytes: 1},
		FallbackPolicy: FallbackPolicyV1{Mode: FallbackNone, MaxAttempts: 1},
		Fallback:       []Target{fixture.Catalog.Models[1].Target},
	}
	assertCode(t, ValidateDecision(decision), CodeInvalidFallback)
}

func TestRoutingBudgetAndPromptVersionsAreBounded(t *testing.T) {
	fixture := loadFixture(t)
	tooLarge := fixture.Requirement
	tooLarge.MaxContextBytes = MaxRoutingBudgetBytes + 1
	assertCode(t, ValidateRequirement(tooLarge), CodeInvalidBudget)
	tooMuch := BudgetGrantV1{MaxCostUnits: MaxCostUnits + 1, ContextBytes: 1, OutputBytes: 1}
	assertCode(t, ValidateBudgetGrant(tooMuch), CodeInvalidBudget)
	badVersion := fixture.PromptProfile
	badVersion.Version = " 1"
	assertCode(t, ValidatePromptProfile(badVersion), CodeInvalidPromptProfile)
}

func loadFixture(t *testing.T) catalogFixtureV1 {
	t.Helper()
	data, err := os.ReadFile("testdata/contracts-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture catalogFixtureV1
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func assertCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s", want)
	}
	var contractErr *ContractError
	if !errors.As(err, &contractErr) || contractErr.Code != want {
		t.Fatalf("error = %v, want code %s", err, want)
	}
}
