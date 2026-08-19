package application

import "encoding/json"

var WorkflowJSONSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"https://fishyume.local/schema/workflow-v2.json","type":"object","additionalProperties":false,"required":["apiVersion","name","execution","nodes"],"properties":{"apiVersion":{"enum":["fishyume/v1","fishyume/v2"]},"name":{"type":"string","minLength":1},"inputs":{"type":"object"},"defaults":{"type":"object","additionalProperties":false,"properties":{"agent":{"$ref":"#/$defs/agent"}}},"context":{"$ref":"#/$defs/context"},"execution":{"type":"object","additionalProperties":false,"required":["maxConcurrency"],"properties":{"maxConcurrency":{"type":"integer","minimum":1,"maximum":32}}},"nodes":{"type":"object","minProperties":1,"additionalProperties":{"oneOf":[{"$ref":"#/$defs/agentNode"},{"$ref":"#/$defs/approvalNode"}]}}},"$defs":{"agent":{"type":"object","additionalProperties":false,"properties":{"driver":{"type":"string"},"target":{"type":"string"}}},"context":{"type":"object","additionalProperties":false,"properties":{"projectInstructions":{"type":"array","maxItems":32,"items":{"type":"string","minLength":1}},"dependencies":{"type":"array","items":{"type":"string","minLength":1}}}},"agentNode":{"type":"object","additionalProperties":false,"required":["type","task"],"properties":{"type":{"const":"agent"},"task":{"type":"string","minLength":1},"dependsOn":{"type":"array","items":{"type":"string"}},"context":{"$ref":"#/$defs/context"},"when":{"type":"object"},"agent":{"$ref":"#/$defs/agent"},"requiredSkills":{"type":"array","items":{"type":"string"}}}},"approvalNode":{"type":"object","additionalProperties":false,"required":["type","prompt"],"properties":{"type":{"const":"approval"},"prompt":{"type":"string","minLength":1},"dependsOn":{"type":"array","items":{"type":"string"}},"when":{"type":"object"}}}}}`)

var MinimalWorkflowExample = json.RawMessage(`{"apiVersion":"fishyume/v2","name":"agent-with-approval","defaults":{"agent":{"driver":"codex","target":"local"}},"context":{"projectInstructions":["AGENTS.md"]},"execution":{"maxConcurrency":1},"nodes":{"plan":{"type":"agent","task":"Create a plan","context":{"dependencies":[]}},"approve":{"type":"approval","dependsOn":["plan"],"prompt":"Approve the plan?"}}}`)

func StableAuthoringGuide() AuthoringGuide {
	return AuthoringGuide{
		SchemaVersion: AuthoringGuideVersion,
		RecommendedFlow: []string{
			"system.capabilities", "memory.list", "memory.get", "workflow.validate",
			"workflow.explain", "run.start", "run.events", "run.get", "run.action", "run.result",
		},
		WorkflowAPIVersion: WorkflowSchemaVersion,
		Rules: []string{
			"Author fishyume/v2; dependsOn controls scheduling while context.dependencies controls dependency-result injection.",
			"Memory selection is explicit: pass identical contextBindings to workflow.validate, workflow.explain, and run.start.",
			"Pass identical workflow, inputs, driver, and target to workflow.validate, workflow.explain, and run.start.",
			"Reuse a clientRequestId only for an identical run.start request; a changed payload is a conflict.",
			"Read run.get before run.action and derive expectedStateVersion and expectedAttempt from the latest durable state.",
			"Use run.events with afterSequence for bounded pagination, then run.get for authoritative current state.",
			"Use run.result only after the Run is terminal; not_ready means continue observing.",
			"The attach value returned by run.start opens the human TUI on the same durable Run.",
		},
	}
}
