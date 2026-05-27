//go:build !darwin && !linux

package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// Available reports false: no sandbox mechanism on this platform (e.g. Windows).
func Available() bool { return false }

// Command always fails closed here — callers that requested a sandbox must not
// silently run unconfined.
func Command(_ context.Context, _ string, _ Policy) (*exec.Cmd, error) {
	return nil, ErrUnsupported
}

// ShimMain is never used off Linux.
func ShimMain() int {
	fmt.Fprintln(os.Stderr, "octo: __sandboxed-exec is not supported on this platform")
	return 1
}
