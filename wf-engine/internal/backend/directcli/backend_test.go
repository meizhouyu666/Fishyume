package directcli

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
	"wf.local/wf-engine/internal/backend/contracttest"
)

var buildFixtures struct {
	sync.Once
	directory  string
	agent      string
	supervisor string
	err        error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if buildFixtures.directory != "" {
		_ = os.RemoveAll(buildFixtures.directory)
	}
	os.Exit(code)
}

func fixtureBinaries(t *testing.T) (string, string) {
	t.Helper()
	buildFixtures.Do(func() {
		buildFixtures.directory, buildFixtures.err = os.MkdirTemp("", "fishyume-direct-fixtures-")
		if buildFixtures.err != nil {
			return
		}
		extension := ""
		if runtime.GOOS == "windows" {
			extension = ".exe"
		}
		buildFixtures.agent = filepath.Join(buildFixtures.directory, "fake-codex"+extension)
		buildFixtures.supervisor = filepath.Join(buildFixtures.directory, "fishyume-engine"+extension)
		for _, target := range []struct{ output, pkg string }{
			{buildFixtures.agent, "./testdata/fake-agent"},
			{buildFixtures.supervisor, "../../../cmd/wf-engine"},
		} {
			command := exec.Command("go", "build", "-o", target.output, target.pkg)
			if output, err := command.CombinedOutput(); err != nil {
				buildFixtures.err = &fixtureBuildError{err: err, output: string(output)}
				return
			}
		}
	})
	if buildFixtures.err != nil {
		t.Fatal(buildFixtures.err)
	}
	return buildFixtures.agent, buildFixtures.supervisor
}

type fixtureBuildError struct {
	err    error
	output string
}

func (e *fixtureBuildError) Error() string { return e.err.Error() + ": " + e.output }

type scenarioBackend struct {
	t        *testing.T
	scenario contracttest.Scenario
	delegate *Backend
}

func (b *scenarioBackend) Name() string                       { return b.delegate.Name() }
func (b *scenarioBackend) Capabilities() backend.Capabilities { return b.delegate.Capabilities() }
func (b *scenarioBackend) Doctor(ctx context.Context, request backend.DoctorRequest) backend.DoctorReport {
	return b.delegate.Doctor(ctx, request)
}
func (b *scenarioBackend) Start(ctx context.Context, spec backend.AgentExecutionSpec) (*backend.ExecutionHandle, error) {
	spec.Instructions = "scenario:" + string(b.scenario) + "\n" + spec.Instructions
	handle, err := b.delegate.Start(ctx, spec)
	if handle == nil || err != nil {
		return handle, err
	}
	original := *handle
	original.Data = append(json.RawMessage(nil), handle.Data...)
	b.t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = b.delegate.Cancel(cleanupCtx, original)
	})
	if b.scenario == contracttest.ScenarioLost || b.scenario == contracttest.ScenarioCancelNotConfirmed {
		var data handleData
		if err := json.Unmarshal(handle.Data, &data); err != nil {
			return nil, err
		}
		data.Supervisor.Fingerprint += "-reused"
		data.Child.Fingerprint += "-reused"
		handle.Data, err = json.Marshal(data)
		if err != nil {
			return nil, err
		}
	}
	return handle, nil
}
func (b *scenarioBackend) Observe(ctx context.Context, handle backend.ExecutionHandle) (*backend.ExecutionObservation, error) {
	return b.delegate.Observe(ctx, handle)
}
func (b *scenarioBackend) Output(ctx context.Context, handle backend.ExecutionHandle, lines int) (string, error) {
	return b.delegate.Output(ctx, handle, lines)
}
func (b *scenarioBackend) Cancel(ctx context.Context, handle backend.ExecutionHandle) (*backend.CancelResult, error) {
	return b.delegate.Cancel(ctx, handle)
}

func TestDirectBackendContract(t *testing.T) {
	agent, supervisor := fixtureBinaries(t)
	contracttest.Run(t, func(t *testing.T, scenario contracttest.Scenario) contracttest.Fixture {
		stateRoot := t.TempDir()
		workspace := filepath.Join(t.TempDir(), "workspace with spaces 工作区")
		if err := os.MkdirAll(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		delegate := New(Config{StateRoot: stateRoot, Executable: agent, SupervisorExecutable: supervisor, Sandbox: "read-only", PollInterval: 10 * time.Millisecond})
		return contracttest.Fixture{
			Backend: &scenarioBackend{t: t, scenario: scenario, delegate: delegate},
			Spec:    backend.AgentExecutionSpec{RunID: "run-1", NodeID: "agent-1", Attempt: 1, Workspace: workspace, Tool: "codex", Runtime: "local", Instructions: "fixture task"},
		}
	})
}

func newFixtureBackend(t *testing.T, config ...func(*Config)) (*Backend, backend.AgentExecutionSpec) {
	t.Helper()
	agent, supervisor := fixtureBinaries(t)
	stateRoot := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "workspace with spaces 工作区")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	value := Config{StateRoot: stateRoot, Executable: agent, SupervisorExecutable: supervisor, Sandbox: "read-only", PollInterval: 10 * time.Millisecond}
	for _, update := range config {
		update(&value)
	}
	return New(value), backend.AgentExecutionSpec{RunID: "run-1", NodeID: "agent-1", Attempt: 1, Workspace: workspace, Tool: "codex", Runtime: "local", Instructions: "fixture task"}
}

func startScenario(t *testing.T, candidate *Backend, spec backend.AgentExecutionSpec, scenario string) backend.ExecutionHandle {
	t.Helper()
	spec.Instructions = "scenario:" + scenario + "\n" + spec.Instructions
	handle, err := candidate.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = candidate.Cancel(ctx, *handle)
	})
	return *handle
}

func awaitObservation(t *testing.T, candidate *Backend, handle backend.ExecutionHandle, accept func(*backend.ExecutionObservation) bool) *backend.ExecutionObservation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		observation, err := candidate.Observe(context.Background(), handle)
		if err != nil {
			t.Fatal(err)
		}
		if accept(observation) {
			return observation
		}
		if time.Now().After(deadline) {
			t.Fatalf("observation did not reach expected state: %+v", observation)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestDirectBackendRejectsMalformedAndMismatchedResults(t *testing.T) {
	for _, scenario := range []string{"malformed-result", "wrong-identity", "conflict-result"} {
		t.Run(scenario, func(t *testing.T) {
			candidate, spec := newFixtureBackend(t)
			handle := startScenario(t, candidate, spec, scenario)
			observation := awaitObservation(t, candidate, handle, func(value *backend.ExecutionObservation) bool { return value.State == backend.ObservationTerminal })
			if err := backend.ValidateExecutionObservation(*observation); err == nil || observation.Diagnostic == "" {
				t.Fatalf("invalid result observation=%+v err=%v", observation, err)
			}
		})
	}
}

func TestDirectBackendRejectsOversizedResult(t *testing.T) {
	candidate, spec := newFixtureBackend(t)
	spec.ResultContract.MaxBytes = 1024
	handle := startScenario(t, candidate, spec, "oversized-result")
	observation := awaitObservation(t, candidate, handle, func(value *backend.ExecutionObservation) bool { return value.State == backend.ObservationTerminal })
	if err := backend.ValidateExecutionObservation(*observation); err == nil || !strings.Contains(observation.Diagnostic, "exceeds") {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
}

func TestDirectBackendDoesNotAcceptPrematureResult(t *testing.T) {
	candidate, spec := newFixtureBackend(t)
	handle := startScenario(t, candidate, spec, "premature-result")
	observation, err := candidate.Observe(context.Background(), handle)
	if err != nil || observation.State != backend.ObservationActive || observation.Result != nil {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
}

func TestDirectBackendDetectsResultChangesAfterExit(t *testing.T) {
	candidate, spec := newFixtureBackend(t)
	handle := startScenario(t, candidate, spec, "terminal-succeeded")
	awaitObservation(t, candidate, handle, func(value *backend.ExecutionObservation) bool {
		return value.State == backend.ObservationTerminal && value.Result != nil && value.Result.Status == "succeeded"
	})
	data, paths, err := candidate.decodeHandle(handle)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, statErr := os.Stat(paths.Exit); statErr == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Direct exit record was not persisted")
		}
		time.Sleep(10 * time.Millisecond)
	}
	changed := resultEnvelope{ExecutionID: data.ExecutionID, RunID: data.RunID, NodeID: data.NodeID, Attempt: data.Attempt,
		Result: backend.AgentResult{Status: "succeeded", Summary: "changed after exit"}}
	encoded, _ := json.Marshal(changed)
	if err := os.WriteFile(paths.Result, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	observation, err := candidate.Observe(context.Background(), handle)
	if err != nil || observation.State != backend.ObservationTerminal || observation.Diagnostic == "" || backend.ValidateExecutionObservation(*observation) == nil {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
}

func TestDirectBackendUsesExitCodeOnlyForConfirmedFailure(t *testing.T) {
	candidate, spec := newFixtureBackend(t)
	handle := startScenario(t, candidate, spec, "nonzero-missing")
	observation := awaitObservation(t, candidate, handle, func(value *backend.ExecutionObservation) bool { return value.State == backend.ObservationTerminal })
	if observation.Result == nil || observation.Result.Status != "failed" || !strings.Contains(observation.Result.Summary, "code 17") {
		t.Fatalf("observation=%+v", observation)
	}
}

func TestDirectBackendRecoversWithANewInstance(t *testing.T) {
	candidate, spec := newFixtureBackend(t)
	handle := startScenario(t, candidate, spec, "active")
	restarted := New(candidate.config)
	observation, err := restarted.Observe(context.Background(), handle)
	if err != nil || observation.State != backend.ObservationActive {
		t.Fatalf("restarted Observe=%+v err=%v", observation, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := restarted.Cancel(ctx, handle)
	if err != nil || result.State != backend.CancelConfirmed {
		t.Fatalf("restarted Cancel=%+v err=%v", result, err)
	}
}

func TestDirectBackendBoundsPersistedAndReturnedOutput(t *testing.T) {
	candidate, spec := newFixtureBackend(t, func(config *Config) { config.MaxEventBytes = 128 * 1024; config.MaxStderrBytes = 64 * 1024 })
	handle := startScenario(t, candidate, spec, "large-output")
	data, paths, err := candidate.decodeHandle(handle)
	if err != nil {
		t.Fatal(err)
	}
	_ = data
	deadline := time.Now().Add(10 * time.Second)
	for {
		info, statErr := os.Stat(paths.Events)
		if statErr == nil && info.Size() > 32*1024 {
			if info.Size() > candidate.config.MaxEventBytes {
				t.Fatalf("event log grew to %d bytes", info.Size())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("large output fixture did not write events: %v", statErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	output, err := candidate.Output(context.Background(), handle, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) > maxOutputBytes {
		t.Fatalf("Output returned %d bytes", len(output))
	}
}

func TestDirectBackendRejectsAttemptPathTraversal(t *testing.T) {
	candidate, spec := newFixtureBackend(t)
	handle := startScenario(t, candidate, spec, "active")
	var data handleData
	if err := json.Unmarshal(handle.Data, &data); err != nil {
		t.Fatal(err)
	}
	data.AttemptDir = "../../outside"
	handle.Data, _ = json.Marshal(data)
	if _, err := candidate.Observe(context.Background(), handle); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("path traversal error=%v", err)
	}
}

func TestDirectBackendDoesNotPersistFullPromptInHandleOrControlArtifacts(t *testing.T) {
	candidate, spec := newFixtureBackend(t)
	secret := "FISHYUME-SECRET-PROMPT-DO-NOT-PERSIST"
	spec.Instructions = "scenario:active\n" + secret
	handle, err := candidate.Start(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = candidate.Cancel(ctx, *handle)
	})
	if strings.Contains(string(handle.Data), secret) {
		t.Fatal("ExecutionHandle persisted the full prompt")
	}
	_, paths, err := candidate.decodeHandle(*handle)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Config, paths.Ready, paths.Schema, paths.Exit} {
		data, readErr := os.ReadFile(path)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(data), secret) {
			t.Fatalf("control artifact %s persisted the full prompt", path)
		}
	}
}

func TestDirectDoctorReportsUnsupportedCombination(t *testing.T) {
	candidate, spec := newFixtureBackend(t)
	report := candidate.Doctor(context.Background(), backend.DoctorRequest{Workspace: spec.Workspace, Tool: "claude", Runtime: "local"})
	if report.Ready || len(report.Diagnostics) == 0 || report.Diagnostics[0].Name != "tool" {
		t.Fatalf("report=%+v", report)
	}
}
