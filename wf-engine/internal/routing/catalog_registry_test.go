package routing

import "testing"

func TestCatalogRegistryKeepsHistoricalCatalogsAndIsolatesCopies(t *testing.T) {
	legacy := BuiltinCatalogV1()
	current := BuiltinCatalogV1()
	current.Models[0].ID = "codex/local/gpt-5.6-sol"
	current.Models[0].Target.Model = "gpt-5.6-sol"
	current = CanonicalCatalogV1(current)

	registry, err := NewCatalogRegistry(current, legacy)
	if err != nil {
		t.Fatal(err)
	}
	legacyHash, _ := CatalogHash(legacy)
	loaded, ok := registry.CatalogByHash(legacyHash)
	if !ok || loaded.Models[0].Target.Model != "gpt-5.6" {
		t.Fatalf("historical catalog was not retained: ok=%v catalog=%+v", ok, loaded)
	}
	loaded.Models[0].Target.Model = "mutated"
	reloaded, ok := registry.CatalogByHash(legacyHash)
	if !ok || reloaded.Models[0].Target.Model != "gpt-5.6" {
		t.Fatalf("registry copy was mutated: ok=%v catalog=%+v", ok, reloaded)
	}
	active, activeHash, err := registry.ActiveCatalog()
	if err != nil || active.Models[0].Target.Model == "gpt-5.6" {
		t.Fatalf("active catalog = %+v, hash=%q, error=%v", active, activeHash, err)
	}
}

func TestCatalogRegistryRejectsInvalidCatalog(t *testing.T) {
	invalid := BuiltinCatalogV1()
	invalid.Models = nil
	if _, err := NewCatalogRegistry(invalid); err == nil {
		t.Fatal("expected invalid catalog to be rejected")
	}
}
