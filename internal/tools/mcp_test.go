package tools

import (
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/mcp"
)

func TestMCPToolName_HappyPath(t *testing.T) {
	got := mcpToolName("filesystem", "read_file")
	if got != "mcp__filesystem__read_file" {
		t.Errorf("mcpToolName = %q", got)
	}
}

func TestSanitizeMCPName_StripsForbidden(t *testing.T) {
	cases := map[string]string{
		"simple":            "simple",
		"with-dash":         "with-dash",
		"with_underscore":   "with_underscore",
		"with.dot":          "with_dot",       // dot replaced
		"with:colon":        "with_colon",     // colon replaced
		"with space":        "with_space",     // space replaced
		"with/slash":        "with_slash",     // slash replaced
		"chars!@#$multiple": "chars_multiple", // consecutive forbidden collapse to one _
		"valid42":           "valid42",
	}
	for in, want := range cases {
		if got := sanitizeMCPName(in); got != want {
			t.Errorf("sanitizeMCPName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseMCPName_RoundTrip(t *testing.T) {
	server, tool, ok := parseMCPName("mcp__filesystem__read_file")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if server != "filesystem" || tool != "read_file" {
		t.Errorf("got (%q, %q)", server, tool)
	}
}

func TestParseMCPName_ToolWithUnderscores(t *testing.T) {
	// SplitN(s, "__", 2) preserves further "__" inside the tool segment.
	server, tool, ok := parseMCPName("mcp__github__list__issues")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if server != "github" || tool != "list__issues" {
		t.Errorf("got (%q, %q)", server, tool)
	}
}

func TestParseMCPName_RejectsMalformed(t *testing.T) {
	for _, bad := range []string{
		"mcp__only-server",    // missing tool segment
		"mcp____empty-server", // empty server  (SplitN gives ["", "empty-server"], parts[0]==""→ok=false)
		"not-mcp__a__b",       // wrong prefix
		"",
	} {
		if _, _, ok := parseMCPName(bad); ok {
			t.Errorf("parseMCPName(%q) ok=true, want false", bad)
		}
	}
}

func TestFormatToolResult_MixedContent(t *testing.T) {
	r := &mcp.CallToolResult{Content: []mcp.Content{
		{Type: "text", Text: "first"},
		{Type: "image", MIMEType: "image/png", Data: "<base64 …>"},
		{Type: "resource", Resource: &mcp.EmbeddedResource{
			URI: "file:///x", MIMEType: "text/plain", Text: "embedded",
		}},
	}}
	out := formatToolResult(r)
	for _, want := range []string{
		"first",
		"[image: image/png",
		"[resource file:///x]",
		"embedded",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestMCPEnabledGate(t *testing.T) {
	// Before any registration the gate is off so DefaultTools won't
	// advertise MCP surfaces.
	SetMCPRegistry(nil)
	if mcpEnabled() {
		t.Error("expected mcpEnabled=false with no registry")
	}
	// Empty registry: still disabled — Len() == 0.
	SetMCPRegistry(&mcp.Registry{})
	if mcpEnabled() {
		t.Error("expected mcpEnabled=false with empty registry")
	}
	SetMCPRegistry(nil) // clean up for other tests
}

func TestMCPToolDefs_NilRegistryYieldsNone(t *testing.T) {
	SetMCPRegistry(nil)
	defer SetMCPRegistry(nil)
	if defs := mcpToolDefs(); len(defs) != 0 {
		t.Errorf("expected empty defs with no registry, got %d", len(defs))
	}
}
