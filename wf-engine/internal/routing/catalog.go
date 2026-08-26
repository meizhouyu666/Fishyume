package routing

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

const BuiltinCatalogSourceV1 = "fishyume.builtin"
const DynamicCatalogSourceV1 = "fishyume.dynamic"

const (
	AgentRoutesFileEnv  = "FISHYUME_AGENT_ROUTES_FILE"
	MaxCatalogFileBytes = 1024 * 1024
)

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

// BuiltinCodexCatalogV2 is the M7.8 product-qualified Codex family. Its v1
// wire shape is retained for resolver compatibility; the function name marks
// product policy generation rather than a new capability-catalog schema.
func BuiltinCodexCatalogV2() CapabilityCatalogV1 {
	capabilities := []Capability{CapabilityNeedsInput, CapabilityRepoEdit, CapabilityRepoRead, CapabilityStreaming, CapabilityStructuredOutput, CapabilityToolUse}
	model := func(name string, quality QualityClass, cost CostClass, latency LatencyClass, contextBytes, outputBytes int) ModelCapabilityV1 {
		return ModelCapabilityV1{
			ID: "codex/local/" + name, Target: Target{Driver: "codex", Provider: "local", Model: name},
			Capabilities: append([]Capability(nil), capabilities...), ContextLimitBytes: contextBytes, MaxOutputBytes: outputBytes,
			Quality: quality, Cost: cost, Latency: latency, SupportsCancellation: true,
		}
	}
	return CanonicalCatalogV1(CapabilityCatalogV1{
		SchemaVersion: CapabilityCatalogV1Version,
		PolicyVersion: RoutingPolicyV1Version,
		Models: []ModelCapabilityV1{
			model("gpt-5.6-sol", QualityPremium, CostMedium, LatencyBalanced, 256*1024, 64*1024),
			model("gpt-5.6-terra", QualityPremium, CostHigh, LatencySlow, 256*1024, 64*1024),
			model("gpt-5.6-luna", QualityEconomy, CostLow, LatencyFast, 128*1024, 32*1024),
		},
	})
}

// BuiltinTeamCatalogV1 is the usable production default for Team workers. It
// is deliberately separate from the frozen M6 Workflow catalog: two
// independent roles may use the same locally available Codex model without
// retaining the unavailable Luna route.
func BuiltinTeamCatalogV1() CapabilityCatalogV1 {
	capabilities := []Capability{CapabilityNeedsInput, CapabilityRepoRead, CapabilityStreaming, CapabilityToolUse}
	model := func(id string) ModelCapabilityV1 {
		return ModelCapabilityV1{
			ID: id, Target: Target{Driver: "codex", Provider: "local", Model: "gpt-5.6-sol"},
			Capabilities: append([]Capability(nil), capabilities...), ContextLimitBytes: 256 * 1024, MaxOutputBytes: 64 * 1024,
			Quality: QualityPremium, Cost: CostHigh, Latency: LatencyBalanced, SupportsCancellation: true,
		}
	}
	return CapabilityCatalogV1{
		SchemaVersion: CapabilityCatalogV1Version,
		PolicyVersion: RoutingPolicyV1Version,
		Models: []ModelCapabilityV1{
			model("codex/architect/gpt-5.6-sol"),
			model("codex/reviewer/gpt-5.6-sol"),
		},
	}
}

// LoadCatalogFromEnvironment loads the trusted Team Agent routes selected by
// the Engine operator. An unset variable preserves the frozen M6 catalog.
func LoadCatalogFromEnvironment() (CapabilityCatalogV1, error) {
	path := os.Getenv(AgentRoutesFileEnv)
	if path == "" {
		return BuiltinTeamCatalogV1(), nil
	}
	return LoadCatalogFile(path)
}

// LoadCatalogFile strictly decodes and canonicalizes a trusted Agent route
// catalog. The file contains route metadata only; credentials stay owned by
// the selected Agent harness.
func LoadCatalogFile(path string) (CapabilityCatalogV1, error) {
	if !filepath.IsAbs(path) {
		return CapabilityCatalogV1{}, fmt.Errorf("%s must be an absolute path", AgentRoutesFileEnv)
	}
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return CapabilityCatalogV1{}, fmt.Errorf("open Agent route catalog: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return CapabilityCatalogV1{}, fmt.Errorf("stat Agent route catalog: %w", err)
	}
	if !info.Mode().IsRegular() {
		return CapabilityCatalogV1{}, fmt.Errorf("Agent route catalog is not a regular file")
	}
	if info.Size() > MaxCatalogFileBytes {
		return CapabilityCatalogV1{}, fmt.Errorf("Agent route catalog exceeds %d bytes", MaxCatalogFileBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, MaxCatalogFileBytes+1))
	decoder.DisallowUnknownFields()
	var catalog CapabilityCatalogV1
	if err := decoder.Decode(&catalog); err != nil {
		return CapabilityCatalogV1{}, fmt.Errorf("decode Agent route catalog: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return CapabilityCatalogV1{}, fmt.Errorf("Agent route catalog contains trailing data")
	}
	catalog = CanonicalCatalogV1(catalog)
	if err := ValidateCatalog(catalog); err != nil {
		return CapabilityCatalogV1{}, fmt.Errorf("validate Agent route catalog: %w", err)
	}
	return catalog, nil
}

// CanonicalCatalogV1 returns an isolated catalog copy in the ordering required
// by the routing contract and CatalogHash.
func CanonicalCatalogV1(source CapabilityCatalogV1) CapabilityCatalogV1 {
	result := cloneCatalog(source)
	for index := range result.Models {
		sort.Slice(result.Models[index].Capabilities, func(left, right int) bool {
			return result.Models[index].Capabilities[left] < result.Models[index].Capabilities[right]
		})
	}
	sort.Slice(result.Models, func(left, right int) bool {
		return result.Models[left].ID < result.Models[right].ID
	})
	return result
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
