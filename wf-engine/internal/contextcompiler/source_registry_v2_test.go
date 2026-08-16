package contextcompiler

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/workflow"
)

type sourceRegistryFixtureV2 struct {
	SchemaVersion string                         `json:"schemaVersion"`
	Input         ContextSourceResolutionInputV2 `json:"input"`
	Resolution    ContextSourceResolutionV2      `json:"resolution"`
}

func TestContextSourceRegistryV2GoldenFixtureIsDeterministicAndImmutable(t *testing.T) {
	fixture := loadSourceRegistryFixtureV2(t)
	root := t.TempDir()
	fixture.Input.ProjectRoot = root
	for index := range fixture.Input.SelectedMemory {
		fixture.Input.SelectedMemory[index].Record.Project = root
	}
	registry := BuiltinContextSourceRegistryV2()
	kinds := registry.Kinds()
	wantKinds := []ComponentKind{KindProjectInstructions, KindWorkflowPolicy, KindNodeTask, KindUserAnswer, KindDependencyResult, KindMemory}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Fatalf("registry kinds = %v, want %v", kinds, wantKinds)
	}
	kinds[0] = KindOutputContract
	if !reflect.DeepEqual(registry.Kinds(), wantKinds) {
		t.Fatal("caller mutated the built-in registry order")
	}

	first, err := registry.Resolve(fixture.Input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, fixture.Resolution) {
		got, _ := json.MarshalIndent(first, "", "  ")
		want, _ := json.MarshalIndent(fixture.Resolution, "", "  ")
		t.Fatalf("golden resolution drifted\ngot:  %s\nwant: %s", got, want)
	}

	reversed := fixture.Input
	reversed.SelectedMemory = reverseSelectedMemoryV2(fixture.Input.SelectedMemory)
	second, err := registry.Resolve(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("resolution changed with caller ordering: %+v != %+v", first, second)
	}
}

func TestContextSourceRegistryV2RequiredUnavailableDuplicateAndConflictErrors(t *testing.T) {
	registry := BuiltinContextSourceRegistryV2()
	base := minimalSourceInputV2()
	tests := []struct {
		name string
		edit func(*ContextSourceResolutionInputV2)
		code ContextErrorCode
	}{
		{name: "required Node task missing", edit: func(value *ContextSourceResolutionInputV2) { value.NodeTasks = nil }, code: CodeContextRequiredMissing},
		{name: "required Node content missing", edit: func(value *ContextSourceResolutionInputV2) { value.NodeTasks[0].Content = "" }, code: CodeContextRequiredMissing},
		{name: "Node identity missing", edit: func(value *ContextSourceResolutionInputV2) { value.NodeTasks[0].NodeID = "" }, code: CodeContextInvalidComponent},
		{name: "declared source unavailable", edit: func(value *ContextSourceResolutionInputV2) {
			value.WorkflowPolicies = []WorkflowPolicySourceV2{{Declaration: sourceDeclarationV2("workflow-policy", TierImportant, SensitivityProject), Content: "policy"}}
			value.WorkflowPolicies[0].Declaration.Unavailable = true
		}, code: CodeContextSourceUnavailable},
		{name: "duplicate stable ID", edit: func(value *ContextSourceResolutionInputV2) {
			value.WorkflowPolicies = []WorkflowPolicySourceV2{{Declaration: sourceDeclarationV2("node-task", TierImportant, SensitivityProject), Content: "policy"}}
		}, code: CodeContextInvalidComponent},
		{name: "duplicate dependency declaration", edit: func(value *ContextSourceResolutionInputV2) {
			first := DependencyResultSourceV2{Declaration: sourceDeclarationV2("dependency-a", TierImportant, SensitivityProject), UpstreamNode: "plan", Result: &workflow.Result{Summary: "a"}}
			second := DependencyResultSourceV2{Declaration: sourceDeclarationV2("dependency-b", TierImportant, SensitivityProject), UpstreamNode: "plan", Result: &workflow.Result{Summary: "b"}}
			value.AllowedUpstreamNodes = []string{"plan"}
			value.DependencyResults = []DependencyResultSourceV2{second, first}
		}, code: CodeContextInvalidComponent},
		{name: "declared source count bounded", edit: func(value *ContextSourceResolutionInputV2) {
			value.WorkflowPolicies = make([]WorkflowPolicySourceV2, MaxContextComponents)
			for index := range value.WorkflowPolicies {
				value.WorkflowPolicies[index] = WorkflowPolicySourceV2{Declaration: sourceDeclarationV2(fmt.Sprintf("workflow-%03d", index), TierImportant, SensitivityProject), Content: "policy"}
			}
		}, code: CodeContextInvalidComponent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.NodeTasks = append([]NodeTaskSourceV2(nil), base.NodeTasks...)
			test.edit(&candidate)
			_, err := registry.Resolve(candidate)
			assertContextErrorCodeV2(t, err, test.code)
		})
	}
}

func TestContextSourceRegistryV2SortsSameKindSourcesByStableID(t *testing.T) {
	input := minimalSourceInputV2()
	input.AllowedUpstreamNodes = []string{"z", "a"}
	input.WorkflowPolicies = []WorkflowPolicySourceV2{
		{Declaration: sourceDeclarationV2("workflow-z", TierImportant, SensitivityProject), Content: "z policy"},
		{Declaration: sourceDeclarationV2("workflow-a", TierImportant, SensitivityProject), Content: "a policy"},
	}
	input.DependencyResults = []DependencyResultSourceV2{
		{Declaration: sourceDeclarationV2("dependency-z", TierImportant, SensitivityProject), UpstreamNode: "z", Result: &workflow.Result{Summary: "z result"}},
		{Declaration: sourceDeclarationV2("dependency-a", TierImportant, SensitivityProject), UpstreamNode: "a", Result: &workflow.Result{Summary: "a result"}},
	}
	resolved, err := BuiltinContextSourceRegistryV2().Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"workflow-a", "workflow-z", "node-task", "dependency-a", "dependency-z"}
	got := make([]string, 0, len(resolved.Components))
	for _, component := range resolved.Components {
		got = append(got, component.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("component order = %v, want %v", got, want)
	}
}

func TestContextSourceRegistryV2ProjectFileStaysInsideCanonicalRootAndIsBounded(t *testing.T) {
	registry := BuiltinContextSourceRegistryV2()
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "docs", "instructions.md")
	if err := os.WriteFile(inside, []byte("bounded project instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	input := minimalSourceInputV2()
	input.ProjectRoot = root
	input.ProjectInstructions = []ProjectInstructionSourceV2{{Declaration: sourceDeclarationV2("project-file", TierRequired, SensitivityProject), RelativePath: filepath.Join("docs", "instructions.md")}}
	resolved, err := registry.Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Components[0].Provenance.Source != "project:file/docs/instructions.md" || resolved.Components[0].Content != "bounded project instructions" {
		t.Fatalf("unexpected project file component: %+v", resolved.Components[0])
	}

	outside := filepath.Join(parent, "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	escape := input
	escape.ProjectInstructions = append([]ProjectInstructionSourceV2(nil), input.ProjectInstructions...)
	escape.ProjectInstructions[0].RelativePath = filepath.Join("..", "outside.md")
	_, err = registry.Resolve(escape)
	assertContextErrorCodeV2(t, err, CodeContextSourceUnavailable)

	oversized := filepath.Join(root, "docs", "oversized.md")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("x", MaxProjectInstructionFileBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	escape.ProjectInstructions[0].RelativePath = filepath.Join("docs", "oversized.md")
	_, err = registry.Resolve(escape)
	assertContextErrorCodeV2(t, err, CodeContextSourceUnavailable)

	link := filepath.Join(root, "docs", "outside-link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Logf("symlink boundary assertion skipped on this host: %v", err)
		return
	}
	escape.ProjectInstructions[0].RelativePath = filepath.Join("docs", "outside-link.md")
	_, err = registry.Resolve(escape)
	assertContextErrorCodeV2(t, err, CodeContextSourceUnavailable)
}

func TestContextSourceRegistryV2AcceptsOnlyExplicitDependencyResults(t *testing.T) {
	input := minimalSourceInputV2()
	input.AllowedUpstreamNodes = []string{"plan"}
	input.DependencyResults = []DependencyResultSourceV2{
		{Declaration: sourceDeclarationV2("dependency-plan", TierImportant, SensitivityProject), UpstreamNode: "plan", Result: &workflow.Result{Summary: "declared plan"}},
	}
	resolved, err := BuiltinContextSourceRegistryV2().Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if countKindV2(resolved.Components, KindDependencyResult) != 1 {
		t.Fatalf("unselected sibling crossed the dependency boundary: %s", encoded)
	}

	unauthorized := input
	unauthorized.DependencyResults = append([]DependencyResultSourceV2(nil), input.DependencyResults...)
	unauthorized.DependencyResults[0] = DependencyResultSourceV2{Declaration: sourceDeclarationV2("dependency-sibling", TierImportant, SensitivityProject), UpstreamNode: "sibling", Result: &workflow.Result{Summary: "unrelated sibling marker"}}
	_, err = BuiltinContextSourceRegistryV2().Resolve(unauthorized)
	assertContextErrorCodeV2(t, err, CodeContextInvalidComponent)

	self := input
	self.DependencyResults = append([]DependencyResultSourceV2(nil), input.DependencyResults...)
	self.DependencyResults[0] = DependencyResultSourceV2{Declaration: sourceDeclarationV2("dependency-self", TierImportant, SensitivityProject), UpstreamNode: "node", Result: &workflow.Result{Summary: "self result"}}
	_, err = BuiltinContextSourceRegistryV2().Resolve(self)
	assertContextErrorCodeV2(t, err, CodeContextInvalidComponent)

	for name, allowlist := range map[string][]string{
		"duplicate allowlist": {"plan", "plan"},
		"invalid allowlist":   {"bad/path"},
		"self allowlist":      {"node"},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := input
			candidate.AllowedUpstreamNodes = allowlist
			_, resolveErr := BuiltinContextSourceRegistryV2().Resolve(candidate)
			assertContextErrorCodeV2(t, resolveErr, CodeContextInvalidComponent)
		})
	}

	ordered := input
	ordered.AllowedUpstreamNodes = []string{"z", "plan", "a"}
	reversed := input
	reversed.AllowedUpstreamNodes = []string{"a", "plan", "z"}
	left, err := BuiltinContextSourceRegistryV2().Resolve(ordered)
	if err != nil {
		t.Fatal(err)
	}
	right, err := BuiltinContextSourceRegistryV2().Resolve(reversed)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("allowlist order changed resolution: %+v != %+v", left, right)
	}

	invalidLeft := input
	invalidLeft.AllowedUpstreamNodes = []string{"bad/path", ""}
	invalidRight := input
	invalidRight.AllowedUpstreamNodes = []string{"", "bad/path"}
	_, leftErr := BuiltinContextSourceRegistryV2().Resolve(invalidLeft)
	_, rightErr := BuiltinContextSourceRegistryV2().Resolve(invalidRight)
	if leftErr == nil || rightErr == nil || leftErr.Error() != rightErr.Error() {
		t.Fatalf("allowlist order changed stable error: %v != %v", leftErr, rightErr)
	}
}

func TestContextSourceRegistryV2MemoryRequiresExplicitValidActiveUnexpiredRecords(t *testing.T) {
	fixture := loadSourceRegistryFixtureV2(t)
	root := t.TempDir()
	input := minimalSourceInputV2()
	input.ProjectRoot = root
	input.AsOf = fixture.Input.AsOf
	input.SelectedMemory = append([]SelectedMemorySourceV2(nil), fixture.Input.SelectedMemory...)
	for index := range input.SelectedMemory {
		input.SelectedMemory[index].Record.Project = root
	}
	resolved, err := BuiltinContextSourceRegistryV2().Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	if countKindV2(resolved.Components, KindMemory) != 1 || !reflect.DeepEqual(resolved.Omissions, fixture.Resolution.Omissions) {
		t.Fatalf("Memory lifecycle resolution drifted: %+v", resolved)
	}

	invalid := input
	invalid.SelectedMemory = append([]SelectedMemorySourceV2(nil), input.SelectedMemory...)
	invalid.SelectedMemory[0].Record.Provenance.Writer = MemoryWriter("node_agent")
	_, err = BuiltinContextSourceRegistryV2().Resolve(invalid)
	assertContextErrorCodeV2(t, err, CodeMemoryInvalidRecord)

	missingReferenceTime := input
	missingReferenceTime.AsOf = time.Time{}
	_, err = BuiltinContextSourceRegistryV2().Resolve(missingReferenceTime)
	assertContextErrorCodeV2(t, err, CodeContextInvalidComponent)

	crossProject := input
	crossProject.SelectedMemory = append([]SelectedMemorySourceV2(nil), input.SelectedMemory...)
	crossProject.SelectedMemory[0].Record.Project = t.TempDir()
	_, err = BuiltinContextSourceRegistryV2().Resolve(crossProject)
	assertContextErrorCodeV2(t, err, CodeMemoryConflict)
}

func TestContextSourceRegistryV2RejectsInvalidUTF8BeforeHashingWithoutContentLeak(t *testing.T) {
	registry := BuiltinContextSourceRegistryV2()

	t.Run("inline Node task", func(t *testing.T) {
		const marker = "inline-secret-marker"
		input := minimalSourceInputV2()
		input.NodeTasks[0].Content = marker + string([]byte{0xff})
		_, err := registry.Resolve(input)
		assertContextErrorCodeV2(t, err, CodeContextInvalidComponent)
		assertErrorDoesNotLeakV2(t, err, marker)
	})

	t.Run("project instruction file", func(t *testing.T) {
		const marker = "file-secret-marker"
		root := t.TempDir()
		path := filepath.Join(root, "instructions.md")
		content := append([]byte(marker), 0xff)
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
		input := minimalSourceInputV2()
		input.ProjectRoot = root
		input.ProjectInstructions = []ProjectInstructionSourceV2{{Declaration: sourceDeclarationV2("project-file", TierRequired, SensitivityProject), RelativePath: "instructions.md"}}
		_, err := registry.Resolve(input)
		assertContextErrorCodeV2(t, err, CodeContextInvalidComponent)
		assertErrorDoesNotLeakV2(t, err, marker)
	})

	t.Run("user answer", func(t *testing.T) {
		const marker = "answer-secret-marker"
		input := minimalSourceInputV2()
		answer := append([]byte(`{"answer":"`+marker), 0xff)
		answer = append(answer, []byte(`"}`)...)
		input.UserAnswers = []UserAnswerSourceV2{{Declaration: sourceDeclarationV2("user-answer", TierRequired, SensitivitySensitive), Answer: answer}}
		_, err := registry.Resolve(input)
		assertContextErrorCodeV2(t, err, CodeContextInvalidComponent)
		assertErrorDoesNotLeakV2(t, err, marker)
	})

	t.Run("dependency Result", func(t *testing.T) {
		const marker = "dependency-secret-marker"
		input := minimalSourceInputV2()
		input.AllowedUpstreamNodes = []string{"plan"}
		input.DependencyResults = []DependencyResultSourceV2{{Declaration: sourceDeclarationV2("dependency-plan", TierImportant, SensitivityProject), UpstreamNode: "plan", Result: &workflow.Result{Summary: marker + string([]byte{0xff})}}}
		_, err := registry.Resolve(input)
		assertContextErrorCodeV2(t, err, CodeContextInvalidComponent)
		assertErrorDoesNotLeakV2(t, err, marker)
	})

	t.Run("selected Memory", func(t *testing.T) {
		const marker = "memory-secret-marker"
		fixture := loadSourceRegistryFixtureV2(t)
		root := t.TempDir()
		var selected SelectedMemorySourceV2
		for _, candidate := range fixture.Input.SelectedMemory {
			if candidate.Record.ID == "memory-determinism" {
				selected = candidate
				break
			}
		}
		selected.Record.Project = root
		selected.Record.Content = marker + string([]byte{0xff})
		selected.Record.ContentHash = hashBytes([]byte(selected.Record.Content))
		selected.Record.Provenance.SourceHash = selected.Record.ContentHash
		input := minimalSourceInputV2()
		input.ProjectRoot = root
		input.AsOf = fixture.Input.AsOf
		input.SelectedMemory = []SelectedMemorySourceV2{selected}
		_, err := registry.Resolve(input)
		assertContextErrorCodeV2(t, err, CodeContextInvalidComponent)
		assertErrorDoesNotLeakV2(t, err, marker)
	})
}

func TestContextSourceRegistryV2SensitiveBodyDoesNotEnterDurableManifest(t *testing.T) {
	const marker = "relay-token-secret-marker"
	input := minimalSourceInputV2()
	input.UserAnswers = []UserAnswerSourceV2{{Declaration: sourceDeclarationV2("user-answer", TierRequired, SensitivitySensitive), Answer: json.RawMessage(`{"answer":"relay-token-secret-marker"}`)}}
	resolved, err := BuiltinContextSourceRegistryV2().Resolve(input)
	if err != nil {
		t.Fatal(err)
	}
	components := append([]ContextComponentV2{testComponentV2("execution-contract", KindExecutionContract, TierRequired, SensitivityPublic, "execute")}, resolved.Components...)
	components = append(components, testComponentV2("output-contract", KindOutputContract, TierRequired, SensitivityPublic, "return result"))
	requiredBytes := 0
	for _, component := range components {
		requiredBytes += component.IncludedBytes
	}
	envelope := ContextEnvelopeV2{
		SchemaVersion: EnvelopeV2Version, CompilerVersion: CompilerV2Version,
		Identity: agent.AttemptIdentity{RunID: "run-registry", NodeID: "node", Attempt: 1},
		Budget:   AttentionBudgetV2{TotalBytes: requiredBytes, RequiredBytes: requiredBytes}, Components: components, Omissions: resolved.Omissions,
	}
	manifest, err := BuildContextManifestV2(envelope)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), marker) {
		t.Fatalf("sensitive source body entered durable metadata: %s", encoded)
	}
}

func minimalSourceInputV2() ContextSourceResolutionInputV2 {
	return ContextSourceResolutionInputV2{AllowedUpstreamNodes: []string{}, NodeTasks: []NodeTaskSourceV2{{Declaration: sourceDeclarationV2("node-task", TierRequired, SensitivityProject), NodeID: "node", Content: "implement task"}}}
}

func sourceDeclarationV2(id string, tier AttentionTier, sensitivity Sensitivity) SourceDeclarationV2 {
	return SourceDeclarationV2{ID: id, SourceVersion: "1", Reason: "Explicitly selected for this test.", Tier: tier, Sensitivity: sensitivity}
}

func testComponentV2(id string, kind ComponentKind, tier AttentionTier, sensitivity Sensitivity, content string) ContextComponentV2 {
	hash := hashBytes([]byte(content))
	return ContextComponentV2{ID: id, Kind: kind, Tier: tier, Sensitivity: sensitivity, Provenance: ComponentProvenanceV2{Source: "test:" + id, SourceVersion: "1", SourceHash: hash, Reason: "Test contract component."}, Content: content, ContentHash: hash, OriginalBytes: len([]byte(content)), IncludedBytes: len([]byte(content)), Truncation: TruncationNone}
}

func loadSourceRegistryFixtureV2(t *testing.T) sourceRegistryFixtureV2 {
	t.Helper()
	data, err := os.ReadFile("testdata/source-registry-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture sourceRegistryFixtureV2
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SchemaVersion != "fishyume.context-source-registry-fixture/v1" {
		t.Fatalf("fixture version = %q", fixture.SchemaVersion)
	}
	return fixture
}

func reverseSelectedMemoryV2(source []SelectedMemorySourceV2) []SelectedMemorySourceV2 {
	result := append([]SelectedMemorySourceV2(nil), source...)
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result
}

func countKindV2(components []ContextComponentV2, kind ComponentKind) int {
	count := 0
	for _, component := range components {
		if component.Kind == kind {
			count++
		}
	}
	return count
}

func assertContextErrorCodeV2(t *testing.T, err error, code ContextErrorCode) {
	t.Helper()
	var contractErr *ContractError
	if !errors.As(err, &contractErr) || contractErr.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func assertErrorDoesNotLeakV2(t *testing.T, err error, marker string) {
	t.Helper()
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("error leaked source content marker %q: %v", marker, err)
	}
}
