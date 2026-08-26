package routing

import "fmt"

const ExecutionProfileV1Version = "fishyume.execution-profile/v1"

type ExecutionProfileV1 struct {
	SchemaVersion   string `json:"schemaVersion"`
	Target          Target `json:"target"`
	ReasoningEffort string `json:"reasoningEffort"`
}

func ExecutionProfileForDecision(decision RoutingDecisionV1) (ExecutionProfileV1, error) {
	if err := ValidateDecision(decision); err != nil {
		return ExecutionProfileV1{}, err
	}
	effort := "medium"
	switch decision.Requirement.Complexity {
	case ComplexitySimple:
		effort = "low"
	case ComplexityComplex:
		effort = "high"
	}
	return ExecutionProfileV1{SchemaVersion: ExecutionProfileV1Version, Target: decision.Selected, ReasoningEffort: effort}, nil
}

func ValidateExecutionProfile(profile ExecutionProfileV1, decision *RoutingDecisionV1) error {
	if profile.SchemaVersion != ExecutionProfileV1Version || ValidateTarget(profile.Target) != nil {
		return fmt.Errorf("execution profile identity is invalid")
	}
	switch profile.ReasoningEffort {
	case "low", "medium", "high", "xhigh", "max", "ultra":
	default:
		return fmt.Errorf("execution profile reasoning effort %q is invalid", profile.ReasoningEffort)
	}
	if decision != nil && profile.Target != decision.Selected {
		return fmt.Errorf("execution profile target does not match routing decision")
	}
	return nil
}
