package agent

import (
	"fmt"
	"strings"
)

const (
	ToolSlackAction       = "slack_action"
	ToolAuthorizeResource = "authorize_resource"
)

func ToolList() []ToolSpec {
	return []ToolSpec{
		{
			Name:        ToolSlackAction,
			Description: "Perform a Slack message action (send/edit/delete/react/dm)",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type": "string",
						"enum": []string{"send", "edit", "delete", "react", "dm"},
					},
					"channel":        map[string]any{"type": "string"},
					"thread_ts":      map[string]any{"type": "string"},
					"ts":             map[string]any{"type": "string"},
					"text":           map[string]any{"type": "string"},
					"reaction":       map[string]any{"type": "string"},
					"user_id":        map[string]any{"type": "string"},
					"allow_external": map[string]any{"type": "boolean"},
				},
				"required": []string{"action"},
			},
		},
		{
			Name:        ToolAuthorizeResource,
			Description: "Send a Keycard authorization link for one or more specific resources (debugging)",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"resource": map[string]any{"type": "string"},
					"resources": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "string"},
					},
					"destination": map[string]any{
						"type": "string",
						"enum": []string{"auto", "dm", "channel"},
					},
					"channel":        map[string]any{"type": "string"},
					"thread_ts":      map[string]any{"type": "string"},
					"allow_external": map[string]any{"type": "boolean"},
				},
			},
		},
	}
}

type ToolSpec struct {
	Name        string
	Description string
	InputSchema map[string]any
}

func findToolSpec(specs []ToolSpec, name string) (ToolSpec, bool) {
	for _, spec := range specs {
		if spec.Name == name {
			return spec, true
		}
	}
	return ToolSpec{}, false
}

func validateToolArguments(tool ToolSpec, args map[string]any) (map[string]any, error) {
	if tool.InputSchema == nil {
		return args, nil
	}

	required, _ := tool.InputSchema["required"].([]string)
	missing := make([]string, 0)
	for _, key := range required {
		if _, ok := args[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("validation failed for tool %q: missing required fields: %s", tool.Name, strings.Join(missing, ", "))
	}

	properties, _ := tool.InputSchema["properties"].(map[string]any)
	for key, spec := range properties {
		value, ok := args[key]
		if !ok {
			continue
		}
		specMap, _ := spec.(map[string]any)
		if specMap == nil {
			continue
		}

		if enumValues, ok := specMap["enum"]; ok {
			if !enumContains(enumValues, value) {
				return nil, fmt.Errorf("validation failed for tool %q: %s must be one of %v", tool.Name, key, enumValues)
			}
		}

		if typeName, ok := specMap["type"].(string); ok && typeName == "string" {
			if _, ok := value.(string); !ok {
				args[key] = fmt.Sprint(value)
			}
		}
	}

	return args, nil
}

func enumContains(enumValues any, value any) bool {
	switch vals := enumValues.(type) {
	case []string:
		str, ok := value.(string)
		if !ok {
			return false
		}
		for _, v := range vals {
			if v == str {
				return true
			}
		}
	case []any:
		for _, v := range vals {
			if fmt.Sprint(v) == fmt.Sprint(value) {
				return true
			}
		}
	}
	return false
}
