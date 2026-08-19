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
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

// ContextAssembly is the Run-owned, complete input to the v2 compiler. It is
// deliberately separate from persistence and contains no rendered prompt.
type ContextAssembly struct {
	Identity             agent.AttemptIdentity
	Project              string
	Target               string
	NodeID               string
	NodeTask             string
	RequiredSkills       []string
	WorkflowPolicy       string
	ContextPolicyVersion string
	ProjectInstructions  []string
	DependencyResults    map[string]workflow.Result
	UserAnswer           json.RawMessage
	SelectedMemory       []contextcompiler.SelectedMemorySourceV2
	SelectedMemoryIDs    []string
	MemoryBindings       []workflow.MemoryBinding
}

type compiledRunContext struct {
	Compilation    contextcompiler.CompilationV2
	Envelope       agent.AttemptEnvelope
	LegacyManifest contextcompiler.Manifest
}

func (s *Service) consumeCompiledMemory(project string, identity agent.AttemptIdentity, compilation contextcompiler.CompilationV2) (*MemoryUsageReceipt, error) {
	ids := make([]string, 0)
	for _, component := range compilation.Manifest.Components {
		if component.Kind == contextcompiler.KindMemory {
			ids = append(ids, component.ID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sort.Strings(ids)
	mutationID := fmt.Sprintf("context-use:%s:%s:%d:%s", identity.RunID, identity.NodeID, identity.Attempt, compilation.Hash)
	if _, err := s.store.ConsumeMemory(store.MemoryConsumeInput{Project: project, MutationID: mutationID, RecordIDs: ids, Reason: "Memory included in compiled Attempt context"}); err != nil {
		return nil, err
	}
	return &MemoryUsageReceipt{SchemaVersion: "fishyume.memory-usage/v1", MutationID: mutationID, RecordIDs: ids, Committed: true}, nil
}

func (s *Service) compileRunContext(assembly ContextAssembly) (compiledRunContext, error) {
	if len(assembly.SelectedMemory) == 0 && (len(assembly.SelectedMemoryIDs) > 0 || len(assembly.MemoryBindings) > 0) {
		if s.store == nil {
			return compiledRunContext{}, fmt.Errorf("Memory store is unavailable")
		}
		ids := append([]string(nil), assembly.SelectedMemoryIDs...)
		if len(assembly.MemoryBindings) > 0 {
			ids = ids[:0]
			for _, binding := range assembly.MemoryBindings {
				ids = append(ids, binding.ID)
			}
		}
		sort.Strings(ids)
		for _, id := range ids {
			record, _, err := s.store.GetMemory(assembly.Project, id)
			if err != nil {
				return compiledRunContext{}, err
			}
			reason := "explicitly selected Memory ID"
			for _, binding := range assembly.MemoryBindings {
				if binding.ID == id {
					reason = binding.Reason
					break
				}
			}
			assembly.SelectedMemory = append(assembly.SelectedMemory, contextcompiler.SelectedMemorySourceV2{Record: record, Reason: reason})
		}
	}
	registry := contextcompiler.BuiltinContextSourceRegistryV2()
	resolutionInput := contextcompiler.ContextSourceResolutionInputV2{
		ProjectRoot: assembly.Project, AllowedUpstreamNodes: sortedKeys(assembly.DependencyResults),
		NodeTasks:        []contextcompiler.NodeTaskSourceV2{{Declaration: contextcompiler.SourceDeclarationV2{ID: "node-task", SourceVersion: "v1", Reason: "current Workflow Node task", Tier: contextcompiler.TierRequired, Sensitivity: contextcompiler.SensitivityProject}, NodeID: assembly.NodeID, Content: assembly.NodeTask}},
		WorkflowPolicies: []contextcompiler.WorkflowPolicySourceV2{{Declaration: contextcompiler.SourceDeclarationV2{ID: "workflow-policy", SourceVersion: "v1", Reason: "engine workflow policy/defaults", Tier: contextcompiler.TierImportant, Sensitivity: contextcompiler.SensitivityProject}, Content: assembly.WorkflowPolicy}},
		AsOf:             s.now().UTC(), SelectedMemory: append([]contextcompiler.SelectedMemorySourceV2(nil), assembly.SelectedMemory...),
	}
	// v2 resolves only explicitly selected project instruction files. Legacy v1
	// retains the historical fixed discovery behavior for compatibility resumes.
	instructionNames := assembly.ProjectInstructions
	if assembly.ContextPolicyVersion != "context-policy/v1" {
		instructionNames = []string{"AGENTS.md", "CLAUDE.md", ".fishyume/instructions.md"}
	}
	for _, name := range instructionNames {
		id := "project-instruction-" + hashForAssembly([]byte(filepath.ToSlash(name)))[:16]
		if assembly.ContextPolicyVersion != "context-policy/v1" {
			id = "project-" + strings.ToLower(strings.ReplaceAll(filepath.Base(name), ".", "-"))
		}
		if assembly.ContextPolicyVersion != "context-policy/v1" {
			if _, err := os.Stat(filepath.Join(assembly.Project, name)); err != nil {
				continue
			}
		}
		resolutionInput.ProjectInstructions = append(resolutionInput.ProjectInstructions, contextcompiler.ProjectInstructionSourceV2{Declaration: contextcompiler.SourceDeclarationV2{ID: id, SourceVersion: "v1", Reason: "project instructions", Tier: contextcompiler.TierRequired, Sensitivity: contextcompiler.SensitivityProject}, RelativePath: name})
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

func cloneContextBindings(bindings workflow.ContextBindings) workflow.ContextBindings {
	clone := workflow.ContextBindings{MemoryByNode: make(map[string][]workflow.MemoryBinding, len(bindings.MemoryByNode))}
	for nodeID, selections := range bindings.MemoryByNode {
		clone.MemoryByNode[nodeID] = append([]workflow.MemoryBinding(nil), selections...)
	}
	if len(clone.MemoryByNode) == 0 {
		clone.MemoryByNode = nil
	}
	return clone
}
