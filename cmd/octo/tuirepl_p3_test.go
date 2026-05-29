package main

import (
	"strings"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
)

func TestTUI_TickAdvancesOnlyWhileRunning(t *testing.T) {
	m := newTestModel()

	m.turnRunning = true
	before := m.spinnerFrame
	_, cmd := m.Update(tickMsg{})
	if m.spinnerFrame != before+1 {
		t.Errorf("spinnerFrame = %d, want %d", m.spinnerFrame, before+1)
	}
	if cmd == nil {
		t.Error("tick should reschedule itself while a turn runs")
	}

	m.turnRunning = false
	m.spinnerFrame = 7
	_, cmd = m.Update(tickMsg{})
	if m.spinnerFrame != 7 {
		t.Errorf("idle tick should not advance the frame; got %d", m.spinnerFrame)
	}
	if cmd != nil {
		t.Error("tick should stop rescheduling once the turn ends")
	}
}

func TestTUI_RunningToolIndicator(t *testing.T) {
	m := newTestModel() // cfg.plain == false → terminal renders as a card

	m.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolID: "c1", ToolName: "terminal",
		Input: map[string]any{"command": "ls"},
	})
	if m.running == nil {
		t.Fatal("a running card tool should set the live indicator")
	}
	if m.running.verb != "Run" || m.running.target != "ls" {
		t.Errorf("running = %+v, want verb=Run target=ls", *m.running)
	}

	m.handleEvent(agent.AgentEvent{Kind: agent.EventToolDone, ToolID: "c1", ToolName: "terminal", Output: "file"})
	if m.running != nil {
		t.Error("done should clear the live indicator (the card replaces it)")
	}
}

func TestTUI_NonCardToolNoIndicator(t *testing.T) {
	m := newTestModel()
	m.handleEvent(agent.AgentEvent{
		Kind: agent.EventToolStarted, ToolID: "c1", ToolName: "launch_agent",
		Input: map[string]any{},
	})
	if m.running != nil {
		t.Error("non-card tools commit a started line; they must not set the live indicator")
	}
}

func TestSpinnerLine_Contents(t *testing.T) {
	m := newTestModel()
	out := m.spinnerLine("Run(ls)", time.Now())
	if !strings.Contains(out, "Run(ls)") || !strings.Contains(out, "0s") {
		t.Errorf("spinnerLine = %q, want it to contain the label and elapsed", out)
	}
}

func TestThinkingPhrase_Rotates(t *testing.T) {
	m := newTestModel()
	m.spinnerFrame = 0
	if got := m.thinkingPhrase(); got != "Thinking" {
		t.Errorf("frame 0 phrase = %q, want Thinking", got)
	}
	m.spinnerFrame = 16
	if got := m.thinkingPhrase(); got == "Thinking" {
		t.Errorf("phrase should rotate by frame; still %q at frame 16", got)
	}
}
