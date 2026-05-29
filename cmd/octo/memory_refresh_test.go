package main

import (
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/memory"
)

func TestMemoryRefresher_OnlySurfacesNewEntries(t *testing.T) {
	store := memory.NewStoreAt(t.TempDir())
	// Pre-existing entry — captured by the session-start injection.
	if err := store.Save(memory.Entry{Name: "old", Description: "old fact", Type: memory.TypeReference}); err != nil {
		t.Fatal(err)
	}

	r := newMemoryRefresher(store, "")

	// Nothing new yet → empty delta (and no churn).
	if d := r.delta(); d != "" {
		t.Fatalf("delta with no new entries = %q, want empty (old entry was seeded as seen)", d)
	}

	// Another session writes a new entry.
	if err := store.Save(memory.Entry{Name: "new", Description: "fresh fact from terminal A", Type: memory.TypeProject}); err != nil {
		t.Fatal(err)
	}

	d := r.delta()
	if !strings.Contains(d, "fresh fact from terminal A") {
		t.Fatalf("delta = %q, want it to surface the new entry", d)
	}
	if strings.Contains(d, "old fact") {
		t.Errorf("delta should not re-surface the already-seen entry: %q", d)
	}

	// Reconciled — a second call with no further writes is empty again.
	if d := r.delta(); d != "" {
		t.Errorf("delta after reconciling = %q, want empty", d)
	}
}

func TestMemoryRefresher_FiltersByProject(t *testing.T) {
	store := memory.NewStoreAt(t.TempDir())
	r := newMemoryRefresher(store, "/proj/a")

	// An entry scoped to a different project must not be surfaced.
	if err := store.Save(memory.Entry{Name: "other", Description: "belongs to B", Type: memory.TypeProject, Cwd: "/proj/b"}); err != nil {
		t.Fatal(err)
	}
	if d := r.delta(); d != "" {
		t.Errorf("entry for another project leaked into delta: %q", d)
	}

	// A global (no-cwd) entry and a same-project entry should surface.
	if err := store.Save(memory.Entry{Name: "glob", Description: "global fact", Type: memory.TypeUser}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(memory.Entry{Name: "mine", Description: "for project A", Type: memory.TypeProject, Cwd: "/proj/a"}); err != nil {
		t.Fatal(err)
	}
	d := r.delta()
	if !strings.Contains(d, "global fact") || !strings.Contains(d, "for project A") {
		t.Errorf("delta missing global/same-project entries: %q", d)
	}
	if strings.Contains(d, "belongs to B") {
		t.Errorf("other-project entry leaked: %q", d)
	}
}

func TestRenderMemoryUpdate_EmptyIsEmpty(t *testing.T) {
	if got := renderMemoryUpdate(""); got != "" {
		t.Errorf("renderMemoryUpdate(\"\") = %q, want empty", got)
	}
	got := renderMemoryUpdate("- [project] x")
	if !strings.Contains(got, "<memory-update>") || !strings.Contains(got, "- [project] x") {
		t.Errorf("renderMemoryUpdate wrapped output = %q", got)
	}
}
