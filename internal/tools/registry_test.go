package tools

import (
	"context"
	"strings"
	"testing"
)

func TestDefaultTools_IncludesAllExpected(t *testing.T) {
	defs := DefaultTools()
	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{
		"terminal",
		"read_file",
		"write_file",
		"edit_file",
		"glob",
		"grep",
		"web_fetch",
		"web_search",
	} {
		if !names[want] {
			t.Errorf("DefaultTools missing %q", want)
		}
	}
}

func TestDefaultTools_DefinitionsHaveRequiredFields(t *testing.T) {
	for _, d := range DefaultTools() {
		if d.Name == "" {
			t.Errorf("tool with empty Name in DefaultTools")
		}
		if d.Description == "" {
			t.Errorf("tool %q has empty Description", d.Name)
		}
		if d.Parameters["type"] != "object" {
			t.Errorf("tool %q Parameters.type = %v, want 'object'", d.Name, d.Parameters["type"])
		}
	}
}

func TestDefaultRegistry_UnknownTool(t *testing.T) {
	_, err := DefaultRegistry{}.Execute(context.Background(), "no_such_tool", nil)
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("expected unknown-tool error, got %v", err)
	}
}

func TestDefaultRegistry_RoutesByName(t *testing.T) {
	// Pick a tool with very obvious failure mode (read_file missing path)
	// to confirm the registry dispatched to the right tool.
	_, err := DefaultRegistry{}.Execute(context.Background(), "read_file", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Errorf("expected read_file routing, got %v", err)
	}
}
