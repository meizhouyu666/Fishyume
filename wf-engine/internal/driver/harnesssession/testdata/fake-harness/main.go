package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"
)

func main() {
	prompt, _ := io.ReadAll(os.Stdin)
	if strings.Contains(string(prompt), "BLOCK") {
		for {
			time.Sleep(time.Second)
		}
	}
	args := os.Args[1:]
	if contains(args, "--output-format") {
		id := after(args, "--session-id")
		if id == "" {
			id = after(args, "--resume")
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"result": "fake Claude answer", "session_id": id, "is_error": false})
		return
	}
	id := after(args, "--session")
	if id == "" {
		id = "ses_fake_opencode"
	}
	w := bufio.NewWriter(os.Stdout)
	data, _ := json.Marshal(map[string]any{"type": "text", "sessionID": id, "part": map[string]any{"text": "fake OpenCode answer"}})
	_, _ = w.Write(append(data, '\n'))
	_ = w.Flush()
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func after(values []string, target string) string {
	for index, value := range values {
		if value == target && index+1 < len(values) {
			return values[index+1]
		}
	}
	return ""
}
