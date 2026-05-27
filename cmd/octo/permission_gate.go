package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Leihb/octo-agent/internal/permission"
)

// cliPermissionGate adapts a permission.Engine into an agent.PermissionGate.
// It resolves allow/deny verdicts directly and, for ask verdicts in
// interactive mode, prompts the user on the shared REPL stdin/stdout.
//
// In strict mode the engine has already collapsed ask → deny, so the prompt
// path is never reached — Check just returns the denial with its reason.
type cliPermissionGate struct {
	engine *permission.Engine
	in     *bufio.Scanner // shared with the REPL loop; reads one line per prompt
	out    io.Writer
}

// Check implements agent.PermissionGate.
func (g *cliPermissionGate) Check(_ context.Context, name string, input map[string]any) (bool, string) {
	switch g.engine.Check(name, input) {
	case permission.Allow:
		return true, ""
	case permission.Deny:
		return false, g.engine.DenialReason(name, input)
	case permission.Ask:
		return g.prompt(name, input)
	}
	return false, g.engine.DenialReason(name, input)
}

// prompt asks the user to approve a tool call. Answers:
//
//	y / yes      → allow this once
//	a / always   → allow for the rest of this session (cached in the engine)
//	anything else (incl. empty / N) → deny
func (g *cliPermissionGate) prompt(name string, input map[string]any) (bool, string) {
	fmt.Fprintf(g.out, "\n⚠ permission: %s wants to run\n", name)
	fmt.Fprintf(g.out, "    %s\n", summariseInput(input))
	fmt.Fprint(g.out, "  allow? [y]es / [a]lways this session / [N]o: ")

	answer := ""
	if g.in != nil && g.in.Scan() {
		answer = strings.ToLower(strings.TrimSpace(g.in.Text()))
	}

	switch answer {
	case "y", "yes":
		return true, ""
	case "a", "always":
		g.engine.Remember(name, input, permission.Allow)
		return true, ""
	default:
		return false, fmt.Sprintf("permission_denied: user declined to run %s", name)
	}
}

// permissionConfigPath returns ~/.octo/permissions.yml. An empty string is
// returned (and the engine falls back to embedded defaults) when the home
// directory can't be resolved.
func permissionConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".octo", "permissions.yml")
}

// resolvePermissionMode maps the --permission-mode flag string onto a
// permission.Mode. Unknown values fall back to interactive (the safe,
// CLI-friendly default) and the caller is expected to have validated.
func resolvePermissionMode(s string) permission.Mode {
	if s == string(permission.ModeStrict) {
		return permission.ModeStrict
	}
	return permission.ModeInteractive
}
