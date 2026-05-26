package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTestFile is a thin helper used across the tools test suite. It
// writes content to path, mkdir -p'ing the parent and failing the test on
// any error. Keeps individual tests focused on assertions, not setup.
func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// readTestFile reads a file, failing the test on error.
func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
