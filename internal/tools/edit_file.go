package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/Leihb/octo-agent/internal/agent"
)

// EditFileTool replaces an exact substring inside an existing file. The
// match must be unique unless replace_all is true. Refuses to create the
// file if it doesn't exist — use write_file for that.
type EditFileTool struct{}

func (EditFileTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "edit_file",
		Description: "Replace an exact substring in an existing file. old_string must " +
			"appear exactly once (or set replace_all=true to swap every occurrence). " +
			"The file must already exist — use write_file to create. Preserve " +
			"indentation and surrounding context when picking old_string so it stays unique.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path (absolute preferred).",
				},
				"old_string": map[string]any{
					"type":        "string",
					"description": "Exact text to find. Must appear in the file. Include enough surrounding context for it to be unique unless replace_all is set.",
				},
				"new_string": map[string]any{
					"type":        "string",
					"description": "Replacement text. Empty string is allowed (deletes old_string).",
				},
				"replace_all": map[string]any{
					"type":        "boolean",
					"description": "When true, replace every occurrence instead of requiring a unique match. Defaults to false.",
				},
			},
			"required": []string{"path", "old_string", "new_string"},
		},
	}
}

func (EditFileTool) Execute(_ context.Context, _ string, input map[string]any) (string, error) {
	path, _ := input["path"].(string)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("edit_file: path is required")
	}
	oldStr, ok1 := input["old_string"].(string)
	newStr, ok2 := input["new_string"].(string)
	if !ok1 {
		return "", fmt.Errorf("edit_file: old_string is required")
	}
	if !ok2 {
		return "", fmt.Errorf("edit_file: new_string is required (use empty string to delete)")
	}
	if oldStr == "" {
		return "", fmt.Errorf("edit_file: old_string must be non-empty")
	}
	if oldStr == newStr {
		return "", fmt.Errorf("edit_file: old_string and new_string are identical — nothing to do")
	}
	replaceAll, _ := input["replace_all"].(bool)

	abs, err := resolvePath(path)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("edit_file: read %q: %w", path, err)
	}
	body := string(data)

	count := strings.Count(body, oldStr)
	if count == 0 {
		return "", fmt.Errorf("edit_file: old_string not found in %s", path)
	}
	if count > 1 && !replaceAll {
		return "", fmt.Errorf(
			"edit_file: old_string matches %d times — either include more context to make it unique, or set replace_all=true",
			count,
		)
	}

	var updated string
	if replaceAll {
		updated = strings.ReplaceAll(body, oldStr, newStr)
	} else {
		updated = strings.Replace(body, oldStr, newStr, 1)
	}

	if err := os.WriteFile(abs, []byte(updated), 0o644); err != nil {
		return "", fmt.Errorf("edit_file: write %q: %w", path, err)
	}

	if replaceAll {
		return fmt.Sprintf("Replaced %d occurrence(s) in %s", count, abs), nil
	}
	return fmt.Sprintf("Replaced 1 occurrence in %s", abs), nil
}
