package tools

import (
	"context"
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
