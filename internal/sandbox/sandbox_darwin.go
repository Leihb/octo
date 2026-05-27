//go:build darwin

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sandboxExec is the macOS Seatbelt front-end. Deprecated by Apple but still
// functional and without a public replacement; it remains the standard way to
// confine a child process on macOS.
const sandboxExec = "/usr/bin/sandbox-exec"

// Available reports whether sandbox-exec is present.
func Available() bool {
	_, err := exec.LookPath(sandboxExec)
	return err == nil
}

// Command runs `sh -c command` under a generated SBPL profile via sandbox-exec.
func Command(ctx context.Context, command string, p Policy) (*exec.Cmd, error) {
	if !Available() {
		return nil, ErrUnsupported
	}
	profile := buildProfile(p)
	// -p <profile> applies an inline profile; then the program + args to run.
	return exec.CommandContext(ctx, sandboxExec, "-p", profile, "/bin/sh", "-c", command), nil
}

// buildProfile renders an SBPL profile. The base is `allow default` so ordinary
// commands (dyld, /dev, mach services) keep working — a full deny-default
// profile aborts most binaries. On top of that:
//
//   - Writes become an allowlist: deny all, then re-allow the write roots (and
//     the device nodes commands expect). The later, more specific rule wins.
//   - Reads are confined within $HOME: deny everything under $HOME, then
//     re-allow the read roots. System paths (/usr, /System, …) stay readable
//     via allow-default, so this protects every home secret (~/.ssh, ~/.aws, …)
//     without the brittleness of denying all reads. (Read confinement is thus
//     scoped to $HOME on macOS; Linux/Landlock enforces a full read allowlist.)
//   - Network is denied unless AllowNetwork.
func buildProfile(p Policy) string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n")

	b.WriteString("(deny file-write*)\n")
	if subs := subpaths(p.WriteRoots); subs != "" {
		b.WriteString("(allow file-write* " + subs + ")\n")
	}
	b.WriteString(`(allow file-write* (literal "/dev/null") (literal "/dev/dtracehelper") (literal "/dev/tty") (literal "/dev/stdout") (literal "/dev/stderr"))` + "\n")

	if home := homeDir(); home != "" {
		b.WriteString(fmt.Sprintf("(deny file-read* (subpath %q))\n", home))
		if subs := subpaths(p.ReadRoots); subs != "" {
			b.WriteString("(allow file-read* " + subs + ")\n")
		}
	}

	if !p.AllowNetwork {
		b.WriteString("(deny network*)\n")
	}
	return b.String()
}

// homeDir resolves $HOME (symlinks too, so it matches the canonical path the
// kernel checks). Returns "" when it can't be determined.
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(h); err == nil {
		return real
	}
	return h
}

// subpaths renders roots as a sequence of (subpath "…") clauses, resolving
// symlinks so the canonical path the kernel sees matches (e.g. /tmp →
// /private/tmp). Roots that can't be resolved are used as-is.
func subpaths(roots []string) string {
	var parts []string
	for _, r := range roots {
		resolved := r
		if real, err := filepath.EvalSymlinks(r); err == nil {
			resolved = real
		}
		parts = append(parts, fmt.Sprintf("(subpath %q)", resolved))
	}
	return strings.Join(parts, " ")
}

// ShimMain is the re-exec entry point used only on Linux; on macOS the sandbox
// is applied by sandbox-exec, so this should never be invoked.
func ShimMain() int {
	fmt.Fprintln(os.Stderr, "octo: __sandboxed-exec is not used on darwin")
	return 1
}
