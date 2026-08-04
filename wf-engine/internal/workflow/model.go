package workflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	APIVersion       = "fishyume/v1"
	LegacyAPIVersion = "wf/v1"
	MaxConcurrency   = 1
	MaxSummaryBytes  = 16 * 1024
	MaxPromptBytes   = 128 * 1024
	MaxResultBytes   = 64 * 1024
	MaxResultItems   = 256
	MaxArtifactBytes = 4096
)

var nodeIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

type Document struct {
	APIVersion string                      `json:"apiVersion" yaml:"apiVersion"`
	Name       string                      `json:"name" yaml:"name"`
	Inputs     map[string]InputDeclaration `json:"inputs,omitempty" yaml:"inputs,omitempty"`
	Defaults   Defaults                    `json:"defaults,omitempty" yaml:"defaults,omitempty"`
	Execution  Execution                   `json:"execution" yaml:"execution"`
	Nodes      map[string]Node             `json:"nodes" yaml:"nodes"`
}

type InputDeclaration struct {
	Required bool `json:"required,omitempty" yaml:"required,omitempty"`
	Default  any  `json:"default,omitempty" yaml:"default,omitempty"`
}

type Defaults struct {
	Tool    string `json:"tool,omitempty" yaml:"tool,omitempty"`
	Runtime string `json:"runtime,omitempty" yaml:"runtime,omitempty"`
}

type Execution struct {
	MaxConcurrency int `json:"maxConcurrency" yaml:"maxConcurrency"`
}

type Node struct {
	Type           string     `json:"type" yaml:"type"`
	DependsOn      []string   `json:"dependsOn,omitempty" yaml:"dependsOn,omitempty"`
	When           *Condition `json:"when,omitempty" yaml:"when,omitempty"`
	Task           string     `json:"task,omitempty" yaml:"task,omitempty"`
	Prompt         string     `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Tool           string     `json:"tool,omitempty" yaml:"tool,omitempty"`
	Runtime        string     `json:"runtime,omitempty" yaml:"runtime,omitempty"`
	RequiredSkills []string   `json:"requiredSkills,omitempty" yaml:"requiredSkills,omitempty"`
}

type Condition struct {
	Node   string      `json:"node,omitempty" yaml:"node,omitempty"`
	Field  string      `json:"field,omitempty" yaml:"field,omitempty"`
	Equals any         `json:"equals,omitempty" yaml:"equals,omitempty"`
	All    []Condition `json:"all,omitempty" yaml:"all,omitempty"`
	Any    []Condition `json:"any,omitempty" yaml:"any,omitempty"`
	Not    *Condition  `json:"not,omitempty" yaml:"not,omitempty"`
}

type Result struct {
	Summary   string   `json:"summary,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
	Checks    []string `json:"checks,omitempty"`
	Usage     Usage    `json:"usage,omitempty"`
	Decision  string   `json:"decision,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}

type Usage struct {
	InputTokensEstimated  int `json:"inputTokensEstimated,omitempty"`
	OutputTokensEstimated int `json:"outputTokensEstimated,omitempty"`
}

type Normalized struct {
	Document         Document       `json:"document"`
	Inputs           map[string]any `json:"inputs,omitempty"`
	TopologicalOrder []string       `json:"topologicalOrder"`
}

func (d Document) CanonicalJSON() ([]byte, error) {
	return json.MarshalIndent(d, "", "  ")
}

func (n Normalized) CanonicalJSON() ([]byte, error) {
	return json.MarshalIndent(n, "", "  ")
}

func ResolveInputs(doc Document, provided map[string]any) (map[string]any, error) {
	resolved := make(map[string]any, len(doc.Inputs))
	for name := range provided {
		if _, ok := doc.Inputs[name]; !ok {
			return nil, fmt.Errorf("input %q is not declared", name)
		}
	}
	for name, declaration := range doc.Inputs {
		value, ok := provided[name]
		if !ok && declaration.Default != nil {
			value, ok = declaration.Default, true
		}
		if !ok {
			if declaration.Required {
				return nil, fmt.Errorf("required input %q is missing", name)
			}
			continue
		}
		normalized, err := normalizeScalar(value)
		if err != nil {
			return nil, fmt.Errorf("input %q: %w", name, err)
		}
		if declaration.Default != nil {
			defaultValue, err := normalizeScalar(declaration.Default)
			if err != nil {
				return nil, fmt.Errorf("input %q default: %w", name, err)
			}
			if scalarKind(defaultValue) != scalarKind(normalized) {
				return nil, fmt.Errorf("input %q has type %s, want %s", name, scalarKind(normalized), scalarKind(defaultValue))
			}
		}
		resolved[name] = normalized
	}
	return resolved, nil
}

func normalizeScalar(value any) (any, error) {
	switch value := value.(type) {
	case string, bool, json.Number:
		return value, nil
	case float64:
		return json.Number(strconv.FormatFloat(value, 'g', -1, 64)), nil
	case float32:
		return json.Number(strconv.FormatFloat(float64(value), 'g', -1, 32)), nil
	case int:
		return json.Number(strconv.FormatInt(int64(value), 10)), nil
	case int8:
		return json.Number(strconv.FormatInt(int64(value), 10)), nil
	case int16:
		return json.Number(strconv.FormatInt(int64(value), 10)), nil
	case int32:
		return json.Number(strconv.FormatInt(int64(value), 10)), nil
	case int64:
		return json.Number(strconv.FormatInt(value, 10)), nil
	case uint:
		return json.Number(strconv.FormatUint(uint64(value), 10)), nil
	case uint8:
		return json.Number(strconv.FormatUint(uint64(value), 10)), nil
	case uint16:
		return json.Number(strconv.FormatUint(uint64(value), 10)), nil
	case uint32:
		return json.Number(strconv.FormatUint(uint64(value), 10)), nil
	case uint64:
		return json.Number(strconv.FormatUint(value, 10)), nil
	case nil:
		return nil, fmt.Errorf("null is not a supported input scalar")
	default:
		return nil, fmt.Errorf("unsupported scalar type %T", value)
	}
}

func scalarKind(value any) string {
	switch value.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	default:
		return "unknown"
	}
}

func TopologicalOrder(doc Document) ([]string, error) {
	indegree := make(map[string]int, len(doc.Nodes))
	children := make(map[string][]string, len(doc.Nodes))
	for id, node := range doc.Nodes {
		indegree[id] = len(node.DependsOn)
		for _, dependency := range node.DependsOn {
			children[dependency] = append(children[dependency], id)
		}
	}
	ready := make([]string, 0, len(doc.Nodes))
	for id, degree := range indegree {
		if degree == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)
	order := make([]string, 0, len(doc.Nodes))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, child := range children[id] {
			indegree[child]--
			if indegree[child] == 0 {
				ready = append(ready, child)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(doc.Nodes) {
		return nil, fmt.Errorf("workflow contains a dependency cycle")
	}
	return order, nil
}

func ancestors(doc Document, nodeID string) map[string]bool {
	result := make(map[string]bool)
	var visit func(string)
	visit = func(id string) {
		for _, dependency := range doc.Nodes[id].DependsOn {
			if !result[dependency] {
				result[dependency] = true
				visit(dependency)
			}
		}
	}
	visit(nodeID)
	return result
}

func ValidateResult(result Result) error {
	if strings.TrimSpace(result.Summary) == "" {
		return fmt.Errorf("result summary is required")
	}
	if len([]byte(result.Summary)) > MaxSummaryBytes {
		return fmt.Errorf("result summary exceeds %d bytes", MaxSummaryBytes)
	}
	if len(result.Artifacts) > MaxResultItems || len(result.Warnings) > MaxResultItems || len(result.Checks) > MaxResultItems {
		return fmt.Errorf("result list exceeds %d items", MaxResultItems)
	}
	for _, artifact := range result.Artifacts {
		if len([]byte(artifact)) > MaxArtifactBytes {
			return fmt.Errorf("artifact identifier exceeds %d bytes", MaxArtifactBytes)
		}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode result: %w", err)
	}
	if len(data) > MaxResultBytes {
		return fmt.Errorf("result exceeds %d bytes", MaxResultBytes)
	}
	return nil
}
