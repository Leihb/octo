package tools

import (
	"fmt"
	"regexp"
)

// sedInPlace matches an invocation of `sed` with an in-place edit flag
// (`-i`, `-i.bak`, `-i ”`, bundled short flags like `-ni`, or the long
// `--in-place`). It deliberately stops at a shell separator (| ; &) so a
// pipeline like `cat x | sed 's/a/b/' > y` — which is NOT an in-place edit —
// doesn't false-positive on a later `sed -i` in a different command.
var sedInPlace = regexp.MustCompile(`\bsed\b[^|;&\n]*\s-(?:-in-place\b|[a-z]*i\b)`)

// guardCommand inspects a shell command for patterns that should be refused
// with an educational message, distinct from the permission engine's broad
// allow/deny/ask policy. The engine decides "may this run at all"; the guard
// catches terminal-specific footguns and steers the model to a better tool.
//
// Currently it only intercepts in-place stream edits (`sed -i` and friends),
// which would otherwise bypass the file tools' permission checks, diff
// rendering, and read-before-write tracking. Returning an error here turns
// into an IsError tool_result, so the model reads the hint and retries with
// edit_file.
func guardCommand(command string) error {
	if sedInPlace.MatchString(command) {
		return fmt.Errorf("refusing in-place `sed` edit: use the edit_file tool instead, " +
			"so the change is permission-checked, shown as a diff, and tracked for " +
			"read-before-write")
	}
	return nil
}
