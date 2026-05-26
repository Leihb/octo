package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	writeTestFile(t, path, "line1\nline2\nline3\n")

	out, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{"path": path})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"     1\tline1", "     2\tline2", "     3\tline3"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestReadFile_OffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "many.txt")
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString("line")
		b.WriteByte(byte('0' + (i % 10)))
		b.WriteByte('\n')
	}
	writeTestFile(t, path, b.String())

	out, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{
		"path":   path,
		"offset": 3,
		"limit":  2,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "     3\tline3") || !strings.Contains(out, "     4\tline4") {
		t.Errorf("offset/limit slice wrong:\n%s", out)
	}
	if strings.Contains(out, "     2\tline2") || strings.Contains(out, "     5\tline5") {
		t.Errorf("offset/limit returned unwanted lines:\n%s", out)
	}
}

func TestReadFile_PastEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "short.txt")
	writeTestFile(t, path, "only\n")

	_, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{
		"path":   path,
		"offset": 100,
	})
	if err == nil || !strings.Contains(err.Error(), "past end") {
		t.Errorf("expected past-end error, got %v", err)
	}
}

func TestReadFile_Missing(t *testing.T) {
	_, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{
		"path": "/definitely/not/a/real/path/12345",
	})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	writeTestFile(t, path, "")

	out, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{"path": path})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "empty") {
		t.Errorf("expected empty-file marker, got %q", out)
	}
}

func TestReadFile_RequiresPath(t *testing.T) {
	_, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestReadFile_RejectsBinaryExtensions(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		ext     string
		wantSub string
	}{
		{"exe", ".exe", "Windows executable"},
		{"zip", ".zip", "ZIP archive"},
		{"png", ".png", "PNG image"},
		{"pdf", ".pdf", "PDF document"},
		{"sqlite", ".sqlite", "SQLite database"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(dir, "file"+c.ext)
			writeTestFile(t, path, "bogus content")
			_, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{"path": path})
			if err == nil {
				t.Fatalf("%s extension should be refused", c.ext)
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error should mention %q; got: %v", c.wantSub, err)
			}
		})
	}
}

func TestReadFile_AllowsTextExtensions(t *testing.T) {
	// Sanity check that the binary list isn't too aggressive — common
	// source-code extensions must still be readable.
	dir := t.TempDir()
	cases := []string{".go", ".ts", ".tsx", ".py", ".rb", ".md", ".json", ".yml", ".html", ".css", ".sql", ".rs"}
	for _, ext := range cases {
		t.Run(strings.TrimPrefix(ext, "."), func(t *testing.T) {
			path := filepath.Join(dir, "x"+ext)
			writeTestFile(t, path, "hello\n")
			if _, err := (ReadFileTool{}).Execute(context.Background(), "read_file", map[string]any{"path": path}); err != nil {
				t.Errorf("%s should be readable as text; got %v", ext, err)
			}
		})
	}
}

func TestReadFile_RejectsBlockedDevicePaths(t *testing.T) {
	// On non-Unix, these paths simply don't exist; the test is meaningful
	// only where they would otherwise be readable. Skip if /dev itself
	// isn't there.
	if _, err := os.Stat("/dev"); err != nil {
		t.Skip("/dev not present; skipping device-file test")
	}
	for _, p := range []string{"/dev/random", "/dev/urandom", "/dev/zero", "/dev/tty"} {
		t.Run(strings.TrimPrefix(p, "/dev/"), func(t *testing.T) {
			_, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{"path": p})
			if err == nil {
				t.Fatalf("%s should be refused", p)
			}
			if !strings.Contains(err.Error(), "device file") {
				t.Errorf("error should mention device file; got %v", err)
			}
		})
	}
}

func TestReadFile_AllowsDevNull(t *testing.T) {
	// /dev/null returns EOF immediately; it's not on the blocklist.
	if _, err := os.Stat("/dev/null"); err != nil {
		t.Skip("/dev/null not present; skipping")
	}
	out, err := ReadFileTool{}.Execute(context.Background(), "read_file", map[string]any{"path": "/dev/null"})
	if err != nil {
		t.Fatalf("/dev/null should be readable (returns EOF): %v", err)
	}
	if !strings.Contains(out, "empty") {
		t.Errorf("expected empty-file marker, got %q", out)
	}
}
