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
	case strings.Contains(command, "call launch_task"):
		fmt.Print(`{"launchId":"launch-e2e","sessionId":"session-e2e"}`)
	case strings.Contains(command, "update_task_binding"):
		fmt.Print(`{"ok":true}`)
	case strings.Contains(command, "wait_for_session"):
		fmt.Print(`{"satisfied":true,"finalStatus":"idle","sessionId":"session-e2e"}`)
	case strings.Contains(command, "query_task_bindings"):
		if os.Getenv("WF_FAKE_BINDING_STATUS") == "failed" {
			fmt.Print(`{"items":[{"id":"binding-e2e","status":"failed","exitCode":1,"completionSummary":"fixture integration failed","metadata":{"artifacts":[],"warnings":["fixture failure"],"checks":[],"usage":{"inputTokensEstimated":1,"outputTokensEstimated":2}}}]}`)
		} else {
			fmt.Print(`{"items":[{"id":"binding-e2e","status":"completed","exitCode":0,"completionSummary":"fixture integration completed","metadata":{"artifacts":["fixture.txt"],"warnings":[],"checks":["fake ctl integration"],"usage":{"inputTokensEstimated":1,"outputTokensEstimated":2}}}]}`)
		}
	case strings.Contains(command, "sessions read"):
		fmt.Print(`{"output":"fixture agent output\n"}`)
	case strings.Contains(command, "call kill_session"):
		fmt.Print(`{"success":true,"sessionId":"session-e2e"}`)
	case strings.Contains(command, "sessions kill"):
		fmt.Fprint(os.Stderr, "legacy sessions kill must not be used")
		os.Exit(9)
	default:
		fmt.Print(`{"ok":true}`)
	}
}
