package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Leihb/octo-agent/internal/agent"
)

// ReadFileMaxLines caps how many lines a single read_file call returns.
// Past this, the caller must paginate via offset/limit. Matches the cap
// Claude Code's Read tool uses (helps keep LLM context sane).
const ReadFileMaxLines = 2000

// ReadFileTool reads a UTF-8 text file, optionally a window of it, and
// returns its content with each line prefixed by its 1-based line number
// (the `cat -n` format the LLM is already familiar with).
type ReadFileTool struct{}

func (ReadFileTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "read_file",
		Description: "Read a UTF-8 text file and return its content with cat -n-style " +
			"line numbers. Up to 2000 lines per call — use offset/limit to paginate. " +
			"Absolute paths are preferred; relative paths resolve against the current " +
			"working directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path (absolute preferred).",
				},
				"offset": map[string]any{
					"type":        "integer",
					"description": "1-based line number to start from. Defaults to 1 (beginning of file).",
				},
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum lines to return. Defaults to 2000.",
				},
			},
			"required": []string{"path"},
		},
	}
}

func (ReadFileTool) Execute(_ context.Context, _ string, input map[string]any) (string, error) {
	path, _ := input["path"].(string)
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("read_file: path is required")
	}
	abs, err := resolvePath(path)
	if err != nil {
		return "", err
	}

	offset := intArg(input, "offset", 1)
	if offset < 1 {
		offset = 1
	}
	limit := intArg(input, "limit", ReadFileMaxLines)
	if limit < 1 || limit > ReadFileMaxLines {
		limit = ReadFileMaxLines
	}

	f, err := os.Open(abs)
	if err != nil {
		return "", fmt.Errorf("read_file: open %q: %w", path, err)
	}
	defer f.Close()

	// Larger initial buffer than bufio.Scanner's 64KiB default so long
	// lines (minified JS, base64 blobs) don't trip MaxScanTokenSize.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var (
		out      strings.Builder
		lineNum  int
		returned int
	)
	for scanner.Scan() {
		lineNum++
		if lineNum < offset {
			continue
		}
		if returned >= limit {
			break
		}
		// Width-6 line number column matches `cat -n` so a 100k-line file
		// still aligns. Tab after the column keeps Markdown/code blocks
		// rendering correctly in UIs.
		fmt.Fprintf(&out, "%6d\t%s\n", lineNum, scanner.Text())
		returned++
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read_file: scan %q: %w", path, err)
	}

	if returned == 0 && lineNum > 0 {
		return "", fmt.Errorf("read_file: offset %d is past end of file (only %d lines)", offset, lineNum)
	}
	if returned == 0 {
		return "(empty file)", nil
	}
	return out.String(), nil
}

// resolvePath normalises path → absolute, expanding "~" and cleaning.
func resolvePath(path string) (string, error) {
	if strings.HasPrefix(path, "~/") || path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~: %w", err)
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve cwd: %w", err)
		}
		path = filepath.Join(cwd, path)
	}
	return filepath.Clean(path), nil
}

// intArg pulls an int from input, accepting either int or float64
// (JSON numbers arrive as float64 from encoding/json).
func intArg(input map[string]any, key string, dflt int) int {
	switch v := input[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return dflt
}
