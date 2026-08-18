package application

import (
	"encoding/json"
	"strings"
	"testing"

	"wf.local/wf-engine/internal/contextcompiler"
	"wf.local/wf-engine/internal/run"
)

func TestM54InspectContextIsMetadataOnlyAndDeterministic(t *testing.T) {
	const marker = "M54-INSPECT-CONTENT-MARKER"
	manifest := &contextcompiler.ContextManifestV2{
		SchemaVersion:   contextcompiler.ManifestV2Version,
		CompilerVersion: contextcompiler.CompilerV2Version,
		EnvelopeHash:    strings.Repeat("a", 64),
		Budget:          contextcompiler.AttentionBudgetV2{TotalBytes: 1024, RequiredBytes: 512, ImportantBytes: 256, OptionalBytes: 256},
		Usage:           contextcompiler.AttentionUsageV2{TotalBytes: 256, RequiredBytes: 128, ImportantBytes: 128},
		Components:      []contextcompiler.ContextComponentManifestV2{{ID: "node-task", Kind: contextcompiler.KindNodeTask, Tier: contextcompiler.TierRequired, Sensitivity: contextcompiler.SensitivityProject, Provenance: contextcompiler.ComponentProvenanceV2{Source: "workflow:node/work", SourceVersion: "v1", SourceHash: strings.Repeat("b", 64), Reason: "current task"}, ContentHash: strings.Repeat("c", 64), OriginalBytes: len(marker), IncludedBytes: len(marker), Truncation: contextcompiler.TruncationNone}},
		Omissions:       []contextcompiler.ContextOmissionV2{{ComponentID: "memory-secret", Kind: contextcompiler.KindMemory, Tier: contextcompiler.TierOptional, Reason: contextcompiler.OmissionSensitivityPolicy, SourceHash: strings.Repeat("d", 64), OriginalBytes: 32}},
	}
	view := inspectContext(run.AttemptSnapshot{ContextCompilerVersionV2: contextcompiler.CompilerV2Version, ContextHash: manifest.EnvelopeHash, ContextManifestV2: manifest})
	if view == nil || view.CompilerVersion != contextcompiler.CompilerV2Version || view.Hash != manifest.EnvelopeHash || len(view.Components) != 1 || len(view.Omissions) != 1 || view.Truncated {
		t.Fatalf("inspect view=%+v", view)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), marker) || strings.Contains(string(encoded), "contentHash") || strings.Contains(string(encoded), "provenance") {
		t.Fatalf("inspect projection leaked component body/provenance: %s", encoded)
	}
	if got := view.Components[0]; got.ID != "node-task" || got.Kind != string(contextcompiler.KindNodeTask) || got.Tier != string(contextcompiler.TierRequired) {
		t.Fatalf("component projection=%+v", got)
	}
}
