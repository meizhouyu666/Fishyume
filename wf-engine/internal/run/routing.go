package run

import (
	"fmt"

	"wf.local/wf-engine/internal/routing"
)

// resolveAttemptRouting resolves only against the Engine-owned catalog. A
// custom test/embedded Driver that has no catalog entry keeps the pre-M6.4
// behavior; production Drivers must have a matching catalog target.
func resolveAttemptRouting(driver string, requirement routing.RoutingRequirementV1) (*routing.RoutingDecisionV1, error) {
	catalog := routing.BuiltinCatalogV1()
	knownDriver := false
	for _, model := range catalog.Models {
		if model.Target.Driver == driver {
			knownDriver = true
			break
		}
	}
	if !knownDriver {
		return nil, nil
	}
	catalogHash, err := routing.CatalogHash(catalog)
	if err != nil {
		return nil, fmt.Errorf("hash routing catalog: %w", err)
	}
	decision, err := routing.ResolveV1(routing.ResolveRequestV1{
		Catalog: catalog, CatalogHash: catalogHash, Requirement: requirement,
		Budget: routing.BudgetGrantV1{MaxCostUnits: requirement.MaxCostUnits, ContextBytes: requirement.MaxContextBytes, OutputBytes: requirement.MaxOutputBytes},
	})
	if err != nil {
		return nil, err
	}
	if decision.Selected.Driver != driver {
		return nil, fmt.Errorf("routing selected Driver %q for requested Driver %q", decision.Selected.Driver, driver)
	}
	copy := cloneRoutingDecision(decision)
	return &copy, nil
}

func cloneRoutingDecision(source routing.RoutingDecisionV1) routing.RoutingDecisionV1 {
	result := source
	result.Requirement.Capabilities = append([]routing.Capability(nil), source.Requirement.Capabilities...)
	result.Requirement.Candidates = append([]string(nil), source.Requirement.Candidates...)
	result.ReasonCodes = append([]string(nil), source.ReasonCodes...)
	result.Fallback = append([]routing.Target(nil), source.Fallback...)
	return result
}

func routingModel(decision *routing.RoutingDecisionV1) string {
	if decision == nil {
		return ""
	}
	return decision.Selected.Model
}
