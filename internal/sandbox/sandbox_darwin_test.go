//go:build darwin

package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func run(t *testing.T, cwd, command string, p Policy) error {
	t.Helper()
	cmd, err := Command(context.Background(), command, p)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	cmd.Dir = cwd
	out, runErr := cmd.CombinedOutput()
	t.Logf("command %q → err=%v out=%s", command, runErr, out)
	return runErr
}

func TestSandbox_FilesystemBoundary(t *testing.T) {
	if !Available() {
		t.Skip("sandbox-exec not available")
	}
	cwd := t.TempDir()
	p := DefaultPolicy(cwd)

	// A normal command runs.
	if err := run(t, cwd, "echo hello", p); err != nil {
		t.Errorf("plain echo should run under sandbox: %v", err)
	}

	// Write INSIDE cwd succeeds.
	if err := run(t, cwd, "echo in > inside.txt", p); err != nil {
		t.Errorf("write inside cwd should succeed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, "inside.txt")); err != nil {
		t.Errorf("inside.txt should exist: %v", err)
	}

	// Write OUTSIDE the roots (under $HOME) is denied.
	home, _ := os.UserHomeDir()
	outside := filepath.Join(home, ".octo_sbx_write_probe")
	defer os.Remove(outside)
	if err := run(t, cwd, "echo out > "+outside, p); err == nil {
		t.Errorf("write outside roots should be denied")
	}
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("outside file must not have been created")
	}

	// Read OUTSIDE the roots is denied. Seed a secret in $HOME (not a root),
	// then try to read it from within the sandbox.
	secret := filepath.Join(home, ".octo_sbx_read_probe")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(secret)
	if err := run(t, cwd, "cat "+secret, p); err == nil {
		t.Errorf("read outside roots should be denied")
	}
}
