package run

import (
	"context"
	"fmt"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/workflow"
)

// resolveAttemptRouting resolves only against the Engine-owned catalog. A
// custom test/embedded Driver that has no catalog entry keeps the pre-M6.4
// behavior; production Drivers must have a matching catalog target.
func resolveAttemptRouting(driver string, requirement routing.RoutingRequirementV1) (*routing.RoutingDecisionV1, error) {
	return resolveAttemptRoutingWithCatalogs(routing.BuiltinCatalogRegistry(), driver, requirement)
}

func resolveAttemptRoutingWithCatalogs(catalogs routing.CatalogProvider, driver string, requirement routing.RoutingRequirementV1) (*routing.RoutingDecisionV1, error) {
	catalog, catalogHash, err := catalogs.ActiveCatalog()
	if err != nil {
		return nil, fmt.Errorf("load active routing catalog: %w", err)
	}
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
	requirement = routing.ApplyCodexProductPreference(catalog, driver, requirement)
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

func routingExecutionProfile(decision *routing.RoutingDecisionV1) (*routing.ExecutionProfileV1, error) {
	if decision == nil {
		return nil, nil
	}
	profile, err := routing.ExecutionProfileForDecision(*decision)
	if err != nil {
		return nil, err
	}
	return &profile, nil
}

func routingReasoningEffort(profile *routing.ExecutionProfileV1) string {
	if profile == nil {
		return ""
	}
	return profile.ReasoningEffort
}

func (s *Service) prepareAttemptRouting(ctx context.Context, runID string, node NodeSnapshot, driver string, requirement routing.RoutingRequirementV1) (*routing.RoutingDecisionV1, *routing.RoutingUsageV1, error) {
	var decision *routing.RoutingDecisionV1
	var err error
	for probeAttempt := 0; probeAttempt < 3; probeAttempt++ {
		decision, err = resolveAttemptRoutingWithCatalogs(s.catalogs, driver, requirement)
		if err != nil || decision == nil {
			return decision, nil, err
		}
		gate, ok := s.catalogs.(routing.TargetAvailabilityGate)
		if !ok {
			break
		}
		if err = gate.EnsureTargetAvailable(ctx, decision.Selected); err == nil {
			break
		}
		if probeAttempt == 2 {
			return nil, nil, err
		}
	}
	routeIndex := 0
	if node.PendingRoutingTarget != nil {
		if node.CurrentAttempt < 1 {
			return nil, nil, fmt.Errorf("pending route requires Attempt history")
		}
		var previous AttemptSnapshot
		if err := s.store.ReadAttempt(runID, node.ID, node.CurrentAttempt, &previous); err != nil {
			return nil, nil, err
		}
		if previous.RoutingDecision == nil {
			return nil, nil, fmt.Errorf("pending route cannot continue a historical Attempt without a routing decision")
		}
		if previous.RoutingDecision.Selected == *node.PendingRoutingTarget {
			copy := cloneRoutingDecision(*previous.RoutingDecision)
			decision = &copy
			if previous.RoutingUsage != nil {
				routeIndex = previous.RoutingUsage.RouteIndex
			}
		} else {
			next, ok, err := routing.AdvanceFallbackV1(*previous.RoutingDecision)
			if err != nil {
				return nil, nil, err
			}
			if !ok || next.Selected != *node.PendingRoutingTarget {
				return nil, nil, fmt.Errorf("pending route is not the next persisted fallback target")
			}
			decision = &next
			routeIndex = 1
			if previous.RoutingUsage != nil {
				routeIndex = previous.RoutingUsage.RouteIndex + 1
			}
		}
	}
	if gate, ok := s.catalogs.(routing.TargetAvailabilityGate); ok {
		if err := gate.EnsureTargetAvailable(ctx, decision.Selected); err != nil {
			return nil, nil, err
		}
	}
	if decision.Selected.Driver != driver {
		return nil, nil, fmt.Errorf("routing selected Driver %q for requested Driver %q", decision.Selected.Driver, driver)
	}
	priorCost, err := s.routingCostBeforeAttempt(runID, node.ID, node.CurrentAttempt+1)
	if err != nil {
		return nil, nil, err
	}
	catalog, ok := s.catalogs.CatalogByHash(decision.CatalogHash)
	if !ok {
		return nil, nil, fmt.Errorf("routing catalog %q is unavailable", decision.CatalogHash)
	}
	usage, err := routing.ReserveRoutingUsageV1(catalog, *decision, priorCost, routeIndex)
	if err != nil {
		return nil, nil, err
	}
	return decision, &usage, nil
}

func (s *Service) routingCostBeforeAttempt(runID, nodeID string, nextAttempt int) (int, error) {
	numbers, err := s.store.ListAttempts(runID, nodeID)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, number := range numbers {
		if number >= nextAttempt {
			continue
		}
		var attempt AttemptSnapshot
		if err := s.store.ReadAttempt(runID, nodeID, number, &attempt); err != nil {
			return 0, err
		}
		if attempt.RoutingUsage != nil {
			if attempt.RoutingDecision == nil {
				return 0, fmt.Errorf("routing usage requires a routing decision")
			}
			catalog, ok := s.catalogs.CatalogByHash(attempt.RoutingDecision.CatalogHash)
			if !ok {
				return 0, fmt.Errorf("historical routing catalog %q is unavailable", attempt.RoutingDecision.CatalogHash)
			}
			if err := routing.ValidateRoutingUsageForDecision(catalog, *attempt.RoutingDecision, *attempt.RoutingUsage); err != nil {
				return 0, err
			}
			expectedCumulative := total + attempt.RoutingUsage.CostUnits
			if attempt.RoutingUsage.CumulativeCostUnits != expectedCumulative {
				return 0, fmt.Errorf("routing usage cumulative cost %d does not match expected cost %d", attempt.RoutingUsage.CumulativeCostUnits, expectedCumulative)
			}
			total = expectedCumulative
		} else if attempt.RoutingDecision != nil {
			if err := routing.ValidateDecision(*attempt.RoutingDecision); err != nil {
				return 0, err
			}
			catalog, ok := s.catalogs.CatalogByHash(attempt.RoutingDecision.CatalogHash)
			if !ok {
				return 0, fmt.Errorf("historical routing catalog %q is unavailable", attempt.RoutingDecision.CatalogHash)
			}
			cost, err := routing.CostUnitsForTarget(catalog, attempt.RoutingDecision.Selected)
			if err != nil {
				return 0, err
			}
			total += cost
		}
		if total > routing.MaxCostUnits {
			return 0, fmt.Errorf("routing cost exceeds global bound")
		}
	}
	return total, nil
}

func (s *Service) pendingRoutingTarget(ctx context.Context, run *WorkflowSnapshot, node NodeSnapshot, allowFallback bool) (*routing.Target, error) {
	if node.CurrentAttempt < 1 {
		return nil, nil
	}
	var previous AttemptSnapshot
	if err := s.store.ReadAttempt(run.ID, node.ID, node.CurrentAttempt, &previous); err != nil {
		return nil, err
	}
	if previous.RoutingDecision == nil {
		return nil, nil
	}
	target := previous.RoutingDecision.Selected
	_, dynamicRouting := s.catalogs.(routing.TargetAvailabilityGate)
	classifiedUnavailable := previous.FailureClass == backend.FailureModelUnavailablePreExecution
	if allowFallback && previous.Conclusion == ConclusionFailed && previous.SideEffectStatus == agent.SideEffectNone && (!dynamicRouting || classifiedUnavailable) {
		next, ok, err := routing.AdvanceFallbackV1(*previous.RoutingDecision)
		if err != nil {
			return nil, err
		}
		if ok {
			target = next.Selected
		}
	}
	var normalized workflow.Normalized
	if err := s.store.ReadWorkflow(run.ID, &normalized); err != nil {
		return nil, err
	}
	definition, ok := normalized.Document.Nodes[node.ID]
	if !ok {
		return nil, fmt.Errorf("workflow node %q is missing", node.ID)
	}
	driver, _, err := workflow.ResolveAgent(normalized.Document.Defaults, definition)
	if err != nil {
		return nil, err
	}
	preview := node
	preview.PendingRoutingTarget = &target
	if _, _, err := s.prepareAttemptRouting(ctx, run.ID, preview, driver, workflow.EffectiveRoutingRequirement(normalized.Document, definition)); err != nil {
		return nil, err
	}
	copy := target
	return &copy, nil
}
