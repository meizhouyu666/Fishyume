package routingconfig

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"wf.local/wf-engine/internal/routing"
)

func TestTeamRoutesAutoDiscoverPersistAndMutate(t *testing.T) {
	t.Setenv(routing.AgentRoutesFileEnv, "")
	bin := t.TempDir()
	writeDiscoveryFixture(t, bin, "codex")
	t.Setenv("PATH", bin)
	t.Setenv("FISHYUME_CODEX_PATH", filepath.Join(bin, executableFixtureName("codex")))
	t.Setenv("FISHYUME_CLAUDE_PATH", filepath.Join(bin, executableFixtureName("missing-claude")))
	t.Setenv("FISHYUME_OPENCODE_PATH", filepath.Join(bin, executableFixtureName("missing-opencode")))
	root := t.TempDir()
	service, err := NewService(root, &inspectorFixture{})
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.TeamRoutesGet()
	if err != nil {
		t.Fatal(err)
	}
	if view.Config.Revision != 1 || len(view.Config.Routes) != 4 || view.CatalogHash == "" {
		t.Fatalf("initial Team routes = %+v", view)
	}
	available := map[string]bool{}
	for _, driver := range view.Drivers {
		available[driver.Driver] = driver.Available
	}
	if !available["codex"] || available["claude"] || available["opencode"] {
		t.Fatalf("Driver discovery = %+v", view.Drivers)
	}

	writeDiscoveryFixture(t, bin, "claude")
	t.Setenv("FISHYUME_CLAUDE_PATH", filepath.Join(bin, executableFixtureName("claude")))
	refreshed, err := service.TeamRoutesRefresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !driverAvailable(refreshed, "claude") {
		t.Fatalf("Claude was not discovered: %+v", refreshed.Drivers)
	}
	request := TeamRouteUpsertRequest{SchemaVersion: APIVersion, MutationID: "add-sonnet", ExpectedRevision: 1, RouteID: "claude/default/sonnet", Driver: "claude", Provider: "default", Model: "sonnet", Enabled: true}
	first, err := service.TeamRouteUpsert(request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.TeamRouteUpsert(request)
	if err != nil || first.Config.Revision != 2 || replay.Config.Revision != 2 || !replay.Replayed {
		t.Fatalf("mutation first=%+v replay=%+v err=%v", first, replay, err)
	}

	restarted, err := NewService(root, &inspectorFixture{})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := restarted.TeamRoutesGet()
	if err != nil || persisted.Config.Revision != 2 || !containsTeamRoute(persisted.Config.Routes, "claude/default/sonnet") {
		t.Fatalf("persisted=%+v err=%v", persisted, err)
	}
}

func TestTeamRoutesRemainInspectableWithoutEnoughDrivers(t *testing.T) {
	t.Setenv(routing.AgentRoutesFileEnv, "")
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	for _, name := range []string{"CODEX", "CLAUDE", "OPENCODE"} {
		t.Setenv("FISHYUME_"+name+"_PATH", filepath.Join(bin, "missing-"+name))
	}
	service, err := NewService(t.TempDir(), &inspectorFixture{})
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.TeamRoutesGet()
	if err != nil || view.CatalogHash != "" || len(view.Routes) != 4 {
		t.Fatalf("inspect unavailable routes = %+v err=%v", view, err)
	}
	if _, err := service.TeamCatalog(); err == nil {
		t.Fatal("TeamCatalog unexpectedly succeeded without two available routes")
	}
}

func TestLegacyAgentRoutesAreImportedOnce(t *testing.T) {
	bin := t.TempDir()
	writeDiscoveryFixture(t, bin, "codex")
	t.Setenv("FISHYUME_CODEX_PATH", filepath.Join(bin, executableFixtureName("codex")))
	legacyPath := filepath.Join(t.TempDir(), "agent-routes.json")
	legacy := routing.CanonicalCatalogV1(routing.CapabilityCatalogV1{SchemaVersion: routing.CapabilityCatalogV1Version, PolicyVersion: routing.RoutingPolicyV1Version, Models: []routing.ModelCapabilityV1{
		teamCapability(TeamRouteSetting{RouteID: "codex/legacy/one", Driver: "codex", Provider: "local", Model: "gpt-5.6-sol", Enabled: true, Source: TeamRouteLegacyImport}),
		teamCapability(TeamRouteSetting{RouteID: "codex/legacy/two", Driver: "codex", Provider: "local", Model: "gpt-5.6-sol", Enabled: true, Source: TeamRouteLegacyImport}),
	}})
	if err := writeJSONAtomic(legacyPath, legacy); err != nil {
		t.Fatal(err)
	}
	t.Setenv(routing.AgentRoutesFileEnv, legacyPath)
	root := t.TempDir()
	first, err := NewService(root, &inspectorFixture{})
	if err != nil {
		t.Fatal(err)
	}
	view, err := first.TeamRoutesGet()
	if err != nil || len(view.Config.Routes) != 2 || view.Config.Routes[0].Source != TeamRouteLegacyImport {
		t.Fatalf("legacy import=%+v err=%v", view, err)
	}
	t.Setenv(routing.AgentRoutesFileEnv, filepath.Join(t.TempDir(), "missing.json"))
	restarted, err := NewService(root, &inspectorFixture{})
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := restarted.TeamRoutesGet()
	if err != nil || len(persisted.Config.Routes) != 2 || persisted.Config.Routes[0].RouteID != "codex/legacy/one" {
		t.Fatalf("persisted legacy import=%+v err=%v", persisted, err)
	}
}

func writeDiscoveryFixture(t *testing.T, directory, name string) {
	t.Helper()
	path := filepath.Join(directory, executableFixtureName(name))
	if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func executableFixtureName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func driverAvailable(response TeamRoutesResponse, name string) bool {
	for _, driver := range response.Drivers {
		if driver.Driver == name {
			return driver.Available
		}
	}
	return false
}

func containsTeamRoute(routes []TeamRouteSetting, id string) bool {
	for _, route := range routes {
		if route.RouteID == id {
			return true
		}
	}
	return false
}
