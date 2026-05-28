package main

import (
	"fmt"
	"io"

	"github.com/Leihb/octo-agent/internal/memory"
)

// runMemory handles `octo memory <subcommand>`. Currently just `list`, which
// prints the stored cross-session memory entries.
func runMemory(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprintln(stderr, "Usage: octo memory list")
		return 2
	}

	store, err := memory.NewStore()
	if err != nil {
		fmt.Fprintf(stderr, "octo memory: %v\n", err)
		return 1
	}
	entries, err := store.List()
	if err != nil {
		fmt.Fprintf(stderr, "octo memory: %v\n", err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "No memories stored.")
		return 0
	}
	fmt.Fprintln(stdout, "Stored memories:")
	for _, e := range entries {
		fmt.Fprintf(stdout, "  %-28s [%-9s] %s\n", e.Name, e.Type, e.Description)
	}
	return 0
}
