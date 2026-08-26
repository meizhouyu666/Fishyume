package harnesssession

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"wf.local/wf-engine/internal/explorationdriver"
	"wf.local/wf-engine/internal/routing"
	"wf.local/wf-engine/internal/sessiondriver"
	"wf.local/wf-engine/internal/teamcontract"
)

func TestClaudeAndOpenCodeSessionLifecycle(t *testing.T) {
	harness := buildHelper(t, "./testdata/fake-harness")
	supervisor := buildHelper(t, "./testdata/fake-supervisor")
	for _, name := range []string{"claude", "opencode"} {
		t.Run(name, func(t *testing.T) {
			driver := testDriver(t, name, harness, supervisor)
			request := sessiondriver.StartSessionRequest{ProtocolVersion: sessiondriver.ProtocolVersion, Identity: sessiondriver.SessionIdentity{TeamID: "team-1", ParticipantID: "participant-1", Generation: 1}, Workspace: t.TempDir(), Target: "profile", ModelID: name + "/profile/model", Sandbox: sessiondriver.SandboxReadOnly}
			session, err := driver.StartSession(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			started, err := driver.StartTurn(context.Background(), *session, sessiondriver.StartTurnRequest{ProtocolVersion: sessiondriver.ProtocolVersion, Identity: sessiondriver.TurnIdentity{TurnID: "turn-1", ExpectedSessionGeneration: 1}, Prompt: "answer", MaxOutputBytes: 4096})
			if err != nil {
				t.Fatal(err)
			}
			recovered, err := newDriver(name, name, driver.config)
			if err != nil {
				t.Fatal(err)
			}
			observed := waitTerminal(t, recovered, started.Session, started.Turn)
			if observed.State != sessiondriver.TurnResponded {
				t.Fatalf("observation=%+v", observed)
			}
			var contribution teamcontract.ContributionV1
			if err := json.Unmarshal([]byte(observed.Output), &contribution); err != nil || !strings.Contains(contribution.ContentMarkdown, "answer") {
				t.Fatalf("output=%s err=%v", observed.Output, err)
			}
			parked, err := recovered.ParkSession(context.Background(), observed.Session)
			if err != nil {
				t.Fatal(err)
			}
			resumed, err := recovered.ResumeSession(context.Background(), *parked)
			if err != nil {
				t.Fatal(err)
			}
			second, err := recovered.StartTurn(context.Background(), *resumed, sessiondriver.StartTurnRequest{ProtocolVersion: sessiondriver.ProtocolVersion, Identity: sessiondriver.TurnIdentity{TurnID: "turn-2", ExpectedSessionGeneration: 1}, Prompt: "BLOCK", MaxOutputBytes: 4096})
			if err != nil {
				t.Fatal(err)
			}
			cancelled, err := recovered.CancelTurn(context.Background(), second.Session, second.Turn)
			if err != nil {
				t.Fatal(err)
			}
			if cancelled.State != sessiondriver.CancelConfirmed {
				t.Fatalf("cancel=%+v", cancelled)
			}
		})
	}
}

func TestExplorationAdapterUsesTheSameRecoverableHarness(t *testing.T) {
	harness := buildHelper(t, "./testdata/fake-harness")
	supervisor := buildHelper(t, "./testdata/fake-supervisor")
	driver := testDriver(t, "opencode", harness, supervisor)
	adapter := driver.Exploration()
	handle, err := adapter.Start(context.Background(), explorationdriver.StartRequest{ProtocolVersion: explorationdriver.ProtocolVersion, Identity: explorationdriver.ExecutionIdentity{TeamID: "team-1", ParticipantID: "participant-1", TurnID: "turn-panel"}, Workspace: t.TempDir(), Target: "profile", ModelID: "opencode/profile/model", Prompt: "compare", Sandbox: explorationdriver.SandboxReadOnly, ResultContract: explorationdriver.ResultContract{MaxBytes: 4096}})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := newDriver("opencode", "opencode", driver.config)
	if err != nil {
		t.Fatal(err)
	}
	recoveredAdapter := recovered.Exploration()
	deadline := time.Now().Add(10 * time.Second)
	for {
		observed, err := recoveredAdapter.Observe(context.Background(), *handle)
		if err != nil {
			t.Fatal(err)
		}
		if observed.State == explorationdriver.ObservationTerminal {
			break
		}
		if observed.State == explorationdriver.ObservationLost || time.Now().After(deadline) {
			record, _ := recovered.read(handle.ID)
			dir, _ := recovered.turnDir(record)
			entries, _ := os.ReadDir(dir)
			exit, _ := os.ReadFile(filepath.Join(dir, "exit.json"))
			t.Logf("record=%+v turnDir=%s entries=%v exit=%s", record, dir, entries, exit)
			t.Fatalf("exploration observation=%+v", observed)
		}
		time.Sleep(10 * time.Millisecond)
	}
	output, err := recoveredAdapter.Output(context.Background(), *handle, 4096)
	if err != nil || !strings.Contains(output, "fake OpenCode answer") {
		t.Fatalf("output=%s err=%v", output, err)
	}
}

func TestHarnessPoliciesAreReadOnlyAndMachineReadable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	claude := testDriver(t, "claude", exe, exe)
	opencode := testDriver(t, "opencode", exe, exe)
	base := record{HandleID: "session-1", ExternalID: "11111111-1111-4111-8111-111111111111", Model: "model"}
	claudeArgs, _ := claude.command(base)
	joined := strings.Join(claudeArgs, " ")
	for _, required := range []string{"--safe-mode", "--output-format json", "--tools Read,Glob,Grep", "--permission-mode dontAsk", "--strict-mcp-config"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("Claude args omit %q: %s", required, joined)
		}
	}
	_, env := opencode.command(base)
	if len(env) != 1 || !strings.Contains(env[0], `"*":"deny"`) || !strings.Contains(env[0], `"read":"allow"`) {
		t.Fatalf("OpenCode policy=%v", env)
	}
}

func testDriver(t *testing.T, name, executable, supervisor string) *Driver {
	t.Helper()
	catalog := routing.CapabilityCatalogV1{SchemaVersion: routing.CapabilityCatalogV1Version, PolicyVersion: routing.RoutingPolicyV1Version, Models: []routing.ModelCapabilityV1{{ID: name + "/profile/model", Target: routing.Target{Driver: name, Provider: "profile", Model: "provider/model"}, Capabilities: []routing.Capability{routing.CapabilityRepoRead}, ContextLimitBytes: 128 * 1024, MaxOutputBytes: 32 * 1024, Quality: routing.QualityBalanced, Cost: routing.CostLow, Latency: routing.LatencyFast, SupportsCancellation: true}}}
	config := Config{StateRoot: t.TempDir(), Executable: executable, SupervisorExecutable: supervisor, Catalog: catalog, PollInterval: 10 * time.Millisecond, StartupTimeout: 10 * time.Second}
	var driver *Driver
	var err error
	if name == "claude" {
		driver, err = NewClaude(config)
	} else {
		driver, err = NewOpenCode(config)
	}
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

func waitTerminal(t *testing.T, driver *Driver, session sessiondriver.SessionHandle, turn sessiondriver.TurnHandle) sessiondriver.TurnObservation {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		observed, err := driver.ObserveTurn(context.Background(), session, turn)
		if err != nil {
			t.Fatal(err)
		}
		session, turn = observed.Session, observed.Turn
		if observed.State != sessiondriver.TurnActive && observed.State != sessiondriver.TurnDispatching {
			return *observed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("turn did not finish")
	return sessiondriver.TurnObservation{}
}

func buildHelper(t *testing.T, source string) string {
	t.Helper()
	name := filepath.Base(source)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output := filepath.Join(t.TempDir(), name)
	command := exec.Command("go", "build", "-o", output, source)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, data)
	}
	return output
}
