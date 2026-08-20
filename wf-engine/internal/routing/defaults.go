package routing

// DefaultRequirementV1 is the compatibility requirement used for an Agent
// Node that predates M6.2. It is deliberately bounded and conservative: it
// does not opt a legacy Workflow into automatic model fallback.
func DefaultRequirementV1() RoutingRequirementV1 {
	return RoutingRequirementV1{
		SchemaVersion:      RoutingRequirementV1Version,
		Capabilities:       []Capability{CapabilityRepoEdit, CapabilityRepoRead, CapabilityStructuredOutput, CapabilityToolUse},
		Complexity:         ComplexityStandard,
		Quality:            QualityBalanced,
		Latency:            LatencyBalanced,
		MaxCostUnits:       20,
		MaxContextBytes:    128 * 1024,
		MaxOutputBytes:     32 * 1024,
		AllowModelFallback: false,
	}
}
