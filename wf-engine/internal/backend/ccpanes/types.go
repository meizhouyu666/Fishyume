package ccpanes

import "encoding/json"

type commandResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

type taskBinding struct {
	ID                string         `json:"id"`
	Status            string         `json:"status"`
	ExitCode          *int           `json:"exitCode"`
	CompletionSummary string         `json:"completionSummary"`
	Metadata          map[string]any `json:"metadata"`
}

type launchResult struct {
	LaunchID  string
	SessionID string
	Metadata  map[string]string
}

func rawObject(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
