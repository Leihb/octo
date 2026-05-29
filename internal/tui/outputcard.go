package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	outBullet    = lipgloss.NewStyle().Foreground(ColAccent).SetString("●")
	outBulletErr = lipgloss.NewStyle().Foreground(ColDanger).SetString("●")
	outGutter    = lipgloss.NewStyle().Foreground(ColDim).SetString("│")
	outMore      = lipgloss.NewStyle().Foreground(ColMuted)
)

// RenderOutputCard renders a tool's textual output as a card: a header row
// (● verb(target)) above the output, each line behind a "│" gutter, capped to
// maxLines (<=0 = no cap) with a "└ N more lines" marker for the remainder.
// isErr tints the bullet red. Empty output renders "(no output)". The trailing
// newline is omitted so callers control spacing (mirrors Card.Render).
func RenderOutputCard(verb, target, output string, maxLines int, isErr bool) string {
	bullet := outBullet
	if isErr {
		bullet = outBulletErr
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %s", bullet, headerVerb.Render(fmt.Sprintf("%s(%s)", verb, target))))

	lines := splitLinesNoTrail(output)
	if len(lines) == 0 {
		b.WriteString("\n  " + outGutter.String() + " " + outMore.Render("(no output)"))
		return b.String()
	}

	shown, extra := lines, 0
	if maxLines > 0 && len(lines) > maxLines {
		shown, extra = lines[:maxLines], len(lines)-maxLines
	}
	for _, ln := range shown {
		b.WriteString("\n  " + outGutter.String() + " " + ln)
	}
	if extra > 0 {
		b.WriteString("\n  " + outMore.Render("└ "+pluralise(extra, "more line")))
	}
	return b.String()
}
