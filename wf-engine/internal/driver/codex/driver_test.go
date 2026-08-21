package codex

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"wf.local/wf-engine/internal/agent"
)

func buildCodexFixtures(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	directory := t.TempDir()
	agentPath := filepath.Join(directory, "fake-codex"+extension)
	supervisorPath := filepath.Join(directory, "fishyume-engine"+extension)
	for output, target := range map[string]string{agentPath: "./internal/driver/codexprocess/testdata/fake-agent", supervisorPath: "./cmd/wf-engine"} {
		command := exec.Command("go", "build", "-o", output, target)
		command.Dir = root
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v: %s", target, err, data)
		}
	}
	return agentPath, supervisorPath
}

func newDriverFixture(t *testing.T) (*Driver, Config) {
	t.Helper()
	agentPath, supervisorPath := buildCodexFixtures(t)
	config := Config{StateRoot: t.TempDir(), Executable: agentPath, SupervisorExecutable: supervisorPath, Sandbox: "read-only", PollInterval: 10 * time.Millisecond}
	return New(config), config
}

func envelopeForScenario(t *testing.T, scenario string) agent.AttemptEnvelope {
	t.Helper()
	return agent.AttemptEnvelope{
		ProtocolVersion: agent.ProtocolVersion,
		Identity:        agent.AttemptIdentity{RunID: "run-driver-" + scenario, NodeID: "agent-1", Attempt: 1},
		Workspace:       t.TempDir(),
		Target:          "local",
		Task:            "driver contract fixture",
		Context:         agent.AttemptContext{UpstreamResults: []agent.UpstreamResult{}, RequiredSkills: []string{}},
		Constraints:     map[string]string{"interaction": "none"},
		Budget:          map[string]int64{},
		ResultContract:  agent.ResultContract{Schema: json.RawMessage(`{"type":"object"}`), MaxBytes: agent.MaxResultBytes},
		Prompt:          "scenario:" + scenario + "\ndriver contract fixture",
	}
}

func awaitDriverObservation(t *testing.T, driver *Driver, handle agent.ExecutionHandle, terminal func(*agent.ExecutionObservation) bool) *agent.ExecutionObservation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		observation, err := driver.Observe(context.Background(), handle)
		if err != nil {
			t.Fatal(err)
		}
		if terminal(observation) {
			return observation
		}
		if time.Now().After(deadline) {
			t.Fatalf("observation did not settle: %+v", observation)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCodexDriverCapabilitiesAndResultNormalization(t *testing.T) {
	driver, _ := newDriverFixture(t)
	if driver.Name() != "codex" {
		t.Fatalf("name=%q", driver.Name())
	}
	if err := agent.ValidateCapabilities(driver.Capabilities()); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		scenario string
		status   string
		valid    bool
		event    agent.DriverEventType
	}{
		{scenario: "terminal-succeeded", status: "succeeded", valid: true, event: agent.EventAttemptCompleted},
		{scenario: "terminal-failed", status: "failed", valid: true, event: agent.EventAttemptCompleted},
		{scenario: "terminal-needs-input", status: "needs_input", valid: true, event: agent.EventAttemptNeedsInput},
		{scenario: "malformed-result", status: "invalid_result", valid: false, event: agent.EventAttemptCompleted},
	}
	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			handle, err := driver.Start(context.Background(), envelopeForScenario(t, test.scenario))
			if err != nil {
				t.Fatal(err)
			}
			if handle.Driver != "codex" || handle.Target != "local" || handle.SchemaVersion < 1 {
				t.Fatalf("handle=%+v", handle)
			}
			observation := awaitDriverObservation(t, driver, *handle, func(value *agent.ExecutionObservation) bool { return value.State == agent.ObservationTerminal })
			if observation.Result == nil || observation.Result.Status != test.status || len(observation.Events) != 1 || observation.Events[0].Type != test.event {
				t.Fatalf("observation=%+v", observation)
			}
			validationErr := agent.ValidateObservation(*observation)
			if test.valid && validationErr != nil {
				t.Fatal(validationErr)
			}
			if !test.valid && (validationErr == nil || !strings.Contains(validationErr.Error(), "unsupported Agent result status")) {
				t.Fatalf("validation error=%v", validationErr)
			}
		})
	}
}

func TestCodexDriverRecoveryAndConfirmedCancel(t *testing.T) {
	driver, config := newDriverFixture(t)
	handle, err := driver.Start(context.Background(), envelopeForScenario(t, "active"))
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(config)
	observation, err := restarted.Observe(context.Background(), *handle)
	if err != nil || observation.State != agent.ObservationActive {
		t.Fatalf("recovered observation=%+v err=%v", observation, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := restarted.Cancel(ctx, *handle)
	if err != nil || result == nil || result.State != agent.CancelConfirmed {
		t.Fatalf("cancel=%+v err=%v", result, err)
	}
}
