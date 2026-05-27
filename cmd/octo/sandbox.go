package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/Leihb/octo-agent/internal/sandbox"
	"github.com/Leihb/octo-agent/internal/tools"
)

// errSandboxUnavailable is returned (after a message) when --sandbox is asked
// for but the host can't enforce it. Callers fail closed.
var errSandboxUnavailable = errors.New("sandbox unavailable")

// activateSandbox turns on OS-level command confinement for cwd, or fails
// closed when the host can't enforce a sandbox — the user asked for a guarantee
// we can't provide, so we refuse rather than run unconfined.
func activateSandbox(cwd string, stderr io.Writer) error {
	if !sandbox.Available() {
		fmt.Fprintln(stderr, "octo: --sandbox requested but no OS sandbox is available on this host\n"+
			"  (needs macOS, or Linux with Landlock — kernel ≥ 5.13). Refusing to run unconfined.")
		return errSandboxUnavailable
	}
	policy := sandbox.DefaultPolicy(cwd)
	tools.SetSandbox(&policy)
	return nil
}
