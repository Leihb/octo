package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/mcp"
)

// MCP integration. The tools package exposes one global *mcp.Registry that
// cmd/octo populates at session start; DefaultRegistry.Execute branches on
// the "mcp__" tool-name prefix to dispatch through the registry, and
// DefaultTools synthesises ToolDefinitions for every MCP surface so they
// ride alongside the built-ins in the model's tool list.
//
// Naming: tool names are "mcp__<server>__<tool>". The double-underscore
// separator avoids the LLM-side restriction that tool names match
// ^[a-zA-Z0-9_-]+$ (no dots, slashes, or colons) while still being
// unambiguous to parse. Mirrors Claude Code's convention.
//
// Synthetic per-server tools:
//   - mcp__<server>__resource_read  — fetches one resource by URI.
//   - mcp__<server>__prompt_get     — materialises one named prompt.
//
// Only synthesized when the server's capabilities advertised the surface
// — a tools-only server gets just its tools.

var (
	mcpRegistryMu sync.RWMutex
	mcpRegistry   *mcp.Registry
)

// SetMCPRegistry installs the active registry for this session. Passing
// nil clears it (used by defer in cmd/octo so the next session starts
// clean). Idempotent.
func SetMCPRegistry(r *mcp.Registry) {
	mcpRegistryMu.Lock()
	mcpRegistry = r
	mcpRegistryMu.Unlock()
}

// ActiveMCPRegistry returns the registered registry or nil if MCP is off.
func ActiveMCPRegistry() *mcp.Registry {
	mcpRegistryMu.RLock()
	defer mcpRegistryMu.RUnlock()
	return mcpRegistry
}

// mcpEnabled is the DefaultTools gating function — true when SetMCPRegistry
// has been called with a non-nil registry AND that registry has at least
// one live connection.
func mcpEnabled() bool {
	r := ActiveMCPRegistry()
	return r != nil && r.Len() > 0
}

// mcpToolDefs synthesises one ToolDefinition per MCP surface for every live
// connection. Called from DefaultTools at session start; returns empty if
// MCP is off so the caller can splice it in unconditionally.
func mcpToolDefs() []agent.ToolDefinition {
	reg := ActiveMCPRegistry()
	if reg == nil {
		return nil
	}
	var defs []agent.ToolDefinition
	for _, conn := range reg.Connections() {
		// Server tools: one definition per advertised tool. The MCP tool's
		// inputSchema is already a JSON Schema object, so we pass it through
		// verbatim into Parameters.
		for _, t := range conn.Tools {
			defs = append(defs, agent.ToolDefinition{
				Name:        mcpToolName(conn.Name, t.Name),
				Description: fmt.Sprintf("[mcp:%s] %s", conn.Name, t.Description),
				Parameters:  decodeSchema(t.InputSchema),
			})
		}
		// Synthetic resource_read: present when the server advertises
		// resources/* capability at all (some servers expose resources via
		// templates without prepopulating the list).
		if conn.Client.Capabilities().Resources != nil {
			defs = append(defs, agent.ToolDefinition{
				Name:        mcpToolName(conn.Name, "resource_read"),
				Description: fmt.Sprintf("[mcp:%s] Read an MCP resource by URI. Available resources: %s", conn.Name, joinResourceURIs(conn.Resources)),
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"uri": map[string]any{
							"type":        "string",
							"description": "The resource URI to read (see the description for what this server advertises).",
						},
					},
					"required": []string{"uri"},
				},
			})
		}
		// Synthetic prompt_get: same gating, present whenever the server
		// advertises prompts/*.
		if conn.Client.Capabilities().Prompts != nil {
			defs = append(defs, agent.ToolDefinition{
				Name:        mcpToolName(conn.Name, "prompt_get"),
				Description: fmt.Sprintf("[mcp:%s] Materialise an MCP prompt template. Available prompts: %s", conn.Name, joinPromptNames(conn.Prompts)),
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Prompt name from the available list.",
						},
						"arguments": map[string]any{
							"type":                 "object",
							"description":          "Key-value arguments for the prompt template (string keys, string values).",
							"additionalProperties": map[string]any{"type": "string"},
						},
					},
					"required": []string{"name"},
				},
			})
		}
	}
	return defs
}

// executeMCP dispatches an "mcp__…" tool call through the registry.
// Returns ok=false when the name isn't an MCP tool so the caller can fall
// through to its normal dispatch. Errors from the underlying client are
// propagated as-is so the agent loop sees the same error surface it would
// for any other tool.
func executeMCP(ctx context.Context, name string, input map[string]any) (out string, ok bool, err error) {
	if !strings.HasPrefix(name, "mcp__") {
		return "", false, nil
	}
	server, tool, ok2 := parseMCPName(name)
	if !ok2 {
		return "", true, fmt.Errorf("mcp: malformed tool name %q (want mcp__<server>__<tool>)", name)
	}
	reg := ActiveMCPRegistry()
	if reg == nil {
		return "", true, fmt.Errorf("mcp: no registry registered")
	}
	conn := reg.Get(server)
	if conn == nil {
		return "", true, fmt.Errorf("mcp: server %q is not connected", server)
	}

	switch tool {
	case "resource_read":
		uri, _ := input["uri"].(string)
		if uri == "" {
			return "", true, fmt.Errorf("mcp: resource_read needs uri")
		}
		contents, err := conn.Client.ReadResource(ctx, uri)
		if err != nil {
			return "", true, err
		}
		return formatResourceContents(contents), true, nil

	case "prompt_get":
		promptName, _ := input["name"].(string)
		if promptName == "" {
			return "", true, fmt.Errorf("mcp: prompt_get needs name")
		}
		argMap := convertStringMap(input["arguments"])
		result, err := conn.Client.GetPrompt(ctx, promptName, argMap)
		if err != nil {
			return "", true, err
		}
		return formatPromptResult(result), true, nil

	default:
		result, err := conn.Client.CallTool(ctx, tool, input)
		if err != nil {
			return "", true, err
		}
		out := formatToolResult(result)
		if result.IsError {
			// The tool ran but reported an error in-band. Surface it as a
			// Go error so the agent loop tags the tool_result IsError too —
			// matches the contract built-in tools use.
			return out, true, fmt.Errorf("mcp tool error: %s", out)
		}
		return out, true, nil
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

func mcpToolName(server, tool string) string {
	return "mcp__" + sanitizeMCPName(server) + "__" + sanitizeMCPName(tool)
}

// sanitizeMCPName conforms a string to the LLM tool-name regex
// ^[a-zA-Z0-9_-]+$. The MCP spec doesn't constrain server/tool names, so we
// strip what an LLM provider would otherwise reject. Replaces forbidden
// runes with underscores; collapses consecutive replacements.
func sanitizeMCPName(s string) string {
	var b strings.Builder
	prevReplaced := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-':
			b.WriteRune(r)
			prevReplaced = false
		default:
			if !prevReplaced {
				b.WriteByte('_')
				prevReplaced = true
			}
		}
	}
	return b.String()
}

// parseMCPName splits "mcp__<server>__<tool>" into its components. Returns
// ok=false on a malformed name. The tool segment may itself contain "__"
// (we use SplitN to keep it intact).
//
// Strict on the prefix: callers shouldn't rely on TrimPrefix's silent no-op
// when the prefix doesn't match — that would let "not-mcp__a__b" through.
func parseMCPName(name string) (server, tool string, ok bool) {
	if !strings.HasPrefix(name, "mcp__") {
		return "", "", false
	}
	rest := name[len("mcp__"):]
	parts := strings.SplitN(rest, "__", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// decodeSchema turns the MCP tool's raw JSON Schema into the map[string]any
// shape agent.ToolDefinition.Parameters wants. Falls back to an empty
// "object" schema on parse failure so the LLM still sees a valid (if
// unhelpful) tool definition.
func decodeSchema(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{"type": "object"}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{"type": "object"}
	}
	return out
}

func joinResourceURIs(rs []mcp.Resource) string {
	if len(rs) == 0 {
		return "(none pre-listed; check with the server's templates)"
	}
	uris := make([]string, 0, len(rs))
	for _, r := range rs {
		uris = append(uris, r.URI)
	}
	if len(uris) > 8 {
		uris = append(uris[:8], "…")
	}
	return strings.Join(uris, ", ")
}

func joinPromptNames(ps []mcp.Prompt) string {
	if len(ps) == 0 {
		return "(none pre-listed)"
	}
	names := make([]string, 0, len(ps))
	for _, p := range ps {
		names = append(names, p.Name)
	}
	if len(names) > 8 {
		names = append(names[:8], "…")
	}
	return strings.Join(names, ", ")
}

// formatToolResult flattens an MCP tool result's content blocks into a
// single string for the agent's text-content tool_result. Non-text blocks
// are summarised so the agent at least knows what came back.
func formatToolResult(r *mcp.CallToolResult) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	for i, c := range r.Content {
		if i > 0 {
			b.WriteString("\n")
		}
		switch c.Type {
		case "text":
			b.WriteString(c.Text)
		case "image":
			fmt.Fprintf(&b, "[image: %s, %d bytes]", c.MIMEType, len(c.Data))
		case "audio":
			fmt.Fprintf(&b, "[audio: %s, %d bytes]", c.MIMEType, len(c.Data))
		case "resource":
			if c.Resource != nil {
				if c.Resource.Text != "" {
					fmt.Fprintf(&b, "[resource %s]\n%s", c.Resource.URI, c.Resource.Text)
				} else {
					fmt.Fprintf(&b, "[resource %s: %s, blob len=%d]", c.Resource.URI, c.Resource.MIMEType, len(c.Resource.Blob))
				}
			}
		default:
			fmt.Fprintf(&b, "[unknown content type %q]", c.Type)
		}
	}
	return b.String()
}

func formatResourceContents(cs []mcp.ResourceContent) string {
	var b strings.Builder
	for i, c := range cs {
		if i > 0 {
			b.WriteString("\n")
		}
		if c.Text != "" {
			fmt.Fprintf(&b, "[resource %s]\n%s", c.URI, c.Text)
		} else {
			fmt.Fprintf(&b, "[resource %s: %s, blob len=%d]", c.URI, c.MIMEType, len(c.Blob))
		}
	}
	return b.String()
}

func formatPromptResult(r *mcp.GetPromptResult) string {
	if r == nil {
		return ""
	}
	var b strings.Builder
	if r.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n\n", r.Description)
	}
	for i, m := range r.Messages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%s]\n", m.Role)
		switch m.Content.Type {
		case "text":
			b.WriteString(m.Content.Text)
		default:
			fmt.Fprintf(&b, "(non-text content: %s)", m.Content.Type)
		}
	}
	return b.String()
}

// convertStringMap normalises the "arguments" field of prompt_get to
// map[string]string. The agent passes arguments as map[string]any so we
// stringify each value (which is what the prompt template expects anyway).
func convertStringMap(v any) map[string]string {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, val := range m {
		out[k] = fmt.Sprintf("%v", val)
	}
	return out
}
