package run

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/contextcompiler"
	"wf.local/wf-engine/internal/store"
	"wf.local/wf-engine/internal/workflow"
)

func TestM54FakeSinglePersistsV2MetadataWithoutPromptContent(t *testing.T) {
	const marker = "M54-SINGLE-COMPONENT-CONTENT-MARKER"
	project := t.TempDir()
	if err := os.WriteFile(project+"/AGENTS.md", []byte("project instructions "+marker), 0o600); err != nil {
		t.Fatal(err)
	}
	backendFixture := &fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "succeeded", Summary: "single complete"}}}
	state := store.New(t.TempDir())
	service := NewService(backendFixture, state)
	workflowContent := "apiVersion: wf/v1\nname: m5-4-single\ndefaults: {tool: codex, runtime: local}\nexecution: {maxConcurrency: 1}\nnodes:\n  work: {type: agent, task: \"do " + marker + "\"}\n"
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: project, Filename: "workflow.yaml", Content: workflowContent})
	if err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
	waitForControllers(t, service)
	backendFixture.mu.Lock()
	prompts := append([]string(nil), backendFixture.prompts...)
	backendFixture.mu.Unlock()
	if len(prompts) != 1 || !strings.Contains(prompts[0], marker) {
		t.Fatalf("driver did not receive complete ephemeral context: %q", prompts)
	}
	var attempt AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "work", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	if attempt.ContextCompilerVersionV2 != contextcompiler.CompilerV2Version || attempt.ContextManifestV2 == nil || attempt.ContextManifestV2.EnvelopeHash != attempt.ContextHash {
		t.Fatalf("v2 attempt metadata=%+v", attempt)
	}
	if attempt.ContextManifestV2.Components == nil || len(attempt.ContextManifestV2.Components) == 0 {
		t.Fatal("v2 metadata omitted component identities")
	}
	for _, path := range []string{state.SnapshotPath(started.ID), state.NodePath(started.ID, "work"), state.AttemptPath(started.ID, "work", 1), state.EventsPath(started.ID)} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), marker) {
			t.Fatalf("durable state %s leaked complete context content: %s", path, data)
		}
	}
	encodedAttempt, err := json.Marshal(attempt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encodedAttempt), "promptHash") || strings.Contains(string(encodedAttempt), marker) {
		t.Fatalf("attempt wire metadata leaked prompt: %s", encodedAttempt)
	}
}

func TestM54FakeParallelPersistsIndependentV2Attempts(t *testing.T) {
	fixture := &barrierBackend{entered: make(chan struct{}), release: make(chan struct{})}
	state := store.New(t.TempDir())
	service := NewService(fixture, state)
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: t.TempDir(), Content: parallelWorkflow(2)})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("parallel fake-driver launches did not overlap")
	}
	for _, nodeID := range []string{"a", "b"} {
		var attempt AttemptSnapshot
		if err := state.ReadAttempt(started.ID, nodeID, 1, &attempt); err != nil {
			t.Fatal(err)
		}
		if attempt.ContextCompilerVersionV2 != contextcompiler.CompilerV2Version || attempt.ContextManifestV2 == nil || attempt.ContextHash == "" {
			t.Fatalf("parallel node %s metadata=%+v", nodeID, attempt)
		}
	}
	close(fixture.release)
	final := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("parallel final=%+v", final)
	}
}

func TestM54V1AttemptReadAndResumeKeepsV1Metadata(t *testing.T) {
	state := store.New(t.TempDir())
	const runID = "run-m54-v1-resume"
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	run := WorkflowSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: 1, ID: runID, WorkflowName: "m54-v1", Project: t.TempDir(), ResolvedDriver: "fake", ResolvedTarget: "local", Phase: PhasePaused, Reason: ReasonControllerDetach, TopologicalOrder: []string{"work"}, Nodes: map[string]NodeSummary{"work": {ID: "work", Type: "agent", Phase: NodePhaseRunning, CurrentAttempt: 1}}, StateDir: state.RunDir(runID), CreatedAt: now, UpdatedAt: now}
	node := NodeSnapshot{ProtocolVersion: protocolVersion, StateSchemaVersion: 1, RunID: runID, ID: "work", Type: "agent", Phase: NodePhaseRunning, CurrentAttempt: 1, CreatedAt: now, UpdatedAt: now}
	document := workflow.Document{APIVersion: workflow.APIVersion, Name: run.WorkflowName, Defaults: workflow.Defaults{Backend: "fake", Tool: "codex", Runtime: "local"}, Execution: workflow.Execution{MaxConcurrency: 1}, Nodes: map[string]workflow.Node{"work": {Type: "agent", Task: "historical task"}}}
	normalized := workflow.Normalized{Document: document, Inputs: map[string]any{}, TopologicalOrder: []string{"work"}}
	if err := state.InitWorkflowRun(runID); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteSnapshot(runID, run); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteNode(runID, "work", node); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteWorkflow(runID, normalized); err != nil {
		t.Fatal(err)
	}
	v1Attempt := json.RawMessage(`{"protocolVersion":2,"stateSchemaVersion":1,"runId":"run-m54-v1-resume","nodeId":"work","number":1,"phase":"running","backend":"fake","launchState":"handle_persisted","execution":{"backend":"fake","schemaVersion":1,"id":"historical-handle","data":{}},"contextCompilerVersion":"context-compiler/v1","contextManifest":{"compilerVersion":"context-compiler/v1","components":[{"name":"node-task","source":"workflow.node.task","version":"v1"}]},"contextHash":"historical-v1-hash","promptHash":"historical-v1-hash","startedAt":"2026-08-19T00:00:00Z","updatedAt":"2026-08-19T00:00:00Z"}`)
	if err := state.WriteAttempt(runID, "work", 1, v1Attempt); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(state.AttemptPath(runID, "work", 1))
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(&fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "succeeded", Summary: "resumed"}}}, state)
	if _, err := service.Resume(context.Background(), ResumeRequest{RunID: runID}); err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, service, runID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("resumed final=%+v", final)
	}
	var attempt AttemptSnapshot
	if err := state.ReadAttempt(runID, "work", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	if attempt.ContextCompilerVersion != "context-compiler/v1" || attempt.ContextCompilerVersionV2 != "" || attempt.ContextManifestV2 != nil || attempt.ContextHash != "historical-v1-hash" {
		t.Fatalf("resume rewrote v1 context metadata=%+v", attempt)
	}
	if !strings.Contains(string(before), "context-compiler/v1") {
		t.Fatal("fixture did not contain v1 metadata")
	}
}

func TestM55IncludedMemoryIsConsumedOnceAndOmittedMemoryIsNot(t *testing.T) {
	project := t.TempDir()
	state := store.New(t.TempDir())
	included, err := state.CreateMemory(store.MemoryCreateInput{Project: project, MutationID: "memory-included", Type: contextcompiler.MemoryProcedure, Content: "use the existing error handler", Sensitivity: contextcompiler.SensitivityProject, Writer: contextcompiler.MemoryWriterHostAgent, Reason: "known project convention", MaxUses: 2})
	if err != nil {
		t.Fatal(err)
	}
	omitted, err := state.CreateMemory(store.MemoryCreateInput{Project: project, MutationID: "memory-omitted", Type: contextcompiler.MemoryFact, Content: strings.Repeat("optional ", 1125), Sensitivity: contextcompiler.SensitivityProject, Writer: contextcompiler.MemoryWriterHostAgent, Reason: "optional background", MaxUses: 2})
	if err != nil {
		t.Fatal(err)
	}
	backendFixture := &fakeWorkflowBackend{waitResults: []backend.BackendResult{{Status: "succeeded", Summary: "done"}}}
	service := NewService(backendFixture, state)
	content := "apiVersion: fishyume/v2\nname: m5-5-memory\nexecution: {maxConcurrency: 1}\nnodes:\n  work: {type: agent, task: do work}\n"
	started, err := service.StartWorkflow(context.Background(), StartWorkflowRequest{Project: project, Filename: "workflow.yaml", Content: content, ContextBindings: workflow.ContextBindings{MemoryByNode: map[string][]workflow.MemoryBinding{"work": {{ID: included.RecordID, Reason: "known project convention"}, {ID: omitted.RecordID, Reason: "optional background"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	final := waitForRun(t, service, started.ID, func(snapshot WorkflowSnapshot) bool { return snapshot.Phase == PhaseCompleted })
	if final.Conclusion != ConclusionSucceeded {
		t.Fatalf("final=%+v", final)
	}
	waitForControllers(t, service)
	var attempt AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "work", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	gotIncluded, _, err := state.GetMemory(project, included.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	gotOmitted, _, err := state.GetMemory(project, omitted.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, component := range attempt.ContextManifestV2.Components {
		if component.Kind == contextcompiler.KindMemory {
			used[component.ID] = true
		}
	}
	if (gotIncluded.UseCount == 1) != used[included.RecordID] || (gotOmitted.UseCount == 1) != used[omitted.RecordID] {
		t.Fatalf("Memory usage does not match manifest: included=%d omitted=%d used=%v", gotIncluded.UseCount, gotOmitted.UseCount, used)
	}
}
