package contextcompiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"wf.local/wf-engine/internal/agent"
)

// AdaptEnvelopeV2 deterministically projects an ephemeral v2 envelope onto
// the unchanged agent.AttemptEnvelope wire contract. The returned Prompt is
// intentionally process-local; callers must never persist it.
func AdaptEnvelopeV2(envelope ContextEnvelopeV2, workspace, target string) (agent.AttemptEnvelope, error) {
	return AdaptEnvelopeV2WithSkills(envelope, workspace, target, nil)
}

func AdaptEnvelopeV2WithSkills(envelope ContextEnvelopeV2, workspace, target string, requiredSkills []string) (agent.AttemptEnvelope, error) {
	if err := ValidateContextEnvelopeV2(envelope); err != nil {
		return agent.AttemptEnvelope{}, err
	}
	if strings.TrimSpace(workspace) == "" {
		return agent.AttemptEnvelope{}, fmt.Errorf("workspace is required")
	}
	if strings.TrimSpace(target) == "" {
		target = "local"
	}
	skills := append([]string(nil), requiredSkills...)
	if skills == nil {
		skills = []string{}
	}
	// Required skills are a set at the wire boundary.  Canonicalize their
	// order (and remove duplicate declarations) so adapter output does not
	// depend on workflow/YAML ordering while preserving the caller's slice.
	sort.Strings(skills)
	if len(skills) > 1 {
		unique := skills[:1]
		for _, skill := range skills[1:] {
			if skill != unique[len(unique)-1] {
				unique = append(unique, skill)
			}
		}
		skills = unique
	}
	envelopeValue := agent.AttemptEnvelope{ProtocolVersion: agent.ProtocolVersion, Identity: envelope.Identity, Workspace: workspace, Target: target,
		Context: agent.AttemptContext{UpstreamResults: []agent.UpstreamResult{}, RequiredSkills: skills}, Constraints: map[string]string{}, Budget: map[string]int64{},
		ResultContract: agent.ResultContract{MaxBytes: 64 * 1024}}
	var promptComponents []struct {
		ID      string        `json:"id"`
		Kind    ComponentKind `json:"kind"`
		Content string        `json:"content"`
	}
	for _, component := range envelope.Components {
		promptComponents = append(promptComponents, struct {
			ID      string        `json:"id"`
			Kind    ComponentKind `json:"kind"`
			Content string        `json:"content"`
		}{component.ID, component.Kind, component.Content})
		switch component.Kind {
		case KindNodeTask:
			envelopeValue.Task = component.Content
		case KindDependencyResult:
			upstreamID := component.Provenance.Source
			const prefix = "run:node/"
			if strings.HasPrefix(upstreamID, prefix) {
				upstreamID = strings.TrimPrefix(upstreamID, prefix)
				upstreamID = strings.TrimSuffix(upstreamID, "/result")
			}
			if upstreamID == "" {
				upstreamID = component.ID
			}
			encoded := json.RawMessage(component.Content)
			if !json.Valid(encoded) {
				return agent.AttemptEnvelope{}, fmt.Errorf("dependency result %q is not valid JSON", component.ID)
			}
			envelopeValue.Context.UpstreamResults = append(envelopeValue.Context.UpstreamResults, agent.UpstreamResult{NodeID: upstreamID, Result: append(json.RawMessage(nil), encoded...)})
		case KindUserAnswer:
			if json.Valid([]byte(component.Content)) {
				envelopeValue.Context.InputAnswer = json.RawMessage(component.Content)
			}
		case KindOutputContract:
			if json.Valid([]byte(component.Content)) {
				envelopeValue.ResultContract.Schema = json.RawMessage(component.Content)
			}
		case KindExecutionContract:
			// Engine contract content is rendered in Prompt; the stable wire fields
			// remain explicit and bounded below.
		}
	}
	sort.Slice(envelopeValue.Context.UpstreamResults, func(i, j int) bool {
		return envelopeValue.Context.UpstreamResults[i].NodeID < envelopeValue.Context.UpstreamResults[j].NodeID
	})
	// These fields are engine-owned and intentionally independent of Provider
	// routing. They preserve the pre-M5 one-shot semantics.
	envelopeValue.Constraints = map[string]string{"interaction": "none", "processMode": "one-shot", "pty": "disabled"}
	envelopeValue.Budget = map[string]int64{"totalBytes": int64(envelope.Budget.TotalBytes), "requiredBytes": int64(envelope.Budget.RequiredBytes), "importantBytes": int64(envelope.Budget.ImportantBytes), "optionalBytes": int64(envelope.Budget.OptionalBytes)}
	canonical, err := json.Marshal(struct {
		Compiler string `json:"compiler"`
		Hash     string `json:"hash"`
		Parts    any    `json:"components"`
	}{Compiler: envelope.CompilerVersion, Hash: func() string { h, _ := CanonicalEnvelopeHashV2(envelope); return h }(), Parts: promptComponents})
	if err != nil {
		return agent.AttemptEnvelope{}, err
	}
	var prompt bytes.Buffer
	prompt.WriteString("Fishyume headless Attempt envelope (context-compiler/v2):\n")
	prompt.Write(canonical)
	wireEnvelope := envelopeValue
	if len(envelopeValue.Context.InputAnswer) > 0 {
		var answer map[string]json.RawMessage
		if json.Unmarshal(envelopeValue.Context.InputAnswer, &answer) == nil {
			ordered := struct {
				Attempt   json.RawMessage `json:"attempt,omitempty"`
				Questions json.RawMessage `json:"questions,omitempty"`
				Answers   json.RawMessage `json:"answers,omitempty"`
			}{Attempt: answer["attempt"], Questions: answer["questions"], Answers: answer["answers"]}
			if encodedAnswer, encodeErr := json.Marshal(ordered); encodeErr == nil {
				wireEnvelope.Context.InputAnswer = encodedAnswer
			}
		}
	}
	wire, _ := json.Marshal(wireEnvelope)
	prompt.WriteString("\nwire=")
	prompt.Write(wire)
	prompt.WriteString("\n\nExecute the task with the declared constraints. Return exactly one structured result matching resultContract.")
	envelopeValue.Prompt = prompt.String()
	if err := agent.ValidateAttemptEnvelope(envelopeValue); err != nil {
		return agent.AttemptEnvelope{}, err
	}
	return envelopeValue, nil
}
