package routing

import "testing"

func TestExecutionProfileUsesPersistedDecisionComplexity(t *testing.T) {
	for complexity, want := range map[Complexity]string{ComplexitySimple: "low", ComplexityStandard: "medium", ComplexityComplex: "high"} {
		requirement := DefaultRequirementV1()
		requirement.Complexity = complexity
		requirement.MaxCostUnits = 100
		decision, err := ResolveV1(ResolveRequestV1{Catalog: BuiltinCatalogV1(), Requirement: requirement, Budget: BudgetGrantV1{MaxCostUnits: requirement.MaxCostUnits, ContextBytes: requirement.MaxContextBytes, OutputBytes: requirement.MaxOutputBytes}})
		if err != nil {
			t.Fatal(err)
		}
		profile, err := ExecutionProfileForDecision(decision)
		if err != nil || profile.ReasoningEffort != want || profile.Target != decision.Selected {
			t.Fatalf("complexity %s profile=%+v error=%v", complexity, profile, err)
		}
	}
}
