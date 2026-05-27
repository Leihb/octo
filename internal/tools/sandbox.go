package tools

import (
	"context"
	"os/exec"

	"github.com/Leihb/octo-agent/internal/sandbox"
)

// activeSandbox, when non-nil, confines every terminal command (foreground and
// background) to the given policy. nil means no OS sandbox — the default.
// Set once at startup via SetSandbox; mirrors the package-level defaultBg.
var activeSandbox *sandbox.Policy

// SetSandbox enables OS-level command confinement for the terminal tools.
// Pass nil to disable. cmd/octo calls this when --sandbox is requested.
func SetSandbox(p *sandbox.Policy) { activeSandbox = p }

// shellCommand builds the *exec.Cmd that runs `command` via the shell, wrapped
// in the active sandbox when one is set. Both TerminalTool and the background
// manager route through here so confinement is uniform.
func shellCommand(ctx context.Context, command string) (*exec.Cmd, error) {
	if activeSandbox != nil {
		return sandbox.Command(ctx, command, *activeSandbox)
	}
	return exec.CommandContext(ctx, "sh", "-c", command), nil
}
