package routing

import "testing"

const builtinCatalogHashV1 = "7b993c75a20b1a783a1ab9aaeae1235c6514f408f33bc8bdc32de9e85be4a1da"

func TestBuiltinCatalogV1IsValidAndHashPinned(t *testing.T) {
	catalog := BuiltinCatalogV1()
	if err := ValidateCatalog(catalog); err != nil {
		t.Fatal(err)
	}
	hash, err := CatalogHash(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if hash != builtinCatalogHashV1 {
		t.Fatalf("builtin catalog hash = %s, want %s", hash, builtinCatalogHashV1)
	}
}

func TestBuiltinCatalogV1ReturnsMutationIsolatedCopies(t *testing.T) {
	first := BuiltinCatalogV1()
	first.Models[0].ID = "changed"
	first.Models[0].Capabilities[0] = CapabilityRepoRead
	second := BuiltinCatalogV1()
	if second.Models[0].ID != "codex/local/gpt-5.6" || second.Models[0].Capabilities[0] != CapabilityNeedsInput {
		t.Fatalf("builtin catalog was mutated through a returned value: %+v", second.Models[0])
	}
}

func TestBuiltinCodexCatalogV2ContainsQualifiedGPT56Family(t *testing.T) {
	catalog := BuiltinCodexCatalogV2()
	if err := ValidateCatalog(catalog); err != nil {
		t.Fatal(err)
	}
	want := []string{"gpt-5.6-luna", "gpt-5.6-sol", "gpt-5.6-terra"}
	for index, model := range catalog.Models {
		if model.Target.Model != want[index] || model.Target.Driver != "codex" || model.Target.Provider != "local" {
			t.Fatalf("model[%d] = %+v", index, model)
		}
	}
}

// TestBuiltinCodexCatalogV2TiersMatchOfficialNaming pins the GPT-5.6 family
// tiering to the official semantics: Sol (sun) is the flagship/high-cost tier,
// Terra (earth) is the balanced mid tier, Luna (moon) is the budget tier.
func TestBuiltinCodexCatalogV2TiersMatchOfficialNaming(t *testing.T) {
	catalog := BuiltinCodexCatalogV2()
	byModel := map[string]ModelCapabilityV1{}
	for _, model := range catalog.Models {
		byModel[model.Target.Model] = model
	}
	sol := byModel["gpt-5.6-sol"]
	terra := byModel["gpt-5.6-terra"]
	luna := byModel["gpt-5.6-luna"]
	if sol.Cost != CostHigh || sol.Latency != LatencyBalanced {
		t.Fatalf("sol tier = cost %s latency %s, want high/balanced (flagship)", sol.Cost, sol.Latency)
	}
	if terra.Cost != CostMedium || terra.Latency != LatencyBalanced {
		t.Fatalf("terra tier = cost %s latency %s, want medium/balanced (mid)", terra.Cost, terra.Latency)
	}
	if luna.Cost != CostLow || luna.Latency != LatencyFast {
		t.Fatalf("luna tier = cost %s latency %s, want low/fast (budget)", luna.Cost, luna.Latency)
	}
}
