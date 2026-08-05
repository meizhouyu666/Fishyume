package ccpanes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wf.local/wf-engine/internal/backend"
	"wf.local/wf-engine/internal/backend/contracttest"
)

func TestCCPanesCtlFixture(t *testing.T) {
	if os.Getenv("WF_CTL_FIXTURE") != "1" {
		return
	}
	args := os.Args
	separator := 0
	for index, arg := range args {
		if arg == "--" {
			separator = index + 1
			break
		}
	}
	args = args[separator:]
	if logPath := os.Getenv("WF_CTL_LOG"); logPath != "" {
		file, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		_, _ = fmt.Fprintln(file, strings.Join(args, "\t"))
		_ = file.Close()
	}
	scenario := os.Getenv("WF_CTL_SCENARIO")
	command := strings.Join(args, " ")
	if scenario == "command-failure" && strings.Contains(command, "call launch_task") {
		os.Exit(7)
	}
	if scenario == "update-failure" && strings.Contains(command, "call update_task_binding") {
		os.Exit(8)
	}
	if scenario == "malformed" && strings.Contains(command, "wait_for_session") {
		fmt.Print("not-json")
		os.Exit(0)
	}
	switch {
	case strings.HasSuffix(command, "status"):
		fmt.Print(`{"instances":[{"instance":"release","orchestrator":{"lifecycle":"ready"},"daemon":{"lifecycle":"ready"}}]}`)
	case strings.Contains(command, "list_projects"):
		fmt.Print(`{"projects":[{"projectPath":"C:\\fixture-project"}]}`)
	case strings.Contains(command, "create_task_binding"):
		fmt.Print(`{"content":[{"type":"text","text":"{\"id\":\"binding-1\"}"}],"isError":false}`)
	case strings.Contains(command, "call launch_task"):
		fmt.Print(`{"launchId":"launch-1","sessionId":"session-1","resumeId":"resume-1","paneId":"pane-1"}`)
	case strings.Contains(command, "update_task_binding"):
		fmt.Print(`{"ok":true}`)
	case strings.Contains(command, "wait_for_session"):
		state := "idle"
		satisfied := true
		switch scenario {
		case "waiting-input":
			state = "waitingInput"
		case "active":
			state, satisfied = "active", false
		case "exited":
			state = "exited"
		case "session-error":
			state = "error"
		case "session-not-found":
			fmt.Fprint(os.Stderr, "session not found")
			os.Exit(7)
		case "active-then-idle":
			counterPath := os.Getenv("WF_CTL_COUNTER")
			data, _ := os.ReadFile(counterPath)
			if len(data) == 0 {
				state, satisfied = "active", false
				_ = os.WriteFile(counterPath, []byte("1"), 0o600)
			}
		}
		if scenario == "invalid-final-status" {
			state = "completed"
		}
		fmt.Printf(`{"satisfied":%t,"finalStatus":%q,"sessionId":"session-1"}`, satisfied, state)
	case strings.Contains(command, "query_task_bindings"):
		status, summary, exitCode := "completed", "fixture complete", "0"
		metadata := `{"artifacts":["artifact.txt"],"checks":["fixture check"],"usage":{"inputTokensEstimated":3,"outputTokensEstimated":4}}`
		if scenario == "failed-binding" {
			status, summary, exitCode = "failed", "fixture failed", "2"
		}
		if scenario == "waiting-input" || scenario == "idle" || scenario == "active" || scenario == "exited" || scenario == "session-error" || scenario == "session-not-found" {
			status, summary, exitCode = "running", "", "null"
		}
		if scenario == "malformed-binding" {
			metadata = `{"artifacts":[7]}`
		}
		fmt.Printf(`{"items":[{"id":"binding-1","status":%q,"exitCode":%s,"completionSummary":%q,"metadata":%s}]}`, status, exitCode, summary, metadata)
	case strings.Contains(command, "sessions read"):
		fmt.Print(`{"output":"fixture output"}`)
	case strings.Contains(command, "call kill_session"):
		switch scenario {
		case "kill-error-envelope":
			fmt.Print(`{"content":[{"type":"text","text":"kill denied"}],"isError":true}`)
		case "kill-unsuccessful":
			fmt.Print(`{"success":false,"sessionId":"session-own"}`)
		case "kill-session-mismatch":
			fmt.Print(`{"success":true,"sessionId":"session-other"}`)
		case "kill-missing-success":
			fmt.Print(`{"sessionId":"session-own"}`)
		case "kill-invalid-session-id":
			fmt.Print(`{"success":true,"sessionId":7}`)
		default:
			fmt.Print(`{"success":true}`)
		}
	case strings.Contains(command, "sessions kill"):
		fmt.Fprint(os.Stderr, "legacy sessions kill must not be used")
		os.Exit(9)
	default:
		fmt.Print(`{"ok":true}`)
	}
	os.Exit(0)
}

func fixtureBackend(t *testing.T, scenario string) (*Backend, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("WF_CTL_FIXTURE", "1")
	t.Setenv("WF_CTL_SCENARIO", scenario)
	t.Setenv("WF_CTL_LOG", logPath)
	t.Setenv("WF_CTL_COUNTER", filepath.Join(t.TempDir(), "wait-counter"))
	t.Setenv(ProfileIDEnv, "fishyume-profile")
	runner := ExecRunner{PrefixArgs: []string{"-test.run=TestCCPanesCtlFixture", "--"}}
	return NewWithClient(NewClientWithRunner(os.Args[0], runner)), logPath
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	value, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, value)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestProfileConfigurationPrecedenceAndCompatibility(t *testing.T) {
	t.Run("fishyume-precedes-legacy", func(t *testing.T) {
		t.Setenv(ProfileIDEnv, "fishyume-profile")
		t.Setenv(LegacyProfileIDEnv, "legacy-profile")
		profileID, err := ResolveProfileID()
		if err != nil || profileID != "fishyume-profile" {
			t.Fatalf("profileID=%q err=%v", profileID, err)
		}
	})
	t.Run("legacy-alias", func(t *testing.T) {
		unsetEnv(t, ProfileIDEnv)
		t.Setenv(LegacyProfileIDEnv, "legacy-profile")
		profileID, err := ResolveProfileID()
		if err != nil || profileID != "legacy-profile" {
			t.Fatalf("profileID=%q err=%v", profileID, err)
		}
	})
	t.Run("missing-is-actionable", func(t *testing.T) {
		unsetEnv(t, ProfileIDEnv)
		unsetEnv(t, LegacyProfileIDEnv)
		_, err := ResolveProfileID()
		if err == nil || !strings.Contains(err.Error(), ProfileIDEnv) || !strings.Contains(err.Error(), "create one in CC-Panes") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("invalid-fishyume-does-not-fall-back", func(t *testing.T) {
		t.Setenv(ProfileIDEnv, " ")
		t.Setenv(LegacyProfileIDEnv, "legacy-profile")
		_, err := ResolveProfileID()
		if err == nil || !strings.Contains(err.Error(), ProfileIDEnv) {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestDoctorAndProjectRegistration(t *testing.T) {
	b, _ := fixtureBackend(t, "success")
	if err := b.Doctor(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := b.DoctorProject(context.Background(), `C:\fixture-project`); err != nil {
		t.Fatal(err)
	}
	if err := b.DoctorProject(context.Background(), `C:\missing`); err == nil {
		t.Fatal("expected unregistered project error")
	}
}

func TestBackendStateMappingsWithExecutableFixture(t *testing.T) {
	tests := []struct{ scenario, want string }{
		{"success", "succeeded"}, {"failed-binding", "failed"},
		{"waiting-input", "waiting_input"}, {"idle", "completion_missing"},
		{"active-then-idle", "succeeded"},
		{"malformed-binding", "invalid_result"},
	}
	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			b, logPath := fixtureBackend(t, test.scenario)
			session, err := b.Launch(context.Background(), backend.LaunchSpec{RunID: "run-1", Project: `C:\fixture-project`, Tool: "codex", Runtime: "local", Prompt: "do work"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := b.Wait(context.Background(), *session)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != test.want {
				t.Fatalf("status = %q, want %q", result.Status, test.want)
			}
			if test.scenario == "success" {
				if result.Summary != "fixture complete" || result.Usage.InputTokensEstimated != 3 {
					t.Fatalf("unexpected result: %+v", result)
				}
				calls, _ := os.ReadFile(logPath)
				if !strings.Contains(string(calls), "binding-1 is mandatory") || !strings.Contains(string(calls), "Do not modify workflow-engine plan") {
					t.Fatalf("completion contract missing from launch: %s", calls)
				}
				launchCall := string(calls)
				for _, field := range []string{
					`call` + "\t" + `launch_task`,
					`"projectPath":"C:\\fixture-project"`,
					`"cliTool":"codex"`,
					`"runtimeKind":"local"`,
					`"title":"Fishyume run-1"`,
					`"profileId":"fishyume-profile"`,
				} {
					if !strings.Contains(launchCall, field) {
						t.Fatalf("launch_task payload omitted %s: %s", field, launchCall)
					}
				}
				if strings.Contains(launchCall, "\tlaunch\t") {
					t.Fatalf("legacy launch surface was used: %s", launchCall)
				}
				if session.Metadata["resumeId"] != "resume-1" || session.Metadata["paneId"] != "pane-1" {
					t.Fatalf("opaque launch metadata was not preserved: %+v", session.Metadata)
				}
			}
			calls, _ := os.ReadFile(logPath)
			for _, line := range strings.Split(string(calls), "\n") {
				if !strings.Contains(line, "wait_for_session") {
					continue
				}
				if strings.Contains(line, `"completed"`) || strings.Contains(line, `"failed"`) {
					t.Fatalf("wait_for_session sent a non-session state: %s", line)
				}
				for _, allowed := range []string{`"idle"`, `"waitingInput"`, `"error"`, `"exited"`} {
					if !strings.Contains(line, allowed) {
						t.Fatalf("wait_for_session omitted real state %s: %s", allowed, line)
					}
				}
			}
		})
	}
}

func TestReconcileStateMappingsWithExecutableFixture(t *testing.T) {
	tests := []struct {
		scenario string
		want     backend.ObservationState
	}{
		{"success", backend.ObservationTerminal},
		{"active", backend.ObservationActive},
		{"waiting-input", backend.ObservationWaitingInput},
		{"idle", backend.ObservationCompletionMissing},
		{"exited", backend.ObservationExited},
		{"session-error", backend.ObservationError},
		{"session-not-found", backend.ObservationLost},
	}
	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			b, _ := fixtureBackend(t, test.scenario)
			observation, err := b.Reconcile(context.Background(), backend.Session{ID: "session-1", Metadata: map[string]string{
				"bindingId": "binding-1", "project": `C:\fixture-project`,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if observation.State != test.want {
				t.Fatalf("state=%q, want %q", observation.State, test.want)
			}
			if test.want == backend.ObservationTerminal && (observation.Result == nil || observation.Result.Status != "succeeded") {
				t.Fatalf("terminal observation=%+v", observation)
			}
		})
	}
}

func TestAdapterContract(t *testing.T) {
	contracttest.Run(t, func(t *testing.T, scenario contracttest.Scenario) contracttest.Fixture {
		t.Helper()
		fixtureScenario := map[contracttest.Scenario]string{
			contracttest.ScenarioActive:                "active",
			contracttest.ScenarioWaitingInput:          "waiting-input",
			contracttest.ScenarioResultPending:         "idle",
			contracttest.ScenarioTerminalSucceeded:     "success",
			contracttest.ScenarioTerminalFailed:        "failed-binding",
			contracttest.ScenarioTerminalIndeterminate: "exited",
			contracttest.ScenarioLost:                  "session-not-found",
			contracttest.ScenarioCancelConfirmed:       "success",
			contracttest.ScenarioCancelNotConfirmed:    "kill-unsuccessful",
		}[scenario]
		legacy, _ := fixtureBackend(t, fixtureScenario)
		return contracttest.Fixture{
			Backend: NewAdapterWithBackend(legacy),
			Spec: backend.AgentExecutionSpec{
				RunID: "run-contract", NodeID: "agent-1", Attempt: 1, Workspace: `C:\fixture-project`,
				Tool: "codex", Runtime: "local", Instructions: "do work",
			},
		}
	})
}

func TestAdapterLegacySessionRoundTrip(t *testing.T) {
	legacy, _ := fixtureBackend(t, "success")
	adapter := NewAdapterWithBackend(legacy)
	handle, err := adapter.DecodeLegacySession(backend.Session{ID: "session-legacy", Metadata: map[string]string{
		"bindingId": "binding-legacy", "project": `C:\fixture-project`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := adapter.sessionFromHandle(*handle)
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "session-legacy" || session.Metadata["bindingId"] != "binding-legacy" {
		t.Fatalf("session=%+v", session)
	}
}

func TestWaitForSessionContinuesAfterUnsatisfiedActiveTimeout(t *testing.T) {
	b, logPath := fixtureBackend(t, "active-then-idle")
	status, err := b.client.WaitForSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if status != "idle" {
		t.Fatalf("status = %q, want idle", status)
	}
	calls, _ := os.ReadFile(logPath)
	waits := 0
	for _, line := range strings.Split(string(calls), "\n") {
		if strings.Contains(line, "wait_for_session") {
			waits++
			if !strings.Contains(line, `"timeoutMs":5000`) {
				t.Fatalf("wait_for_session did not use short poll timeout: %s", line)
			}
			if strings.Contains(line, `"timeoutMs":570000`) {
				t.Fatalf("wait_for_session regressed to long timeout: %s", line)
			}
		}
	}
	if waits != 2 {
		t.Fatalf("wait_for_session calls = %d, want 2; log=%s", waits, calls)
	}
}

func TestWaitPollTimeoutStaysBelowControlCLIRequestLimit(t *testing.T) {
	if waitPollTimeoutMS <= 0 || waitPollTimeoutMS >= 10000 {
		t.Fatalf("waitPollTimeoutMS = %d, want a positive value below the ctl global 10s timeout", waitPollTimeoutMS)
	}
}

func TestWaitForSessionRejectsNonSessionFinalStatus(t *testing.T) {
	b, _ := fixtureBackend(t, "invalid-final-status")
	_, err := b.client.WaitForSession(context.Background(), "session-1")
	if err == nil || !strings.Contains(err.Error(), `unsupported session state "completed"`) {
		t.Fatalf("expected unsupported-state error, got %v", err)
	}
}

func TestMalformedJSONAndCommandFailure(t *testing.T) {
	b, _ := fixtureBackend(t, "malformed")
	session := backend.Session{ID: "session-1", Metadata: map[string]string{"bindingId": "binding-1", "project": `C:\fixture-project`}}
	if _, err := b.Wait(context.Background(), session); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
	b, _ = fixtureBackend(t, "command-failure")
	_, err := b.Launch(context.Background(), backend.LaunchSpec{RunID: "run-1", Project: `C:\fixture-project`, Tool: "codex", Runtime: "local", Prompt: "secret prompt"})
	if err == nil || !strings.Contains(err.Error(), "exit code 7") || !strings.Contains(err.Error(), ProfileIDEnv) || !strings.Contains(err.Error(), "non-interactive profile") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "secret prompt") {
		t.Fatalf("prompt leaked in error: %v", err)
	}
}

func TestCancelKillsOnlySession(t *testing.T) {
	b, logPath := fixtureBackend(t, "success")
	if err := b.Cancel(context.Background(), backend.Session{ID: "session-own"}); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(logPath)
	callLog := string(calls)
	if !strings.Contains(callLog, "call\tkill_session\t--json\t{\"sessionId\":\"session-own\"}") {
		t.Fatalf("kill_session call or strict payload missing: %s", calls)
	}
	if strings.Contains(callLog, "sessions\tkill") {
		t.Fatalf("legacy sessions kill surface was used: %s", calls)
	}
}

func TestCancelRequiresTruthfulKillSessionConfirmation(t *testing.T) {
	tests := []struct {
		scenario string
		want     string
	}{
		{scenario: "kill-error-envelope", want: "MCP call returned an error envelope"},
		{scenario: "kill-unsuccessful", want: "success=false"},
		{scenario: "kill-session-mismatch", want: "did not match requested session"},
		{scenario: "kill-missing-success", want: "boolean success field"},
		{scenario: "kill-invalid-session-id", want: "invalid sessionId"},
	}
	for _, test := range tests {
		t.Run(test.scenario, func(t *testing.T) {
			b, logPath := fixtureBackend(t, test.scenario)
			err := b.Cancel(context.Background(), backend.Session{ID: "session-own"})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			calls, _ := os.ReadFile(logPath)
			callLog := string(calls)
			if !strings.Contains(callLog, "call\tkill_session\t--json\t{\"sessionId\":\"session-own\"}") {
				t.Fatalf("kill_session call or strict payload missing: %s", calls)
			}
			if strings.Contains(callLog, "sessions\tkill") {
				t.Fatalf("legacy sessions kill surface was used: %s", calls)
			}
		})
	}
}

func TestLaunchReturnsOwnedSessionWhenPostLaunchBindingUpdateFails(t *testing.T) {
	b, _ := fixtureBackend(t, "update-failure")
	session, err := b.Launch(context.Background(), backend.LaunchSpec{RunID: "run-1", Project: `C:\fixture-project`, Tool: "codex", Runtime: "local", Prompt: "work"})
	if err == nil {
		t.Fatal("expected update failure")
	}
	if session == nil || session.ID != "session-1" || session.Metadata["bindingId"] != "binding-1" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
}

func TestDecodeMCPEnvelope(t *testing.T) {
	value, err := decodeStructured([]byte(`{"content":[{"type":"text","text":"{\"id\":\"x\"}"}],"isError":false}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(value)
	if string(data) != `{"id":"x"}` {
		t.Fatalf("unexpected value: %s", data)
	}
}
