package run

import "encoding/json"

func legacyDriverName(name string) string {
	if name == "direct" {
		return "codex"
	}
	return name
}

func runDriver(snapshot WorkflowSnapshot) string {
	if snapshot.ResolvedDriver != "" {
		return snapshot.ResolvedDriver
	}
	return snapshot.Backend
}

func runTarget(snapshot WorkflowSnapshot) string {
	if snapshot.ResolvedTarget != "" {
		return snapshot.ResolvedTarget
	}
	return "local"
}

func attemptDriver(snapshot AttemptSnapshot) string {
	if snapshot.ResolvedDriver != "" {
		return snapshot.ResolvedDriver
	}
	return snapshot.Backend
}

func attemptTarget(snapshot AttemptSnapshot) string {
	if snapshot.ResolvedTarget != "" {
		return snapshot.ResolvedTarget
	}
	return "local"
}

func (snapshot WorkflowSnapshot) MarshalJSON() ([]byte, error) {
	type alias WorkflowSnapshot
	copy := snapshot
	copy.ResolvedDriver = runDriver(snapshot)
	copy.ResolvedTarget = runTarget(snapshot)
	copy.Backend = ""
	return json.Marshal(alias(copy))
}

func (snapshot *WorkflowSnapshot) UnmarshalJSON(data []byte) error {
	type alias WorkflowSnapshot
	var wire struct {
		alias
		Backend string `json:"backend,omitempty"`
		Runtime string `json:"runtime,omitempty"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*snapshot = WorkflowSnapshot(wire.alias)
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
	return nil
}
