// Package sandbox confines an arbitrary shell command to an OS-enforced
// filesystem and network boundary — defense-in-depth beneath the
// permission engine (internal/permission), which only gates command strings.
//
// The boundary is enforced per platform: macOS via Seatbelt (sandbox-exec),
// Linux via a Landlock + seccomp re-exec shim. Other platforms are
// unsupported (Available reports false and Command returns ErrUnsupported).
//
// See dev-docs/c11-sandbox-design.md for the full design.
package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// ErrUnsupported is returned by Command when sandboxing was requested but the
// host can't enforce it (unsupported OS, kernel too old, mechanism missing).
// Callers that asked for a sandbox should fail closed on this.
var ErrUnsupported = errors.New("sandbox: not supported on this host")

// Policy describes the filesystem and network confinement for a command.
// Roots are absolute paths; a root grants access to that path and everything
// beneath it.
type Policy struct {
	// ReadRoots are directories the command may read (and execute from).
	ReadRoots []string
	// WriteRoots are directories the command may write to (a subset use of
	// ReadRoots — write roots should also be readable).
	WriteRoots []string
	// AllowNetwork, when false, denies IP networking (AF_INET/AF_INET6). Unix
	// domain sockets remain available.
	AllowNetwork bool
}

// DefaultPolicy builds the standard confinement for a working directory:
// read access to common system roots plus cwd and temp, write access to cwd
// and temp, and no network. Credential paths under $HOME (~/.ssh, ~/.aws, …)
// are deliberately NOT included, so secrets stay unreadable.
func DefaultPolicy(cwd string) Policy {
	tmp := os.TempDir()

	read := []string{}
	add := func(p string) {
		if p == "" {
			return
		}
		if abs, err := filepath.Abs(p); err == nil {
			read = append(read, abs)
		}
	}
	add(cwd)
	add(tmp)
	// System roots needed to run ordinary tooling. Read-only; not writable.
	for _, p := range systemReadRoots() {
		add(p)
	}

	write := []string{}
	if cwd != "" {
		if abs, err := filepath.Abs(cwd); err == nil {
			write = append(write, abs)
		}
	}
	if tmp != "" {
		if abs, err := filepath.Abs(tmp); err == nil {
			write = append(write, abs)
		}
	}

	return Policy{ReadRoots: dedupe(read), WriteRoots: dedupe(write), AllowNetwork: false}
}

// systemReadRoots returns the OS-specific read-only DIRECTORY roots ordinary
// commands need (toolchains, shared libs, config). Directories only — device
// files like /dev/null are granted separately by the platform layer, since
// directory access rights are invalid on a character device. Best-effort:
// missing paths are harmless (a rule for a nonexistent path never matches).
func systemReadRoots() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/usr", "/bin", "/sbin", "/etc", "/var", "/private", "/System", "/Library", "/opt"}
	case "linux":
		return []string{"/usr", "/bin", "/sbin", "/lib", "/lib64", "/etc", "/opt", "/proc"}
	default:
		return nil
	}
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// Command and Available are defined per-platform in
// sandbox_{darwin,linux,other}.go:
//
//	func Command(ctx context.Context, command string, p Policy) (*exec.Cmd, error)
//	func Available() bool
