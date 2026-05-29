package tui

import (
	"strings"
	"testing"
)

func TestRenderOutputCard_HeaderAndBody(t *testing.T) {
	got := RenderOutputCard("Run", "go test ./...", "ok  pkg/a  0.2s\nok  pkg/b  0.1s", 0, false)
	for _, want := range []string{"Run(go test ./...)", "ok  pkg/a  0.2s", "ok  pkg/b  0.1s"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderOutputCard_CapsWithMoreMarker(t *testing.T) {
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "line")
	}
	got := RenderOutputCard("Search", "foo", strings.Join(lines, "\n"), 5, false)
	if !strings.Contains(got, "15 more lines") {
		t.Errorf("expected '15 more lines' marker; got:\n%s", got)
	}
	// Exactly 5 body lines shown (count the gutter glyph occurrences).
	if n := strings.Count(got, "│"); n != 5 {
		t.Errorf("expected 5 gutter rows, got %d:\n%s", n, got)
	}
}

func TestRenderOutputCard_EmptyOutput(t *testing.T) {
	got := RenderOutputCard("Run", "true", "", 0, false)
	if !strings.Contains(got, "(no output)") {
		t.Errorf("expected '(no output)'; got:\n%s", got)
	}
}

func TestRenderOutputCard_SingularMoreLine(t *testing.T) {
	got := RenderOutputCard("Run", "x", "a\nb\nc", 2, false)
	if !strings.Contains(got, "1 more line") || strings.Contains(got, "1 more lines") {
		t.Errorf("expected singular '1 more line'; got:\n%s", got)
	}
}
