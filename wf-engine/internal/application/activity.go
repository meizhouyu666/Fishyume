package application

import (
	"bufio"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	activitySchemaVersion = "fishyume.attempt-activity/v1"
	maxActivityItems      = MaxAttemptActivityItems
	maxActivityMessage    = MaxAttemptActivityMessageBytes
	maxActivityInputBytes = MaxAttemptActivityReadBytes
)

var (
	secretAssignment = regexp.MustCompile(`(?i)(password|token|secret|api[_-]?key)"?\s*[:=]\s*"?[^\s,;}"\']+"?`)
	bearerSecret     = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]+`)
	apiSecret        = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}`)
)

type activityEvent struct {
	Type  string          `json:"type"`
	Item  json.RawMessage `json:"item"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type activityItem struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Command string `json:"command"`
	Text    string `json:"text"`
}

// parseAttemptActivity converts bounded Codex JSONL into a deliberately small
// public view. It never returns raw tool output, prompts, credentials, or
// reasoning text.
func parseAttemptActivity(output string) *AttemptActivityView {
	if strings.TrimSpace(output) == "" {
		return nil
	}
	view := &AttemptActivityView{SchemaVersion: activitySchemaVersion, Items: []ActivityItemView{}}
	if len(output) > maxActivityInputBytes {
		output = output[len(output)-maxActivityInputBytes:]
		view.Truncated = true
	}
	add := func(kind, status, message string) {
		message = activityText(message)
		if message == "" {
			return
		}
		if len(view.Items) >= maxActivityItems {
			view.Items = view.Items[1:]
			view.Truncated = true
		}
		view.Items = append(view.Items, ActivityItemView{Kind: kind, Status: status, Message: message})
		view.Summary = message
	}
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 1024), maxActivityInputBytes)
	inEvents := true
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "events:" {
			inEvents = true
			continue
		}
		if line == "stderr:" {
			inEvents = false
			continue
		}
		if line == "" || !inEvents {
			continue
		}
		var event activityEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil || event.Type == "" {
			continue
		}
		var item activityItem
		if len(event.Item) > 0 && json.Unmarshal(event.Item, &item) != nil {
			continue
		}
		switch event.Type {
		case "thread.started":
			add("session", "running", "Codex \u4f1a\u8bdd\u5df2\u542f\u52a8")
		case "turn.started":
			add("turn", "running", "Codex \u6b63\u5728\u5904\u7406\u4efb\u52a1")
		case "turn.completed":
			message := "Codex \u5df2\u5b8c\u6210\u672c\u8f6e\u5904\u7406"
			if event.Usage.InputTokens > 0 || event.Usage.OutputTokens > 0 {
				message += fmt.Sprintf("\uff08\u8f93\u5165 %d / \u8f93\u51fa %d tokens\uff09", event.Usage.InputTokens, event.Usage.OutputTokens)
			}
			add("turn", "completed", message)
		case "item.started":
			parseActivityItem(add, item, true)
		case "item.completed":
			parseActivityItem(add, item, false)
		}
	}
	if scanner.Err() != nil {
		view.Truncated = true
	}
	if len(view.Items) == 0 {
		return nil
	}
	return view
}

func parseActivityItem(add func(string, string, string), item activityItem, started bool) {
	switch item.Type {
	case "command_execution":
		status := item.Status
		if status == "" {
			if started {
				status = "running"
			} else {
				status = "completed"
			}
		}
		if status == "in_progress" {
			status = "running"
		}
		message := "\u547d\u4ee4\u5df2\u5b8c\u6210"
		if status == "running" {
			message = "\u6b63\u5728\u6267\u884c\u547d\u4ee4"
		} else if status == "failed" {
			message = "\u547d\u4ee4\u6267\u884c\u5931\u8d25"
		}
		if strings.TrimSpace(item.Command) != "" {
			message += "\uff1a" + item.Command
		}
		add("command", status, message)
	case "reasoning":
		add("reasoning", "running", "Codex \u6b63\u5728\u5206\u6790")
	case "agent_message":
		if strings.HasPrefix(strings.TrimSpace(item.Text), "{") {
			add("message", "completed", "Codex \u5df2\u751f\u6210\u7ed3\u6784\u5316\u7ed3\u679c")
		} else if strings.TrimSpace(item.Text) != "" {
			add("message", "completed", item.Text)
		}
	case "file_change", "file_search", "web_search", "tool_call":
		add("tool", "completed", "Codex \u6b63\u5728\u4f7f\u7528\u5de5\u5177\uff1a"+item.Type)
	}
}

func activityText(value string) string {
	value = strings.TrimSpace(value)
	value = secretAssignment.ReplaceAllString(value, "$1=[\u5df2\u9690\u85cf]")
	value = bearerSecret.ReplaceAllString(value, "Bearer [\u5df2\u9690\u85cf]")
	value = apiSecret.ReplaceAllString(value, "[\u5df2\u9690\u85cf]")
	if len(value) > maxActivityMessage {
		value = truncateUTF8(value, maxActivityMessage)
	}
	return value
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	data := []byte(value[:maxBytes])
	for len(data) > 0 && !utf8.Valid(data) {
		data = data[:len(data)-1]
	}
	return string(data) + "\u2026"
}
