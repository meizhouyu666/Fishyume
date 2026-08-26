package codexprocess

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDiscoverModelsUsesCodexAppServerCatalog(t *testing.T) {
	t.Setenv("FISHYUME_FAKE_APP_SERVER_STATE", filepath.Join(t.TempDir(), "codex-models.json"))
	backend := New(Config{StateRoot: t.TempDir(), Executable: sessionFixtureBinary(t), MaxStderrBytes: 64 * 1024})
	models, err := backend.DiscoverModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 || models[0].Model != "gpt-5.6-sol" || !models[0].Default || models[0].DefaultEffort != "medium" {
		t.Fatalf("discovered models = %+v", models)
	}
	if len(models[0].SupportedEfforts) != 3 || models[0].ServiceTiers[0] != "priority" || !models[0].SupportsMultiAgentMode {
		t.Fatalf("Sol metadata = %+v", models[0])
	}
}
