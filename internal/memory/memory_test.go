package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSave_GetRoundTrip(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	in := Entry{
		Name:        "tests-before-commit",
		Description: "Run tests before committing: user asked for this",
		Type:        TypeFeedback,
		Body:        "**Why:** user said so.\n**How to apply:** run `make test` first.",
	}
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get("tests-before-commit")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Description != in.Description {
		t.Errorf("Description round-trip: %q (colon-bearing scalar mangled?)", got.Description)
	}
	if got.Type != TypeFeedback {
		t.Errorf("Type = %q", got.Type)
	}
	if got.Created == "" || got.LastVerified == "" {
		t.Errorf("Created/LastVerified should be stamped: %+v", got)
	}
	if !strings.Contains(got.Body, "How to apply") {
		t.Errorf("Body round-trip lost content: %q", got.Body)
	}
}

func TestSave_RequiresNameAndDescription(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	if err := s.Save(Entry{Description: "x"}); err == nil {
		t.Error("expected error for missing Name")
	}
	if err := s.Save(Entry{Name: "x"}); err == nil {
		t.Error("expected error for missing Description")
	}
}

func TestSave_UnknownTypeDefaultsReference(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	if err := s.Save(Entry{Name: "n", Description: "d", Type: "bogus"}); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.Get("n")
	if got.Type != TypeReference {
		t.Errorf("unknown type should default to reference, got %q", got.Type)
	}
}

func TestSave_OverwritesSameName(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	_ = s.Save(Entry{Name: "n", Description: "first", Type: TypeUser})
	_ = s.Save(Entry{Name: "n", Description: "second", Type: TypeUser})
	entries, _ := s.List()
	if len(entries) != 1 {
		t.Fatalf("same name should overwrite, got %d entries", len(entries))
	}
	if entries[0].Description != "second" {
		t.Errorf("Description = %q, want second", entries[0].Description)
	}
}

func TestList_SortedAndExcludesIndex(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	_ = s.Save(Entry{Name: "zebra", Description: "z", Type: TypeProject})
	_ = s.Save(Entry{Name: "alpha", Description: "a", Type: TypeProject})

	entries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "alpha" || entries[1].Name != "zebra" {
		t.Fatalf("List should be sorted and exclude MEMORY.md: %+v", entries)
	}
	// MEMORY.md must exist but never appear as an entry.
	if _, err := os.Stat(filepath.Join(s.dir, indexFile)); err != nil {
		t.Errorf("index file missing: %v", err)
	}
}

func TestList_MissingDirIsEmpty(t *testing.T) {
	s := NewStoreAt(filepath.Join(t.TempDir(), "nope"))
	entries, err := s.List()
	if err != nil || entries != nil {
		t.Errorf("missing dir → nil,nil; got %v,%v", entries, err)
	}
}

func TestRenderInjection(t *testing.T) {
	s := NewStoreAt(t.TempDir())

	// Empty store → empty injection.
	if out, _ := s.RenderInjection(); out != "" {
		t.Errorf("empty store injection = %q, want empty", out)
	}

	_ = s.Save(Entry{Name: "n", Description: "prefers Go", Type: TypeUser})
	out, err := s.RenderInjection()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "# Memory (from past sessions)") || !strings.Contains(out, "prefers Go") {
		t.Errorf("injection missing header/entry:\n%s", out)
	}

	// A consolidated summary takes precedence over the entry list.
	if err := os.WriteFile(filepath.Join(s.dir, summaryFile), []byte("CONSOLIDATED SUMMARY"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ = s.RenderInjection()
	if !strings.Contains(out, "CONSOLIDATED SUMMARY") || strings.Contains(out, "prefers Go") {
		t.Errorf("summary should take precedence over entry list:\n%s", out)
	}
}

func TestReadEntry_MalformedSkipped(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	_ = s.ensureDir()
	// No frontmatter fence.
	if err := os.WriteFile(filepath.Join(s.dir, "bad.md"), []byte("just text"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = s.Save(Entry{Name: "good", Description: "d", Type: TypeUser})

	entries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "good" {
		t.Errorf("malformed entry should be skipped, got %+v", entries)
	}
}
