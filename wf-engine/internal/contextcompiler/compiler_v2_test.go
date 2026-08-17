package contextcompiler

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"wf.local/wf-engine/internal/agent"
)

func testBudgetComponentV2(id string, kind ComponentKind, tier AttentionTier, content string) ContextComponentV2 {
	h := hashBytes([]byte(content))
	return ContextComponentV2{ID: id, Kind: kind, Tier: tier, Sensitivity: SensitivityProject,
		Provenance: ComponentProvenanceV2{Source: "test:" + id, SourceVersion: "v1", SourceHash: h, Reason: "approved test source"},
		Content:    content, ContentHash: h, OriginalBytes: len([]byte(content)), IncludedBytes: len([]byte(content)), Truncation: TruncationNone}
}

func testInputV2() ContextCompilerInputV2 {
	return ContextCompilerInputV2{
		Identity:          agent.AttemptIdentity{RunID: "run-test", NodeID: "node", Attempt: 1},
		ExecutionContract: testBudgetComponentV2("execution-contract", KindExecutionContract, TierRequired, "execute safely"),
		OutputContract:    testBudgetComponentV2("output-contract", KindOutputContract, TierRequired, "return structured result"),
		Resolution: ContextSourceResolutionV2{Components: []ContextComponentV2{
			testBudgetComponentV2("node-task", KindNodeTask, TierRequired, "implement the approved task"),
			testBudgetComponentV2("policy-a", KindWorkflowPolicy, TierImportant, strings.Repeat("A", 10)),
			testBudgetComponentV2("policy-b", KindWorkflowPolicy, TierImportant, strings.Repeat("B", 20)),
			testBudgetComponentV2("memory-a", KindMemory, TierOptional, "memory A"),
			testBudgetComponentV2("memory-b", KindMemory, TierOptional, strings.Repeat("memory too large", 20)),
		}},
		Budget: AttentionBudgetV2{TotalBytes: 160, RequiredBytes: 80, ImportantBytes: 40, OptionalBytes: 40},
	}
}

func TestCompileContextV2DeterministicAndMetadataOnly(t *testing.T) {
	in := testInputV2()
	want, err := CompileContextV2(in)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		got, err := CompileContextV2(in)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d is not byte-identical", i)
		}
	}
	encoded, _ := json.Marshal(want.Manifest)
	if strings.Contains(string(encoded), "implement the approved task") || strings.Contains(string(encoded), "memory A") {
		t.Fatal("manifest leaked source content")
	}
}

func TestAttentionCompilerV2GoldenFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/attention-compiler-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Expected struct {
			EnvelopeHash string           `json:"envelopeHash"`
			Usage        AttentionUsageV2 `json:"usage"`
			Included     []string         `json:"includedComponentIds"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	got, err := CompileContextV2(testInputV2())
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash != fixture.Expected.EnvelopeHash {
		t.Fatalf("hash = %s, want %s", got.Hash, fixture.Expected.EnvelopeHash)
	}
	if !reflect.DeepEqual(got.Manifest.Usage, fixture.Expected.Usage) {
		t.Fatalf("usage = %+v, want %+v", got.Manifest.Usage, fixture.Expected.Usage)
	}
}

func TestCompileContextV2PermutationAndAliasing(t *testing.T) {
	in := testInputV2()
	first, err := CompileContextV2(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Resolution.Components[0], in.Resolution.Components[1] = in.Resolution.Components[1], in.Resolution.Components[0]
	second, err := CompileContextV2(in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash != second.Hash || !reflect.DeepEqual(first.Envelope, second.Envelope) {
		t.Fatal("input permutation changed output")
	}
	if in.Resolution.Components[0].Content != strings.Repeat("A", 10) {
		t.Fatal("compiler mutated caller input")
	}
}

func TestCompileContextV2RequiredNeverBorrows(t *testing.T) {
	in := testInputV2()
	in.Budget = AttentionBudgetV2{TotalBytes: 160, RequiredBytes: 8, ImportantBytes: 132, OptionalBytes: 20}
	_, err := CompileContextV2(in)
	if code := errorCode(err); code != CodeContextBudgetUnsatisfiable {
		t.Fatalf("error code = %v", code)
	}
}

func TestCompileContextV2ImportantBalancedAndUTF8Safe(t *testing.T) {
	in := testInputV2()
	in.Resolution.Components[1] = testBudgetComponentV2("policy-a", KindWorkflowPolicy, TierImportant, "前缀🙂"+strings.Repeat("甲", 20))
	in.Resolution.Components[2] = testBudgetComponentV2("policy-b", KindWorkflowPolicy, TierImportant, "后缀🚀"+strings.Repeat("乙", 20))
	in.Budget = AttentionBudgetV2{TotalBytes: 160, RequiredBytes: 80, ImportantBytes: 13, OptionalBytes: 67}
	got, err := CompileContextV2(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got.Envelope.Components {
		if c.Tier == TierImportant && !isUTF8(c.Content) {
			t.Fatalf("invalid UTF-8 in %s", c.ID)
		}
	}
}

func TestCompileContextV2OptionalWholeRecordAndLaterFit(t *testing.T) {
	in := testInputV2()
	in.Budget = AttentionBudgetV2{TotalBytes: 160, RequiredBytes: 80, ImportantBytes: 40, OptionalBytes: 40}
	got, err := CompileContextV2(in)
	if err != nil {
		t.Fatal(err)
	}
	if !hasComponent(got.Envelope.Components, "memory-a") || hasComponent(got.Envelope.Components, "memory-b") {
		t.Fatalf("unexpected optional selection: %+v", got.Envelope.Components)
	}
	if !hasOmission(got.Envelope.Omissions, "memory-b", OmissionBudgetExhausted) {
		t.Fatal("missing budget omission")
	}
}

func TestCompileContextV2MergesExistingOmissionsCanonically(t *testing.T) {
	in := testInputV2()
	in.Resolution.Omissions = []ContextOmissionV2{{ComponentID: "old-memory", Kind: KindMemory, Tier: TierOptional, Reason: OmissionExpired, SourceHash: strings.Repeat("a", 64), OriginalBytes: 4}, {ComponentID: "older-memory", Kind: KindMemory, Tier: TierOptional, Reason: OmissionSuperseded, SourceHash: strings.Repeat("b", 64), OriginalBytes: 4}}
	got, err := CompileContextV2(in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Envelope.Omissions[0].ComponentID != "memory-b" || got.Envelope.Omissions[1].ComponentID != "old-memory" || got.Envelope.Omissions[2].ComponentID != "older-memory" {
		t.Fatalf("omissions not canonical: %+v", got.Envelope.Omissions)
	}
}

func TestCompileContextV2RejectsSpoofedEngineSource(t *testing.T) {
	in := testInputV2()
	spoof := testBudgetComponentV2("spoof", KindExecutionContract, TierRequired, "caller contract")
	in.Resolution.Components = append(in.Resolution.Components, spoof)
	if errorCodeMustCompile(in) != CodeContextInvalidComponent {
		t.Fatal("spoofed engine source was accepted")
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
}

func errorCode(err error) ContextErrorCode {
	if err == nil {
		return ""
	}
	if e, ok := err.(*ContractError); ok {
		return e.Code
	}
	return ""
}
func errorCodeMustCompile(in ContextCompilerInputV2) ContextErrorCode {
	_, err := CompileContextV2(in)
	return errorCode(err)
}
func isUTF8(s string) bool { return utf8.ValidString(s) }
func hasComponent(cs []ContextComponentV2, id string) bool {
	for _, c := range cs {
		if c.ID == id {
			return true
		}
	}
	return false
}
func hasOmission(os []ContextOmissionV2, id string, reason OmissionReason) bool {
	for _, o := range os {
		if o.ComponentID == id && o.Reason == reason {
			return true
		}
	}
	return false
}
