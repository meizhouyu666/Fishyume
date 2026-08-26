package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type state struct {
	Thread            *thread `json:"thread,omitempty"`
	NextTurn          int     `json:"nextTurn"`
	EmptyReadFailures int     `json:"emptyReadFailures,omitempty"`
}

type thread struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Turns     []turn `json:"turns"`
}

type turn struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Items  []item `json:"items"`
}

type item struct {
	Type     string `json:"type"`
	ClientID string `json:"clientId,omitempty"`
	Text     string `json:"text,omitempty"`
}

type request struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" {
			fmt.Println("codex-cli 0.0.0-fishyume-session-fixture")
			return
		}
	}
	path := os.Getenv("FISHYUME_FAKE_APP_SERVER_STATE")
	if path == "" {
		fmt.Fprintln(os.Stderr, "FISHYUME_FAKE_APP_SERVER_STATE is required")
		os.Exit(2)
	}
	value := load(path)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		var message request
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			continue
		}
		if message.Method == "initialized" {
			continue
		}
		handle(path, &value, message)
	}
}

func handle(path string, value *state, message request) {
	switch message.Method {
	case "initialize":
		if os.Getenv("FISHYUME_FAKE_SERVER_REQUEST") == "1" {
			emit(map[string]any{"id": message.ID, "method": "request/approval", "params": map[string]any{}})
		}
		respond(message.ID, map[string]any{"userAgent": "fake-codex-app-server", "platformFamily": "fixture", "platformOs": "fixture", "codexHome": os.TempDir()})
	case "thread/start":
		var params struct {
			CWD            string `json:"cwd"`
			Model          string `json:"model"`
			ApprovalPolicy string `json:"approvalPolicy"`
		}
		decode(message.Params, &params)
		value.Thread = &thread{ID: "fixture-thread", SessionID: "fixture-session", CWD: params.CWD}
		value.NextTurn = 1
		if os.Getenv("FISHYUME_FAKE_EMPTY_THREAD_READ_ONCE") == "1" {
			value.EmptyReadFailures = 1
		}
		persist(path, *value)
		respond(message.ID, threadResponse(value.Thread, params.Model, params.ApprovalPolicy))
	case "thread/resume":
		var params struct {
			ThreadID       string `json:"threadId"`
			CWD            string `json:"cwd"`
			Model          string `json:"model"`
			ApprovalPolicy string `json:"approvalPolicy"`
		}
		decode(message.Params, &params)
		if value.Thread == nil || params.ThreadID != value.Thread.ID {
			fail(message.ID, "no rollout found for thread id "+params.ThreadID)
			return
		}
		respond(message.ID, threadResponse(value.Thread, params.Model, params.ApprovalPolicy))
	case "thread/read":
		var params struct {
			ThreadID string `json:"threadId"`
		}
		decode(message.Params, &params)
		if value.Thread == nil || params.ThreadID != value.Thread.ID {
			fail(message.ID, "thread not found")
			return
		}
		if value.EmptyReadFailures > 0 {
			value.EmptyReadFailures--
			persist(path, *value)
			fail(message.ID, "failed to read thread: thread-store internal error: failed to read session metadata rollout fixture.jsonl: rollout fixture.jsonl is empty")
			return
		}
		respond(message.ID, map[string]any{"thread": externalThread(value.Thread)})
	case "turn/start":
		var params struct {
			ThreadID            string `json:"threadId"`
			ClientUserMessageID string `json:"clientUserMessageId"`
			Input               []struct {
				Text string `json:"text"`
			} `json:"input"`
		}
		decode(message.Params, &params)
		if value.Thread == nil || params.ThreadID != value.Thread.ID {
			fail(message.ID, "thread not found")
			return
		}
		prompt := ""
		if len(params.Input) > 0 {
			prompt = params.Input[0].Text
		}
		created := turn{ID: fmt.Sprintf("fixture-turn-%d", value.NextTurn), Status: "inProgress", Items: []item{{Type: "userMessage", ClientID: params.ClientUserMessageID}}}
		value.NextTurn++
		value.Thread.Turns = append(value.Thread.Turns, created)
		persist(path, *value)
		if strings.Contains(prompt, "scenario:launch-then-disconnect") {
			last := &value.Thread.Turns[len(value.Thread.Turns)-1]
			last.Status = "completed"
			last.Items = append(last.Items, item{Type: "agentMessage", Text: contribution(prompt)})
			persist(path, *value)
			os.Exit(23)
		}
		respond(message.ID, map[string]any{"turn": created})
		if !strings.Contains(prompt, "scenario:active") {
			last := &value.Thread.Turns[len(value.Thread.Turns)-1]
			last.Status = "completed"
			output := contribution(prompt)
			if strings.Contains(prompt, "scenario:oversized-output") {
				output = strings.Repeat("x", 1024)
			}
			last.Items = append(last.Items, item{Type: "agentMessage", Text: output})
			persist(path, *value)
		}
	case "turn/interrupt":
		var params struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
		}
		decode(message.Params, &params)
		if value.Thread == nil || params.ThreadID != value.Thread.ID {
			fail(message.ID, "thread not found")
			return
		}
		for index := range value.Thread.Turns {
			candidate := &value.Thread.Turns[index]
			if candidate.ID == params.TurnID && candidate.Status == "inProgress" {
				candidate.Status = "interrupted"
				persist(path, *value)
				respond(message.ID, map[string]any{})
				return
			}
		}
		fail(message.ID, "no active turn to interrupt")
	default:
		fail(message.ID, "unsupported fixture method "+message.Method)
	}
}

func contribution(prompt string) string {
	label := "fixture initial response"
	if strings.Contains(prompt, "follow-up") {
		label = "fixture directed follow-up"
	}
	return label
}

func threadResponse(value *thread, model, approval string) map[string]any {
	return map[string]any{
		"thread": externalThread(value), "model": model, "modelProvider": "openai",
		"cwd": value.CWD, "approvalPolicy": approval, "sandbox": map[string]any{"type": "readOnly", "networkAccess": false},
	}
}

func externalThread(value *thread) map[string]any {
	return map[string]any{"id": value.ID, "sessionId": value.SessionID, "cwd": value.CWD, "ephemeral": false, "modelProvider": "openai", "turns": value.Turns}
}

func load(path string) state {
	data, err := os.ReadFile(path)
	if err != nil {
		return state{NextTurn: 1}
	}
	var value state
	if json.Unmarshal(data, &value) != nil {
		return state{NextTurn: 1}
	}
	return value
}

func persist(path string, value state) {
	data, _ := json.Marshal(value)
	_ = os.WriteFile(path, data, 0o600)
}

func decode(data json.RawMessage, value any) { _ = json.Unmarshal(data, value) }

func respond(id json.RawMessage, result any) {
	emit(map[string]any{"id": id, "result": result})
}

func fail(id json.RawMessage, message string) {
	emit(map[string]any{"id": id, "error": map[string]any{"code": -32000, "message": message}})
}

func emit(value any) {
	data, _ := json.Marshal(value)
	fmt.Println(string(data))
	time.Sleep(time.Millisecond)
}
