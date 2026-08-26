package run

import (
	"testing"

	"wf.local/wf-engine/internal/routing"
)

func TestM78HistoricalAttemptValidatesAfterActiveCatalogChanges(t *testing.T) {
	legacy := routing.BuiltinCatalogV1()
	decision, err := resolveAttemptRouting("codex", routing.DefaultRequirementV1())
	if err != nil {
		t.Fatal(err)
	}
	usage, err := routing.ReserveRoutingUsageV1(legacy, *decision, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	current := routing.BuiltinCatalogV1()
	current.Models[0].ID = "codex/local/gpt-5.6-sol"
	current.Models[0].Target.Model = "gpt-5.6-sol"
	current = routing.CanonicalCatalogV1(current)
	registry, err := routing.NewCatalogRegistry(current, legacy)
	if err != nil {
		t.Fatal(err)
	}
	attempt := AttemptSnapshot{
		Number: 1, Phase: NodePhaseRunning, ResolvedDriver: "codex", ResolvedTarget: "local",
		LaunchState: LaunchPrepared, RoutingDecision: decision, RoutingUsage: &usage,
	}
	if err := ValidateAttemptSnapshotWithCatalogs(attempt, registry); err != nil {
		t.Fatalf("historical Attempt did not validate against its catalog hash: %v", err)
	}
	active, activeHash, err := registry.ActiveCatalog()
	if err != nil || activeHash == decision.CatalogHash || active.Models[0].Target.Model == "gpt-5.6" {
		t.Fatalf("test did not activate a distinct catalog: catalog=%+v hash=%q error=%v", active, activeHash, err)
	}
}

func TestM78UnknownHistoricalCatalogFailsClosed(t *testing.T) {
	decision, err := resolveAttemptRouting("codex", routing.DefaultRequirementV1())
	if err != nil {
		t.Fatal(err)
	}
	usage, err := routing.ReserveRoutingUsageV1(routing.BuiltinCatalogV1(), *decision, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	decision.CatalogHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	attempt := AttemptSnapshot{Number: 1, Phase: NodePhaseRunning, ResolvedDriver: "codex", RoutingDecision: decision, RoutingUsage: &usage}
	if err := ValidateAttemptSnapshotWithCatalogs(attempt, routing.BuiltinCatalogRegistry()); err == nil {
		t.Fatal("expected an unknown historical catalog hash to fail closed")
	}
}
