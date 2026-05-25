// Package tools provides built-in tool implementations for the octo agentic
// loop. Each tool implements agent.ToolExecutor and exposes a Definition()
// method that returns the agent.ToolDefinition the LLM sees.
package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/Leihb/octo/internal/agent"
)

// BashTimeout is the maximum time a single bash command may run.
const BashTimeout = 30 * time.Second

// BashTool is an agent.ToolExecutor that runs shell commands via `sh -c`.
// Stdout and stderr are combined and returned as the tool result. Non-zero
// exit codes are reported as extra metadata in the result text rather than
// as a tool error, so the LLM can see the failure output and adapt.
type BashTool struct{}

// Definition returns the agent.ToolDefinition the LLM receives in the tools
// list. The JSON Schema describes a single required "command" string parameter.
func (BashTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "bash",
		Description: "Run a bash/shell command and return stdout+stderr. Use for file operations, running programs, searching code, etc.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The shell command to execute",
				},
			},
			"required": []string{"command"},
		},
	}
}

// Execute runs the command and returns combined output. A non-zero exit code
// is appended to the output as `[exit: <error>]` rather than being surfaced
// as an error, giving the LLM visibility into what went wrong.
func (BashTool) Execute(ctx context.Context, _ string, input map[string]any) (string, error) {
	command, _ := input["command"].(string)
	if command == "" {
		return "", fmt.Errorf("bash: command is required")
	}

	ctx, cancel := context.WithTimeout(ctx, BashTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Surface as result text (not a tool-level error) so the LLM sees it.
		return strings.TrimSpace(string(out)) + "\n[exit: " + err.Error() + "]", nil
	}
	return strings.TrimSpace(string(out)), nil
}

// RegistryWithBash is a simple ToolExecutor registry that contains only
// BashTool. It is the default executor used when --tools is enabled.
type RegistryWithBash struct{}

// Execute dispatches to BashTool for "bash" and returns an error for unknown
// tools so the LLM receives a clean error result.
func (RegistryWithBash) Execute(ctx context.Context, name string, input map[string]any) (string, error) {
	switch name {
	case "bash":
		return BashTool{}.Execute(ctx, name, input)
	default:
		return "", fmt.Errorf("unknown tool %q", name)
	}
}

// DefaultTools returns the set of tool definitions that are active when
// --tools is enabled. Currently this is just the bash tool.
func DefaultTools() []agent.ToolDefinition {
	return []agent.ToolDefinition{BashTool{}.Definition()}
}
