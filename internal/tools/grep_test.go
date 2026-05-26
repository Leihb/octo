package tools

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// requireRg skips the test if ripgrep isn't installed. We intentionally
// don't reimplement rg in Go for tests — if a contributor has rg they get
// real coverage; if not, the build still passes.
func requireRg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) not installed; skipping grep test")
	}
}

func TestGrep_ContentMode(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "package main\n\nfunc Hello() {}\n")
	writeTestFile(t, filepath.Join(dir, "b.go"), "package main\n\nfunc World() {}\n")

	out, err := GrepTool{}.Execute(context.Background(), "grep", map[string]any{
		"pattern": "func ",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "func Hello") || !strings.Contains(out, "func World") {
		t.Errorf("expected both functions in output:\n%s", out)
	}
}

func TestGrep_FilesWithMatches(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.txt"), "needle\n")
	writeTestFile(t, filepath.Join(dir, "b.txt"), "haystack\n")

	out, err := GrepTool{}.Execute(context.Background(), "grep", map[string]any{
		"pattern": "needle",
		"path":    dir,
		"mode":    "files_with_matches",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "a.txt") {
		t.Errorf("expected a.txt in output:\n%s", out)
	}
	if strings.Contains(out, "b.txt") {
		t.Errorf("b.txt has no match — shouldn't appear:\n%s", out)
	}
}

func TestGrep_NoMatches(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.txt"), "hello\n")

	out, err := GrepTool{}.Execute(context.Background(), "grep", map[string]any{
		"pattern": "definitely-not-there-xyz",
		"path":    dir,
	})
	if err != nil {
		t.Fatalf("Execute should not error on zero matches: %v", err)
	}
	if !strings.Contains(out, "no matches") {
		t.Errorf("expected 'no matches' message: %q", out)
	}
}

func TestGrep_CaseInsensitive(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.txt"), "HELLO world\n")

	out, err := GrepTool{}.Execute(context.Background(), "grep", map[string]any{
		"pattern":          "hello",
		"path":             dir,
		"case_insensitive": true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "HELLO") {
		t.Errorf("case-insensitive should match: %q", out)
	}
}

func TestGrep_IncludeGlob(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "a.go"), "match here\n")
	writeTestFile(t, filepath.Join(dir, "b.txt"), "match here\n")

	out, err := GrepTool{}.Execute(context.Background(), "grep", map[string]any{
		"pattern": "match",
		"path":    dir,
		"include": "*.go",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "a.go") {
		t.Errorf("a.go should match:\n%s", out)
	}
	if strings.Contains(out, "b.txt") {
		t.Errorf("b.txt should be excluded by include glob:\n%s", out)
	}
}

func TestGrep_UnknownMode_Errors(t *testing.T) {
	requireRg(t)
	_, err := GrepTool{}.Execute(context.Background(), "grep", map[string]any{
		"pattern": "x",
		"mode":    "bogus",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Errorf("expected unknown-mode error, got %v", err)
	}
}

func TestGrep_RequiresPattern(t *testing.T) {
	requireRg(t)
	_, err := GrepTool{}.Execute(context.Background(), "grep", map[string]any{})
	if err == nil {
		t.Fatal("missing pattern should error")
	}
}
