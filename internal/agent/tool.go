package agent

import "context"

// ToolDefinition describes a tool the LLM may invoke. The Parameters field
// must be a valid JSON Schema "object" definition; most tools only need
// "type", "properties", and "required".
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"` // JSON Schema object
}

// ToolExecutor dispatches tool calls on behalf of the agentic loop. Each
// implementation maps a tool name to a function; unknown names should return
// an error so the LLM sees a clean error result rather than a panic.
type ToolExecutor interface {
	Execute(ctx context.Context, name string, input map[string]any) (string, error)
}
