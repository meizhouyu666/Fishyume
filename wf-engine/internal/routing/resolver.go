package routing

import "sort"

// ResolveRequestV1 is the complete, deterministic input to the M6.3 resolver.
// The upper layer owns the budget grant; the resolver only reduces it against
// the requirement and never expands it.
type ResolveRequestV1 struct {
	Catalog     CapabilityCatalogV1
	CatalogHash string
	Requirement RoutingRequirementV1
	Budget      BudgetGrantV1
}

// ResolveV1 matches a requirement against a trusted immutable catalog. It is
// intentionally side-effect free: no Driver, Provider, clock, filesystem, or
// network is consulted.
func ResolveV1(request ResolveRequestV1) (RoutingDecisionV1, error) {
	if err := ValidateRequirement(request.Requirement); err != nil {
		return RoutingDecisionV1{}, err
	}
	if err := ValidateBudgetGrant(request.Budget); err != nil {
		return RoutingDecisionV1{}, err
	}
	if err := ValidateCatalog(request.Catalog); err != nil {
		return RoutingDecisionV1{}, err
	}
	catalogHash, err := CatalogHash(request.Catalog)
	if err != nil {
		return RoutingDecisionV1{}, err
	}
	if request.CatalogHash != "" && request.CatalogHash != catalogHash {
		return RoutingDecisionV1{}, contractError(CodeCatalogHashMismatch, "catalog hash does not match the trusted catalog", "")
	}

	budget := BudgetGrantV1{
		MaxCostUnits: request.Requirement.MaxCostUnits,
		ContextBytes: request.Requirement.MaxContextBytes,
		OutputBytes:  request.Requirement.MaxOutputBytes,
	}
	budget = restrictBudget(budget, request.Budget)

	candidates := eligibleModels(request.Catalog.Models, request.Requirement, budget)
	if len(candidates) == 0 {
		return RoutingDecisionV1{}, contractError(CodeInvalidTarget, "no catalog model satisfies the routing requirement and budget", "")
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].score.less(candidates[j].score)
	})

	selected := candidates[0].model
	decision := RoutingDecisionV1{
		SchemaVersion: RoutingDecisionV1Version,
		CatalogHash:   catalogHash,
		Requirement:   cloneRequirement(request.Requirement),
		Selected:      selected.Target,
		ReasonCodes:   reasonCodes(request.Requirement, selected, candidates[0].score),
		Budget:        budget,
		PromptProfile: request.Requirement.PromptProfile,
		FallbackPolicy: FallbackPolicyV1{
			Mode:        FallbackNone,
			MaxAttempts: 1,
		},
	}
	if request.Requirement.AllowModelFallback {
		fallbackCount := len(candidates) - 1
		if fallbackCount > MaxFallbacks {
			fallbackCount = MaxFallbacks
		}
		decision.Fallback = make([]Target, 0, fallbackCount)
		for _, candidate := range candidates[1 : fallbackCount+1] {
			decision.Fallback = append(decision.Fallback, candidate.model.Target)
		}
		decision.FallbackPolicy = FallbackPolicyV1{
			Mode:                FallbackEligible,
			MaxAttempts:         1 + len(decision.Fallback),
			RequireNoSideEffect: true,
			RequireApproval:     true,
		}
		decision.ReasonCodes = append(decision.ReasonCodes, "fallback_declared")
	} else {
		decision.ReasonCodes = append(decision.ReasonCodes, "fallback_disabled")
	}
	if err := ValidateDecision(decision); err != nil {
		return RoutingDecisionV1{}, err
	}
	return decision, nil
}

// Resolve is the short package-level spelling used by internal callers.
func Resolve(request ResolveRequestV1) (RoutingDecisionV1, error) {
	return ResolveV1(request)
}

type eligibleModel struct {
	model ModelCapabilityV1
	score modelScore
}

type modelScore struct {
	candidateRank int
	qualityDelta  int
	latencyDelta  int
	costUnits     int
	id            string
}

func (s modelScore) less(other modelScore) bool {
	if s.candidateRank != other.candidateRank {
		return s.candidateRank < other.candidateRank
	}
	if s.qualityDelta != other.qualityDelta {
		return s.qualityDelta < other.qualityDelta
	}
	if s.latencyDelta != other.latencyDelta {
		return s.latencyDelta < other.latencyDelta
	}
	if s.costUnits != other.costUnits {
		return s.costUnits < other.costUnits
	}
	return s.id < other.id
}

func eligibleModels(models []ModelCapabilityV1, requirement RoutingRequirementV1, budget BudgetGrantV1) []eligibleModel {
	allowed := make(map[string]int, len(requirement.Candidates))
	for index, candidate := range requirement.Candidates {
		allowed[candidate] = index
	}
	qualityFloor := qualityRank(minQualityForComplexity(requirement.Complexity))
	requestedQuality := qualityRank(requirement.Quality)
	requestedLatency := latencyRank(requirement.Latency)
	result := make([]eligibleModel, 0, len(models))
	for _, model := range models {
		rank := len(requirement.Candidates) + 1
		if len(requirement.Candidates) > 0 {
			var ok bool
			rank, ok = allowed[model.ID]
			if !ok {
				continue
			}
		}
		if !containsAll(model.Capabilities, requirement.Capabilities) ||
			qualityRank(model.Quality) < qualityFloor ||
			costUnits(model.Cost) > budget.MaxCostUnits ||
			model.ContextLimitBytes < budget.ContextBytes ||
			model.MaxOutputBytes < budget.OutputBytes {
			continue
		}
		result = append(result, eligibleModel{model: model, score: modelScore{
			candidateRank: rank,
			qualityDelta:  abs(qualityRank(model.Quality) - requestedQuality),
			latencyDelta:  abs(latencyRank(model.Latency) - requestedLatency),
			costUnits:     costUnits(model.Cost),
			id:            model.ID,
		}})
	}
	return result
}

func reasonCodes(requirement RoutingRequirementV1, selected ModelCapabilityV1, score modelScore) []string {
	reasons := []string{"capability_match", "complexity_" + string(requirement.Complexity), "quality_preference_" + string(selected.Quality), "latency_preference_" + string(selected.Latency), "cost_preference_" + string(selected.Cost), "budget_context", "budget_output"}
	if len(requirement.Candidates) > 0 && score.candidateRank <= len(requirement.Candidates) {
		reasons = append(reasons, "candidate_preference")
	}
	return reasons
}

func restrictBudget(requirement BudgetGrantV1, grant BudgetGrantV1) BudgetGrantV1 {
	if grant.MaxCostUnits < requirement.MaxCostUnits {
		requirement.MaxCostUnits = grant.MaxCostUnits
	}
	if grant.ContextBytes < requirement.ContextBytes {
		requirement.ContextBytes = grant.ContextBytes
	}
	if grant.OutputBytes < requirement.OutputBytes {
		requirement.OutputBytes = grant.OutputBytes
	}
	return requirement
}

func cloneRequirement(source RoutingRequirementV1) RoutingRequirementV1 {
	result := source
	result.Capabilities = append([]Capability(nil), source.Capabilities...)
	result.Candidates = append([]string(nil), source.Candidates...)
	return result
}

func containsAll(have, required []Capability) bool {
	set := make(map[Capability]struct{}, len(have))
	for _, capability := range have {
		set[capability] = struct{}{}
	}
	for _, capability := range required {
		if _, ok := set[capability]; !ok {
			return false
		}
	}
	return true
}

func minQualityForComplexity(complexity Complexity) QualityClass {
	switch complexity {
	case ComplexityComplex:
		return QualityPremium
	case ComplexityStandard:
		return QualityBalanced
	default:
		return QualityEconomy
	}
}

func qualityRank(value QualityClass) int {
	switch value {
	case QualityPremium:
		return 2
	case QualityBalanced:
		return 1
	default:
		return 0
	}
}

func latencyRank(value LatencyClass) int {
	switch value {
	case LatencySlow:
		return 2
	case LatencyBalanced:
		return 1
	default:
		return 0
	}
}

func costUnits(value CostClass) int {
	switch value {
	case CostHigh:
		return 100
	case CostMedium:
		return 10
	default:
		return 1
	}
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
