package application

import (
	"strings"
	"testing"
)

func TestParseAttemptActivityNormalizesCodexEvents(t *testing.T) {
	output := strings.Join([]string{
		"events:",
		`{"type":"thread.started"}`,
		`{"type":"turn.started"}`,
		`{"type":"item.started","item":{"type":"command_execution","command":"go test ./...","status":"in_progress"}}`,
		`{"type":"item.completed","item":{"type":"command_execution","command":"go test ./...","aggregated_output":"secret raw output","exit_code":0,"status":"completed"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"{\"status\":\"succeeded\"}"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":10,"output_tokens":2}}`,
	}, "\n")
	activity := parseAttemptActivity(output)
	if activity == nil || activity.SchemaVersion != activitySchemaVersion {
		t.Fatalf("activity=%+v", activity)
	}
	if activity.Summary != "Codex 已完成本轮处理（输入 10 / 输出 2 tokens）" || len(activity.Items) != 6 {
		t.Fatalf("activity=%+v", activity)
	}
	encoded := strings.Join(func() []string {
		items := make([]string, 0, len(activity.Items))
		for _, item := range activity.Items {
			items = append(items, item.Message)
		}
		return items
	}(), "\n")
	if strings.Contains(encoded, "secret raw output") || strings.Contains(encoded, "input_tokens") {
		t.Fatalf("raw output leaked: %q", encoded)
	}
	command := activity.Items[2]
	if command.Command == nil || command.Command.Program != "go" || command.Command.Category != "test" || command.Command.Text != "go test ./..." {
		t.Fatalf("command metadata = %+v", command)
	}
}

func TestParseAttemptActivityNormalizesFileResources(t *testing.T) {
	activity := parseAttemptActivity(`{"type":"item.completed","item":{"type":"file_change","operation":"write","changes":[{"path":"enterprise/app.py","kind":"source"}]}}`)
	if activity == nil || len(activity.Items) != 1 {
		t.Fatalf("activity=%+v", activity)
	}
	item := activity.Items[0]
	if item.Kind != "file" || item.Resource == nil || item.Resource.Operation != "write" || item.Resource.Path != "enterprise/app.py" || item.Resource.Kind != "source" {
		t.Fatalf("file metadata = %+v", item)
	}
}

func TestParseAttemptActivityRedactsAndBoundsUTF8(t *testing.T) {
	long := strings.Repeat("界", 300)
	activity := parseAttemptActivity(`{"type":"item.completed","item":{"type":"agent_message","text":"token=sk-1234567890 password=hunter2 apiKey=secret-value ` + long + `"}}`)
	if activity == nil || len(activity.Items) != 1 {
		t.Fatalf("activity=%+v", activity)
	}
	message := activity.Items[0].Message
	if strings.Contains(message, "sk-1234567890") || strings.Contains(message, "hunter2") || strings.Contains(message, "secret-value") {
		t.Fatalf("secret leaked: %q", message)
	}
	if len([]byte(message)) > maxActivityMessage+len([]byte("…")) || !strings.HasSuffix(message, "…") {
		t.Fatalf("message was not bounded: bytes=%d value=%q", len([]byte(message)), message)
	}
}

func TestParseAttemptActivityIgnoresUnknownAndBoundsItems(t *testing.T) {
	lines := make([]string, 0, maxActivityItems+3)
	for index := 0; index < maxActivityItems+3; index++ {
		lines = append(lines, `{"type":"item.completed","item":{"type":"tool_call","name":"tool"}}`)
	}
	lines = append(lines, `{"type":"future.event","secret":"do not expose"}`, "not json")
	activity := parseAttemptActivity(strings.Join(lines, "\n"))
	if activity == nil || len(activity.Items) != maxActivityItems || !activity.Truncated {
		t.Fatalf("activity=%+v", activity)
	}
}

func TestParseAttemptActivityLeavesLegacyTextWithoutActivity(t *testing.T) {
	if activity := parseAttemptActivity("legacy backend output\nplain diagnostic"); activity != nil {
		t.Fatalf("legacy text produced activity: %+v", activity)
	}
	if activity := parseAttemptActivity("events:\n{\"type\":\"future.event\"}\nstderr:\n{\"type\":\"turn.started\"}"); activity != nil {
		t.Fatalf("unknown event or stderr produced activity: %+v", activity)
	}
}
