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
	if scenario == "command-failure" && strings.Contains(command, " launch ") {
		os.Exit(7)
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
	case strings.Contains(command, " launch "):
		fmt.Print(`{"launchId":"launch-1","sessionId":"session-1"}`)
	case strings.Contains(command, "update_task_binding"):
		fmt.Print(`{"ok":true}`)
	case strings.Contains(command, "wait_for_session"):
		state := "idle"
		if scenario == "waiting-input" {
			state = "waitingInput"
		}
		if scenario == "idle" {
			state = "idle"
		}
		satisfied := true
		if scenario == "active-then-idle" {
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
		if scenario == "failed-binding" {
			status, summary, exitCode = "failed", "fixture failed", "2"
		}
		if scenario == "waiting-input" || scenario == "idle" {
			status, summary, exitCode = "running", "", "null"
		}
		fmt.Printf(`{"items":[{"id":"binding-1","status":%q,"exitCode":%s,"completionSummary":%q,"metadata":{"artifacts":["artifact.txt"],"checks":["fixture check"],"usage":{"inputTokensEstimated":3,"outputTokensEstimated":4}}}]}`, status, exitCode, summary)
	case strings.Contains(command, "sessions read"):
		fmt.Print(`{"output":"fixture output"}`)
	case strings.Contains(command, "sessions kill"):
		fmt.Print(`{"killed":true}`)
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
	runner := ExecRunner{PrefixArgs: []string{"-test.run=TestCCPanesCtlFixture", "--"}}
	return NewWithClient(NewClientWithRunner(os.Args[0], runner)), logPath
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
		{"waiting-input", "blocked"}, {"idle", "indeterminate"},
		{"active-then-idle", "succeeded"},
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
	if err == nil || !strings.Contains(err.Error(), "exit code 7") {
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
	if !strings.Contains(string(calls), "sessions\tkill\tsession-own") {
		t.Fatalf("kill command missing: %s", calls)
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
