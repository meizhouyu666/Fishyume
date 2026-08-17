package contextcompiler

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"wf.local/wf-engine/internal/agent"
)

type attentionCompilerFixtureV2 struct {
	SchemaVersion string         `json:"schemaVersion"`
	Policy        BudgetPolicyV1 `json:"policy"`
	Expected      CompilationV2  `json:"expected"`
}

func budgetComponentV2(id string, kind ComponentKind, tier AttentionTier, content string) ContextComponentV2 {
	h := hashBytes([]byte(content))
	return ContextComponentV2{ID: id, Kind: kind, Tier: tier, Sensitivity: SensitivityProject,
		Provenance: ComponentProvenanceV2{Source: "test:" + id, SourceVersion: "v1", SourceHash: h, Reason: "approved test source"},
		Content:    content, ContentHash: h, OriginalBytes: len([]byte(content)), IncludedBytes: len([]byte(content)), Truncation: TruncationNone}
}

func goldenCompilerInputV2() ContextCompilerInputV2 {
	return ContextCompilerInputV2{
		Identity:          agent.AttemptIdentity{RunID: "run-golden-m53", NodeID: "implement", Attempt: 2},
		ExecutionContract: budgetComponentV2("execution-contract", KindExecutionContract, TierRequired, "execute safely"),
		OutputContract:    budgetComponentV2("output-contract", KindOutputContract, TierRequired, "return structured result"),
		Resolution: ContextSourceResolutionV2{
			Components: []ContextComponentV2{
				budgetComponentV2("node-task", KindNodeTask, TierRequired, "implement the approved task"),
				budgetComponentV2("policy-a-small", KindWorkflowPolicy, TierImportant, "small"),
				budgetComponentV2("policy-b-mixed", KindWorkflowPolicy, TierImportant, "alpha-世界🙂TAIL"),
				budgetComponentV2("policy-c-mixed", KindWorkflowPolicy, TierImportant, "beta-🚀終端"),
				budgetComponentV2("memory-a-large", KindMemory, TierOptional, strings.Repeat("L", 20)),
				budgetComponentV2("memory-z-small", KindMemory, TierOptional, "remember"),
			},
			Omissions: []ContextOmissionV2{
				{ComponentID: "memory-superseded", Kind: KindMemory, Tier: TierOptional, Reason: OmissionSuperseded, SourceHash: strings.Repeat("b", 64), OriginalBytes: 11},
				{ComponentID: "memory-expired", Kind: KindMemory, Tier: TierOptional, Reason: OmissionExpired, SourceHash: strings.Repeat("a", 64), OriginalBytes: 7},
			},
		},
		Budget: AttentionBudgetV2{TotalBytes: 109, RequiredBytes: 80, ImportantBytes: 17, OptionalBytes: 12},
	}
}

func loadAttentionCompilerFixtureV2(t *testing.T) attentionCompilerFixtureV2 {
	t.Helper()
	data, err := os.ReadFile("testdata/attention-compiler-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture attentionCompilerFixtureV2
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestAttentionCompilerV2GoldenFixtureExact(t *testing.T) {
	fixture := loadAttentionCompilerFixtureV2(t)
	if fixture.SchemaVersion != "fishyume.attention-compiler-fixture/v1" {
		t.Fatalf("fixture version = %q", fixture.SchemaVersion)
	}
	if fixture.Policy != DefaultBudgetPolicyV1() {
		t.Fatalf("fixture policy drifted: %+v", fixture.Policy)
	}
	got, err := CompileContextV2(goldenCompilerInputV2())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, fixture.Expected) {
		t.Fatalf("complete compilation drifted\ngot:  %+v\nwant: %+v", got, fixture.Expected)
	}
	gotJSON, err := canonicalJSON(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := canonicalJSON(fixture.Expected)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotJSON, wantJSON) {
		t.Fatal("canonical compilation bytes drifted")
	}
	ids := componentIDsV2(got.Envelope.Components)
	wantIDs := []string{"execution-contract", "policy-a-small", "policy-b-mixed", "policy-c-mixed", "node-task", "memory-z-small", "output-contract"}
	if !reflect.DeepEqual(ids, wantIDs) {
		t.Fatalf("included IDs = %v, want %v", ids, wantIDs)
	}
	wantOmissions := []string{"memory-a-large:budget_exhausted", "memory-expired:expired", "memory-superseded:superseded"}
	if gotOmissions := omissionKeysV2(got.Envelope.Omissions); !reflect.DeepEqual(gotOmissions, wantOmissions) {
		t.Fatalf("omissions = %v, want %v", gotOmissions, wantOmissions)
	}
	if got.Manifest.Usage != (AttentionUsageV2{TotalBytes: 88, RequiredBytes: 65, ImportantBytes: 15, OptionalBytes: 8}) {
		t.Fatalf("usage = %+v", got.Manifest.Usage)
	}
	if got.Hash != fixture.Expected.Hash || got.Manifest.EnvelopeHash != got.Hash {
		t.Fatalf("hash mismatch: %+v", got)
	}
}

func TestCompileContextV2RepeatedDeterminismAndMetadataOnly(t *testing.T) {
	in := goldenCompilerInputV2()
	const marker = "LEAK-MARKER-M53"
	in.Resolution.Components[0] = budgetComponentV2("node-task", KindNodeTask, TierRequired, marker)
	want, err := CompileContextV2(in)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := canonicalJSON(want)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		got, err := CompileContextV2(in)
		if err != nil {
			t.Fatal(err)
		}
		gotJSON, err := canonicalJSON(got)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotJSON, wantJSON) || got.Hash != want.Hash {
			t.Fatalf("iteration %d is not byte-identical", i)
		}
	}
	encoded, _ := json.Marshal(want.Manifest)
	if strings.Contains(string(encoded), marker) {
		t.Fatal("manifest leaked Component content")
	}
}

func TestCompileContextV2PermutationAndDeepAliasing(t *testing.T) {
	in := goldenCompilerInputV2()
	before := cloneCompilerInputV2(t, in)
	first, err := CompileContextV2(in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, before) {
		t.Fatal("compiler mutated caller input")
	}
	permuted := cloneCompilerInputV2(t, in)
	sort.SliceStable(permuted.Resolution.Components, func(i, j int) bool {
		return permuted.Resolution.Components[i].ID > permuted.Resolution.Components[j].ID
	})
	sort.SliceStable(permuted.Resolution.Omissions, func(i, j int) bool {
		return permuted.Resolution.Omissions[i].ComponentID > permuted.Resolution.Omissions[j].ComponentID
	})
	permutedBefore := cloneCompilerInputV2(t, permuted)
	second, err := CompileContextV2(permuted)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(permuted, permutedBefore) {
		t.Fatal("compiler mutated permuted caller input")
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("input permutation changed exact output")
	}
}

func TestImportantBalancedAllocationExactFairAndTied(t *testing.T) {
	in := goldenCompilerInputV2()
	important := append([]ContextComponentV2(nil), in.Resolution.Components[1:4]...)
	if got := balancedAlloc(important, 17); !reflect.DeepEqual(got, []int{5, 6, 6}) {
		t.Fatalf("allocation(17) = %v", got)
	}
	if got := balancedAlloc(important, 18); !reflect.DeepEqual(got, []int{5, 7, 6}) {
		t.Fatalf("canonical remainder tie-break = %v", got)
	}
	got, err := CompileContextV2(in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		content  string
		included int
		trunc    TruncationMode
	}{
		"policy-a-small": {"small", 5, TruncationNone},
		"policy-b-mixed": {"TAIL", 4, TruncationTail},
		"policy-c-mixed": {"終端", 6, TruncationTail},
	}
	for id, expected := range want {
		c := findComponentV2(t, got.Envelope.Components, id)
		if c.Content != expected.content || c.IncludedBytes != expected.included || c.Truncation != expected.trunc || c.ContentHash != hashBytes([]byte(expected.content)) {
			t.Fatalf("%s = %+v", id, c)
		}
		if c.OriginalBytes != len([]byte(findInputComponentV2(t, in, id).Content)) {
			t.Fatalf("%s original accounting drifted", id)
		}
	}
}

func TestValidTailEveryMixedUTF8ByteBoundary(t *testing.T) {
	content := "A界🙂Z終"
	for budget := 0; budget <= len([]byte(content)); budget++ {
		got := validTail(content, budget)
		want := maximalValidTailV2(content, budget)
		if got != want || !utf8.ValidString(got) || len([]byte(got)) > budget || !strings.HasSuffix(content, got) {
			t.Fatalf("budget %d: got %q (%d), want %q", budget, got, len([]byte(got)), want)
		}
	}
	in := goldenCompilerInputV2()
	in.Resolution.Components = append(in.Resolution.Components[:1], budgetComponentV2("policy-zero", KindWorkflowPolicy, TierImportant, "🙂"))
	in.Resolution.Omissions = nil
	in.Budget = AttentionBudgetV2{TotalBytes: 81, RequiredBytes: 80, ImportantBytes: 1, OptionalBytes: 0}
	got, err := CompileContextV2(in)
	if err != nil {
		t.Fatal(err)
	}
	if !hasOmissionV2(got.Envelope.Omissions, "policy-zero", OmissionBudgetExhausted) || hasComponentV2(got.Envelope.Components, "policy-zero") {
		t.Fatal("zero-valid-byte allocation was not omitted")
	}
}

func TestOptionalMemorySkipsEarlyNonFitAndIncludesLaterFitWhole(t *testing.T) {
	got, err := CompileContextV2(goldenCompilerInputV2())
	if err != nil {
		t.Fatal(err)
	}
	if hasComponentV2(got.Envelope.Components, "memory-a-large") || !hasOmissionV2(got.Envelope.Omissions, "memory-a-large", OmissionBudgetExhausted) {
		t.Fatal("first non-fitting Memory was not omitted")
	}
	c := findComponentV2(t, got.Envelope.Components, "memory-z-small")
	if c.Content != "remember" || c.Truncation != TruncationNone || c.OriginalBytes != c.IncludedBytes {
		t.Fatalf("later Memory was not included whole: %+v", c)
	}
}

func TestCompileContextV2RequiredNeverBorrows(t *testing.T) {
	in := goldenCompilerInputV2()
	in.Budget = AttentionBudgetV2{TotalBytes: 109, RequiredBytes: 8, ImportantBytes: 89, OptionalBytes: 12}
	assertCompileCodeV2(t, in, CodeContextBudgetUnsatisfiable)
}

func TestCompileContextV2RejectsPretruncatedResolution(t *testing.T) {
	for _, id := range []string{"policy-b-mixed", "memory-z-small"} {
		in := goldenCompilerInputV2()
		c := findInputComponentPointerV2(t, &in, id)
		c.Content = validTail(c.Content, 4)
		c.ContentHash = hashBytes([]byte(c.Content))
		c.IncludedBytes = len([]byte(c.Content))
		c.Truncation = TruncationTail
		if c.OriginalBytes <= c.IncludedBytes {
			c.OriginalBytes = c.IncludedBytes + 1
		}
		assertCompileCodeV2(t, in, CodeContextInvalidComponent)
	}
}

func TestCompileContextV2RejectsIdentityHashUTF8AndBounds(t *testing.T) {
	tests := []struct {
		name string
		edit func(*ContextCompilerInputV2)
		code ContextErrorCode
	}{
		{"duplicate components", func(v *ContextCompilerInputV2) {
			v.Resolution.Components = append(v.Resolution.Components, v.Resolution.Components[0])
		}, CodeContextInvalidComponent},
		{"duplicate omissions", func(v *ContextCompilerInputV2) {
			v.Resolution.Omissions = append(v.Resolution.Omissions, v.Resolution.Omissions[0])
		}, CodeContextInvalidComponent},
		{"included omitted overlap", func(v *ContextCompilerInputV2) { v.Resolution.Omissions[0].ComponentID = "node-task" }, CodeContextInvalidComponent},
		{"engine ID source collision", func(v *ContextCompilerInputV2) { v.ExecutionContract.ID = "node-task" }, CodeContextInvalidComponent},
		{"engine ID collision", func(v *ContextCompilerInputV2) { v.OutputContract.ID = v.ExecutionContract.ID }, CodeContextInvalidComponent},
		{"engine kind spoof", func(v *ContextCompilerInputV2) { v.Resolution.Components[0].Kind = KindExecutionContract }, CodeContextInvalidComponent},
		{"invalid hash", func(v *ContextCompilerInputV2) { v.Resolution.Components[0].ContentHash = strings.Repeat("0", 64) }, CodeContextHashMismatch},
		{"invalid UTF8", func(v *ContextCompilerInputV2) {
			c := &v.Resolution.Components[0]
			c.Content = string([]byte{0xff})
			c.ContentHash = hashBytes([]byte(c.Content))
			c.Provenance.SourceHash = c.ContentHash
			c.OriginalBytes = 1
			c.IncludedBytes = 1
		}, CodeContextInvalidComponent},
		{"component bound", func(v *ContextCompilerInputV2) {
			v.Resolution.Components = make([]ContextComponentV2, MaxContextComponents+1)
		}, CodeContextInvalidComponent},
		{"omission bound", func(v *ContextCompilerInputV2) {
			v.Resolution.Omissions = make([]ContextOmissionV2, MaxContextOmissions+1)
		}, CodeContextInvalidComponent},
		{"budget overflow", func(v *ContextCompilerInputV2) {
			v.Budget = AttentionBudgetV2{TotalBytes: MaxContextPayloadBytes, RequiredBytes: math.MaxInt, ImportantBytes: math.MaxInt, OptionalBytes: math.MaxInt}
		}, CodeContextBudgetUnsatisfiable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { in := goldenCompilerInputV2(); tt.edit(&in); assertCompileCodeV2(t, in, tt.code) })
	}
}

func TestCompileContextV2ErrorsAreBoundedAndContentFree(t *testing.T) {
	const marker = "SENSITIVE-M53-MARKER-DO-NOT-LEAK"
	in := goldenCompilerInputV2()
	in.Resolution.Components[0].Content = marker
	_, err := CompileContextV2(in)
	if err == nil {
		t.Fatal("expected error")
	}
	encoded, _ := json.Marshal(err)
	if strings.Contains(err.Error(), marker) || strings.Contains(string(encoded), marker) {
		t.Fatalf("error leaked source content: %v / %s", err, encoded)
	}
	if len(err.Error()) > 512 || len(encoded) > 1024 {
		t.Fatalf("error surface is unbounded: %d/%d", len(err.Error()), len(encoded))
	}
}

func TestBudgetPolicyV1Stable(t *testing.T) {
	p := DefaultBudgetPolicyV1()
	if p != (BudgetPolicyV1{TotalBytes: 131072, RequiredBytes: 65536, ImportantBytes: 49152, OptionalBytes: 16384}) {
		t.Fatalf("policy drifted: %+v", p)
	}
	if err := ValidateBudgetPolicyV1(p); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AttentionBudget(); err != nil {
		t.Fatal(err)
	}
	bad := p
	bad.OptionalBytes--
	if errorCode(ValidateBudgetPolicyV1(bad)) != CodeContextBudgetUnsatisfiable {
		t.Fatal("invalid policy accepted")
	}
}

func cloneCompilerInputV2(t *testing.T, in ContextCompilerInputV2) ContextCompilerInputV2 {
	t.Helper()
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out ContextCompilerInputV2
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func componentIDsV2(cs []ContextComponentV2) []string {
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	return ids
}
func omissionKeysV2(os []ContextOmissionV2) []string {
	keys := make([]string, len(os))
	for i, o := range os {
		keys[i] = o.ComponentID + ":" + string(o.Reason)
	}
	return keys
}
func errorCode(err error) ContextErrorCode {
	if e, ok := err.(*ContractError); ok {
		return e.Code
	}
	return ""
}
func assertCompileCodeV2(t *testing.T, in ContextCompilerInputV2, want ContextErrorCode) {
	t.Helper()
	_, err := CompileContextV2(in)
	if got := errorCode(err); got != want {
		t.Fatalf("error=%v code=%q want=%q", err, got, want)
	}
}
func hasComponentV2(cs []ContextComponentV2, id string) bool {
	for _, c := range cs {
		if c.ID == id {
			return true
		}
	}
	return false
}
func hasOmissionV2(os []ContextOmissionV2, id string, reason OmissionReason) bool {
	for _, o := range os {
		if o.ComponentID == id && o.Reason == reason {
			return true
		}
	}
	return false
}
func findComponentV2(t *testing.T, cs []ContextComponentV2, id string) ContextComponentV2 {
	t.Helper()
	for _, c := range cs {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("component %s missing", id)
	return ContextComponentV2{}
}
func findInputComponentV2(t *testing.T, in ContextCompilerInputV2, id string) ContextComponentV2 {
	t.Helper()
	return *findInputComponentPointerV2(t, &in, id)
}
func findInputComponentPointerV2(t *testing.T, in *ContextCompilerInputV2, id string) *ContextComponentV2 {
	t.Helper()
	for i := range in.Resolution.Components {
		if in.Resolution.Components[i].ID == id {
			return &in.Resolution.Components[i]
		}
	}
	t.Fatalf("input component %s missing", id)
	return nil
}
func maximalValidTailV2(content string, budget int) string {
	b := []byte(content)
	best := ""
	for start := 0; start <= len(b); start++ {
		candidate := b[start:]
		if len(candidate) <= budget && utf8.Valid(candidate) && len(candidate) > len([]byte(best)) {
			best = string(candidate)
		}
	}
	return best
}
