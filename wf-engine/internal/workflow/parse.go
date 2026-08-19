package workflow

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func Parse(data []byte, filename string, provided map[string]any) (Normalized, error) {
	jsonData, err := documentJSON(data, filename)
	if err != nil {
		return Normalized{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(jsonData))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var doc Document
	if err := decoder.Decode(&doc); err != nil {
		return Normalized{}, fmt.Errorf("decode workflow: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return Normalized{}, err
	}
	warnings, err := normalizeDocument(&doc)
	if err != nil {
		return Normalized{}, err
	}
	order, err := Validate(doc)
	if err != nil {
		return Normalized{}, err
	}
	inputs, err := ResolveInputs(doc, provided)
	if err != nil {
		return Normalized{}, err
	}
	policyVersion := "context-policy/legacy"
	if doc.APIVersion == ContextPolicyAPIVersion {
		policyVersion = "context-policy/v1"
	}
	return Normalized{Document: doc, Inputs: inputs, TopologicalOrder: order, Warnings: warnings, ContextPolicyVersion: policyVersion}, nil
}

func documentJSON(data []byte, filename string) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(filename))
	if ext == ".json" || (ext == "" && firstNonSpace(data) == '{') {
		return data, nil
	}
	if ext != "" && ext != ".yaml" && ext != ".yml" {
		return nil, fmt.Errorf("unsupported workflow extension %q", ext)
	}
	var node yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&node); err != nil {
		return nil, fmt.Errorf("decode workflow YAML: %w", err)
	}
	if err := rejectDuplicateYAMLKeys(&node); err != nil {
		return nil, err
	}
	var value any
	if err := node.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode workflow YAML value: %w", err)
	}
	converted, err := yamlValueToJSON(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(converted)
}

func firstNonSpace(data []byte) byte {
	for _, value := range data {
		if value != ' ' && value != '\t' && value != '\r' && value != '\n' {
			return value
		}
	}
	return 0
}

func rejectDuplicateYAMLKeys(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]bool, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if seen[key.Value] {
				return fmt.Errorf("duplicate YAML key %q at line %d", key.Value, key.Line)
			}
			seen[key.Value] = true
		}
	}
	for _, child := range node.Content {
		if err := rejectDuplicateYAMLKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func yamlValueToJSON(value any) (any, error) {
	switch value := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			converted, err := yamlValueToJSON(item)
			if err != nil {
				return nil, err
			}
			result[key] = converted
		}
		return result, nil
	case map[any]any:
		result := make(map[string]any, len(value))
		for key, item := range value {
			text, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("YAML mapping key must be a string")
			}
			converted, err := yamlValueToJSON(item)
			if err != nil {
				return nil, err
			}
			result[text] = converted
		}
		return result, nil
	case []any:
		result := make([]any, len(value))
		for index, item := range value {
			converted, err := yamlValueToJSON(item)
			if err != nil {
				return nil, err
			}
			result[index] = converted
		}
		return result, nil
	case nil, string, bool, int, int64, uint64, float64:
		return value, nil
	default:
		return nil, fmt.Errorf("unsupported YAML value type %T", value)
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("workflow contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing workflow data: %w", err)
	}
	return nil
}

func normalizeDocument(doc *Document) ([]string, error) {
	if doc.APIVersion != APIVersion && doc.APIVersion != ContextPolicyAPIVersion && doc.APIVersion != LegacyAPIVersion {
		return nil, fmt.Errorf("unsupported apiVersion %q", doc.APIVersion)
	}
	warnings := make([]string, 0, 2)
	if doc.APIVersion == LegacyAPIVersion {
		doc.APIVersion = APIVersion
	}
	if doc.Inputs == nil {
		doc.Inputs = map[string]InputDeclaration{}
	}
	for name, declaration := range doc.Inputs {
		if !nodeIDPattern.MatchString(name) {
			return nil, fmt.Errorf("invalid input name %q", name)
		}
		if declaration.Default != nil {
			value, err := normalizeScalar(declaration.Default)
			if err != nil {
				return nil, fmt.Errorf("input %q default: %w", name, err)
			}
			declaration.Default = value
			doc.Inputs[name] = declaration
		}
	}
	if doc.Defaults.Backend != "" || doc.Defaults.Tool != "" || doc.Defaults.Runtime != "" {
		driver, target, err := ResolveAgent(doc.Defaults, Node{})
		if err != nil {
			return nil, err
		}
		doc.Defaults.Agent = AgentSelection{Driver: driver, Target: target}
		doc.Defaults.Backend, doc.Defaults.Tool, doc.Defaults.Runtime = "", "", ""
		warnings = append(warnings, "defaults.backend/tool/runtime are deprecated; use defaults.agent.driver/target")
	}
	for id, node := range doc.Nodes {
		if node.DependsOn == nil {
			node.DependsOn = []string{}
		}
		if node.RequiredSkills == nil {
			node.RequiredSkills = []string{}
		}
		if node.Tool != "" || node.Runtime != "" {
			driver, target, err := ResolveAgent(doc.Defaults, node)
			if err != nil {
				return nil, fmt.Errorf("node %q: %w", id, err)
			}
			node.Agent = AgentSelection{Driver: driver, Target: target}
			node.Tool, node.Runtime = "", ""
			warnings = append(warnings, fmt.Sprintf("node %q tool/runtime are deprecated; use agent.driver/target", id))
		}
		doc.Nodes[id] = node
	}
	return warnings, nil
}
