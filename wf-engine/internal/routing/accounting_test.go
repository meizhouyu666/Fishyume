package routing

import (
	"strings"
	"testing"
)

func TestReserveRoutingUsageV1AccountsCumulativelyWithinBudget(t *testing.T) {
	catalog := BuiltinCatalogV1()
	requirement := DefaultRequirementV1()
	requirement.MaxCostUnits = 101
	requirement.AllowModelFallback = true
	decision, err := ResolveV1(ResolveRequestV1{Catalog: catalog, Requirement: requirement, Budget: BudgetGrantV1{MaxCostUnits: 101, ContextBytes: requirement.MaxContextBytes, OutputBytes: requirement.MaxOutputBytes}})
	if err != nil {
		t.Fatal(err)
	}
	primary, err := ReserveRoutingUsageV1(catalog, decision, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if primary.CostUnits != 1 || primary.CumulativeCostUnits != 1 || primary.Target.Model != "gpt-5.6-luna" {
		t.Fatalf("primary usage = %+v", primary)
	}
	next, ok, err := AdvanceFallbackV1(decision)
	if err != nil || !ok {
		t.Fatalf("advance fallback: ok=%v err=%v", ok, err)
	}
	fallback, err := ReserveRoutingUsageV1(catalog, next, primary.CumulativeCostUnits, 1)
	if err != nil {
		t.Fatal(err)
	}
	if fallback.CostUnits != 100 || fallback.CumulativeCostUnits != 101 || fallback.RouteIndex != 1 || fallback.Target.Model != "gpt-5.6" {
		t.Fatalf("fallback usage = %+v", fallback)
	}
}

func TestReserveRoutingUsageV1RejectsCumulativeBudgetOverflow(t *testing.T) {
	catalog := BuiltinCatalogV1()
	requirement := DefaultRequirementV1()
	requirement.MaxCostUnits = 100
	requirement.AllowModelFallback = true
	decision, err := ResolveV1(ResolveRequestV1{Catalog: catalog, Requirement: requirement, Budget: BudgetGrantV1{MaxCostUnits: 100, ContextBytes: requirement.MaxContextBytes, OutputBytes: requirement.MaxOutputBytes}})
	if err != nil {
		t.Fatal(err)
	}
	next, ok, err := AdvanceFallbackV1(decision)
	if err != nil || !ok {
		t.Fatalf("advance fallback: ok=%v err=%v", ok, err)
	}
	if _, err := ReserveRoutingUsageV1(catalog, next, 1, 1); err == nil || !strings.Contains(err.Error(), string(CodeInvalidBudget)) {
		t.Fatalf("budget overflow error = %v", err)
	}
}

func TestValidateRoutingUsageForDecisionRejectsUnderreportedCatalogCost(t *testing.T) {
	catalog := BuiltinCatalogV1()
	requirement := DefaultRequirementV1()
	requirement.Complexity = ComplexityComplex
	requirement.MaxCostUnits = 100
	requirement.MaxContextBytes = 256 * 1024
	requirement.MaxOutputBytes = 64 * 1024
	decision, err := ResolveV1(ResolveRequestV1{Catalog: catalog, Requirement: requirement, Budget: BudgetGrantV1{MaxCostUnits: 100, ContextBytes: requirement.MaxContextBytes, OutputBytes: requirement.MaxOutputBytes}})
	if err != nil {
		t.Fatal(err)
	}
	usage := RoutingUsageV1{SchemaVersion: RoutingUsageV1Version, Target: decision.Selected, CostUnits: 1, CumulativeCostUnits: 1}
	if err := ValidateRoutingUsageForDecision(catalog, decision, usage); err == nil || !strings.Contains(err.Error(), string(CodeInvalidBudget)) {
		t.Fatalf("underreported cost error = %v", err)
	}
}
