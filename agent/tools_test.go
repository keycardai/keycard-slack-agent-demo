package agent

import "testing"

func TestValidateToolArgumentsMissingRequired(t *testing.T) {
	spec := ToolSpec{
		Name: "read",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
	}
	_, err := validateToolArguments(spec, map[string]any{})
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
}

func TestValidateToolArgumentsEnum(t *testing.T) {
	spec := ToolSpec{
		Name: "memory_delete",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{"type": "string"},
			},
			"required": []string{"key"},
		},
	}
	_, err := validateToolArguments(spec, map[string]any{})
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
}
