package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/Leihb/octo-agent/internal/sandbox"
	"github.com/Leihb/octo-agent/internal/tools"
)

// errSandboxUnavailable is returned (after a message) when --sandbox is asked
// for but the host can't enforce it. Callers fail closed.
var errSandboxUnavailable = errors.New("sandbox unavailable")

// stringList is a repeatable string flag: each --flag value appends one entry.
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// sandboxOpts carries the user's --sandbox-* tuning on top of the default
// policy.
type sandboxOpts struct {
	allowNet   bool
	writeRoots []string // extra writable (and thus readable) dirs
	readRoots  []string // extra read-only dirs
}

// activateSandbox turns on OS-level command confinement for cwd, extended by
// opts, or fails closed when the host can't enforce a sandbox — the user asked
// for a guarantee we can't provide, so we refuse rather than run unconfined.
func activateSandbox(cwd string, opts sandboxOpts, stderr io.Writer) error {
	if !sandbox.Available() {
		fmt.Fprintln(stderr, "octo: --sandbox requested but no OS sandbox is available on this host\n"+
			"  (needs macOS, or Linux with Landlock — kernel ≥ 5.13). Refusing to run unconfined.")
		return errSandboxUnavailable
	}
	p := sandbox.DefaultPolicy(cwd)
	p.AllowNetwork = opts.allowNet
	for _, d := range opts.writeRoots {
		if abs, err := filepath.Abs(d); err == nil {
			p.WriteRoots = append(p.WriteRoots, abs)
			p.ReadRoots = append(p.ReadRoots, abs) // writable implies readable
		}
	}
	for _, d := range opts.readRoots {
		if abs, err := filepath.Abs(d); err == nil {
			p.ReadRoots = append(p.ReadRoots, abs)
		}
	}
	tools.SetSandbox(&p)
	return nil
}
