package tools

import (
	"os"
	"testing"

	"github.com/Leihb/octo-agent/internal/sandbox"
)

// TestMain lets the test binary stand in for the `octo` executable when the
// Linux sandbox re-execs itself: sandbox.Command runs os.Executable() with the
// __sandboxed-exec subcommand, and under `go test` that executable IS this test
// binary. Without this dispatch the re-exec would recurse into the test suite.
// On macOS (sandbox-exec, no re-exec) the arg never appears, so this is a no-op.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "__sandboxed-exec" {
		os.Exit(sandbox.ShimMain())
	}
	os.Exit(m.Run())
}
