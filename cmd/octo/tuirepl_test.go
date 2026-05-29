package main

import (
	"sync"
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
	tea "github.com/charmbracelet/bubbletea"
)

// fakeProg records messages the sink sends, standing in for *tea.Program.
type fakeProg struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (f *fakeProg) Send(m tea.Msg) {
	f.mu.Lock()
	f.msgs = append(f.msgs, m)
	f.mu.Unlock()
}

func newTestModel() *tuiModel {
	a := agent.New(&stubSender{reply: "ok"}, "m")
	m := newTUIModel(replConfig{a: a, noSave: true})
	m.sink = &tuiSink{prog: &fakeProg{}}
	return m
}

func typeRunes(m *tuiModel, s string) {
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
}

func TestTUI_IdleEnterStartsTurn(t *testing.T) {
	m := newTestModel()
	typeRunes(m, "hello")
	_, _ = m.submit(false)
	if !m.turnRunning {
		t.Fatal("idle Enter should start a turn")
	}
	if len(m.input) != 0 {
		t.Errorf("input should clear after submit, got %q", string(m.input))
	}
}

func TestTUI_RunningEnterSteers(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	typeRunes(m, "also handle errors")
	_, _ = m.submit(false) // Enter
	if !m.a.HasPendingSteer() {
		t.Fatal("Enter while running should steer the agent")
	}
	if got := m.a.DrainSteer(); got != "also handle errors" {
		t.Errorf("steer = %q", got)
	}
	if len(m.queue) != 0 {
		t.Errorf("steer must not enqueue, queue = %+v", m.queue)
	}
}

func TestTUI_RunningAltEnterQueues(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	typeRunes(m, "run the linter")
	_, _ = m.submit(true) // Alt+Enter
	if len(m.queue) != 1 || m.queue[0].text != "run the linter" {
		t.Fatalf("Alt+Enter should queue, got %+v", m.queue)
	}
	if m.a.HasPendingSteer() {
		t.Error("queue must not steer the agent")
	}
}

func TestTUI_EscInterruptsRunningTurn(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	cancelled := false
	m.cancelTurn = func() { cancelled = true }
	m.queue = []pendingItem{{text: "later"}}

	_, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})

	if !cancelled {
		t.Error("Esc should cancel the running turn")
	}
	if len(m.queue) != 1 {
		t.Error("Esc must NOT clear the queue (design §9)")
	}
	if m.quit {
		t.Error("Esc must not quit")
	}
}

func TestTUI_CtrlDQuits(t *testing.T) {
	m := newTestModel()
	_, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	if !m.quit || cmd == nil {
		t.Error("Ctrl+D should set quit and return a quit cmd")
	}
}

func TestTUI_TurnFinishedDequeuesNext(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.queue = []pendingItem{{text: "next task"}}

	_, _ = m.handleTurnFinished()

	// Dequeued and a new turn started.
	if !m.turnRunning {
		t.Fatal("a queued item should start the next turn")
	}
	if len(m.queue) != 0 {
		t.Errorf("queue should be drained, got %+v", m.queue)
	}
}

func TestTUI_DegradedSteerRunsBeforeQueue(t *testing.T) {
	m := newTestModel()
	m.turnRunning = true
	m.a.Steer("degraded steer") // never injected → pending at turn end
	m.queue = []pendingItem{{text: "queued"}}

	_, _ = m.handleTurnFinished()

	// The degraded steer is prepended, so it's the one that starts running and
	// "queued" remains in the queue.
	if len(m.queue) != 1 || m.queue[0].text != "queued" {
		t.Fatalf("queue after dequeue = %+v, want [queued]", m.queue)
	}
	if m.a.HasPendingSteer() {
		t.Error("steer should have been drained into the queue")
	}
}

func TestTUI_PermissionModal(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{Kind: KindPermission, ToolName: "terminal"}, resp: resp})
	if m.modal == nil {
		t.Fatal("modal should be open")
	}

	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got := <-resp
	if !got.Allow || !got.Always {
		t.Errorf("'a' should allow+always, got %+v", got)
	}
	if m.modal != nil {
		t.Error("modal should close after answering")
	}
}

func TestTUI_PermissionModalEscDenies(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{Kind: KindPermission, ToolName: "terminal"}, resp: resp})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEsc})
	if got := <-resp; got.Allow {
		t.Errorf("Esc should deny, got %+v", got)
	}
}

func TestTUI_QuestionModalSelect(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{
		Kind:     KindQuestion,
		Question: "pick",
		Options:  []string{"alpha", "beta"},
	}, resp: resp})

	// Move to "beta" and confirm.
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})

	got := <-resp
	if len(got.Choices) != 1 || got.Choices[0] != "beta" {
		t.Errorf("selection = %+v, want [beta]", got.Choices)
	}
}

func TestTUI_QuestionModalMultiSelect(t *testing.T) {
	m := newTestModel()
	resp := make(chan UserResponse, 1)
	m.openModal(askMsg{prompt: UserPrompt{
		Kind:        KindQuestion,
		Question:    "pick many",
		Options:     []string{"a", "b", "c"},
		MultiSelect: true,
	}, resp: resp})

	// Toggle index 0, move to 2, toggle, confirm.
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeySpace})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyDown})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeySpace})
	_, _ = m.handleModalKey(tea.KeyMsg{Type: tea.KeyEnter})

	got := <-resp
	if len(got.Choices) != 2 || got.Choices[0] != "a" || got.Choices[1] != "c" {
		t.Errorf("multi-select = %+v, want [a c]", got.Choices)
	}
}

func TestTUI_AppendTextBuffersPartialLine(t *testing.T) {
	m := newTestModel()
	m.appendText("hello ")
	m.appendText("world\nnext")
	if got := m.partial.String(); got != "next" {
		t.Errorf("partial = %q, want 'next' (completed line flushed)", got)
	}
}
