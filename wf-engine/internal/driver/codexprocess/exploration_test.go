package codexprocess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wf.local/wf-engine/internal/explorationdriver"
)

func newExplorationFixture(t *testing.T) (*ExplorationAdapter, Config, string) {
	t.Helper()
	agent, supervisor := fixtureBinaries(t)
	workspace := filepath.Join(t.TempDir(), "workspace with spaces")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	config := Config{
		StateRoot:            t.TempDir(),
		Executable:           agent,
		SupervisorExecutable: supervisor,
		Sandbox:              "workspace-write",
		PollInterval:         10 * time.Millisecond,
	}
	return NewExplorationAdapter(New(config)), config, workspace
}

func explorationRequest(workspace, scenario string) explorationdriver.StartRequest {
	return explorationdriver.StartRequest{
		ProtocolVersion: explorationdriver.ProtocolVersion,
		Identity: explorationdriver.ExecutionIdentity{
			TeamID:        "team-1",
			ParticipantID: "participant-1",
			TurnID:        "turn-1",
		},
		Workspace: workspace,
		Target:    "local",
		ModelID:   "codex/local/gpt-5.6-luna",
		Prompt:    "scenario:" + scenario + "\nfixture Team task",
		Sandbox:   explorationdriver.SandboxReadOnly,
		ResultContract: explorationdriver.ResultContract{
			MaxBytes: 32 * 1024,
		},
	}
}

func startExploration(t *testing.T, adapter *ExplorationAdapter, request explorationdriver.StartRequest) explorationdriver.ExecutionHandle {
	t.Helper()
	handle, err := adapter.Start(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		data, _, decodeErr := adapter.decodeHandle(*handle)
		if decodeErr != nil {
			t.Errorf("decode exploration handle for cleanup: %v", decodeErr)
			return
		}
		matched := false
		for _, ref := range []processRef{data.Child, data.Supervisor} {
			status, inspectErr := inspectProcessRef(ref)
			if inspectErr != nil {
				t.Errorf("inspect exploration process for cleanup: %v", inspectErr)
				return
			}
			matched = matched || status == processMatched
		}
		if !matched {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, cancelErr := adapter.Cancel(ctx, *handle)
		if cancelErr != nil || result == nil || result.State != explorationdriver.CancelConfirmed {
			t.Errorf("exploration cleanup was not confirmed: result=%+v err=%v", result, cancelErr)
		}
	})
	return *handle
}

func awaitExploration(t *testing.T, adapter *ExplorationAdapter, handle explorationdriver.ExecutionHandle, state explorationdriver.ObservationState) *explorationdriver.Observation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		observation, err := adapter.Observe(context.Background(), handle)
		if err != nil {
			t.Fatal(err)
		}
		if observation.State == state {
			return observation
		}
		if time.Now().After(deadline) {
			t.Fatalf("exploration observation did not reach %q: %+v", state, observation)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestExplorationAdapterUsesTeamArtifactsAndRecoversOutput(t *testing.T) {
	adapter, config, workspace := newExplorationFixture(t)
	sentinel := filepath.Join(workspace, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	handle := startExploration(t, adapter, explorationRequest(workspace, "team-contribution"))

	data, _, err := adapter.decodeHandle(handle)
	if err != nil {
		t.Fatal(err)
	}
	wantArtifactDir := filepath.Join("teams", "team-1", "turns", "turn-1", "executions", "1")
	if filepath.Clean(filepath.FromSlash(data.ArtifactDir)) != filepath.Clean(wantArtifactDir) || strings.Contains(data.ArtifactDir, "runs/") {
		t.Fatalf("artifact dir = %q, want %q under teams", data.ArtifactDir, filepath.ToSlash(wantArtifactDir))
	}
	awaitExploration(t, adapter, handle, explorationdriver.ObservationTerminal)
	recovered := NewExplorationAdapter(New(config))
	observation, err := recovered.Observe(context.Background(), handle)
	if err != nil || observation.State != explorationdriver.ObservationTerminal {
		t.Fatalf("recovered observation=%+v err=%v", observation, err)
	}
	output, err := recovered.Output(context.Background(), handle, 32*1024)
	if err != nil || !strings.Contains(output, `"contentMarkdown":"fixture contribution from gpt-5.6-luna"`) {
		t.Fatalf("recovered output=%q err=%v", output, err)
	}
	content, err := os.ReadFile(sentinel)
	if err != nil || string(content) != "unchanged" {
		t.Fatalf("workspace sentinel changed: content=%q err=%v", content, err)
	}
	if _, err := os.Stat(filepath.Join(config.StateRoot, "runs")); !os.IsNotExist(err) {
		t.Fatalf("Team execution created a Workflow Run namespace: %v", err)
	}
}

func TestExplorationAdapterRejectsInvalidContributions(t *testing.T) {
	for _, scenario := range []string{"team-malformed", "team-unknown-field"} {
		t.Run(scenario, func(t *testing.T) {
			adapter, _, workspace := newExplorationFixture(t)
			handle := startExploration(t, adapter, explorationRequest(workspace, scenario))
			awaitExploration(t, adapter, handle, explorationdriver.ObservationTerminal)
			if output, err := adapter.Output(context.Background(), handle, 32*1024); err == nil {
				t.Fatalf("invalid contribution was accepted: %s", output)
			}
		})
	}
}

func TestExplorationAdapterConfirmsCancellation(t *testing.T) {
	adapter, _, workspace := newExplorationFixture(t)
	handle := startExploration(t, adapter, explorationRequest(workspace, "active"))
	awaitExploration(t, adapter, handle, explorationdriver.ObservationActive)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := adapter.Cancel(ctx, handle)
	if err != nil || result.State != explorationdriver.CancelConfirmed {
		t.Fatalf("cancel result=%+v err=%v", result, err)
	}
}

func TestExplorationAdapterRejectsTamperedHandle(t *testing.T) {
	adapter, _, workspace := newExplorationFixture(t)
	handle := startExploration(t, adapter, explorationRequest(workspace, "active"))
	var data explorationHandleData
	if err := json.Unmarshal(handle.Data, &data); err != nil {
		t.Fatal(err)
	}

	t.Run("path traversal", func(t *testing.T) {
		tampered := handle
		copyData := data
		copyData.ArtifactDir = "../outside"
		tampered.Data, _ = json.Marshal(copyData)
		if _, _, err := adapter.decodeHandle(tampered); err == nil {
			t.Fatal("path traversal handle was accepted")
		}
	})
	t.Run("artifact identity", func(t *testing.T) {
		tampered := handle
		copyData := data
		copyData.Identity.TeamID = "team-2"
		tampered.Data, _ = json.Marshal(copyData)
		if _, _, err := adapter.decodeHandle(tampered); err == nil {
			t.Fatal("mismatched Team artifact identity was accepted")
		}
	})
	t.Run("unknown field", func(t *testing.T) {
		var value map[string]any
		if err := json.Unmarshal(handle.Data, &value); err != nil {
			t.Fatal(err)
		}
		value["unexpected"] = true
		tampered := handle
		tampered.Data, _ = json.Marshal(value)
		if _, _, err := adapter.decodeHandle(tampered); err == nil {
			t.Fatal("unknown handle field was accepted")
		}
	})
}

func TestBoundedExplorationDiagnosticPreservesUTF8(t *testing.T) {
	value := strings.Repeat("界", explorationdriver.MaxDiagnosticBytes)
	got := boundedExplorationDiagnostic(value)
	if len([]byte(got)) > explorationdriver.MaxDiagnosticBytes || !strings.HasPrefix(value, got) {
		t.Fatalf("diagnostic truncation is invalid: bytes=%d", len([]byte(got)))
	}
}

var _ explorationdriver.Driver = (*ExplorationAdapter)(nil)
