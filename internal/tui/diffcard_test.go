package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCard_Render_Structure(t *testing.T) {
	c := Card{
		Verb:    "Update",
		Path:    "internal/example.go",
		Added:   2,
		Removed: 1,
		Lines: []Line{
			{Num: 10, Kind: ' ', Text: "context"},
			{Num: 11, Kind: '-', Text: "old line"},
			{Num: 11, Kind: '+', Text: "new line one"},
			{Num: 12, Kind: '+', Text: "new line two"},
		},
	}

	out := c.Render()

	if !strings.Contains(out, "internal/example.go") {
		t.Errorf("output missing path:\n%s", out)
	}
	if !strings.Contains(out, "Added 2 lines, removed 1 line") {
		t.Errorf("output missing summary row with correct plurals:\n%s", out)
	}
	// Number column right-aligned to width 4 should contain "10", "11", "12".
	for _, n := range []string{"10", "11", "12"} {
		if !strings.Contains(out, n) {
			t.Errorf("output missing line number %q:\n%s", n, out)
		}
	}
	// + rows should carry the deep-green background escape.
	if !strings.Contains(out, bgAdded) {
		t.Errorf("output missing added-row background escape:\n%s", out)
	}
	// - rows should carry the deep-red background escape.
	if !strings.Contains(out, bgRemoved) {
		t.Errorf("output missing removed-row background escape:\n%s", out)
	}
	// Background rows must each end with the right-margin paint then reset.
	if !strings.Contains(out, clearEOL+bgReset+resetAll) {
		t.Errorf("output missing row terminator escape sequence:\n%s", out)
	}
}

func TestCard_SummaryPlural(t *testing.T) {
	cases := []struct {
		added, removed int
		want           string
	}{
		{1, 1, "Added 1 line, removed 1 line"},
		{0, 5, "Added 0 lines, removed 5 lines"},
		{3, 0, "Added 3 lines, removed 0 lines"},
	}
	for _, tc := range cases {
		c := Card{Added: tc.added, Removed: tc.removed}
		if got := c.Render(); !strings.Contains(got, tc.want) {
			t.Errorf("summary %d/%d: missing %q in:\n%s", tc.added, tc.removed, tc.want, got)
		}
	}
}

func TestRenderEditCard_NumbersFromFile(t *testing.T) {
	// Write a file whose new_string lands at a known line, then verify the
	// card numbers its +/- rows starting at that line.
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.go")
	body := "package x\n\nfunc Old() {}\nfunc Bar() {}\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-write the post-edit content so RenderEditCard can locate newStr.
	newBody := "package x\n\nfunc New() {}\nfunc Bar() {}\n"
	if err := os.WriteFile(path, []byte(newBody), 0o644); err != nil {
		t.Fatal(err)
	}

	out := RenderEditCard(path, "func Old() {}", "func New() {}")
	if !strings.Contains(out, "3") {
		t.Errorf("expected line number 3 (location of the new function) in output:\n%s", out)
	}
	if !strings.Contains(out, path) {
		t.Errorf("expected path in header:\n%s", out)
	}
}

func TestRenderEditCard_FileMissing_StillRenders(t *testing.T) {
	// File doesn't exist — card should still render (no line numbers).
	out := RenderEditCard("/nonexistent/path/that/never/was.go", "alpha", "beta")
	if !strings.Contains(out, "beta") {
		t.Errorf("expected new_string in output even on missing file:\n%s", out)
	}
	if !strings.Contains(out, "alpha") {
		t.Errorf("expected old_string in output even on missing file:\n%s", out)
	}
}

func TestGuessLanguage(t *testing.T) {
	cases := map[string]string{
		"foo.go":   "go",
		"foo.py":   "python",
		"foo.tsx":  "typescript",
		"foo.unk":  "",
		"noExt":    "",
		"foo.md":   "markdown",
		"foo.yaml": "yaml",
	}
	for path, want := range cases {
		if got := guessLanguage(path); got != want {
			t.Errorf("guessLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestSplitLinesNoTrail(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a", []string{"a"}},
		{"a\n", []string{"a"}},
		{"a\nb", []string{"a", "b"}},
		{"a\nb\n", []string{"a", "b"}},
		{"a\n\nb", []string{"a", "", "b"}}, // blank line preserved
	}
	for _, tc := range cases {
		got := splitLinesNoTrail(tc.in)
		if !equalSlices(got, tc.want) {
			t.Errorf("splitLinesNoTrail(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
