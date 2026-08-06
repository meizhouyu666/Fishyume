package workflow

import (
	"strings"
	"testing"
)

func TestValidateResultAcceptsStructuredInputQuestions(t *testing.T) {
	result := Result{Summary: "approval required", Questions: []InputQuestion{{ID: "approval", Prompt: "Proceed?", Choices: []string{"yes", "no"}, Required: true}}}
	if err := ValidateResult(result); err != nil {
		t.Fatal(err)
	}
	result.Questions[0].Prompt = ""
	if err := ValidateResult(result); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("accepted malformed question: %v", err)
	}
	result.Questions = []InputQuestion{{ID: "approval", Prompt: "Proceed?", Required: true}, {ID: "approval", Prompt: "Really proceed?", Required: true}}
	if err := ValidateResult(result); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("accepted duplicate question IDs: %v", err)
	}
}
