// tui-preview renders a sample of every TUI card to stdout so the
// maintainer can eyeball them on a real terminal. It exists outside the
// `octo` binary by design — this is a dev-only helper, not user-facing.
//
// Run:   go run ./cmd/tui-preview
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Leihb/octo-agent/internal/tui"
)

func main() {
	// Sample 1: an edit_file diff card, line numbers computed from a real
	// (temporary) file so the rendering exercises the full code path.
	dir, err := os.MkdirTemp("", "tui-preview-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mktmp:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "event.go")
	post := `package agent

type AgentEvent struct {
	Kind       EventKind      ` + "`json:\"kind\"`" + `
	Text       string         ` + "`json:\"text,omitempty\"`" + `
	InputDelta string         ` + "`json:\"input_delta,omitempty\"`" + `
	Chunk      string         ` + "`json:\"chunk,omitempty\"`" + `
}
`
	if err := os.WriteFile(path, []byte(post), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}

	oldStr := "	Text       string         `json:\"text,omitempty\"`"
	newStr := "	Text       string         `json:\"text,omitempty\"`\n	InputDelta string         `json:\"input_delta,omitempty\"`\n	Chunk      string         `json:\"chunk,omitempty\"`"

	fmt.Println(tui.RenderEditCard(path, oldStr, newStr))
	fmt.Println()
}
