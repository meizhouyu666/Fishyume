package workflow

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestRepositoryHardeningProductExample(t *testing.T) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve workflow test source")
	}
	path := filepath.Join(filepath.Dir(source), "..", "..", "..", "docs", "examples", "repository-hardening.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := Parse(data, filepath.Base(path), nil)
	if err != nil {
		t.Fatalf("parse product example: %v", err)
	}
	wantOrder := []string{
		"architecture-audit",
		"product-docs-audit",
		"test-build-audit",
		"synthesis",
		"approve-implementation",
		"implementation",
		"independent-verification",
		"accept-result",
	}
	if !slices.Equal(parsed.TopologicalOrder, wantOrder) {
		t.Fatalf("topological order = %v, want %v", parsed.TopologicalOrder, wantOrder)
	}
	if parsed.Document.Execution.MaxConcurrency != 3 {
		t.Fatalf("maxConcurrency = %d, want 3", parsed.Document.Execution.MaxConcurrency)
	}

	implementation := parsed.Document.Nodes["implementation"]
	if implementation.When == nil || implementation.When.Node != "approve-implementation" || implementation.When.Equals != "approved" {
		t.Fatalf("implementation approval condition = %#v", implementation.When)
	}
	verification := EffectiveContextPolicy(parsed.Document, parsed.Document.Nodes["independent-verification"])
	if !slices.Equal(verification.Dependencies, []string{"synthesis", "implementation"}) {
		t.Fatalf("verification context dependencies = %v", verification.Dependencies)
	}
}
