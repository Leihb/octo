package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderStatusBar_ShowsModelAndHint(t *testing.T) {
	m := newTestModel() // agent model name is "m"
	out := m.renderStatusBar()
	if !strings.Contains(out, "m") {
		t.Errorf("status bar should include the model; got:\n%s", out)
	}
	// Idle: no persistent hint (the startup hint is shown once in the Banner).
	if strings.Contains(out, "Enter send") {
		t.Errorf("idle status bar should NOT show a persistent send hint; got:\n%s", out)
	}
	// Running hint switches.
	m.turnRunning = true
	if got := m.renderStatusBar(); !strings.Contains(got, "interrupt") {
		t.Errorf("running status bar should show the interrupt hint; got:\n%s", got)
	}
}

func TestRenderInputBox_FlatStyle(t *testing.T) {
	m := newTestModel()
	setInput(m, "hi")

	// Flat style: no border chars at any width, just prompt + input.
	m.width = 0
	if got := m.renderInputBox(); strings.ContainsAny(got, "╭╮╰╯") {
		t.Errorf("no border expected at width 0; got:\n%s", got)
	}
	if !strings.Contains(m.renderInputBox(), "> ") {
		t.Error("input box should always show the prompt")
	}

	m.width = 60
	if got := m.renderInputBox(); strings.ContainsAny(got, "╭╮╰╯") {
		t.Errorf("flat style should never draw a border; got:\n%s", got)
	}
	if !strings.Contains(m.renderInputBox(), "> ") {
		t.Error("input box should always show the prompt")
	}
}

func TestAbbreviateHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if got := abbreviateHome(home); got != "~" {
		t.Errorf("abbreviateHome(home) = %q, want ~", got)
	}
	sub := filepath.Join(home, "proj", "octo")
	if got := abbreviateHome(sub); got != "~"+string(filepath.Separator)+filepath.Join("proj", "octo") {
		t.Errorf("abbreviateHome(sub) = %q, want ~/proj/octo", got)
	}
	if got := abbreviateHome("/etc/hosts"); got != "/etc/hosts" {
		t.Errorf("abbreviateHome outside home should be unchanged; got %q", got)
	}
	if got := abbreviateHome(""); got != "" {
		t.Errorf("abbreviateHome(\"\") = %q, want empty", got)
	}
}
