package contextcompiler

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAdaptEnvelopeV2CanonicalizesSkillsAndKeepsPromptEphemeral(t *testing.T) {
	fixture := loadContractFixtureV2(t)
	envelope := fixture.Envelope
	const marker = "adapter-sensitive-component-marker"
	for index := range envelope.Components {
		if envelope.Components[index].Kind == KindNodeTask {
			envelope.Components[index].Content = marker
			envelope.Components[index].ContentHash = hashBytes([]byte(marker))
			envelope.Components[index].Provenance.SourceHash = envelope.Components[index].ContentHash
			envelope.Components[index].OriginalBytes = len([]byte(marker))
			envelope.Components[index].IncludedBytes = envelope.Components[index].OriginalBytes
		}
		if envelope.Components[index].Kind == KindDependencyResult {
			content := `{"summary":"Dependency plan completed."}`
			envelope.Components[index].Content = content
			envelope.Components[index].ContentHash = hashBytes([]byte(content))
			envelope.Components[index].Provenance.SourceHash = envelope.Components[index].ContentHash
			envelope.Components[index].OriginalBytes = len([]byte(content))
			envelope.Components[index].IncludedBytes = envelope.Components[index].OriginalBytes
		}
	}
	first, err := AdaptEnvelopeV2WithSkills(envelope, "C:/workspace", "local", []string{"zeta", "alpha", "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := AdaptEnvelopeV2WithSkills(envelope, "C:/workspace", "local", []string{"alpha", "zeta"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Prompt == "" || first.Prompt != second.Prompt {
		t.Fatal("adapter output changed with equivalent skill ordering")
	}
	if got := first.Context.RequiredSkills; len(got) != 2 || got[0] != "alpha" || got[1] != "zeta" {
		t.Fatalf("canonical skills = %v", got)
	}
	wireFirst, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wireFirst), `"prompt"`) {
		t.Fatalf("wire envelope persisted rendered prompt field: %s", wireFirst)
	}
	if !strings.Contains(first.Prompt, marker) {
		t.Fatal("ephemeral prompt did not carry the component content to the driver")
	}
}
