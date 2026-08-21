package codexprocess

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wf.local/wf-engine/internal/backend"
)

type processRef struct {
	PID         int    `json:"pid"`
	GroupID     int    `json:"groupId,omitempty"`
	Fingerprint string `json:"fingerprint"`
	Executable  string `json:"executable"`
}

type handleData struct {
	ExecutionID           string     `json:"executionId"`
	RunID                 string     `json:"runId"`
	NodeID                string     `json:"nodeId"`
	Attempt               int        `json:"attempt"`
	AttemptDir            string     `json:"attemptDir"`
	Workspace             string     `json:"workspace"`
	ResultMaxBytes        int        `json:"resultMaxBytes"`
	EventMaxBytes         int64      `json:"eventMaxBytes,omitempty"`
	AgentExecutable       string     `json:"agentExecutable"`
	AgentExecutableSHA256 string     `json:"agentExecutableSha256"`
	Supervisor            processRef `json:"supervisor"`
	Child                 processRef `json:"child"`
	StartedAt             time.Time  `json:"startedAt"`
}

type artifactSet struct {
	Config string
	Ready  string
	Events string
	Stderr string
	Schema string
	Result string
	Exit   string
}

func artifactPaths(attemptDir string) artifactSet {
	return artifactSet{
		Config: filepath.Join(attemptDir, "direct-supervisor.json"), Ready: filepath.Join(attemptDir, "direct-ready.json"),
		Events: filepath.Join(attemptDir, "direct-events.jsonl"), Stderr: filepath.Join(attemptDir, "direct-stderr.log"),
		Schema: filepath.Join(attemptDir, "direct-result.schema.json"), Result: filepath.Join(attemptDir, "direct-result.json"),
		Exit: filepath.Join(attemptDir, "direct-exit.json"),
	}
}

func encodeHandle(data handleData) (*backend.ExecutionHandle, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	handle := &backend.ExecutionHandle{Backend: "direct", SchemaVersion: handleSchemaVersion, ID: data.ExecutionID, Data: encoded}
	if err := backend.ValidateExecutionHandle(*handle); err != nil {
		return nil, err
	}
	return handle, nil
}

func (b *Backend) decodeHandle(handle backend.ExecutionHandle) (handleData, artifactSet, error) {
	if err := backend.ValidateExecutionHandle(handle); err != nil {
		return handleData{}, artifactSet{}, err
	}
	if handle.Backend != b.Name() {
		return handleData{}, artifactSet{}, fmt.Errorf("Direct Backend cannot decode handle for Backend %q", handle.Backend)
	}
	if handle.SchemaVersion != handleSchemaVersion {
		return handleData{}, artifactSet{}, fmt.Errorf("unsupported Direct handle schema version %d", handle.SchemaVersion)
	}
	var data handleData
	decoder := json.NewDecoder(strings.NewReader(string(handle.Data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return handleData{}, artifactSet{}, fmt.Errorf("decode Direct handle: %w", err)
	}
	if data.ExecutionID == "" || data.ExecutionID != handle.ID || data.RunID == "" || data.NodeID == "" || data.Attempt < 1 || data.AgentExecutableSHA256 == "" {
		return handleData{}, artifactSet{}, fmt.Errorf("Direct handle identity is incomplete")
	}
	if data.Supervisor.PID <= 0 || data.Supervisor.Fingerprint == "" || data.Child.PID <= 0 || data.Child.Fingerprint == "" {
		return handleData{}, artifactSet{}, fmt.Errorf("Direct handle process identity is incomplete")
	}
	if !sameExecutable(data.Child.Executable, data.AgentExecutable) {
		return handleData{}, artifactSet{}, fmt.Errorf("Direct handle child executable does not match the Agent executable")
	}
	if data.ResultMaxBytes <= 0 || data.ResultMaxBytes > maxResultBytes {
		return handleData{}, artifactSet{}, fmt.Errorf("Direct handle result limit is invalid")
	}
	if data.EventMaxBytes < 0 {
		return handleData{}, artifactSet{}, fmt.Errorf("Direct handle event limit is invalid")
	}
	attemptDir, err := b.resolveRelativePath(filepath.FromSlash(data.AttemptDir))
	if err != nil {
		return handleData{}, artifactSet{}, err
	}
	return data, artifactPaths(attemptDir), nil
}

func (b *Backend) resolveRelativePath(relative string) (string, error) {
	if strings.TrimSpace(b.config.StateRoot) == "" || !filepath.IsAbs(b.config.StateRoot) {
		return "", fmt.Errorf("Direct Backend state root must be an absolute path")
	}
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("Direct Attempt path must be relative")
	}
	root := filepath.Clean(b.config.StateRoot)
	joined := filepath.Clean(filepath.Join(root, relative))
	rel, err := filepath.Rel(root, joined)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("Direct Attempt path escapes the state root")
	}
	return joined, nil
}

type resultEnvelope struct {
	ExecutionID string              `json:"executionId"`
	RunID       string              `json:"runId"`
	NodeID      string              `json:"nodeId"`
	Attempt     int                 `json:"attempt"`
	Result      backend.AgentResult `json:"result"`
}

func resultSchema(spec backend.AgentExecutionSpec, executionID string) map[string]any {
	stringArray := map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": 256}
	questions := map[string]any{"type": "array", "maxItems": 32, "items": map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"id", "prompt", "choices", "required"},
		"properties": map[string]any{"id": map[string]any{"type": "string"}, "prompt": map[string]any{"type": "string"}, "choices": stringArray, "required": map[string]any{"type": "boolean"}},
	}}
	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object", "additionalProperties": false,
		"required": []string{"executionId", "runId", "nodeId", "attempt", "result"},
		"properties": map[string]any{
			"executionId": map[string]any{"type": "string", "const": executionID},
			"runId":       map[string]any{"type": "string", "const": spec.RunID},
			"nodeId":      map[string]any{"type": "string", "const": spec.NodeID},
			"attempt":     map[string]any{"type": "integer", "const": spec.Attempt},
			"result": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"status", "summary", "artifacts", "warnings", "checks", "questions"},
				"properties": map[string]any{
					"status":    map[string]any{"type": "string", "enum": []string{"succeeded", "failed", "needs_input", "indeterminate"}},
					"summary":   map[string]any{"type": "string", "minLength": 1, "maxLength": 16384},
					"artifacts": stringArray, "warnings": stringArray, "checks": stringArray,
					"questions": questions,
				},
			},
		},
	}
}

func completionPrompt(spec backend.AgentExecutionSpec, executionID string) string {
	identity, _ := json.Marshal(map[string]any{"executionId": executionID, "runId": spec.RunID, "nodeId": spec.NodeID, "attempt": spec.Attempt})
	return spec.Instructions + "\n\nFishyume completion protocol:\n" +
		"Return exactly one JSON object matching the provided output schema. Preserve every identity field exactly. " +
		"Put the task outcome in result; status must be succeeded, failed, needs_input, or indeterminate. " +
		"needs_input must include structured questions and end this one-shot process. Use empty arrays when there are no artifacts, warnings, checks, or questions.\n" +
		"FISHYUME_RESULT_IDENTITY=" + string(identity)
}

type readyRecord struct {
	ExecutionID string     `json:"executionId"`
	Child       processRef `json:"child"`
	StartedAt   time.Time  `json:"startedAt"`
}

type exitRecord struct {
	ExecutionID  string    `json:"executionId"`
	ChildPID     int       `json:"childPid"`
	Fingerprint  string    `json:"fingerprint"`
	ExitCode     int       `json:"exitCode"`
	ResultSHA256 string    `json:"resultSha256,omitempty"`
	ExitedAt     time.Time `json:"exitedAt"`
}

func readExitRecord(path string, data handleData) (exitRecord, bool, error) {
	var record exitRecord
	if err := readJSONFile(path, 64*1024, &record); err != nil {
		if os.IsNotExist(unwrapPathError(err)) {
			return exitRecord{}, false, nil
		}
		return exitRecord{}, false, fmt.Errorf("read Direct exit record: %w", err)
	}
	if record.ExecutionID != data.ExecutionID || record.ChildPID != data.Child.PID || record.Fingerprint != data.Child.Fingerprint {
		return exitRecord{}, true, fmt.Errorf("Direct exit record identity does not match the handle")
	}
	return record, true, nil
}
