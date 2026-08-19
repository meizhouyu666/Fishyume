package application

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"wf.local/wf-engine/internal/contextcompiler"
)

func TestApplicationContractFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/contracts-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]json.RawMessage
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	wanted := []string{"error", "memory.create.request", "memory.delete.request", "memory.get.response", "memory.list.response", "memory.mutation.response", "memory.supersede.request", "run.action.request", "run.action.response", "run.events.response", "run.get.response", "run.list.response", "run.result.response", "run.start.request", "run.start.response", "system.capabilities.response", "workflow.explain.response", "workflow.validate.response"}
	got := make([]string, 0, len(fixture))
	for key, raw := range fixture {
		got = append(got, key)
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		assertNoLegacyPublicKeys(t, key, value)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, wanted) {
		t.Fatalf("fixture keys = %v, want %v", got, wanted)
	}
	var memoryGet MemoryGetResponse
	if err := json.Unmarshal(fixture["memory.get.response"], &memoryGet); err != nil {
		t.Fatal(err)
	}
	if err := contextcompiler.ValidateMemoryRecordV1(memoryGet.Record); err != nil {
		t.Fatalf("memory.get fixture record: %v", err)
	}
}

func TestStableContractLimitsAndErrors(t *testing.T) {
	if len(WorkflowJSONSchema) > MaxSchemaResponseBytes || len(MinimalWorkflowExample) > MaxSchemaResponseBytes {
		t.Fatal("schema contract exceeds response limit")
	}
	want := []ErrorCode{CodeInvalidArgument, CodeInvalidWorkflow, CodeNotFound, CodeConflict, CodeCapabilityUnavailable, CodeNotReady, CodeProtocolMismatch, CodeInternal}
	if !reflect.DeepEqual(StableErrorCodes, want) {
		t.Fatalf("error codes = %v, want %v", StableErrorCodes, want)
	}
	limits := StableLimits()
	if limits.DefaultListLimit <= 0 || limits.DefaultListLimit > limits.MaxListLimit || limits.DefaultEventLimit <= 0 || limits.DefaultEventLimit > limits.MaxEventLimit || limits.MaxEventWaitMS <= 0 || limits.MaxResponseBytes <= limits.MaxErrorDataBytes || limits.MaxMemoryContentBytes != 16*1024 || limits.MaxProjectMemoryRecords != 2048 || limits.MaxMemorySupersedes != 16 || limits.MaxMemoryReceipts != 4096 || limits.DefaultMemoryListLimit > limits.MaxMemoryListLimit || limits.MaxAttemptActivityItems != 12 || limits.MaxAttemptActivityMessageBytes != 240 || limits.MaxAttemptActivityReadBytes != 32*1024 {
		t.Fatalf("invalid stable limits: %+v", limits)
	}
}

func TestAuthoringGuideIsStableBoundedAndContainsNoDynamicContent(t *testing.T) {
	guide := StableAuthoringGuide()
	if guide.SchemaVersion != AuthoringGuideVersion || guide.WorkflowAPIVersion != WorkflowSchemaVersion {
		t.Fatalf("guide versions = %+v", guide)
	}
	if len(guide.RecommendedFlow) == 0 || len(guide.RecommendedFlow) > MaxAuthoringFlowSteps || len(guide.Rules) == 0 || len(guide.Rules) > MaxAuthoringRules {
		t.Fatalf("guide bounds = %+v", guide)
	}
	for _, rule := range guide.Rules {
		if len(rule) == 0 || len(rule) > MaxAuthoringRuleBytes {
			t.Fatalf("rule exceeds bounds: %q", rule)
		}
	}
	encoded, err := json.Marshal(guide)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"M5_6_DYNAMIC_PROJECT_SECRET", "M5_6_PROVIDER_CREDENTIAL", "M5_6_MEMORY_CONTENT"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("guide leaked dynamic content marker %q", forbidden)
		}
	}
}

func assertNoLegacyPublicKeys(t *testing.T, path string, value any) {
	t.Helper()
	switch value := value.(type) {
	case map[string]any:
		for key, child := range value {
			switch key {
			case "backend", "tool", "runtime":
				t.Fatalf("%s contains legacy public key %q", path, key)
			}
			assertNoLegacyPublicKeys(t, path+"."+key, child)
		}
	case []any:
		for _, child := range value {
			assertNoLegacyPublicKeys(t, path, child)
		}
	}
}
