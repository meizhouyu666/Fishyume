package routing

// ApplyCodexProductPreference makes the qualified GPT-5.6 family prefer Sol
// without changing explicit Workflow candidate order or the frozen M6 catalog.
func ApplyCodexProductPreference(catalog CapabilityCatalogV1, driver string, requirement RoutingRequirementV1) RoutingRequirementV1 {
	if len(requirement.Candidates) != 0 || driver != "codex" {
		return requirement
	}
	ids := make(map[string]bool, len(catalog.Models))
	for _, model := range catalog.Models {
		ids[model.ID] = true
	}
	if !ids["codex/local/gpt-5.6-sol"] {
		return requirement
	}
	result := cloneRequirement(requirement)
	for _, id := range []string{"codex/local/gpt-5.6-sol", "codex/local/gpt-5.6-terra", "codex/local/gpt-5.6-luna"} {
		if ids[id] {
			result.Candidates = append(result.Candidates, id)
		}
	}
	return result
}
