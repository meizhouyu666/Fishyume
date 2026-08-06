package run

import "encoding/json"

type legacySessionSnapshot struct {
	ID       string            `json:"id"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type legacyExecutionSnapshot struct {
	SessionID string
	Metadata  map[string]string
}

// UnmarshalJSON accepts M2.1.1 Attempt fields without keeping CC-Panes
// concepts in the current persisted model. Marshal uses only AttemptSnapshot's
// exported generic fields, so old session/TaskBinding fields are never written
// back by the M2.1.2 Engine.
func (snapshot *AttemptSnapshot) UnmarshalJSON(data []byte) error {
	type attemptAlias AttemptSnapshot
	var wire struct {
		attemptAlias
		Backend         string                 `json:"backend,omitempty"`
		Runtime         string                 `json:"runtime,omitempty"`
		PromptHash      string                 `json:"promptHash,omitempty"`
		Session         *legacySessionSnapshot `json:"session,omitempty"`
		TaskBindingID   string                 `json:"taskBindingId,omitempty"`
		LaunchMetadata  map[string]string      `json:"launchMetadata,omitempty"`
		BindingConsumed bool                   `json:"bindingConsumed,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*snapshot = AttemptSnapshot(wire.attemptAlias)
	if snapshot.ResolvedDriver == "" {
		snapshot.ResolvedDriver = legacyDriverName(wire.Backend)
	}
	if snapshot.ResolvedTarget == "" {
		snapshot.ResolvedTarget = wire.Runtime
	}
	if snapshot.ResolvedTarget == "" {
		snapshot.ResolvedTarget = "local"
	}
	snapshot.Backend = snapshot.ResolvedDriver
	if snapshot.ContextHash == "" {
		snapshot.ContextHash = wire.PromptHash
	}
	snapshot.PromptHash = snapshot.ContextHash
	if snapshot.Execution != nil && snapshot.Execution.DriverName() == "direct" && snapshot.ResolvedDriver == "codex" {
		snapshot.Execution.Driver, snapshot.Execution.Backend = "codex", "codex"
	}
	switch snapshot.LaunchState {
	case LaunchSessionPersisted:
		snapshot.LaunchState = LaunchHandlePersisted
	case LaunchFinishedWithoutSession:
		snapshot.LaunchState = LaunchFinishedWithoutHandle
	}
	if wire.BindingConsumed {
		snapshot.ResultConsumed = true
	}
	if wire.Session == nil || wire.Session.ID == "" {
		return nil
	}
	metadata := make(map[string]string, len(wire.Session.Metadata)+len(wire.LaunchMetadata)+1)
	for key, value := range wire.LaunchMetadata {
		metadata[key] = value
	}
	for key, value := range wire.Session.Metadata {
		metadata[key] = value
	}
	if wire.TaskBindingID != "" && metadata["bindingId"] == "" {
		metadata["bindingId"] = wire.TaskBindingID
	}
	snapshot.legacyExecution = &legacyExecutionSnapshot{SessionID: wire.Session.ID, Metadata: metadata}
	return nil
}

func (snapshot AttemptSnapshot) MarshalJSON() ([]byte, error) {
	type attemptAlias AttemptSnapshot
	copy := snapshot
	copy.ResolvedDriver = attemptDriver(snapshot)
	copy.ResolvedTarget = attemptTarget(snapshot)
	copy.Backend = ""
	if copy.ContextHash == "" {
		copy.ContextHash = copy.PromptHash
	}
	copy.PromptHash = ""
	return json.Marshal(attemptAlias(copy))
}
