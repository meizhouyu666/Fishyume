package routing

import "fmt"

// ReserveRoutingUsageV1 reserves the selected route's catalog cost before an
// Attempt is launched. Counting reservations, rather than terminal results,
// keeps accounting conservative across crashes and indeterminate outcomes.
func ReserveRoutingUsageV1(catalog CapabilityCatalogV1, decision RoutingDecisionV1, priorCostUnits, routeIndex int) (RoutingUsageV1, error) {
	if err := ValidateCatalog(catalog); err != nil {
		return RoutingUsageV1{}, err
	}
	if err := ValidateDecision(decision); err != nil {
		return RoutingUsageV1{}, err
	}
	hash, err := CatalogHash(catalog)
	if err != nil {
		return RoutingUsageV1{}, err
	}
	if decision.CatalogHash != hash {
		return RoutingUsageV1{}, contractError(CodeCatalogHashMismatch, "routing decision catalog hash does not match the trusted catalog", "")
	}
	if priorCostUnits < 0 || priorCostUnits > MaxCostUnits {
		return RoutingUsageV1{}, contractError(CodeInvalidBudget, "prior routing cost is outside its bound", "")
	}
	cost, err := CostUnitsForTarget(catalog, decision.Selected)
	if err != nil {
		return RoutingUsageV1{}, err
	}
	cumulative := priorCostUnits + cost
	if cumulative > MaxCostUnits || cumulative > decision.Budget.MaxCostUnits {
		return RoutingUsageV1{}, contractError(CodeInvalidBudget, fmt.Sprintf("routing cost %d exceeds budget %d", cumulative, decision.Budget.MaxCostUnits), "")
	}
	usage := RoutingUsageV1{SchemaVersion: RoutingUsageV1Version, Target: decision.Selected, RouteIndex: routeIndex, CostUnits: cost, CumulativeCostUnits: cumulative}
	if err := ValidateRoutingUsageForDecision(catalog, decision, usage); err != nil {
		return RoutingUsageV1{}, err
	}
	return usage, nil
}

func ValidateRoutingUsageForDecision(catalog CapabilityCatalogV1, decision RoutingDecisionV1, usage RoutingUsageV1) error {
	if err := ValidateCatalog(catalog); err != nil {
		return err
	}
	if err := ValidateDecision(decision); err != nil {
		return err
	}
	if err := ValidateRoutingUsage(usage); err != nil {
		return err
	}
	hash, err := CatalogHash(catalog)
	if err != nil {
		return err
	}
	if decision.CatalogHash != hash {
		return contractError(CodeCatalogHashMismatch, "routing decision catalog hash does not match the trusted catalog", "")
	}
	if usage.Target != decision.Selected {
		return contractError(CodeInvalidTarget, "routing usage target conflicts with routing decision", usage.Target.Model)
	}
	cost, err := CostUnitsForTarget(catalog, usage.Target)
	if err != nil {
		return err
	}
	if usage.CostUnits != cost || usage.CumulativeCostUnits > decision.Budget.MaxCostUnits {
		return contractError(CodeInvalidBudget, "routing usage cost conflicts with the trusted catalog or budget", usage.Target.Model)
	}
	return nil
}

func CostUnitsForTarget(catalog CapabilityCatalogV1, target Target) (int, error) {
	if err := ValidateCatalog(catalog); err != nil {
		return 0, err
	}
	for _, model := range catalog.Models {
		if model.Target == target {
			return costUnits(model.Cost), nil
		}
	}
	return 0, contractError(CodeInvalidTarget, "routing target is absent from the trusted catalog", target.Model)
}

// AdvanceFallbackV1 derives the next immutable Attempt decision without
// rerunning the resolver. The caller owns approval and side-effect checks.
func AdvanceFallbackV1(previous RoutingDecisionV1) (RoutingDecisionV1, bool, error) {
	if err := ValidateDecision(previous); err != nil {
		return RoutingDecisionV1{}, false, err
	}
	if previous.FallbackPolicy.Mode != FallbackEligible || len(previous.Fallback) == 0 {
		return RoutingDecisionV1{}, false, nil
	}
	next := previous
	next.Requirement = cloneRequirement(previous.Requirement)
	next.Selected = previous.Fallback[0]
	next.Fallback = append([]Target(nil), previous.Fallback[1:]...)
	next.ReasonCodes = append([]string(nil), previous.ReasonCodes...)
	if !containsString(next.ReasonCodes, "fallback_selected") {
		next.ReasonCodes = append(next.ReasonCodes, "fallback_selected")
	}
	if err := ValidateDecision(next); err != nil {
		return RoutingDecisionV1{}, false, err
	}
	return next, true, nil
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
