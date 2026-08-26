package routingconfig

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wf.local/wf-engine/internal/driver/codexprocess"
	"wf.local/wf-engine/internal/routing"
)

type inspectorFixture struct {
	models []codexprocess.ModelInfo
	probes map[string]bool
}

func (f *inspectorFixture) DiscoverModels(context.Context) ([]codexprocess.ModelInfo, error) {
	return cloneModels(f.models), nil
}

func (f *inspectorFixture) ProbeModel(_ context.Context, model, effort string) codexprocess.ProbeResult {
	return codexprocess.ProbeResult{Model: model, Effort: effort, Available: f.probes[model], Diagnostic: "fixture probe"}
}

func TestConfigPersistsRevisionAndReplaysMutation(t *testing.T) {
	root := t.TempDir()
	service, err := NewService(root, &inspectorFixture{})
	if err != nil {
		t.Fatal(err)
	}
	initial := service.ConfigGet().Config
	request := ConfigUpdateRequest{SchemaVersion: APIVersion, MutationID: "disable-luna", ExpectedRevision: initial.Revision, RouteID: "codex/local/gpt-5.6-luna", Enabled: false}
	updated, err := service.ConfigUpdate(request)
	if err != nil || updated.Config.Revision != initial.Revision+1 || updated.Replayed {
		t.Fatalf("update = %+v, error=%v", updated, err)
	}
	replayed, err := service.ConfigUpdate(request)
	if err != nil || !replayed.Replayed || replayed.Config.Revision != updated.Config.Revision {
		t.Fatalf("replay = %+v, error=%v", replayed, err)
	}
	restarted, err := NewService(root, &inspectorFixture{})
	if err != nil {
		t.Fatal(err)
	}
	if restarted.ConfigGet().Config.Revision != updated.Config.Revision {
		t.Fatalf("restarted config = %+v", restarted.ConfigGet())
	}
	conflict := request
	conflict.MutationID = "stale"
	if _, err := restarted.ConfigUpdate(conflict); err == nil {
		t.Fatal("expected stale revision conflict")
	}
	data, err := os.ReadFile(filepath.Join(root, "config", "routing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || containsSecretField(string(data)) {
		t.Fatalf("configuration contains a secret field: %s", data)
	}
}

func TestDiscoveryDoesNotClaimAvailabilityAndProbeNarrowsCatalog(t *testing.T) {
	fixture := &inspectorFixture{
		models: []codexprocess.ModelInfo{
			{ID: "sol", Model: "gpt-5.6-sol", Default: true, DefaultEffort: "medium", SupportedEfforts: []string{"low", "medium", "high"}},
			{ID: "terra", Model: "gpt-5.6-terra", DefaultEffort: "medium", SupportedEfforts: []string{"medium"}},
			{ID: "unqualified", Model: "gpt-5.5", DefaultEffort: "medium", SupportedEfforts: []string{"medium"}},
		},
		probes: map[string]bool{"gpt-5.6-sol": true, "gpt-5.6-terra": false},
	}
	service, err := NewService(t.TempDir(), fixture)
	if err != nil {
		t.Fatal(err)
	}
	discovery, err := service.Discover(context.Background())
	if err != nil || len(discovery.Models) != 3 {
		t.Fatalf("discovery = %+v, error=%v", discovery, err)
	}
	for _, route := range discovery.Routes {
		if route.Availability != AvailabilityUnknown || route.Routable {
			t.Fatalf("discovery incorrectly proved availability: %+v", route)
		}
	}
	probed, err := service.Probe(context.Background(), ProbeRequest{SchemaVersion: APIVersion, RouteIDs: []string{"codex/local/gpt-5.6-sol", "codex/local/gpt-5.6-terra"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(probed.Entries) != 2 || probed.Entries[0].Status != AvailabilityAvailable || probed.Entries[1].Status != AvailabilityUnavailable {
		t.Fatalf("probe = %+v", probed)
	}
	effective, err := service.EffectiveCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(effective.Catalog.Models) != 1 || effective.Catalog.Models[0].Target.Model != "gpt-5.6-sol" {
		t.Fatalf("effective catalog = %+v", effective)
	}
	for _, model := range effective.Catalog.Models {
		if model.Target.Model == "gpt-5.5" {
			t.Fatal("unqualified discovered model became routable")
		}
	}
}

func TestCatalogSnapshotsSurviveRestartAndRetainM6Hash(t *testing.T) {
	root := t.TempDir()
	fixture := &inspectorFixture{models: []codexprocess.ModelInfo{{ID: "sol", Model: "gpt-5.6-sol", DefaultEffort: "medium"}}, probes: map[string]bool{"gpt-5.6-sol": true}}
	service, err := NewService(root, fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Probe(context.Background(), ProbeRequest{SchemaVersion: APIVersion, RouteIDs: []string{"codex/local/gpt-5.6-sol"}}); err != nil {
		t.Fatal(err)
	}
	_, activeHash, _ := service.ActiveCatalog()
	restarted, err := NewService(root, fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restarted.CatalogByHash(activeHash); !ok {
		t.Fatal("active dynamic catalog snapshot did not survive restart")
	}
	legacyHash, _ := routing.CatalogHash(routing.BuiltinCatalogV1())
	if _, ok := restarted.CatalogByHash(legacyHash); !ok {
		t.Fatal("frozen M6 catalog was not retained")
	}
}

func TestAllUnavailableRoutesPersistEvidenceAndBlockExecution(t *testing.T) {
	fixture := &inspectorFixture{
		models: []codexprocess.ModelInfo{
			{ID: "luna", Model: "gpt-5.6-luna", DefaultEffort: "medium"},
			{ID: "sol", Model: "gpt-5.6-sol", DefaultEffort: "medium"},
			{ID: "terra", Model: "gpt-5.6-terra", DefaultEffort: "medium"},
		},
		probes: map[string]bool{},
	}
	service, err := NewService(t.TempDir(), fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Probe(context.Background(), ProbeRequest{SchemaVersion: APIVersion}); err == nil {
		t.Fatal("expected all-unavailable probe to report capability failure")
	}
	availability := service.Availability()
	if len(availability.Entries) != 3 {
		t.Fatalf("availability = %+v", availability)
	}
	for _, entry := range availability.Entries {
		if entry.Status != AvailabilityUnavailable || entry.ObservedAt == nil || entry.ExpiresAt == nil {
			t.Fatalf("probe evidence was not retained: %+v", entry)
		}
	}
	for _, profile := range ProductProfiles() {
		target := routing.Target{Driver: "codex", Provider: "local", Model: profile.Model}
		if err := service.EnsureTargetAvailable(context.Background(), target); err == nil || !strings.Contains(err.Error(), "model_unavailable") {
			t.Fatalf("route %s execution gate error = %v", profile.RouteID, err)
		}
	}
	effective, err := service.EffectiveCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range effective.Routes {
		if route.Routable {
			t.Fatalf("unavailable route remained routable: %+v", route)
		}
	}
}

func TestDisabledAndUndiscoveredTargetsFailAvailabilityGate(t *testing.T) {
	fixture := &inspectorFixture{models: []codexprocess.ModelInfo{{ID: "sol", Model: "gpt-5.6-sol", DefaultEffort: "medium"}}, probes: map[string]bool{"gpt-5.6-sol": true}}
	service, err := NewService(t.TempDir(), fixture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := service.EnsureTargetAvailable(context.Background(), routing.Target{Driver: "codex", Provider: "local", Model: "gpt-5.6-terra"}); err == nil || !strings.Contains(err.Error(), "not exposed") {
		t.Fatalf("undiscovered target error = %v", err)
	}
	config := service.ConfigGet().Config
	updated, err := service.ConfigUpdate(ConfigUpdateRequest{SchemaVersion: APIVersion, MutationID: "disable-luna", ExpectedRevision: config.Revision, RouteID: "codex/local/gpt-5.6-luna", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.EnsureTargetAvailable(context.Background(), routing.Target{Driver: "codex", Provider: "local", Model: "gpt-5.6-luna"}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled target error = %v", err)
	}
	if _, err := service.ConfigUpdate(ConfigUpdateRequest{SchemaVersion: APIVersion, MutationID: "disable-sol", ExpectedRevision: updated.Config.Revision, RouteID: "codex/local/gpt-5.6-sol", Enabled: false}); err == nil {
		// With discovery narrowed to Sol, disabling it would leave no effective
		// candidate and must fail closed before the persisted config changes.
		t.Fatal("accepted configuration with no effective route")
	}
}

func containsSecretField(value string) bool {
	for _, field := range []string{"apiKey", "token", "baseUrl", "credential"} {
		if strings.Contains(value, field) {
			return true
		}
	}
	return false
}
