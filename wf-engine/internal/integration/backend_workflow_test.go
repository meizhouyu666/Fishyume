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
	"wf.local/wf-engine/internal/backend/directcli"
	"wf.local/wf-engine/internal/backend/driveradapter"
	"wf.local/wf-engine/internal/driver/codex"
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

const parallelBackendWorkflow = `apiVersion: wf/v1
name: parallel-backend-integration
defaults: {tool: codex, runtime: local}
execution: {maxConcurrency: 2}
nodes:
  a: {type: agent, task: a}
  b: {type: agent, task: b}
  approve: {type: approval, dependsOn: [a, b], prompt: approve}
  finish: {type: agent, dependsOn: [approve], task: finish}
`

const parallelCancelWorkflow = `apiVersion: wf/v1
name: parallel-cancel-integration
defaults: {tool: codex, runtime: local}
execution: {maxConcurrency: 2}
nodes:
  a:
    type: agent
    task: |-
      scenario:active
      a
  b:
    type: agent
    task: |-
      scenario:active
      b
`

type startBarrier struct {
	mu       sync.Mutex
	expected int
	count    int
	reached  chan struct{}
	once     sync.Once
}

func (b *startBarrier) wait(ctx context.Context) error {
	b.mu.Lock()
	b.count++
	if b.count == b.expected {
		b.once.Do(func() { close(b.reached) })
	}
	reached, shouldWait := b.reached, b.count <= b.expected
	b.mu.Unlock()
	if !shouldWait {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-reached:
		return nil
	}
}

type recordingBackend struct {
	delegate backend.AgentBackend
	mu       sync.Mutex
	starts   []backend.AgentExecutionSpec
	cancels  []backend.ExecutionHandle
	barrier  *startBarrier
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
	if b.barrier != nil {
		if err := b.barrier.wait(ctx); err != nil {
			return nil, err
		}
	}
	return b.delegate.Start(ctx, spec)
}

func (b *recordingBackend) Observe(ctx context.Context, handle backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	return b.delegate.Observe(ctx, handle)
}

func (b *recordingBackend) Output(ctx context.Context, handle backend.ExecutionHandle, lines int) (string, error) {
	return b.delegate.Output(ctx, handle, lines)
}

func (b *recordingBackend) Cancel(ctx context.Context, handle backend.ExecutionHandle) (*backend.CancelResult, error) {
	b.mu.Lock()
	b.cancels = append(b.cancels, handle)
	b.mu.Unlock()
	return b.delegate.Cancel(ctx, handle)
}

func (b *recordingBackend) startSpecs() []backend.AgentExecutionSpec {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]backend.AgentExecutionSpec(nil), b.starts...)
}

func (b *recordingBackend) cancelHandles() []backend.ExecutionHandle {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]backend.ExecutionHandle(nil), b.cancels...)
}

func TestCodexNeedsInputQuestionsSurviveServiceRestart(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("Codex process recovery is currently supported on Windows and Linux")
	}
	moduleRoot := moduleDirectory(t)
	fixtureDir := t.TempDir()
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	agentPath := filepath.Join(fixtureDir, "fake-codex"+extension)
	supervisorPath := filepath.Join(fixtureDir, "fishyume-engine"+extension)
	buildFixture(t, moduleRoot, agentPath, "./internal/backend/directcli/testdata/fake-agent")
	buildFixture(t, moduleRoot, supervisorPath, "./cmd/wf-engine")

	stateRoot, workspace := t.TempDir(), t.TempDir()
	state := store.New(stateRoot)
	candidate := driveradapter.New(codex.New(codex.Config{StateRoot: stateRoot, Executable: agentPath, SupervisorExecutable: supervisorPath, Sandbox: "read-only", PollInterval: 10 * time.Millisecond}))
	first := run.NewService(candidate, state)
	started, err := first.Start(context.Background(), run.StartRequest{Project: workspace, Driver: "codex", Target: "local", Task: "scenario:terminal-needs-input\nrequest approval"})
	if err != nil {
		t.Fatal(err)
	}
	waitForRun(t, first, started.ID, func(value run.WorkflowSnapshot) bool {
		return value.Phase == run.PhaseWaiting && value.Reason == run.ReasonAgentWaitingInput
	})
	waitForControllers(t, first)

	second := run.NewService(candidate, state)
	view, err := second.Status(started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Nodes) != 1 || view.Nodes[0].Result == nil || len(view.Nodes[0].Result.Questions) != 1 {
		t.Fatalf("status after restart=%+v", view)
	}
	question := view.Nodes[0].Result.Questions[0]
	if question.ID != "approval" || question.Prompt != "Proceed?" || !question.Required || strings.Join(question.Choices, ",") != "yes,no" {
		t.Fatalf("question=%+v", question)
	}
	var attempt run.AttemptSnapshot
	if err := state.ReadAttempt(started.ID, "agent-1", 1, &attempt); err != nil {
		t.Fatal(err)
	}
	if !attempt.ResultConsumed {
		t.Fatalf("needs_input attempt was not consumed: %+v", attempt)
	}
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
	directAgent := filepath.Join(fixtureDir, "fake-codex"+extension)
	directSupervisor := filepath.Join(fixtureDir, "fishyume-engine"+extension)
	buildFixture(t, moduleRoot, directAgent, "./internal/backend/directcli/testdata/fake-agent")
	buildFixture(t, moduleRoot, directSupervisor, "./cmd/wf-engine")

	tests := []struct {
		name string
		make func(*testing.T, string, string) backend.AgentBackend
	}{
		{name: "codex", make: func(_ *testing.T, stateRoot, _ string) backend.AgentBackend {
			return driveradapter.New(codex.New(codex.Config{StateRoot: stateRoot, Executable: directAgent, SupervisorExecutable: directSupervisor, Sandbox: "read-only", PollInterval: 10 * time.Millisecond}))
		}},
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
			if starts[1].Envelope == nil || starts[1].Envelope.Task != "Implement "+planNode.Result.Summary || len(starts[1].Envelope.Context.UpstreamResults) != 2 {
				t.Fatalf("implement envelope %+v does not contain deterministic ancestor context", starts[1].Envelope)
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

func TestParallelWorkflowMatchesAcrossBackends(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("Direct Backend process recovery is currently supported on Windows and Linux")
	}
	moduleRoot := moduleDirectory(t)
	fixtureDir := t.TempDir()
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	directAgent := filepath.Join(fixtureDir, "fake-codex"+extension)
	directSupervisor := filepath.Join(fixtureDir, "fishyume-engine"+extension)
	buildFixture(t, moduleRoot, directAgent, "./internal/backend/directcli/testdata/fake-agent")
	buildFixture(t, moduleRoot, directSupervisor, "./cmd/wf-engine")

	tests := []struct {
		name string
		make func(*testing.T, string, string) backend.AgentBackend
	}{
		{name: "codex", make: func(_ *testing.T, stateRoot, _ string) backend.AgentBackend {
			return driveradapter.New(codex.New(codex.Config{StateRoot: stateRoot, Executable: directAgent, SupervisorExecutable: directSupervisor, Sandbox: "read-only", PollInterval: 10 * time.Millisecond}))
		}},
		{name: "direct", make: func(_ *testing.T, stateRoot, _ string) backend.AgentBackend {
			return directcli.New(directcli.Config{StateRoot: stateRoot, Executable: directAgent, SupervisorExecutable: directSupervisor, Sandbox: "read-only", PollInterval: 10 * time.Millisecond})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateRoot, workspace := t.TempDir(), t.TempDir()
			state := store.New(stateRoot)
			barrier := &startBarrier{expected: 2, reached: make(chan struct{})}
			recorded := &recordingBackend{delegate: test.make(t, stateRoot, workspace), barrier: barrier}
			service := run.NewService(recorded, state)
			started, err := service.StartWorkflow(context.Background(), run.StartWorkflowRequest{Project: workspace, Backend: test.name, Content: parallelBackendWorkflow})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-barrier.reached:
			case <-time.After(5 * time.Second):
				t.Fatal("Backend Start calls did not overlap")
			}
			waiting := waitForRun(t, service, started.ID, func(value run.WorkflowSnapshot) bool {
				return value.Phase == run.PhaseWaiting && value.Reason == run.ReasonApprovalRequired
			})
			if waiting.EffectiveConcurrency != 2 {
				t.Fatalf("effective concurrency=%d", waiting.EffectiveConcurrency)
			}
			starts := recorded.startSpecs()
			if len(starts) != 2 || !sameNodeSet(starts, []string{"a", "b"}) {
				t.Fatalf("parallel starts=%+v", starts)
			}
			resumer := run.NewService(recorded, state)
			if _, err := resumer.Resume(context.Background(), run.ResumeRequest{RunID: started.ID, Action: &run.ResumeAction{Type: "approve", NodeID: "approve"}}); err != nil {
				t.Fatal(err)
			}
			final := waitForRun(t, resumer, started.ID, func(value run.WorkflowSnapshot) bool { return value.Phase == run.PhaseCompleted })
			if final.Conclusion != run.ConclusionSucceeded {
				t.Fatalf("final=%+v", final)
			}
			starts = recorded.startSpecs()
			if len(starts) != 3 || starts[2].NodeID != "finish" {
				t.Fatalf("all starts=%+v", starts)
			}
			if test.name == "direct" {
				attempts := readAttempts(t, state, started.ID, []string{"a", "b", "finish"})
				waitForDirectProcesses(t, state, attempts)
			}
		})
	}
	removeFixtureDirectory(t, fixtureDir)
}

func TestConcurrentCancelMatchesAcrossBackends(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("Direct Backend process recovery is currently supported on Windows and Linux")
	}
	moduleRoot := moduleDirectory(t)
	fixtureDir := t.TempDir()
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	directAgent := filepath.Join(fixtureDir, "fake-codex"+extension)
	directSupervisor := filepath.Join(fixtureDir, "fishyume-engine"+extension)
	buildFixture(t, moduleRoot, directAgent, "./internal/backend/directcli/testdata/fake-agent")
	buildFixture(t, moduleRoot, directSupervisor, "./cmd/wf-engine")
	tests := []struct {
		name string
		make func(*testing.T, string, string) backend.AgentBackend
	}{
		{name: "codex", make: func(_ *testing.T, stateRoot, _ string) backend.AgentBackend {
			return driveradapter.New(codex.New(codex.Config{StateRoot: stateRoot, Executable: directAgent, SupervisorExecutable: directSupervisor, Sandbox: "read-only", PollInterval: 10 * time.Millisecond}))
		}},
		{name: "direct", make: func(_ *testing.T, stateRoot, _ string) backend.AgentBackend {
			return directcli.New(directcli.Config{StateRoot: stateRoot, Executable: directAgent, SupervisorExecutable: directSupervisor, Sandbox: "read-only", PollInterval: 10 * time.Millisecond})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateRoot, workspace := t.TempDir(), t.TempDir()
			state := store.New(stateRoot)
			recorded := &recordingBackend{delegate: test.make(t, stateRoot, workspace)}
			service := run.NewService(recorded, state)
			started, err := service.StartWorkflow(context.Background(), run.StartWorkflowRequest{Project: workspace, Backend: test.name, Content: parallelCancelWorkflow})
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				view, viewErr := service.Status(started.ID)
				if viewErr == nil && len(view.ActiveAttempts) == 2 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("active Attempts not ready: view=%+v err=%v", view, viewErr)
				}
				time.Sleep(10 * time.Millisecond)
			}
			cancelled, err := service.Cancel(context.Background(), started.ID)
			if err != nil {
				t.Fatal(err)
			}
			if cancelled.Conclusion != run.ConclusionCancelled || len(recorded.cancelHandles()) != 2 {
				t.Fatalf("cancelled=%+v handles=%+v", cancelled, recorded.cancelHandles())
			}
			attempts := readAttempts(t, state, started.ID, []string{"a", "b"})
			for _, attempt := range attempts {
				if attempt.Conclusion != run.ConclusionCancelled {
					t.Fatalf("attempt=%+v", attempt)
				}
			}
			if test.name == "direct" {
				waitForDirectProcessesStopped(t, attempts)
			}
		})
	}
	removeFixtureDirectory(t, fixtureDir)
}

func sameNodeSet(starts []backend.AgentExecutionSpec, want []string) bool {
	set := map[string]bool{}
	for _, start := range starts {
		set[start.NodeID] = true
	}
	if len(set) != len(want) {
		return false
	}
	for _, nodeID := range want {
		if !set[nodeID] {
			return false
		}
	}
	return true
}

func readAttempts(t *testing.T, state *store.Store, runID string, nodeIDs []string) []run.AttemptSnapshot {
	t.Helper()
	attempts := make([]run.AttemptSnapshot, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		var attempt run.AttemptSnapshot
		if err := state.ReadAttempt(runID, nodeID, 1, &attempt); err != nil {
			t.Fatal(err)
		}
		attempts = append(attempts, attempt)
	}
	return attempts
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

func waitForDirectProcessesStopped(t *testing.T, attempts []run.AttemptSnapshot) {
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
		for {
			supervisorAlive, supervisorErr := processAlive(data.Supervisor.PID)
			childAlive, childErr := processAlive(data.Child.PID)
			if supervisorErr == nil && childErr == nil && !supervisorAlive && !childAlive {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("Direct cancelled processes did not exit: supervisor=%d alive=%t err=%v child=%d alive=%t err=%v", data.Supervisor.PID, supervisorAlive, supervisorErr, data.Child.PID, childAlive, childErr)
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
