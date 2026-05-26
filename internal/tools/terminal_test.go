package tools

import (
	"context"
	"strings"
	"testing"
)

func TestTerminalTool_Definition(t *testing.T) {
	def := TerminalTool{}.Definition()
	if def.Name != "terminal" {
		t.Errorf("Name = %q, want terminal", def.Name)
	}
	if def.Description == "" {
		t.Error("Description should not be empty")
	}
	if def.Parameters["type"] != "object" {
		t.Errorf("Parameters.type = %v", def.Parameters["type"])
	}
	props, ok := def.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters.properties is not a map")
	}
	if _, ok := props["command"]; !ok {
		t.Error("Parameters.properties.command should exist")
	}
	req, ok := def.Parameters["required"].([]string)
	if !ok {
		t.Fatal("Parameters.required is not []string")
	}
	if len(req) != 1 || req[0] != "command" {
		t.Errorf("required = %v, want [command]", req)
	}
}

func TestTerminalTool_Execute_Echo(t *testing.T) {
	result, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command": "echo hello",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.TrimSpace(result) != "hello" {
		t.Errorf("result = %q, want 'hello'", result)
	}
}

func TestTerminalTool_Execute_Multiline(t *testing.T) {
	result, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command": "echo line1 && echo line2",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "line1") || !strings.Contains(result, "line2") {
		t.Errorf("result = %q, want line1 and line2", result)
	}
}

func TestTerminalTool_Execute_NonZeroExit(t *testing.T) {
	// A failing command should return output + exit info as result text,
	// NOT as a Go error, so the LLM can read it.
	result, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command": "sh -c 'echo oops; exit 1'",
	})
	if err != nil {
		t.Fatalf("Execute should not return a Go error for non-zero exit: %v", err)
	}
	if !strings.Contains(result, "oops") {
		t.Errorf("result should contain stdout: %q", result)
	}
	if !strings.Contains(result, "[exit:") {
		t.Errorf("result should contain [exit:...]: %q", result)
	}
}

func TestTerminalTool_Execute_EmptyCommand(t *testing.T) {
	_, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command": "",
	})
	if err == nil {
		t.Error("empty command should return error")
	}
}

func TestTerminalTool_Execute_NoCommandKey(t *testing.T) {
	_, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{})
	if err == nil {
		t.Error("missing command key should return error")
	}
}

func TestDefaultRegistry_Execute(t *testing.T) {
	r := DefaultRegistry{}

	result, err := r.Execute(context.Background(), "terminal", map[string]any{
		"command": "echo registry",
	})
	if err != nil {
		t.Fatalf("Execute terminal: %v", err)
	}
	if strings.TrimSpace(result) != "registry" {
		t.Errorf("result = %q", result)
	}
}

func TestDefaultRegistry_Execute_UnknownTool(t *testing.T) {
	r := DefaultRegistry{}
	_, err := r.Execute(context.Background(), "unknown_tool", nil)
	if err == nil {
		t.Error("unknown tool should return error")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("error = %q, should mention unknown tool", err)
	}
}

func TestDefaultTools(t *testing.T) {
	defs := DefaultTools()
	if len(defs) != 1 {
		t.Fatalf("len = %d, want 1", len(defs))
	}
	if defs[0].Name != "terminal" {
		t.Errorf("Name = %q, want terminal", defs[0].Name)
	}
}
