// Package tui renders agent tool events as visual cards for the terminal
// REPL. The first card type is the diff card emitted for `edit_file`:
// two header rows (`● Update(path)` / `└ Added N, removed M`) above a
// line-numbered diff body with red/green washes for `-`/`+` rows.
//
// The renderer composes Lipgloss for layout and Chroma for syntax
// highlighting. To keep highlighting visible under the row backgrounds we
// re-emit the background ANSI escape after each `\x1b[0m` reset Chroma
// emits inside a row — see renderRow.
package tui

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/lipgloss"
)

// Line is one row of a rendered diff card.
type Line struct {
	Num   int    // 1-based line number; 0 = no number column
	Kind  rune   // ' ' = unchanged, '+' = added, '-' = removed
	Text  string // raw source line, pre-highlight
	DimNo bool   // dim the line-number column (used for unchanged rows)
}

// Card holds everything needed to render one tool-result card.
//
// Verb is the action being reported ("Update", "Create", …); Path is the
// file the action targets. Added / Removed feed the "└ Added N, removed M"
// summary row. Language is the Chroma lexer name (e.g. "go", "python") —
// empty disables highlighting.
type Card struct {
	Verb     string
	Path     string
	Added    int
	Removed  int
	Lines    []Line
	Language string
}

// Render returns the card as an ANSI-coloured string. The trailing newline
// is omitted so callers can decide on spacing.
func (c Card) Render() string {
	var b strings.Builder

	b.WriteString(renderHeader(c.Verb, c.Path))
	b.WriteString("\n")
	b.WriteString(renderSummary(c.Added, c.Removed))
	b.WriteString("\n")
	for _, ln := range c.Lines {
		b.WriteString(renderRow(ln, c.Language))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderEditCard builds and renders a card for an `edit_file` invocation.
// It reads filePath to compute the line number where newString lands; if
// the read fails (file moved, perms, etc.) the card still renders, just
// without line numbers. Language is inferred from the file extension.
func RenderEditCard(filePath, oldString, newString string) string {
	added := nonEmptyLineCount(newString)
	removed := nonEmptyLineCount(oldString)

	startLine := 0
	if data, err := os.ReadFile(filePath); err == nil {
		if idx := bytes.Index(data, []byte(newString)); idx >= 0 {
			startLine = 1 + bytes.Count(data[:idx], []byte("\n"))
		}
	}

	lines := make([]Line, 0, removed+added)
	for i, t := range splitLinesNoTrail(oldString) {
		num := 0
		if startLine > 0 {
			num = startLine + i
		}
		lines = append(lines, Line{Num: num, Kind: '-', Text: t})
	}
	for i, t := range splitLinesNoTrail(newString) {
		num := 0
		if startLine > 0 {
			num = startLine + i
		}
		lines = append(lines, Line{Num: num, Kind: '+', Text: t})
	}

	c := Card{
		Verb:     "Update",
		Path:     filePath,
		Added:    added,
		Removed:  removed,
		Lines:    lines,
		Language: guessLanguage(filePath),
	}
	return c.Render()
}

// ─── internals ────────────────────────────────────────────────────────────

var (
	headerBullet  = lipgloss.NewStyle().Foreground(lipgloss.Color("#3FB950")).SetString("●")
	headerVerb    = lipgloss.NewStyle().Bold(true)
	headerSummary = lipgloss.NewStyle().Foreground(lipgloss.Color("#8B949E"))

	lineNoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#6E7681")).Width(4).Align(lipgloss.Right)
	lineNoDim   = lineNoStyle.Foreground(lipgloss.Color("#484F58"))

	plusMark  = lipgloss.NewStyle().Foreground(lipgloss.Color("#3FB950")).SetString("+")
	minusMark = lipgloss.NewStyle().Foreground(lipgloss.Color("#F85149")).SetString("-")
)

// Raw 24-bit ANSI background escapes for +/- rows. We use raw codes here
// rather than Lipgloss styles because Chroma emits `\x1b[0m` resets
// mid-line; re-applying the background over a plain string after each
// reset is simpler than coaxing Lipgloss to do it.
const (
	bgAdded   = "\x1b[48;2;14;58;28m" // deep green
	bgRemoved = "\x1b[48;2;74;18;18m" // deep red
	bgReset   = "\x1b[49m"
	resetAll  = "\x1b[0m"
	clearEOL  = "\x1b[K" // paints the row background to the right margin
)

func renderHeader(verb, path string) string {
	return fmt.Sprintf("%s %s",
		headerBullet,
		headerVerb.Render(fmt.Sprintf("%s(%s)", verb, path)),
	)
}

func renderSummary(added, removed int) string {
	return "  " + headerSummary.Render(
		fmt.Sprintf("└ Added %s, removed %s",
			pluralise(added, "line"), pluralise(removed, "line")),
	)
}

func renderRow(ln Line, language string) string {
	noCol := renderLineNo(ln)
	mark := renderMark(ln.Kind)
	body := ln.Text
	if language != "" {
		body = highlightLine(body, language)
	}

	if ln.Kind == ' ' {
		return fmt.Sprintf(" %s %s %s", noCol, mark, body)
	}

	bg := bgAdded
	if ln.Kind == '-' {
		bg = bgRemoved
	}
	// Re-apply background after each reset so highlighting tokens don't
	// punch holes in the row colour.
	body = strings.ReplaceAll(body, resetAll, resetAll+bg)
	return fmt.Sprintf("%s %s %s %s%s%s%s", bg, noCol, mark, body, clearEOL, bgReset, resetAll)
}

func renderLineNo(ln Line) string {
	style := lineNoStyle
	if ln.DimNo {
		style = lineNoDim
	}
	s := ""
	if ln.Num > 0 {
		s = fmt.Sprintf("%d", ln.Num)
	}
	return style.Render(s)
}

func renderMark(kind rune) string {
	switch kind {
	case '+':
		return plusMark.String()
	case '-':
		return minusMark.String()
	default:
		return " "
	}
}

// highlightLine returns the input with Chroma syntax-highlighting ANSI
// escapes applied. On any failure (unknown lexer, format error) the raw
// input is returned unchanged.
func highlightLine(src, language string) string {
	lex := lexers.Get(language)
	if lex == nil {
		return src
	}
	style := styles.Get("github-dark")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal16m")
	if formatter == nil {
		return src
	}
	it, err := lex.Tokenise(nil, src)
	if err != nil {
		return src
	}
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, it); err != nil {
		return src
	}
	return strings.TrimRight(buf.String(), "\n")
}

// guessLanguage maps a path extension to a Chroma lexer name. Empty
// return disables highlighting.
func guessLanguage(path string) string {
	switch {
	case strings.HasSuffix(path, ".go"):
		return "go"
	case strings.HasSuffix(path, ".py"):
		return "python"
	case strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".jsx"):
		return "javascript"
	case strings.HasSuffix(path, ".ts"), strings.HasSuffix(path, ".tsx"):
		return "typescript"
	case strings.HasSuffix(path, ".rs"):
		return "rust"
	case strings.HasSuffix(path, ".rb"):
		return "ruby"
	case strings.HasSuffix(path, ".md"):
		return "markdown"
	case strings.HasSuffix(path, ".sh"), strings.HasSuffix(path, ".bash"):
		return "bash"
	case strings.HasSuffix(path, ".json"):
		return "json"
	case strings.HasSuffix(path, ".yaml"), strings.HasSuffix(path, ".yml"):
		return "yaml"
	}
	return ""
}

// splitLinesNoTrail splits s on '\n' but drops the trailing empty element
// that strings.Split would produce when s ends in '\n'. A genuine blank
// line in the middle of s is preserved.
func splitLinesNoTrail(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// nonEmptyLineCount returns how many lines in s contain at least one
// non-whitespace character. Blank lines are still part of the diff body
// but don't count toward "added N, removed M" — which matches Claude
// Code's behaviour for the summary row.
func nonEmptyLineCount(s string) int {
	n := 0
	for _, line := range splitLinesNoTrail(s) {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

func pluralise(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
