package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type identity struct {
	ExecutionID string `json:"executionId"`
	RunID       string `json:"runId"`
	NodeID      string `json:"nodeId"`
	Attempt     int    `json:"attempt"`
}

type teamIdentity struct {
	ExecutionID   string `json:"executionId"`
	TeamID        string `json:"teamId"`
	ParticipantID string `json:"participantId"`
	TurnID        string `json:"turnId"`
}

func main() {
	for _, arg := range os.Args[1:] {
		if arg == "--version" {
			fmt.Println("codex-cli 0.0.0-fishyume-fixture")
			return
		}
	}
	resultPath := valueAfter("--output-last-message")
	promptBytes, _ := io.ReadAll(os.Stdin)
	prompt := string(promptBytes)
	scenario := "terminal-succeeded"
	if first := strings.SplitN(prompt, "\n", 2)[0]; strings.HasPrefix(first, "scenario:") {
		scenario = strings.TrimSpace(strings.TrimPrefix(first, "scenario:"))
	}
	for _, candidate := range []string{"team-contribution", "team-partial", "team-malformed", "team-unknown-field", "terminal-succeeded", "delayed-succeeded", "terminal-failed", "terminal-indeterminate", "terminal-needs-input", "needs-input-then-succeeded", "malformed-result", "wrong-identity", "oversized-result", "conflict-result", "premature-result", "nonzero-missing", "result-pending", "waiting-input", "active", "large-output"} {
		if strings.Contains(prompt, "scenario:"+candidate) {
			scenario = candidate
			break
		}
	}
	var id identity
	marker := "FISHYUME_RESULT_IDENTITY="
	if index := strings.LastIndex(prompt, marker); index >= 0 {
		_ = json.Unmarshal([]byte(strings.TrimSpace(prompt[index+len(marker):])), &id)
	}
	var teamID teamIdentity
	teamMarker := "FISHYUME_TEAM_IDENTITY="
	if index := strings.LastIndex(prompt, teamMarker); index >= 0 {
		_ = json.Unmarshal([]byte(strings.TrimSpace(prompt[index+len(teamMarker):])), &teamID)
	}
	if teamID.ExecutionID != "" && scenario == "terminal-succeeded" {
		scenario = "team-contribution"
	}
	if strings.HasPrefix(scenario, "team-") && (teamID.ExecutionID == "" || teamID.TeamID == "" || teamID.ParticipantID == "" || teamID.TurnID == "") {
		fmt.Fprintln(os.Stderr, "missing Team execution identity")
		os.Exit(20)
	}
	emit(map[string]any{"type": "thread.started", "thread_id": "fixture-thread"})
	switch scenario {
	case "team-contribution":
		model := valueAfter("--model")
		if (model != "gpt-5.6" && model != "gpt-5.6-luna") || valueAfter("--sandbox") != "read-only" {
			fmt.Fprintln(os.Stderr, "Team execution did not receive the selected model and read-only sandbox")
			os.Exit(21)
		}
		writeTeamContribution(resultPath, false, model)
		emitCompleted()
	case "team-partial":
		model := valueAfter("--model")
		if model == "gpt-5.6" {
			writeTeamContribution(resultPath, false, model)
		} else {
			_ = os.WriteFile(resultPath, []byte("{not-json"), 0o600)
		}
		emitCompleted()
	case "team-malformed":
		_ = os.WriteFile(resultPath, []byte("{not-json"), 0o600)
		emitCompleted()
	case "team-unknown-field":
		writeTeamContribution(resultPath, true, valueAfter("--model"))
		emitCompleted()
	case "active", "cancel-confirmed", "cancel-not-confirmed", "lost":
		time.Sleep(60 * time.Second)
	case "waiting-input":
		emit(map[string]any{"type": "fishyume.waiting_input"})
		time.Sleep(60 * time.Second)
	case "result-pending":
		emitCompleted()
	case "terminal-succeeded", "terminal-failed", "terminal-indeterminate":
		status := strings.TrimPrefix(scenario, "terminal-")
		writeResult(resultPath, id, status, status+" fixture in "+mustWorkingDirectory())
		emitCompleted()
	case "delayed-succeeded":
		time.Sleep(500 * time.Millisecond)
		writeResult(resultPath, id, "succeeded", "delayed fixture in "+mustWorkingDirectory())
		emitCompleted()
	case "terminal-needs-input":
		writeNeedsInput(resultPath, id)
		emit(map[string]any{"type": "fishyume.waiting_input"})
		emitCompleted()
	case "needs-input-then-succeeded":
		if id.Attempt == 1 {
			writeNeedsInput(resultPath, id)
			emit(map[string]any{"type": "fishyume.waiting_input"})
		} else {
			writeResult(resultPath, id, "succeeded", "answered fixture in "+mustWorkingDirectory())
		}
		emitCompleted()
	case "malformed-result":
		_ = os.WriteFile(resultPath, []byte("{not-json"), 0o600)
		emitCompleted()
	case "wrong-identity":
		id.ExecutionID = "wrong-execution"
		writeResult(resultPath, id, "succeeded", "wrong identity")
		emitCompleted()
	case "oversized-result":
		writeResult(resultPath, id, "succeeded", strings.Repeat("oversized", 20000))
		emitCompleted()
	case "conflict-result":
		writeResult(resultPath, id, "succeeded", "conflicting success")
		os.Exit(9)
	case "premature-result":
		writeResult(resultPath, id, "succeeded", "premature success")
		time.Sleep(60 * time.Second)
	case "nonzero-missing":
		os.Exit(17)
	case "large-output":
		writer := bufio.NewWriterSize(os.Stdout, 64*1024)
		for index := 0; index < 20000; index++ {
			encoded, _ := json.Marshal(map[string]any{"type": "item.completed", "index": index, "text": strings.Repeat("x", 256)})
			_, _ = writer.Write(append(encoded, '\n'))
		}
		_ = writer.Flush()
		time.Sleep(60 * time.Second)
	default:
		fmt.Fprintln(os.Stderr, "unknown scenario", scenario)
		os.Exit(19)
	}
}

func writeTeamContribution(path string, unknownField bool, model string) {
	value := map[string]any{
		"schemaVersion":   "fishyume.team/v1",
		"status":          "completed",
		"contentMarkdown": "fixture contribution from " + model,
		"warnings":        []string{},
		"openQuestions":   []string{},
	}
	if unknownField {
		value["unexpected"] = true
	}
	data, _ := json.Marshal(value)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}

func valueAfter(name string) string {
	for index, arg := range os.Args[1:] {
		if arg == name && index+2 <= len(os.Args[1:]) {
			return os.Args[1:][index+1]
		}
	}
	return ""
}

func writeResult(path string, id identity, status, summary string) {
	value := map[string]any{
		"executionId": id.ExecutionID, "runId": id.RunID, "nodeId": id.NodeID, "attempt": id.Attempt,
		"result": map[string]any{"status": status, "summary": summary, "artifacts": []string{}, "warnings": []string{}, "checks": []string{}, "questions": []map[string]any{}},
	}
	data, _ := json.Marshal(value)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}

func writeNeedsInput(path string, id identity) {
	value := map[string]any{
		"executionId": id.ExecutionID, "runId": id.RunID, "nodeId": id.NodeID, "attempt": id.Attempt,
		"result": map[string]any{
			"status": "needs_input", "summary": "fixture needs approval", "artifacts": []string{}, "warnings": []string{}, "checks": []string{},
			"questions": []map[string]any{{"id": "approval", "prompt": "Proceed?", "choices": []string{"yes", "no"}, "required": true}},
		},
	}
	data, _ := json.Marshal(value)
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}

func emit(value any) {
	data, _ := json.Marshal(value)
	fmt.Println(string(data))
}

func emitCompleted() {
	emit(map[string]any{"type": "turn.completed", "usage": map[string]int{"input_tokens": 12, "output_tokens": 7}})
}

func mustWorkingDirectory() string {
	value, _ := os.Getwd()
	return value
}
