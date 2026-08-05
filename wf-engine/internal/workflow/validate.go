package workflow

import (
	"fmt"
	"strings"
)

var allowedResultFields = map[string]bool{
	"result.summary": true, "result.artifacts": true, "result.warnings": true,
	"result.checks": true, "result.decision": true, "result.reason": true,
	"result.usage.inputTokensEstimated": true, "result.usage.outputTokensEstimated": true,
}

func Validate(doc Document) ([]string, error) {
	if doc.APIVersion != APIVersion && doc.APIVersion != LegacyAPIVersion {
		return nil, fmt.Errorf("unsupported apiVersion %q", doc.APIVersion)
	}
	if strings.TrimSpace(doc.Name) == "" {
		return nil, fmt.Errorf("workflow name is required")
	}
	if doc.Execution.MaxConcurrency < 1 || doc.Execution.MaxConcurrency > MaxAllowedConcurrency {
		return nil, fmt.Errorf("maxConcurrency must be between 1 and %d", MaxAllowedConcurrency)
	}
	if len(doc.Nodes) == 0 {
		return nil, fmt.Errorf("workflow must contain at least one node")
	}
	if err := validateToolRuntime(doc.Defaults.Tool, doc.Defaults.Runtime, "workflow defaults"); err != nil {
		return nil, err
	}
	for id, node := range doc.Nodes {
		if !nodeIDPattern.MatchString(id) {
			return nil, fmt.Errorf("invalid node id %q", id)
		}
		seenDependencies := map[string]bool{}
		for _, dependency := range node.DependsOn {
			if _, ok := doc.Nodes[dependency]; !ok {
				return nil, fmt.Errorf("node %q depends on missing node %q", id, dependency)
			}
			if dependency == id {
				return nil, fmt.Errorf("node %q cannot depend on itself", id)
			}
			if seenDependencies[dependency] {
				return nil, fmt.Errorf("node %q repeats dependency %q", id, dependency)
			}
			seenDependencies[dependency] = true
		}
		switch node.Type {
		case "agent":
			if strings.TrimSpace(node.Task) == "" {
				return nil, fmt.Errorf("agent node %q requires task", id)
			}
			if node.Prompt != "" {
				return nil, fmt.Errorf("agent node %q cannot define prompt", id)
			}
			if err := validateToolRuntime(node.Tool, node.Runtime, "node "+id); err != nil {
				return nil, err
			}
		case "approval":
			if strings.TrimSpace(node.Prompt) == "" {
				return nil, fmt.Errorf("approval node %q requires prompt", id)
			}
			if node.Task != "" || node.Tool != "" || node.Runtime != "" || len(node.RequiredSkills) > 0 {
				return nil, fmt.Errorf("approval node %q contains agent-only fields", id)
			}
		default:
			return nil, fmt.Errorf("node %q has unknown type %q", id, node.Type)
		}
		for _, skill := range node.RequiredSkills {
			if strings.TrimSpace(skill) == "" {
				return nil, fmt.Errorf("node %q contains an empty required skill", id)
			}
		}
	}
	order, err := TopologicalOrder(doc)
	if err != nil {
		return nil, err
	}
	for id, node := range doc.Nodes {
		ancestors := ancestors(doc, id)
		if node.When != nil {
			if err := validateCondition(*node.When, ancestors); err != nil {
				return nil, fmt.Errorf("node %q condition: %w", id, err)
			}
		}
		template := node.Task
		if node.Type == "approval" {
			template = node.Prompt
		}
		if _, err := ParseTemplate(template, doc.Inputs, ancestors); err != nil {
			return nil, fmt.Errorf("node %q template: %w", id, err)
		}
	}
	return order, nil
}

func validateToolRuntime(tool, runtimeKind, owner string) error {
	if tool != "" && tool != "codex" && tool != "claude" && tool != "opencode" {
		return fmt.Errorf("%s has unsupported tool %q", owner, tool)
	}
	if runtimeKind != "" && runtimeKind != "local" && runtimeKind != "wsl" && runtimeKind != "ssh" {
		return fmt.Errorf("%s has unsupported runtime %q", owner, runtimeKind)
	}
	return nil
}

func validateCondition(condition Condition, ancestors map[string]bool) error {
	forms := 0
	compare := condition.Node != "" || condition.Field != "" || condition.Equals != nil
	if compare {
		forms++
	}
	if condition.All != nil {
		forms++
	}
	if condition.Any != nil {
		forms++
	}
	if condition.Not != nil {
		forms++
	}
	if forms != 1 {
		return fmt.Errorf("condition must contain exactly one of compare, all, any, or not")
	}
	if compare {
		if condition.Node == "" || condition.Field == "" || condition.Equals == nil {
			return fmt.Errorf("compare requires node, field, and equals")
		}
		if !ancestors[condition.Node] {
			return fmt.Errorf("node %q is not an ancestor", condition.Node)
		}
		if !allowedResultFields[condition.Field] {
			return fmt.Errorf("field %q is not public", condition.Field)
		}
		if _, err := normalizeScalar(condition.Equals); err != nil {
			return fmt.Errorf("equals: %w", err)
		}
		return nil
	}
	children := condition.All
	if condition.Any != nil {
		children = condition.Any
	}
	if children != nil {
		if len(children) == 0 {
			return fmt.Errorf("condition group cannot be empty")
		}
		for _, child := range children {
			if err := validateCondition(child, ancestors); err != nil {
				return err
			}
		}
		return nil
	}
	return validateCondition(*condition.Not, ancestors)
}
