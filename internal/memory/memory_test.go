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

func TestSave_AutoSlugFromDescription(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	if err := s.Save(Entry{Description: "Run tests before committing!", Type: TypeFeedback}); err != nil {
		t.Fatal(err)
	}
	entries, _ := s.List()
	if len(entries) != 1 || entries[0].Name != "run-tests-before-committing" {
		t.Errorf("Save should auto-slug from description, got %+v", entries)
	}
}

func TestSave_RequiresDescription(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	if err := s.Save(Entry{Type: TypeUser}); err == nil {
		t.Error("expected error for missing description (no source for auto-slug)")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Run tests before committing!": "run-tests-before-committing",
		"  Hello, World  ":             "hello-world",
		"用户偏好 Go":                      "go",
		"!!! ":                         "",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestState_RoundTrip(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	if st := s.LoadState(); st.LastExtractedSession != "" || st.LastConsolidated != "" {
		t.Errorf("missing state should be zero: %+v", st)
	}
	in := State{LastExtractedSession: "sess-123", LastConsolidated: "2026-05-28"}
	if err := s.SaveState(in); err != nil {
		t.Fatal(err)
	}
	got := s.LoadState()
	if got != in {
		t.Errorf("State round-trip: %+v, want %+v", got, in)
	}
}

func TestWriteSummary_PreferredByInjection(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	_ = s.Save(Entry{Description: "prefers Go", Type: TypeUser})
	if err := s.WriteSummary("CONSOLIDATED"); err != nil {
		t.Fatal(err)
	}
	out, _ := s.RenderInjection()
	if !strings.Contains(out, "CONSOLIDATED") || strings.Contains(out, "prefers Go") {
		t.Errorf("WriteSummary should be preferred over entries:\n%s", out)
	}
}

// requireGit skips the test when `git` isn't on PATH. The git-baseline path
// (auto-commit, ListArchived, HeadSHA, WorkspaceDiff) all degrade to no-ops
// without git; tests for those paths can't meaningfully run without it.
func requireGit(t *testing.T) {
	t.Helper()
	if !gitAvailable() {
		t.Skip("git not on PATH — skipping git-baseline test")
	}
}

func TestArchiveAll_DropsEntriesAndRebuildsIndex(t *testing.T) {
	requireGit(t)
	s := NewStoreAt(t.TempDir()).EnableGit()
	_ = s.Save(Entry{Description: "first", Type: TypeUser})
	_ = s.Save(Entry{Description: "second", Type: TypeFeedback})

	if err := s.ArchiveAll(); err != nil {
		t.Fatal(err)
	}
	// Active list is now empty.
	if active, _ := s.List(); len(active) != 0 {
		t.Errorf("after ArchiveAll, List should be empty: %+v", active)
	}
	// Archived list (recovered via git log) contains both.
	archived, err := s.ListArchived()
	if err != nil {
		t.Fatal(err)
	}
	if len(archived) != 2 {
		t.Fatalf("expected 2 archived entries, got %d: %+v", len(archived), archived)
	}
	// Index was rebuilt (and now empty).
	b, err := os.ReadFile(filepath.Join(s.dir, indexFile))
	if err != nil {
		t.Fatalf("index missing after archive: %v", err)
	}
	if strings.Contains(string(b), "first") || strings.Contains(string(b), "second") {
		t.Errorf("index should not list archived entries:\n%s", string(b))
	}
}

func TestArchiveAll_PreservesArchivedContent(t *testing.T) {
	requireGit(t)
	s := NewStoreAt(t.TempDir()).EnableGit()
	_ = s.Save(Entry{Description: "a fact", Type: TypeProject, Body: "the body"})
	_ = s.ArchiveAll()

	archived, _ := s.ListArchived()
	if len(archived) != 1 || archived[0].Body != "the body" || archived[0].Description != "a fact" {
		t.Errorf("archived entry lost content: %+v", archived)
	}
}

func TestArchiveAll_KeepsLatestVersionAcrossRounds(t *testing.T) {
	requireGit(t)
	s := NewStoreAt(t.TempDir()).EnableGit()
	// First round: save and archive.
	_ = s.Save(Entry{Name: "n", Description: "v1", Type: TypeUser})
	_ = s.ArchiveAll()
	// Second round: same name, new content — Save then Archive again.
	_ = s.Save(Entry{Name: "n", Description: "v2", Type: TypeUser})
	if err := s.ArchiveAll(); err != nil {
		t.Fatal(err)
	}
	archived, _ := s.ListArchived()
	if len(archived) != 1 || archived[0].Description != "v2" {
		t.Errorf("archive should keep the latest version, got %+v", archived)
	}
}

func TestSave_AutoCommitsWhenGitEnabled(t *testing.T) {
	requireGit(t)
	s := NewStoreAt(t.TempDir()).EnableGit()
	_ = s.Save(Entry{Description: "first fact", Type: TypeUser})

	if !isGitRepo(s.dir) {
		t.Fatalf("Save should lazily init a git repo when git is enabled")
	}
	sha, err := s.HeadSHA()
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Errorf("HeadSHA should be non-empty after a successful Save+commit")
	}
}

func TestSave_NoGitWhenDisabled(t *testing.T) {
	// Default NewStoreAt has git off — Save should not create .git/.
	s := NewStoreAt(t.TempDir())
	_ = s.Save(Entry{Description: "first fact", Type: TypeUser})
	if isGitRepo(s.dir) {
		t.Errorf("Save should NOT create .git/ when git is disabled")
	}
}

func TestWorkspaceDiff_ReportsNewEntriesSinceBaseline(t *testing.T) {
	requireGit(t)
	s := NewStoreAt(t.TempDir()).EnableGit()
	_ = s.Save(Entry{Description: "before baseline", Type: TypeUser})
	base, _ := s.HeadSHA()
	if base == "" {
		t.Fatal("baseline SHA must be set after a save")
	}

	_ = s.Save(Entry{Description: "after baseline", Type: TypeFeedback})

	diff, err := s.WorkspaceDiff(base)
	if err != nil {
		t.Fatal(err)
	}
	// The diff must record that after-baseline.md was added (a "new file mode"
	// or "+++ b/after-baseline.md" line) but must NOT report before-baseline.md
	// as newly added — it was already in the baseline.
	if !strings.Contains(diff, "+++ b/after-baseline.md") {
		t.Errorf("WorkspaceDiff should record the new file added since the baseline:\n%s", diff)
	}
	if strings.Contains(diff, "+++ b/before-baseline.md") {
		t.Errorf("WorkspaceDiff should NOT record the baseline file as new:\n%s", diff)
	}
}

func TestListArchived_EmptyWithoutGit(t *testing.T) {
	// Without git enabled, ListArchived returns nil — the older archive/
	// subdir no longer exists and there's no history to walk.
	s := NewStoreAt(t.TempDir())
	_ = s.Save(Entry{Description: "x", Type: TypeUser})
	_ = s.ArchiveAll()

	got, err := s.ListArchived()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("ListArchived without git should be nil, got %+v", got)
	}
}

func TestReadSummary(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	if got := s.ReadSummary(); got != "" {
		t.Errorf("missing summary = %q, want empty", got)
	}
	_ = s.WriteSummary("hello world")
	if got := s.ReadSummary(); got != "hello world" {
		t.Errorf("ReadSummary = %q, want %q", got, "hello world")
	}
}

func TestWriteSummary_StampsV1Marker(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	if err := s.WriteSummary("body content"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(s.dir, summaryFile))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.HasPrefix(got, summaryMarker) {
		t.Errorf("summary file should start with %q, got:\n%s", summaryMarker, got)
	}
	// The body must still be there after the marker line.
	if !strings.Contains(got, "body content") {
		t.Errorf("summary body lost:\n%s", got)
	}
}

func TestReadSummary_HidesMarkerFromCallers(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	_ = s.WriteSummary("alpha\nbeta")
	got := s.ReadSummary()
	if strings.Contains(got, summaryMarker) {
		t.Errorf("ReadSummary should strip the marker, got: %q", got)
	}
	if got != "alpha\nbeta" {
		t.Errorf("ReadSummary = %q, want %q", got, "alpha\nbeta")
	}
}

func TestReadSummary_AcceptsLegacyMarkerless(t *testing.T) {
	// Summaries written by the PR-#96 era code had no marker. They must still
	// read cleanly so the upgrade is non-breaking.
	s := NewStoreAt(t.TempDir())
	_ = s.ensureDir()
	if err := os.WriteFile(filepath.Join(s.dir, summaryFile), []byte("legacy body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := s.ReadSummary(); got != "legacy body" {
		t.Errorf("legacy markerless summary should pass through, got %q", got)
	}
}

func TestRenderInjection_HidesMarker(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	_ = s.WriteSummary("CONSOLIDATED")
	out, err := s.RenderInjection()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, summaryMarker) {
		t.Errorf("injection should not leak the protocol marker:\n%s", out)
	}
	if !strings.Contains(out, "CONSOLIDATED") {
		t.Errorf("injection missing summary body:\n%s", out)
	}
}

func TestSaveRolloutSummary_WritesAndLists(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	if err := s.SaveRolloutSummary("tune-extract", "# tuned the extract prompt\n\ndetails…"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListRolloutSummaries()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 rollout summary, got %d", len(got))
	}
	if got[0].Slug != "tune-extract" {
		t.Errorf("slug = %q, want %q", got[0].Slug, "tune-extract")
	}
	if !strings.Contains(got[0].Body, "tuned the extract prompt") {
		t.Errorf("body lost: %q", got[0].Body)
	}
	// Filename should have the timestamp prefix so on-disk ordering is chronological.
	if !strings.HasSuffix(got[0].Filename, "-tune-extract.md") {
		t.Errorf("filename = %q, expected to end with -tune-extract.md", got[0].Filename)
	}
}

func TestSaveRolloutSummary_NoOpOnEmpty(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	if err := s.SaveRolloutSummary("", "body"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRolloutSummary("slug", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListRolloutSummaries()
	if len(got) != 0 {
		t.Errorf("empty slug or body should be a no-op, got %+v", got)
	}
}

func TestSaveRolloutSummary_CommitsWhenGitEnabled(t *testing.T) {
	requireGit(t)
	s := NewStoreAt(t.TempDir()).EnableGit()
	if err := s.SaveRolloutSummary("first-rollout", "body"); err != nil {
		t.Fatal(err)
	}
	sha, _ := s.HeadSHA()
	if sha == "" {
		t.Errorf("HeadSHA should be non-empty after a rollout-summary commit")
	}
}

func TestExportNotes(t *testing.T) {
	s := NewStoreAt(t.TempDir())
	if notes, _ := s.ExportNotes(); notes != "" {
		t.Errorf("empty store ExportNotes = %q, want empty", notes)
	}
	_ = s.Save(Entry{Description: "prefers Go", Type: TypeUser, Body: "User said so."})
	notes, _ := s.ExportNotes()
	if !strings.Contains(notes, "[user] prefers Go") || !strings.Contains(notes, "User said so") {
		t.Errorf("ExportNotes missing pieces:\n%s", notes)
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
