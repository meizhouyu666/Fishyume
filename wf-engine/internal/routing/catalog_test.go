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
