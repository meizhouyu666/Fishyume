package run

import "encoding/json"

// UnmarshalJSON keeps the bounded wire compatibility needed to read older
// Attempts that used backend/runtime/promptHash field names. Deprecated
// execution-specific fields are intentionally ignored and are never written.
func (snapshot *AttemptSnapshot) UnmarshalJSON(data []byte) error {
	type attemptAlias AttemptSnapshot
	var wire struct {
		attemptAlias
		Backend    string `json:"backend,omitempty"`
		Runtime    string `json:"runtime,omitempty"`
		PromptHash string `json:"promptHash,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*snapshot = AttemptSnapshot(wire.attemptAlias)
	if snapshot.ResolvedDriver == "" {
		snapshot.ResolvedDriver = wire.Backend
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
