package tui

import "github.com/charmbracelet/lipgloss"

// Palette is the shared TUI colour set. These mirror the github-dark-ish
// true-colour values the diff card already uses inline; a later phase swaps
// them for lipgloss.AdaptiveColor (light/dark aware) and folds the diff card's
// own inline styles in here too. Skeleton for now — one place to grow the
// theme as the TUI gains cards, a status bar, and panels.
var (
	ColAccent = lipgloss.Color("#3FB950") // success / additions
	ColDanger = lipgloss.Color("#F85149") // errors / removals
	ColMuted  = lipgloss.Color("#8B949E") // secondary text
	ColDim    = lipgloss.Color("#6E7681") // gutters, line numbers
)
