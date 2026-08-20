package routing

const BuiltinCatalogSourceV1 = "fishyume.builtin"

// BuiltinCatalogV1 returns a fresh copy of the trusted catalog compiled into
// the Engine. M6.1 does not read project files, credentials, environment
// variables, or Provider APIs when constructing this value.
func BuiltinCatalogV1() CapabilityCatalogV1 {
	return cloneCatalog(CapabilityCatalogV1{
		SchemaVersion: CapabilityCatalogV1Version,
		PolicyVersion: RoutingPolicyV1Version,
		Models: []ModelCapabilityV1{
			{
				ID:                "codex/local/gpt-5.6",
				Target:            Target{Driver: "codex", Provider: "local", Model: "gpt-5.6"},
				Capabilities:      []Capability{CapabilityNeedsInput, CapabilityRepoEdit, CapabilityRepoRead, CapabilityStreaming, CapabilityStructuredOutput, CapabilityToolUse},
				ContextLimitBytes: 256 * 1024, MaxOutputBytes: 64 * 1024,
				Quality: QualityPremium, Cost: CostHigh, Latency: LatencyBalanced,
				SupportsCancellation: true,
			},
			{
				ID:                "codex/local/gpt-5.6-luna",
				Target:            Target{Driver: "codex", Provider: "local", Model: "gpt-5.6-luna"},
				Capabilities:      []Capability{CapabilityRepoEdit, CapabilityRepoRead, CapabilityStructuredOutput, CapabilityToolUse},
				ContextLimitBytes: 128 * 1024, MaxOutputBytes: 32 * 1024,
				Quality: QualityBalanced, Cost: CostLow, Latency: LatencyFast,
				SupportsCancellation: true,
			},
		},
	})
}

func cloneCatalog(source CapabilityCatalogV1) CapabilityCatalogV1 {
	result := source
	result.Models = make([]ModelCapabilityV1, len(source.Models))
	for index, model := range source.Models {
		result.Models[index] = model
		result.Models[index].Capabilities = append([]Capability(nil), model.Capabilities...)
	}
	return result
}
