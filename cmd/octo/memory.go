package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/Leihb/octo-agent/internal/memory"
)

// runMemory handles `octo memory <subcommand>`. Currently `list` (active
// entries + consolidated summary) and `list --archive` (entries already folded
// into the summary, kept as authoritative sources).
func runMemory(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprintln(stderr, "Usage: octo memory list [--archive]")
		return 2
	}
	showArchive := false
	for _, a := range args[1:] {
		if a == "--archive" {
			showArchive = true
		}
	}

	store, err := memory.NewStore()
	if err != nil {
		fmt.Fprintf(stderr, "octo memory: %v\n", err)
		return 1
	}

	var (
		entries []memory.Entry
		header  string
	)
	if showArchive {
		entries, err = store.ListArchived()
		header = "Archived memories:"
	} else {
		entries, err = store.List()
		header = "Stored memories:"
	}
	if err != nil {
		fmt.Fprintf(stderr, "octo memory: %v\n", err)
		return 1
	}

	switch {
	case len(entries) == 0 && showArchive:
		fmt.Fprintln(stdout, "No archived memories.")
	case len(entries) == 0:
		fmt.Fprintln(stdout, "No memories stored.")
	default:
		fmt.Fprintln(stdout, header)
		for _, e := range entries {
			fmt.Fprintf(stdout, "  %-28s [%-9s] %s\n", e.Name, e.Type, e.Description)
		}
	}

	// In the non-archive view, also show the consolidated summary (the actual
	// injection source) so users can see consolidation happened — otherwise it
	// generates memory_summary.md silently.
	if !showArchive {
		if sum := store.ReadSummary(); sum != "" {
			fmt.Fprintln(stdout, "\nConsolidated summary (injected next session):")
			for _, line := range strings.Split(sum, "\n") {
				fmt.Fprintf(stdout, "  %s\n", line)
			}
		}
	}
	return 0
}
