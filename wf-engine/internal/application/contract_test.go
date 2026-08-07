package application

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
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
	wanted := []string{"error", "run.action.request", "run.action.response", "run.events.response", "run.get.response", "run.list.response", "run.result.response", "run.start.request", "run.start.response", "system.capabilities.response", "workflow.explain.response", "workflow.validate.response"}
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
	if limits.DefaultListLimit <= 0 || limits.DefaultListLimit > limits.MaxListLimit || limits.DefaultEventLimit <= 0 || limits.DefaultEventLimit > limits.MaxEventLimit || limits.MaxEventWaitMS <= 0 || limits.MaxResponseBytes <= limits.MaxErrorDataBytes {
		t.Fatalf("invalid stable limits: %+v", limits)
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
