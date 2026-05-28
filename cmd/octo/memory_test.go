package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/memory"
)

func TestRunMemory_List(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	var out bytes.Buffer
	if code := runMemory([]string{"list"}, &out, &out); code != 0 {
		t.Fatalf("empty list exit = %d", code)
	}
	if !strings.Contains(out.String(), "No memories") {
		t.Errorf("empty store should say so:\n%s", out.String())
	}

	store, err := memory.NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(memory.Entry{Name: "n", Description: "prefers Go", Type: memory.TypeUser}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	runMemory([]string{"list"}, &out, &out)
	if !strings.Contains(out.String(), "prefers Go") {
		t.Errorf("list should show the entry:\n%s", out.String())
	}
}

func TestRunMemory_BadSubcommand(t *testing.T) {
	var out bytes.Buffer
	if code := runMemory([]string{"bogus"}, &out, &out); code != 2 {
		t.Errorf("bad subcommand exit = %d, want 2", code)
	}
	if code := runMemory(nil, &out, &out); code != 2 {
		t.Errorf("no subcommand exit = %d, want 2", code)
	}
}

func TestREPL_MemoryCommand(t *testing.T) {
	cfg, stdout, _, stub := makeREPLFixture(t, "/memory\n/exit\n")
	store := memory.NewStoreAt(t.TempDir())
	if err := store.Save(memory.Entry{Name: "n", Description: "remembered thing", Type: memory.TypeFeedback}); err != nil {
		t.Fatal(err)
	}
	cfg.memStore = store

	runREPL(cfg)
	if stub.called != 0 {
		t.Errorf("/memory should not call the sender, got %d", stub.called)
	}
	if !strings.Contains(stdout.String(), "remembered thing") {
		t.Errorf("/memory output missing entry:\n%s", stdout.String())
	}
}

func TestREPL_MemoryDisabled(t *testing.T) {
	cfg, stdout, _, _ := makeREPLFixture(t, "/memory\n/exit\n")
	// memStore left nil → memory disabled
	runREPL(cfg)
	if !strings.Contains(stdout.String(), "disabled") {
		t.Errorf("expected disabled notice:\n%s", stdout.String())
	}
}
