package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var templateReferencePattern = regexp.MustCompile(`\{\{\s*([A-Za-z][A-Za-z0-9_-]*(?:\.[A-Za-z][A-Za-z0-9_-]*)+)\s*\}\}`)

type Template struct {
	text       string
	references []string
}

func ParseTemplate(text string, inputs map[string]InputDeclaration, ancestors map[string]bool) (Template, error) {
	matches := templateReferencePattern.FindAllStringSubmatchIndex(text, -1)
	references := make([]string, 0, len(matches))
	covered := make([]bool, len(text))
	for _, match := range matches {
		for index := match[0]; index < match[1]; index++ {
			covered[index] = true
		}
		path := text[match[2]:match[3]]
		if err := validateReference(path, inputs, ancestors); err != nil {
			return Template{}, err
		}
		references = append(references, path)
	}
	for index := 0; index+1 < len(text); index++ {
		if text[index:index+2] == "{{" && !covered[index] {
			return Template{}, fmt.Errorf("unsupported template expression near %q", text[index:])
		}
		if text[index:index+2] == "}}" && !covered[index] {
			return Template{}, fmt.Errorf("unmatched template closing delimiter")
		}
	}
	return Template{text: text, references: references}, nil
}

func validateReference(path string, inputs map[string]InputDeclaration, ancestors map[string]bool) error {
	parts := strings.Split(path, ".")
	if len(parts) == 2 && parts[0] == "inputs" {
		if _, ok := inputs[parts[1]]; !ok {
			return fmt.Errorf("template references undeclared input %q", parts[1])
		}
		return nil
	}
	if len(parts) >= 4 && parts[0] == "nodes" {
		if !ancestors[parts[1]] {
			return fmt.Errorf("template node %q is not an ancestor", parts[1])
		}
		field := strings.Join(parts[2:], ".")
		if !allowedResultFields[field] {
			return fmt.Errorf("template result field %q is not public", field)
		}
		return nil
	}
	return fmt.Errorf("unsupported template reference %q", path)
}

func (t Template) References() []string { return append([]string(nil), t.references...) }

func (t Template) Render(inputs map[string]any, results map[string]Result) (string, error) {
	var renderErr error
	rendered := templateReferencePattern.ReplaceAllStringFunc(t.text, func(expression string) string {
		if renderErr != nil {
			return ""
		}
		match := templateReferencePattern.FindStringSubmatch(expression)
		value, err := referenceValue(match[1], inputs, results)
		if err != nil {
			renderErr = err
			return ""
		}
		text, err := renderValue(value)
		if err != nil {
			renderErr = err
			return ""
		}
		return text
	})
	if renderErr != nil {
		return "", renderErr
	}
	if len([]byte(rendered)) > MaxPromptBytes {
		return "", fmt.Errorf("rendered prompt exceeds %d bytes", MaxPromptBytes)
	}
	return rendered, nil
}

func referenceValue(path string, inputs map[string]any, results map[string]Result) (any, error) {
	parts := strings.Split(path, ".")
	if parts[0] == "inputs" {
		value, ok := inputs[parts[1]]
		if !ok {
			return nil, fmt.Errorf("input reference %q is missing", path)
		}
		return value, nil
	}
	result, ok := results[parts[1]]
	if !ok {
		return nil, fmt.Errorf("node result reference %q is missing", path)
	}
	switch strings.Join(parts[2:], ".") {
	case "result.summary":
		if result.Summary == "" {
			return nil, fmt.Errorf("node result reference %q is missing", path)
		}
		return result.Summary, nil
	case "result.artifacts":
		return result.Artifacts, nil
	case "result.warnings":
		return result.Warnings, nil
	case "result.checks":
		return result.Checks, nil
	case "result.decision":
		if result.Decision == "" {
			return nil, fmt.Errorf("node result reference %q is missing", path)
		}
		return result.Decision, nil
	case "result.reason":
		return result.Reason, nil
	case "result.usage.inputTokensEstimated":
		return result.Usage.InputTokensEstimated, nil
	case "result.usage.outputTokensEstimated":
		return result.Usage.OutputTokensEstimated, nil
	default:
		return nil, fmt.Errorf("unsupported result reference %q", path)
	}
}

func renderValue(value any) (string, error) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("render reference: %w", err)
	}
	return string(data), nil
}

func Evaluate(condition Condition, results map[string]Result) (bool, error) {
	if condition.Not != nil {
		value, err := Evaluate(*condition.Not, results)
		return !value, err
	}
	if condition.All != nil {
		for _, child := range condition.All {
			value, err := Evaluate(child, results)
			if err != nil || !value {
				return value, err
			}
		}
		return true, nil
	}
	if condition.Any != nil {
		for _, child := range condition.Any {
			value, err := Evaluate(child, results)
			if err != nil {
				return false, err
			}
			if value {
				return true, nil
			}
		}
		return false, nil
	}
	value, err := referenceValue("nodes."+condition.Node+"."+condition.Field, nil, results)
	if err != nil {
		return false, err
	}
	want, err := normalizeScalar(condition.Equals)
	if err != nil {
		return false, err
	}
	left, _ := json.Marshal(value)
	right, _ := json.Marshal(want)
	return string(left) == string(right), nil
}
