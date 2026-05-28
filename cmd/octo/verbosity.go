package main

import (
	"os"
	"strings"
)

// verbosity controls how much status chrome the CLI prints around the
// model's actual output. Three levels:
//
//	quiet   — strip everything optional: no spinner, no startup banner,
//	          no per-turn cache line, no auto-save confirmation. Useful
//	          for clean piped output and for scripted use where chrome
//	          gets in the way.
//	normal  — default. Spinner during pauses, startup banner, the
//	          existing ↳ tool status lines.
//	verbose — normal plus extra context: provider + model + endpoint on
//	          first turn, and the per-turn cache line is always shown
//	          (in normal mode it shows only when cache actually moved).
type verbosity int

const (
	verbosityNormal verbosity = iota
	verbosityQuiet
	verbosityVerbose
)

// resolveVerbosity combines the --quiet / --verbose CLI flags with the
// OCTO_VERBOSITY env var. Precedence (highest wins):
//
//  1. --quiet      (CLI)
//  2. --verbose    (CLI)
//  3. OCTO_VERBOSITY  (env: quiet | normal | verbose; anything else → normal)
//
// Both flags at once is an explicit precedence (quiet wins) — they're
// passed in directly here so the flag.FlagSet stays declarative.
func resolveVerbosity(quietFlag, verboseFlag bool) verbosity {
	switch {
	case quietFlag:
		return verbosityQuiet
	case verboseFlag:
		return verbosityVerbose
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OCTO_VERBOSITY"))) {
	case "quiet":
		return verbosityQuiet
	case "verbose":
		return verbosityVerbose
	}
	return verbosityNormal
}

// quiet reports whether status chrome should be suppressed.
func (v verbosity) quiet() bool { return v == verbosityQuiet }

// verbose reports whether extra context should be printed.
func (v verbosity) verbose() bool { return v == verbosityVerbose }
