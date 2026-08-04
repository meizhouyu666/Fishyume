package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func main() {
	command := strings.Join(os.Args[1:], " ")
	project, _ := json.Marshal(os.Getenv("WF_FAKE_PROJECT"))
	switch {
	case strings.HasSuffix(command, "status"):
		fmt.Print(`{"instances":[{"instance":"release","orchestrator":{"lifecycle":"ready"},"daemon":{"lifecycle":"ready"}}]}`)
	case strings.Contains(command, "list_projects"):
		fmt.Printf(`{"projects":[{"projectPath":%s}]}`, project)
	case strings.Contains(command, "create_task_binding"):
		fmt.Print(`{"content":[{"type":"text","text":"{\"id\":\"binding-e2e\"}"}],"isError":false}`)
	case strings.Contains(command, " launch "):
		fmt.Print(`{"launchId":"launch-e2e","sessionId":"session-e2e"}`)
	case strings.Contains(command, "update_task_binding"):
		fmt.Print(`{"ok":true}`)
	case strings.Contains(command, "wait_for_session"):
		fmt.Print(`{"satisfied":true,"finalStatus":"idle","sessionId":"session-e2e"}`)
	case strings.Contains(command, "query_task_bindings"):
		fmt.Print(`{"items":[{"id":"binding-e2e","status":"completed","exitCode":0,"completionSummary":"fixture integration completed","metadata":{"artifacts":["fixture.txt"],"warnings":[],"checks":["fake ctl integration"],"usage":{"inputTokensEstimated":1,"outputTokensEstimated":2}}}]}`)
	case strings.Contains(command, "sessions read"):
		fmt.Print(`{"output":"fixture agent output\n"}`)
	case strings.Contains(command, "sessions kill"):
		fmt.Print(`{"killed":true}`)
	default:
		fmt.Print(`{"ok":true}`)
	}
}
