package contextcompiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"wf.local/wf-engine/internal/agent"
	"wf.local/wf-engine/internal/workflow"
)

const (
	Version                 = "context-compiler/v1"
	MaxCompiledContextBytes = 128 * 1024
)

type Component struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Version string `json:"version"`
}

type Manifest struct {
	CompilerVersion string      `json:"compilerVersion"`
	Components      []Component `json:"components"`
}

type Input struct {
	Identity        agent.AttemptIdentity
	Workspace       string
	Task            string
	AncestorResults map[string]workflow.Result
	RequiredSkills  []string
	Constraints     map[string]string
	Budget          map[string]int64
	ResultSchema    json.RawMessage
	ResultMaxBytes  int
	InputAnswer     json.RawMessage
}

type Compilation struct {
	Envelope agent.AttemptEnvelope
	Manifest Manifest
	Hash     string
	Prompt   string
}

func Compile(input Input) (Compilation, error) {
	nodeIDs := make([]string, 0, len(input.AncestorResults))
	for nodeID := range input.AncestorResults {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)
	upstream := make([]agent.UpstreamResult, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		encoded, err := json.Marshal(input.AncestorResults[nodeID])
		if err != nil {
			return Compilation{}, fmt.Errorf("encode upstream result %q: %w", nodeID, err)
		}
		upstream = append(upstream, agent.UpstreamResult{NodeID: nodeID, Result: encoded})
	}
	skills := make([]string, len(input.RequiredSkills))
	copy(skills, input.RequiredSkills)
	sort.Strings(skills)
	constraints := copyStrings(input.Constraints)
	budget := copyInts(input.Budget)
	envelope := agent.AttemptEnvelope{
		ProtocolVersion: agent.ProtocolVersion,
		Identity:        input.Identity,
		Workspace:       input.Workspace,
		Task:            input.Task,
		Context: agent.AttemptContext{
			UpstreamResults: upstream,
			RequiredSkills:  skills,
			InputAnswer:     append(json.RawMessage(nil), input.InputAnswer...),
		},
		Constraints: constraints,
		Budget:      budget,
		ResultContract: agent.ResultContract{
			Schema:   append(json.RawMessage(nil), input.ResultSchema...),
			MaxBytes: input.ResultMaxBytes,
		},
	}
	if err := agent.ValidateAttemptEnvelope(envelope); err != nil {
		return Compilation{}, err
	}
	manifest := Manifest{CompilerVersion: Version, Components: []Component{
		{Name: "execution-contract", Source: "fishyume-core", Version: "v1"},
		{Name: "node-task", Source: "workflow.node.task", Version: "v1"},
		{Name: "ancestor-results", Source: "workflow.ancestors", Version: "v1"},
		{Name: "required-skills", Source: "workflow.node.requiredSkills", Version: "v1"},
		{Name: "attempt-identity", Source: "engine.attempt", Version: "v1"},
		{Name: "result-contract", Source: "workflow.result", Version: "v1"},
	}}
	canonical, err := json.Marshal(struct {
		Envelope agent.AttemptEnvelope `json:"envelope"`
		Manifest Manifest              `json:"manifest"`
	}{Envelope: envelope, Manifest: manifest})
	if err != nil {
		return Compilation{}, err
	}
	if len(canonical) > MaxCompiledContextBytes {
		return Compilation{}, fmt.Errorf("compiled context exceeds %d bytes", MaxCompiledContextBytes)
	}
	hash := sha256.Sum256(canonical)
	prompt := "Fishyume headless Attempt envelope (protocol v1):\n" + string(canonical) +
		"\n\nExecute the task with the declared constraints. Return exactly one structured result matching resultContract. " +
		"Use status succeeded, failed, needs_input, or indeterminate. needs_input must include at least one structured question and must end this one-shot process."
	envelope.Prompt = prompt
	return Compilation{Envelope: envelope, Manifest: manifest, Hash: hex.EncodeToString(hash[:]), Prompt: prompt}, nil
}

func copyStrings(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func copyInts(source map[string]int64) map[string]int64 {
	result := make(map[string]int64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
