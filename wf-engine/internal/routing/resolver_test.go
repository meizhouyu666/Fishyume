package routing

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolveV1SelectsDeterministicBestModelWithinBudget(t *testing.T) {
	catalog := BuiltinCatalogV1()
	requirement := DefaultRequirementV1()
	decision, err := ResolveV1(ResolveRequestV1{Catalog: catalog, Requirement: requirement, Budget: BudgetGrantV1{MaxCostUnits: 20, ContextBytes: 131072, OutputBytes: 32768}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Model != "gpt-5.6-luna" || decision.FallbackPolicy.Mode != FallbackNone || len(decision.Fallback) != 0 {
		t.Fatalf("decision = %+v", decision)
	}
	if decision.CatalogHash != "7b993c75a20b1a783a1ab9aaeae1235c6514f408f33bc8bdc32de9e85be4a1da" || decision.Budget != (BudgetGrantV1{MaxCostUnits: 20, ContextBytes: 131072, OutputBytes: 32768}) {
		t.Fatalf("decision audit fields = %+v", decision)
	}
	if !reflect.DeepEqual(decision.ReasonCodes[:2], []string{"capability_match", "complexity_standard"}) || !strings.Contains(strings.Join(decision.ReasonCodes, ","), "cost_preference_low") {
		t.Fatalf("reason codes = %v", decision.ReasonCodes)
	}
	if err := ValidateDecision(decision); err != nil {
		t.Fatal(err)
	}
}

func TestResolveV1HonorsCandidatesComplexityAndFallbackSafely(t *testing.T) {
	catalog := BuiltinCatalogV1()
	requirement := DefaultRequirementV1()
	requirement.Complexity = ComplexityComplex
	requirement.Quality = QualityBalanced
	requirement.MaxCostUnits = 100
	requirement.MaxContextBytes = 131072
	requirement.MaxOutputBytes = 32768
	requirement.Candidates = []string{"codex/local/gpt-5.6-luna", "codex/local/gpt-5.6"}
	requirement.AllowModelFallback = true
	decision, err := ResolveV1(ResolveRequestV1{Catalog: catalog, Requirement: requirement, Budget: BudgetGrantV1{MaxCostUnits: 100, ContextBytes: 131072, OutputBytes: 32768}})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Selected.Model != "gpt-5.6" || len(decision.Fallback) != 0 {
		t.Fatalf("complex decision = %+v", decision)
	}
	if decision.FallbackPolicy.Mode != FallbackEligible || !decision.FallbackPolicy.RequireNoSideEffect || !decision.FallbackPolicy.RequireApproval {
		t.Fatalf("fallback policy = %+v", decision.FallbackPolicy)
	}
}

func TestResolveV1RejectsHashMismatchAndNoMatch(t *testing.T) {
	catalog := BuiltinCatalogV1()
	requirement := DefaultRequirementV1()
	if _, err := ResolveV1(ResolveRequestV1{Catalog: catalog, CatalogHash: strings.Repeat("0", 64), Requirement: requirement, Budget: BudgetGrantV1{MaxCostUnits: 20, ContextBytes: 131072, OutputBytes: 32768}}); err == nil || !strings.Contains(err.Error(), string(CodeCatalogHashMismatch)) {
		t.Fatalf("hash mismatch error = %v", err)
	}
	requirement.Capabilities = []Capability{CapabilityNeedsInput, CapabilityRepoEdit, CapabilityRepoRead, CapabilityStreaming, CapabilityStructuredOutput, CapabilityToolUse}
	requirement.MaxCostUnits = 20
	if _, err := ResolveV1(ResolveRequestV1{Catalog: catalog, Requirement: requirement, Budget: BudgetGrantV1{MaxCostUnits: 20, ContextBytes: 131072, OutputBytes: 32768}}); err == nil || !strings.Contains(err.Error(), "no catalog model") {
		t.Fatalf("no-match error = %v", err)
	}
}

func TestResolveV1ReducesCallerBudgetWithoutExpandingIt(t *testing.T) {
	requirement := DefaultRequirementV1()
	decision, err := ResolveV1(ResolveRequestV1{
		Catalog:     BuiltinCatalogV1(),
		Requirement: requirement,
		Budget:      BudgetGrantV1{MaxCostUnits: 10, ContextBytes: 64 * 1024, OutputBytes: 16 * 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := BudgetGrantV1{MaxCostUnits: 10, ContextBytes: 64 * 1024, OutputBytes: 16 * 1024}
	if decision.Budget != want {
		t.Fatalf("effective budget = %+v, want %+v", decision.Budget, want)
	}
	if decision.Selected.Model != "gpt-5.6-luna" {
		t.Fatalf("selected model = %q", decision.Selected.Model)
	}
}

func TestResolveV1DoesNotMutateInputs(t *testing.T) {
	catalog := BuiltinCatalogV1()
	requirement := DefaultRequirementV1()
	requirement.Candidates = []string{"codex/local/gpt-5.6-luna"}
	original := cloneRequirement(requirement)
	decision, err := ResolveV1(ResolveRequestV1{Catalog: catalog, Requirement: requirement, Budget: BudgetGrantV1{MaxCostUnits: 20, ContextBytes: 131072, OutputBytes: 32768}})
	if err != nil {
		t.Fatal(err)
	}
	decision.Requirement.Candidates[0] = "mutated"
	if !reflect.DeepEqual(requirement, original) || catalog.Models[1].ID != "codex/local/gpt-5.6-luna" {
		t.Fatal("resolver mutated caller input")
	}
}
