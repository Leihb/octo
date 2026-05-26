package tools

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFile_UniqueReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	writeTestFile(t, path, "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n")

	out, err := EditFileTool{}.Execute(context.Background(), "edit_file", map[string]any{
		"path":       path,
		"old_string": `println("hello")`,
		"new_string": `println("hi there")`,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Replaced 1 occurrence") {
		t.Errorf("status = %q", out)
	}
	if !strings.Contains(readTestFile(t, path), `"hi there"`) {
		t.Error("replacement did not land in file")
	}
}

func TestEditFile_NonUniqueWithoutReplaceAll_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "many.txt")
	writeTestFile(t, path, "foo\nfoo\nbar\n")

	_, err := EditFileTool{}.Execute(context.Background(), "edit_file", map[string]any{
		"path":       path,
		"old_string": "foo",
		"new_string": "baz",
	})
	if err == nil || !strings.Contains(err.Error(), "matches 2 times") {
		t.Errorf("expected non-unique error, got %v", err)
	}
	// File must be untouched on error.
	if got := readTestFile(t, path); got != "foo\nfoo\nbar\n" {
		t.Errorf("file was modified despite error: %q", got)
	}
}

func TestEditFile_ReplaceAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "many.txt")
	writeTestFile(t, path, "foo\nfoo\nbar\nfoo\n")

	out, err := EditFileTool{}.Execute(context.Background(), "edit_file", map[string]any{
		"path":        path,
		"old_string":  "foo",
		"new_string":  "baz",
		"replace_all": true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Replaced 3 occurrence(s)") {
		t.Errorf("status = %q", out)
	}
	if got := readTestFile(t, path); got != "baz\nbaz\nbar\nbaz\n" {
		t.Errorf("file = %q", got)
	}
}

func TestEditFile_NotFound_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	writeTestFile(t, path, "hello world")

	_, err := EditFileTool{}.Execute(context.Background(), "edit_file", map[string]any{
		"path":       path,
		"old_string": "missing",
		"new_string": "x",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not-found error, got %v", err)
	}
}

func TestEditFile_FileMissing(t *testing.T) {
	_, err := EditFileTool{}.Execute(context.Background(), "edit_file", map[string]any{
		"path":       "/nope/nope/nope.txt",
		"old_string": "a",
		"new_string": "b",
	})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestEditFile_IdenticalRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	writeTestFile(t, path, "hello")

	_, err := EditFileTool{}.Execute(context.Background(), "edit_file", map[string]any{
		"path":       path,
		"old_string": "hello",
		"new_string": "hello",
	})
	if err == nil || !strings.Contains(err.Error(), "identical") {
		t.Errorf("expected identical-strings error, got %v", err)
	}
}

func TestEditFile_EmptyOldString_Rejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	writeTestFile(t, path, "hi")

	_, err := EditFileTool{}.Execute(context.Background(), "edit_file", map[string]any{
		"path":       path,
		"old_string": "",
		"new_string": "x",
	})
	if err == nil {
		t.Fatal("empty old_string should be rejected")
	}
}

func TestEditFile_DeleteByEmptyNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	writeTestFile(t, path, "hello world")

	_, err := EditFileTool{}.Execute(context.Background(), "edit_file", map[string]any{
		"path":       path,
		"old_string": " world",
		"new_string": "",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := readTestFile(t, path); got != "hello" {
		t.Errorf("file = %q", got)
	}
}
