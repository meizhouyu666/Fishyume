package integration_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/backend/ccpanes"
	"wf.local/wf-engine/internal/backend/directcli"
	"wf.local/wf-engine/internal/run"
	"wf.local/wf-engine/internal/store"
)

const backendWorkflow = `apiVersion: wf/v1
name: backend-workflow-integration
inputs:
  goal: {required: true}
defaults:
  tool: codex
  runtime: local
execution:
  maxConcurrency: 1
nodes:
  plan:
    type: agent
    task: "Plan {{ inputs.goal }}"
  approve:
    type: approval
    dependsOn: [plan]
    prompt: "Approve {{ nodes.plan.result.summary }}"
  implement:
    type: agent
    dependsOn: [approve]
    when: {node: approve, field: result.decision, equals: approved}
    task: "Implement {{ nodes.plan.result.summary }}"
`

type recordingBackend struct {
	delegate backend.AgentBackend
	mu       sync.Mutex
	starts   []backend.AgentExecutionSpec
}

func (b *recordingBackend) Name() string { return b.delegate.Name() }

func (b *recordingBackend) Capabilities() backend.Capabilities {
	return b.delegate.Capabilities()
}

func (b *recordingBackend) Doctor(ctx context.Context, request backend.DoctorRequest) backend.DoctorReport {
	return b.delegate.Doctor(ctx, request)
}

func (b *recordingBackend) Start(ctx context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	copy := spec
	copy.RequiredSkills = append([]string(nil), spec.RequiredSkills...)
	copy.ResultContract.Schema = append(json.RawMessage(nil), spec.ResultContract.Schema...)
	b.mu.Lock()
	b.starts = append(b.starts, copy)
	b.mu.Unlock()
	return b.delegate.Start(ctx, spec)
}

func (b *recordingBackend) Observe(ctx context.Context, handle backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	return b.delegate.Observe(ctx, handle)
}

func (b *recordingBackend) Output(ctx context.Context, handle backend.ExecutionHandle, lines int) (string, error) {
	return b.delegate.Output(ctx, handle, lines)
}

func (b *recordingBackend) Cancel(ctx context.Context, handle backend.ExecutionHandle) (*backend.CancelResult, error) {
	return b.delegate.Cancel(ctx, handle)
}

func (b *recordingBackend) startSpecs() []backend.AgentExecutionSpec {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]backend.AgentExecutionSpec(nil), b.starts...)
}

func TestAgentApprovalAgentWorkflowMatchesAcrossBackends(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("Direct Backend process recovery is currently supported on Windows and Linux")
	}
	moduleRoot := moduleDirectory(t)
	fixtureDir := t.TempDir()
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	ccpanesCTL := filepath.Join(fixtureDir, "fake-cc-panes-ctl"+extension)
	directAgent := filepath.Join(fixtureDir, "fake-codex"+extension)
	directSupervisor := filepath.Join(fixtureDir, "fishyume-engine"+extension)
	buildFixture(t, moduleRoot, ccpanesCTL, "./internal/backend/ccpanes/testdata/fake-cc-panes-ctl")
	buildFixture(t, moduleRoot, directAgent, "./internal/backend/directcli/testdata/fake-agent")
	buildFixture(t, moduleRoot, directSupervisor, "./cmd/wf-engine")

	tests := []struct {
		name string
		make func(*testing.T, string, string) backend.AgentBackend
	}{
		{
			name: "ccpanes",
			make: func(t *testing.T, _, workspace string) backend.AgentBackend {
				t.Setenv(ccpanes.ProfileIDEnv, "fishyume-integration-profile")
				t.Setenv("WF_FAKE_PROJECT", workspace)
				legacy := ccpanes.NewWithClient(ccpanes.NewClient(ccpanesCTL))
				return ccpanes.NewAdapterWithBackend(legacy)
			},
		},
		{
			name: "direct",
			make: func(_ *testing.T, stateRoot, _ string) backend.AgentBackend {
				return directcli.New(directcli.Config{
					StateRoot:            stateRoot,
					Executable:           directAgent,
					SupervisorExecutable: directSupervisor,
					Sandbox:              "read-only",
					PollInterval:         10 * time.Millisecond,
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateRoot := t.TempDir()
			workspace := t.TempDir()
			state := store.New(stateRoot)
			recorded := &recordingBackend{delegate: test.make(t, stateRoot, workspace)}

			first := run.NewService(recorded, state)
			started, err := first.StartWorkflow(context.Background(), run.StartWorkflowRequest{
				Project: workspace, Backend: test.name, Filename: "workflow.yaml", Content: backendWorkflow,
				Inputs: map[string]any{"goal": "M2.1.2"},
			})
			if err != nil {
				t.Fatal(err)
			}
			waiting := waitForRun(t, first, started.ID, func(value run.WorkflowSnapshot) bool {
				return value.Phase == run.PhaseWaiting && value.Reason == run.ReasonApprovalRequired
			})
			waitForControllers(t, first)
			if waiting.Backend != test.name {
				t.Fatalf("waiting run Backend = %q, want %q", waiting.Backend, test.name)
			}
			if starts := recorded.startSpecs(); len(starts) != 1 || starts[0].NodeID != "plan" {
				t.Fatalf("starts before approval = %+v, want exactly plan", starts)
			}

			second := run.NewService(recorded, state)
			if _, err := second.Resume(context.Background(), run.ResumeRequest{
				RunID:  started.ID,
				Action: &run.ResumeAction{Type: "approve", NodeID: "approve"},
			}); err != nil {
				t.Fatal(err)
			}
			completed := waitForRun(t, second, started.ID, func(value run.WorkflowSnapshot) bool {
				return value.Phase == run.PhaseCompleted
			})
			waitForControllers(t, second)
			if completed.Conclusion != run.ConclusionSucceeded || completed.Backend != test.name {
				t.Fatalf("completed run = %+v", completed)
			}

			var planNode run.NodeSnapshot
			if err := state.ReadNode(started.ID, "plan", &planNode); err != nil {
				t.Fatal(err)
			}
			if planNode.Result == nil || strings.TrimSpace(planNode.Result.Summary) == "" {
				t.Fatalf("plan result = %+v", planNode.Result)
			}
			starts := recorded.startSpecs()
			if len(starts) != 2 || starts[0].NodeID != "plan" || starts[1].NodeID != "implement" {
				t.Fatalf("Backend Start calls = %+v, want plan then implement exactly once", starts)
			}
			if !strings.Contains(starts[1].Instructions, planNode.Result.Summary) {
				t.Fatalf("implement prompt %q does not contain plan summary %q", starts[1].Instructions, planNode.Result.Summary)
			}

			attempts := make([]run.AttemptSnapshot, 0, 2)
			for _, nodeID := range []string{"plan", "implement"} {
				var attempt run.AttemptSnapshot
				if err := state.ReadAttempt(started.ID, nodeID, 1, &attempt); err != nil {
					t.Fatal(err)
				}
				if attempt.Backend != test.name || attempt.Conclusion != run.ConclusionSucceeded || !attempt.ResultConsumed {
					t.Fatalf("%s attempt = %+v", nodeID, attempt)
				}
				attempts = append(attempts, attempt)
			}
			if test.name == "direct" {
				waitForDirectProcesses(t, state, attempts)
			}
		})
	}

	removeFixtureDirectory(t, fixtureDir)
}

func moduleDirectory(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func buildFixture(t *testing.T, moduleRoot, output, pkg string) {
	t.Helper()
	command := exec.Command("go", "build", "-o", output, pkg)
	command.Dir = moduleRoot
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, result)
	}
}

func waitForRun(t *testing.T, service *run.Service, runID string, accept func(run.WorkflowSnapshot) bool) run.WorkflowSnapshot {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		value, err := service.Get(runID)
		if err == nil && accept(value) {
			return value
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not reach expected state: value=%+v err=%v", runID, value, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForControllers(t *testing.T, service *run.Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := service.WaitControllers(ctx); err != nil {
		t.Fatalf("controllers did not stop: %v", err)
	}
}

func waitForDirectProcesses(t *testing.T, state *store.Store, attempts []run.AttemptSnapshot) {
	t.Helper()
	type process struct {
		PID int `json:"pid"`
	}
	type handle struct {
		Supervisor process `json:"supervisor"`
		Child      process `json:"child"`
	}
	deadline := time.Now().Add(10 * time.Second)
	for _, attempt := range attempts {
		if attempt.Execution == nil {
			t.Fatalf("Direct attempt has no execution handle: %+v", attempt)
		}
		var data handle
		if err := json.Unmarshal(attempt.Execution.Data, &data); err != nil {
			t.Fatal(err)
		}
		exitPath := filepath.Join(state.AttemptDir(attempt.RunID, attempt.NodeID, attempt.Number), "direct-exit.json")
		for {
			_, exitErr := os.Stat(exitPath)
			supervisorAlive, supervisorErr := processAlive(data.Supervisor.PID)
			childAlive, childErr := processAlive(data.Child.PID)
			if exitErr == nil && supervisorErr == nil && childErr == nil && !supervisorAlive && !childAlive {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("Direct processes did not exit: supervisor=%d alive=%t err=%v child=%d alive=%t err=%v exitErr=%v",
					data.Supervisor.PID, supervisorAlive, supervisorErr, data.Child.PID, childAlive, childErr, exitErr)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

func removeFixtureDirectory(t *testing.T, directory string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := os.RemoveAll(directory)
		if err == nil {
			if _, statErr := os.Stat(directory); os.IsNotExist(statErr) {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("remove fixture directory %s: %v", directory, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
