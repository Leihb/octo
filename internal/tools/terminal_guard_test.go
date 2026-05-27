package tools

import (
	"context"
	"strings"
	"testing"
)

func TestGuardCommand_BlocksSedInPlace(t *testing.T) {
	blocked := []string{
		`sed -i 's/a/b/' file.txt`,
		`sed -i.bak 's/a/b/' file.txt`,
		`sed -i '' 's/a/b/' file.txt`, // macOS form
		`sed --in-place 's/a/b/' file.txt`,
		`sed -ni 's/a/b/p' file.txt`, // bundled short flags
		`cat x | sed -i 's/a/b/' y`,
	}
	for _, cmd := range blocked {
		if err := guardCommand(cmd); err == nil {
			t.Errorf("expected %q to be blocked", cmd)
		} else if !strings.Contains(err.Error(), "edit_file") {
			t.Errorf("block message for %q should steer to edit_file, got %q", cmd, err)
		}
	}
}

func TestGuardCommand_AllowsNonInPlaceSed(t *testing.T) {
	ok := []string{
		`sed 's/a/b/' file.txt`,    // stream to stdout, not in-place
		`cat file | sed 's/a/b/'`,  // pipeline, no -i
		`echo hi`,                  // unrelated
		`grep -i pattern file.txt`, // grep -i is case-insensitive, not sed
		`ls -i`,                    // ls -i prints inodes, not sed
	}
	for _, cmd := range ok {
		if err := guardCommand(cmd); err != nil {
			t.Errorf("expected %q to be allowed, got %v", cmd, err)
		}
	}
}

func TestTerminalTool_RefusesSedInPlace(t *testing.T) {
	_, err := TerminalTool{}.Execute(context.Background(), "terminal", map[string]any{
		"command": `sed -i 's/foo/bar/' /tmp/whatever.txt`,
	})
	if err == nil || !strings.Contains(err.Error(), "edit_file") {
		t.Errorf("terminal should refuse sed -i with an edit_file hint, got %v", err)
	}
}
