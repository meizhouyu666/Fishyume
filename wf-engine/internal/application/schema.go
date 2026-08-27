package application

import "encoding/json"

var WorkflowJSONSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://fishyume.local/schema/workflow-v2.json",
  "type": "object",
  "additionalProperties": false,
  "required": ["apiVersion", "name", "execution", "nodes"],
  "properties": {
    "apiVersion": {"enum": ["fishyume/v1", "fishyume/v2"]},
    "name": {"type": "string", "minLength": 1},
    "inputs": {"type": "object"},
    "defaults": {
      "type": "object",
      "additionalProperties": false,
      "properties": {"agent": {"$ref": "#/$defs/agent"}}
    },
    "context": {"$ref": "#/$defs/context"},
    "execution": {
      "type": "object",
      "additionalProperties": false,
      "required": ["maxConcurrency"],
      "properties": {"maxConcurrency": {"type": "integer", "minimum": 1, "maximum": 32}}
    },
    "nodes": {
      "type": "object",
      "minProperties": 1,
      "additionalProperties": {"oneOf": [{"$ref": "#/$defs/agentNode"}, {"$ref": "#/$defs/approvalNode"}]}
    }
  },
  "$defs": {
    "routingRequirement": {
      "type": "object",
      "additionalProperties": false,
      "required": ["schemaVersion", "capabilities", "complexity", "quality", "latency", "maxCostUnits", "maxContextBytes", "maxOutputBytes"],
      "properties": {
        "schemaVersion": {"const": "fishyume.routing-requirement/v1"},
        "capabilities": {"type": "array", "minItems": 1, "maxItems": 6, "items": {"enum": ["needs_input", "repo_edit", "repo_read", "streaming", "structured_output", "tool_use"]}},
        "complexity": {"enum": ["simple", "standard", "complex"]},
        "quality": {"enum": ["economy", "balanced", "premium"]},
        "latency": {"enum": ["fast", "balanced", "slow"]},
        "maxCostUnits": {"type": "integer", "minimum": 1, "maximum": 1000000},
        "maxContextBytes": {"type": "integer", "minimum": 1, "maximum": 16777216},
        "maxOutputBytes": {"type": "integer", "minimum": 1, "maximum": 16777216},
        "candidates": {"type": "array", "maxItems": 32, "items": {"type": "string", "minLength": 1, "maxLength": 128}},
        "promptProfile": {"type": "string", "minLength": 1, "maxLength": 128},
        "allowModelFallback": {"type": "boolean"}
      }
    },
    "agent": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "driver": {"type": "string"},
        "target": {"type": "string"},
        "routing": {"$ref": "#/$defs/routingRequirement"}
      }
    },
    "context": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "projectInstructions": {"type": "array", "maxItems": 32, "items": {"type": "string", "minLength": 1}},
        "dependencies": {"type": "array", "items": {"type": "string", "minLength": 1}}
      }
    },
    "agentNode": {
      "type": "object",
      "additionalProperties": false,
      "required": ["type", "task"],
      "properties": {
        "type": {"const": "agent"},
        "task": {"type": "string", "minLength": 1},
        "dependsOn": {"type": "array", "items": {"type": "string"}},
        "context": {"$ref": "#/$defs/context"},
        "when": {"type": "object"},
        "agent": {"$ref": "#/$defs/agent"},
        "requiredSkills": {"type": "array", "items": {"type": "string"}}
      }
    },
    "approvalNode": {
      "type": "object",
      "additionalProperties": false,
      "required": ["type", "prompt"],
      "properties": {
        "type": {"const": "approval"},
        "prompt": {"type": "string", "minLength": 1},
        "dependsOn": {"type": "array", "items": {"type": "string"}},
        "when": {"type": "object"}
      }
    }
  }
}`)

var MinimalWorkflowExample = json.RawMessage(`{"apiVersion":"fishyume/v2","name":"agent-with-approval","defaults":{"agent":{"driver":"codex","target":"local"}},"context":{"projectInstructions":["AGENTS.md"]},"execution":{"maxConcurrency":1},"nodes":{"plan":{"type":"agent","task":"Create a plan","context":{"dependencies":[]}},"approve":{"type":"approval","dependsOn":["plan"],"prompt":"Approve the plan?"}}}`)

func StableAuthoringGuide() AuthoringGuide {
	return AuthoringGuide{
		SchemaVersion: AuthoringGuideVersion,
		RecommendedFlow: []string{
			"system.capabilities", "routing.catalog", "memory.list", "memory.get", "workflow.validate",
			"workflow.explain", "run.start", "run.events", "run.get", "run.action", "run.result",
		},
		WorkflowAPIVersion: WorkflowSchemaVersion,
		Rules: []string{
			"Author fishyume/v2; dependsOn controls scheduling while context.dependencies controls dependency-result injection.",
			"Inspect routing.catalog for the trusted static model capability catalog; it does not report live Provider availability or select a model.",
			"Agent routing is optional for compatibility; omitted routing uses the bounded standard/balanced default and never enables automatic fallback.",
			"Memory selection is explicit: pass identical contextBindings to workflow.validate, workflow.explain, and run.start.",
			"Pass identical workflow, inputs, driver, and target to workflow.validate, workflow.explain, and run.start.",
			"Reuse a clientRequestId only for an identical run.start request; a changed payload is a conflict.",
			"Read run.get before run.action and derive expectedStateVersion and expectedAttempt from the latest durable state.",
			"After run.start, open the returned Run in the optional Web client and yield control; do not make the Host Agent a background watcher or loop on run.events.",
			"Use run.events with afterSequence for one on-demand bounded read when the user requests progress or a pending action/terminal transition is expected; call run.get only after an event requires authoritative state.",
			"Use run.result only after a terminal event or an explicit user request; not_ready is not a reason to spin a polling loop.",
			"The attach value returned by run.start opens the human TUI on the same durable Run.",
		},
	}
}
