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
		Session         *legacySessionSnapshot `json:"session,omitempty"`
		TaskBindingID   string                 `json:"taskBindingId,omitempty"`
		LaunchMetadata  map[string]string      `json:"launchMetadata,omitempty"`
		BindingConsumed bool                   `json:"bindingConsumed,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*snapshot = AttemptSnapshot(wire.attemptAlias)
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
