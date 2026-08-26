package routing

import "testing"

func TestCodexProductPolicyPrefersSolAndMapsEffort(t *testing.T) {
	catalog := BuiltinCodexCatalogV2()
	for complexity, effort := range map[Complexity]string{ComplexitySimple: "low", ComplexityStandard: "medium", ComplexityComplex: "high"} {
		requirement := DefaultRequirementV1()
		requirement.Complexity = complexity
		requirement.MaxCostUnits = 1000
		requirement.AllowModelFallback = true
		requirement = ApplyCodexProductPreference(catalog, "codex", requirement)
		decision, err := ResolveV1(ResolveRequestV1{Catalog: catalog, Requirement: requirement, Budget: BudgetGrantV1{MaxCostUnits: requirement.MaxCostUnits, ContextBytes: requirement.MaxContextBytes, OutputBytes: requirement.MaxOutputBytes}})
		if err != nil {
			t.Fatal(err)
		}
		profile, err := ExecutionProfileForDecision(decision)
		if err != nil || decision.Selected.Model != "gpt-5.6-sol" || profile.ReasoningEffort != effort {
			t.Fatalf("complexity %s decision=%+v profile=%+v error=%v", complexity, decision, profile, err)
		}
		if len(decision.Fallback) < 1 || decision.Fallback[0].Model != "gpt-5.6-terra" || len(decision.Fallback) > 2 || len(decision.Fallback) == 2 && decision.Fallback[1].Model != "gpt-5.6-luna" {
			t.Fatalf("fallback order = %+v", decision.Fallback)
		}
	}
}

func TestCodexProductPolicyPreservesExplicitCandidates(t *testing.T) {
	requirement := DefaultRequirementV1()
	requirement.Candidates = []string{"codex/local/gpt-5.6-luna"}
	updated := ApplyCodexProductPreference(BuiltinCodexCatalogV2(), "codex", requirement)
	if len(updated.Candidates) != 1 || updated.Candidates[0] != requirement.Candidates[0] {
		t.Fatalf("explicit candidates changed: %+v", updated.Candidates)
	}
}
