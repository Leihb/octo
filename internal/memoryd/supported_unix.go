//go:build !windows

package memoryd

import (
	"os"
	"syscall"
)

// supportedOnThisOS reports whether the daemon model works on this OS.
// Unix-like systems support the foreground-process + PID-file model
// memoryd v1 uses (Signal(0) probes, SIGTERM-driven shutdown).
func supportedOnThisOS() bool { return true }

// isRunning probes the PID via signal 0 (`kill -0`), the standard Unix
// "is this PID alive?" test that doesn't actually deliver a signal.
func isRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}
