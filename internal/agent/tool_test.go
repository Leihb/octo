package agent

import (
	"encoding/json"
	"testing"
)

func TestToolDefinition_Fields(t *testing.T) {
	def := ToolDefinition{
		Name:        "bash",
		Description: "Run a shell command",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type": "string",
				},
			},
			"required": []string{"command"},
		},
	}

	if def.Name != "bash" {
		t.Errorf("Name = %q", def.Name)
	}
	if def.Description != "Run a shell command" {
		t.Errorf("Description = %q", def.Description)
	}
	if def.Parameters["type"] != "object" {
		t.Errorf("Parameters[type] = %v", def.Parameters["type"])
	}
}

func TestToolDefinition_JSONRoundtrip(t *testing.T) {
	def := ToolDefinition{
		Name:        "echo",
		Description: "Echo input",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
			"required":   []string{},
		},
	}

	data, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded ToolDefinition
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.Name != def.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, def.Name)
	}
	if decoded.Description != def.Description {
		t.Errorf("Description = %q", decoded.Description)
	}
}
