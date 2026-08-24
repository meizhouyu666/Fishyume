package execution

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestArtifactLocationUsesStableSegmentedPath(t *testing.T) {
	got, err := (ArtifactLocation{
		Namespace: "runs", OwnerID: "run-1", ResourceKind: "nodes", ResourceID: "plan", GenerationKind: "attempts", Generation: 2,
	}).RelativePath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("runs", "run-1", "nodes", "plan", "attempts", "2"); got != want {
		t.Fatalf("path=%q want=%q", got, want)
	}
}

func TestArtifactLocationRejectsTraversalAndInvalidGeneration(t *testing.T) {
	base := ArtifactLocation{Namespace: "runs", OwnerID: "run-1", ResourceKind: "nodes", ResourceID: "plan", GenerationKind: "attempts", Generation: 1}
	for name, update := range map[string]func(*ArtifactLocation){
		"empty":           func(value *ArtifactLocation) { value.OwnerID = "" },
		"whitespace":      func(value *ArtifactLocation) { value.ResourceID = " plan" },
		"separator":       func(value *ArtifactLocation) { value.ResourceID = `plan\\child` },
		"dot":             func(value *ArtifactLocation) { value.ResourceID = ".." },
		"zero generation": func(value *ArtifactLocation) { value.Generation = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			update(&value)
			if _, err := value.RelativePath(); err == nil {
				t.Fatal("invalid artifact location was accepted")
			}
		})
	}
	if _, err := (ArtifactLocation{Namespace: strings.Repeat("x", 1), OwnerID: "run", ResourceKind: "nodes", ResourceID: "node", GenerationKind: "attempts", Generation: -1}).RelativePath(); err == nil {
		t.Fatal("negative generation was accepted")
	}
}
