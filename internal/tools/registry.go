package tools

import (
	"context"
	"fmt"

	"github.com/Leihb/octo-agent/internal/agent"
)

// tool is the internal interface every built-in tool implements — both a
// Definition (what the LLM sees) and an Execute (what the agent loop calls).
// External callers of the tools package only need agent.ToolExecutor; this
// interface is private so adding methods later doesn't break consumers.
type tool interface {
	Definition() agent.ToolDefinition
	Execute(ctx context.Context, name string, input map[string]any) (string, error)
}

// allTools is the canonical, ordered list of built-in tools shipped with
// octo-agent. Adding a tool means a single new entry here — the registry
// scan and the DefaultTools() listing both pick it up automatically.
var allTools = []tool{
	TerminalTool{},
	ReadFileTool{},
	WriteFileTool{},
	EditFileTool{},
	GlobTool{},
	GrepTool{},
	WebFetchTool{},
	WebSearchTool{},
}

// DefaultRegistry is the agent.ToolExecutor used when `octo chat --tools` is
// enabled. It dispatches each tool call by name to the matching entry in
// allTools, returning a clean error for unknown names.
type DefaultRegistry struct{}

// Execute implements agent.ToolExecutor.
func (DefaultRegistry) Execute(ctx context.Context, name string, input map[string]any) (string, error) {
	for _, t := range allTools {
		if t.Definition().Name == name {
			return t.Execute(ctx, name, input)
		}
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// DefaultTools returns the slice of ToolDefinitions sent to the LLM when
// `--tools` is on. Order matches allTools.
func DefaultTools() []agent.ToolDefinition {
	defs := make([]agent.ToolDefinition, len(allTools))
	for i, t := range allTools {
		defs[i] = t.Definition()
	}
	return defs
}
