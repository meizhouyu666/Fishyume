package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCatalogFileCanonicalizesTrustedRoutes(t *testing.T) {
	catalog := CapabilityCatalogV1{
		SchemaVersion: CapabilityCatalogV1Version,
		PolicyVersion: RoutingPolicyV1Version,
		Models: []ModelCapabilityV1{
			{ID: "opencode/deepseek/deepseek-chat", Target: Target{Driver: "opencode", Provider: "deepseek", Model: "deepseek-chat"}, Capabilities: []Capability{CapabilityToolUse, CapabilityRepoRead}, ContextLimitBytes: 128 * 1024, MaxOutputBytes: 32 * 1024, Quality: QualityBalanced, Cost: CostLow, Latency: LatencyFast, SupportsCancellation: true},
			{ID: "claude/default/sonnet", Target: Target{Driver: "claude", Provider: "default", Model: "sonnet"}, Capabilities: []Capability{CapabilityStreaming, CapabilityRepoRead}, ContextLimitBytes: 256 * 1024, MaxOutputBytes: 64 * 1024, Quality: QualityPremium, Cost: CostHigh, Latency: LatencyBalanced, SupportsCancellation: true},
		},
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "routes.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCatalogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Models[0].ID != "claude/default/sonnet" || loaded.Models[0].Capabilities[0] != CapabilityRepoRead {
		t.Fatalf("catalog was not canonicalized: %+v", loaded)
	}
	firstHash, err := CatalogHash(loaded)
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := CatalogHash(CanonicalCatalogV1(catalog))
	if err != nil || firstHash != secondHash {
		t.Fatalf("canonical hash mismatch: %q %q err=%v", firstHash, secondHash, err)
	}
}

func TestLoadCatalogFileRejectsUntrustedShape(t *testing.T) {
	if _, err := LoadCatalogFile("routes.json"); err == nil {
		t.Fatal("relative route catalog path was accepted")
	}
	valid, err := json.Marshal(BuiltinCatalogV1())
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string][]byte{
		"unknown field":  append(valid[:len(valid)-1], []byte(`,"credential":"secret"}`)...),
		"trailing value": append(valid, []byte(` {}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "routes.json")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadCatalogFile(path); err == nil {
				t.Fatalf("invalid route catalog was accepted: %s", content)
			}
		})
	}
	t.Run("oversized", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "routes.json")
		if err := os.WriteFile(path, make([]byte, MaxCatalogFileBytes+1), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCatalogFile(path); err == nil {
			t.Fatal("oversized route catalog was accepted")
		}
	})
}

func TestLoadCatalogFromEnvironmentDefaultsAndOverrides(t *testing.T) {
	t.Setenv(AgentRoutesFileEnv, "")
	loaded, err := LoadCatalogFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := CatalogHash(BuiltinTeamCatalogV1())
	got, _ := CatalogHash(loaded)
	if got != want {
		t.Fatalf("default catalog hash = %s, want %s", got, want)
	}

	path := filepath.Join(t.TempDir(), "routes.json")
	encoded, _ := json.Marshal(BuiltinCatalogV1())
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(AgentRoutesFileEnv, path)
	if _, err := LoadCatalogFromEnvironment(); err != nil {
		t.Fatal(err)
	}
}

func TestBuiltinTeamCatalogUsesOnlyAvailableCodexRoute(t *testing.T) {
	catalog := BuiltinTeamCatalogV1()
	if err := ValidateCatalog(catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 2 {
		t.Fatalf("Team catalog=%+v", catalog)
	}
	for _, model := range catalog.Models {
		if model.Target.Driver != "codex" || model.Target.Provider != "local" || model.Target.Model != "gpt-5.6-sol" || strings.Contains(model.ID, "luna") {
			t.Fatalf("unexpected default Team route: %+v", model)
		}
	}
}

func TestDocumentedAgentRoutesRemainValid(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "..", "..", "docs", "examples", "agent-routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadCatalogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 3 || catalog.Models[0].Target.Driver != "claude" || catalog.Models[2].Target.Driver != "opencode" {
		t.Fatalf("documented Agent routes drifted: %+v", catalog.Models)
	}
}
