package run

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/contextcompiler"
	"wf.local/wf-engine/internal/workflow"
)

// ContextAssembly is the Run-owned, complete input to the v2 compiler. It is
// deliberately separate from persistence and contains no rendered prompt.
type ContextAssembly struct {
	Identity          agent.AttemptIdentity
	Project           string
	Target            string
	NodeID            string
	NodeTask          string
	RequiredSkills    []string
	WorkflowPolicy    string
	DependencyResults map[string]workflow.Result
	UserAnswer        json.RawMessage
	SelectedMemory    []contextcompiler.SelectedMemorySourceV2
	SelectedMemoryIDs []string
}

type compiledRunContext struct {
	Compilation    contextcompiler.CompilationV2
	Envelope       agent.AttemptEnvelope
	LegacyManifest contextcompiler.Manifest
}

func (s *Service) compileRunContext(assembly ContextAssembly) (compiledRunContext, error) {
	if len(assembly.SelectedMemory) == 0 && len(assembly.SelectedMemoryIDs) > 0 {
		if s.store == nil {
			return compiledRunContext{}, fmt.Errorf("Memory store is unavailable")
		}
		ids := append([]string(nil), assembly.SelectedMemoryIDs...)
		sort.Strings(ids)
		for _, id := range ids {
			record, _, err := s.store.GetMemory(assembly.Project, id)
			if err != nil {
				return compiledRunContext{}, err
			}
			assembly.SelectedMemory = append(assembly.SelectedMemory, contextcompiler.SelectedMemorySourceV2{Record: record, Reason: "explicitly selected Memory ID"})
		}
	}
	registry := contextcompiler.BuiltinContextSourceRegistryV2()
	resolutionInput := contextcompiler.ContextSourceResolutionInputV2{
		ProjectRoot: assembly.Project, AllowedUpstreamNodes: sortedKeys(assembly.DependencyResults),
		NodeTasks:        []contextcompiler.NodeTaskSourceV2{{Declaration: contextcompiler.SourceDeclarationV2{ID: "node-task", SourceVersion: "v1", Reason: "current Workflow Node task", Tier: contextcompiler.TierRequired, Sensitivity: contextcompiler.SensitivityProject}, NodeID: assembly.NodeID, Content: assembly.NodeTask}},
		WorkflowPolicies: []contextcompiler.WorkflowPolicySourceV2{{Declaration: contextcompiler.SourceDeclarationV2{ID: "workflow-policy", SourceVersion: "v1", Reason: "engine workflow policy/defaults", Tier: contextcompiler.TierImportant, Sensitivity: contextcompiler.SensitivityProject}, Content: assembly.WorkflowPolicy}},
		AsOf:             s.now().UTC(), SelectedMemory: append([]contextcompiler.SelectedMemorySourceV2(nil), assembly.SelectedMemory...),
	}
	// Resolve only explicitly selected project instruction files. Missing files
	// are not synthesized; the required compiler contracts remain sufficient.
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", ".fishyume/instructions.md"} {
		if _, err := os.Stat(filepath.Join(assembly.Project, name)); err == nil {
			resolutionInput.ProjectInstructions = append(resolutionInput.ProjectInstructions, contextcompiler.ProjectInstructionSourceV2{Declaration: contextcompiler.SourceDeclarationV2{ID: "project-" + strings.ToLower(strings.ReplaceAll(filepath.Base(name), ".", "-")), SourceVersion: "v1", Reason: "project instructions", Tier: contextcompiler.TierRequired, Sensitivity: contextcompiler.SensitivityProject}, RelativePath: name})
		}
	}
	if len(assembly.UserAnswer) > 0 {
		resolutionInput.UserAnswers = []contextcompiler.UserAnswerSourceV2{{Declaration: contextcompiler.SourceDeclarationV2{ID: "user-answer", SourceVersion: "v1", Reason: "current user answer", Tier: contextcompiler.TierRequired, Sensitivity: contextcompiler.SensitivityProject}, Answer: append(json.RawMessage(nil), assembly.UserAnswer...)}}
	}
	for _, nodeID := range sortedKeys(assembly.DependencyResults) {
		result := assembly.DependencyResults[nodeID]
		if strings.TrimSpace(result.Summary) == "" && result.Decision != "" {
			result.Summary = "approval decision: " + result.Decision
		}
		resolutionInput.DependencyResults = append(resolutionInput.DependencyResults, contextcompiler.DependencyResultSourceV2{Declaration: contextcompiler.SourceDeclarationV2{ID: "dependency-" + nodeID, SourceVersion: "v1", Reason: "explicitly allowed dependency result", Tier: contextcompiler.TierImportant, Sensitivity: contextcompiler.SensitivityProject}, UpstreamNode: nodeID, Result: &result})
	}
	resolution, err := registry.Resolve(resolutionInput)
	if err != nil {
		return compiledRunContext{}, err
	}
	budget, err := contextcompiler.DefaultBudgetPolicyV1().AttentionBudget()
	if err != nil {
		return compiledRunContext{}, err
	}
	executionContent, _ := json.Marshal(map[string]any{"interaction": "none", "processMode": "one-shot", "pty": "disabled", "target": assembly.Target})
	outputContent, _ := json.Marshal(map[string]any{"maxBytes": workflow.MaxResultBytes, "schema": json.RawMessage(agentResultContractSchema())})
	compiled, err := contextcompiler.CompileContextV2(contextcompiler.ContextCompilerInputV2{Identity: assembly.Identity, Resolution: resolution,
		ExecutionContract: contextcompiler.ContextComponentV2{ID: "execution-contract", Kind: contextcompiler.KindExecutionContract, Tier: contextcompiler.TierRequired, Sensitivity: contextcompiler.SensitivityPublic, Provenance: contextcompiler.ComponentProvenanceV2{Source: "engine:execution", SourceVersion: "v1", SourceHash: hashForAssembly(executionContent), Reason: "engine execution contract"}, Content: string(executionContent), ContentHash: hashForAssembly(executionContent), OriginalBytes: len(executionContent), IncludedBytes: len(executionContent), Truncation: contextcompiler.TruncationNone},
		OutputContract:    contextcompiler.ContextComponentV2{ID: "output-contract", Kind: contextcompiler.KindOutputContract, Tier: contextcompiler.TierRequired, Sensitivity: contextcompiler.SensitivityPublic, Provenance: contextcompiler.ComponentProvenanceV2{Source: "engine:output", SourceVersion: "v1", SourceHash: hashForAssembly(outputContent), Reason: "engine output contract"}, Content: string(outputContent), ContentHash: hashForAssembly(outputContent), OriginalBytes: len(outputContent), IncludedBytes: len(outputContent), Truncation: contextcompiler.TruncationNone}, Budget: budget})
	if err != nil {
		return compiledRunContext{}, err
	}
	envelope, err := contextcompiler.AdaptEnvelopeV2WithSkills(compiled.Envelope, assembly.Project, assembly.Target, assembly.RequiredSkills)
	if err != nil {
		return compiledRunContext{}, err
	}
	legacy := contextcompiler.Manifest{CompilerVersion: compiled.Manifest.CompilerVersion, Components: make([]contextcompiler.Component, 0, len(compiled.Manifest.Components))}
	for _, component := range compiled.Manifest.Components {
		legacy.Components = append(legacy.Components, contextcompiler.Component{Name: component.ID, Source: component.Provenance.Source, Version: component.Provenance.SourceVersion})
	}
	for index, component := range legacy.Components {
		if component.Source == "run:answer/user-answer" {
			legacy.Components = append(append(legacy.Components[:index], legacy.Components[index+1:]...), contextcompiler.Component{Name: "input-answer", Source: "run.action.answer", Version: "v1"})
			break
		}
	}
	return compiledRunContext{Compilation: compiled, Envelope: envelope, LegacyManifest: legacy}, nil
}

func sortedKeys(values map[string]workflow.Result) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func hashForAssembly(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("%x", sum[:])
}

func agentIdentity(runID, nodeID string, attempt int) agent.AttemptIdentity {
	return agent.AttemptIdentity{RunID: runID, NodeID: nodeID, Attempt: attempt}
}

func workflowPolicy(document workflow.Document) string {
	value := map[string]any{"execution": document.Execution, "defaults": document.Defaults, "constraints": map[string]string{"interaction": "none", "processMode": "one-shot", "pty": "disabled"}}
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
