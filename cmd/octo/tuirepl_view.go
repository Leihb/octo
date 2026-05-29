package main

import (
	"fmt"
	"strings"

	"github.com/Leihb/octo-agent/internal/agent"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	promptStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	noticeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	toolErrStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	queueStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	modalStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	hintStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
)

// handleKey routes a keypress by context: a modal grabs all keys; otherwise the
// keymap depends on whether a turn is running (design §7).
func (m *tuiModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modal != nil {
		return m.handleModalKey(msg)
	}

	switch msg.Type {
	case tea.KeyCtrlD:
		m.quit = true
		return m, tea.Quit

	case tea.KeyCtrlC:
		if m.turnRunning {
			m.interrupt()
			return m, nil
		}
		m.quit = true
		return m, tea.Quit

	case tea.KeyEsc:
		if m.turnRunning {
			m.interrupt()
			return m, nil
		}
		// Idle: clear the input line.
		m.input = nil
		return m, nil

	case tea.KeyEnter:
		return m.submit(msg.Alt)

	case tea.KeyBackspace:
		if n := len(m.input); n > 0 {
			m.input = m.input[:n-1]
		}
		return m, nil

	case tea.KeyRunes, tea.KeySpace:
		m.input = append(m.input, msg.Runes...)
		return m, nil
	}
	return m, nil
}

// submit acts on Enter / Alt+Enter. Idle → start a turn. Running → steer (Enter)
// or queue (Alt+Enter). Empty input is ignored.
func (m *tuiModel) submit(alt bool) (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(string(m.input))
	if text == "" {
		return m, nil
	}
	m.input = nil

	// Slash commands only when idle (mirror the plain REPL's exits).
	if !m.turnRunning {
		switch strings.ToLower(text) {
		case "/exit", "/quit":
			m.quit = true
			return m, tea.Quit
		}
	}

	if !m.turnRunning {
		return m, m.startTurn(text)
	}

	if alt {
		// Queue: run as a future turn.
		m.queue = append(m.queue, pendingItem{text: text})
		return m, tea.Println(queueStyle.Render("＋ queued: " + text))
	}
	// Steer: fold into the running turn at the next tool-batch boundary.
	m.a.Steer(text)
	return m, tea.Println(queueStyle.Render("→ steering: " + text))
}

func (m *tuiModel) interrupt() {
	if m.cancelTurn != nil {
		m.cancelTurn()
	}
}

// ── modal (Ask) ──

func (m *tuiModel) openModal(msg askMsg) {
	st := &modalState{prompt: msg.prompt, resp: msg.resp, selected: map[int]bool{}}
	if msg.prompt.Kind == KindQuestion {
		st.options = append(st.options, msg.prompt.Options...)
		st.options = append(st.options, "Other (free text)")
	}
	m.modal = st
}

// answerModal sends a response and clears the modal.
func (m *tuiModel) answerModal(r UserResponse) {
	if m.modal == nil {
		return
	}
	select {
	case m.modal.resp <- r:
	default:
	}
	m.modal = nil
}

func (m *tuiModel) handleModalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	st := m.modal
	if st.prompt.Kind == KindPermission {
		switch {
		case keyIs(msg, 'y'):
			m.answerModal(UserResponse{Allow: true})
		case keyIs(msg, 'a'):
			m.answerModal(UserResponse{Allow: true, Always: true})
		case msg.Type == tea.KeyEsc, keyIs(msg, 'n'):
			m.answerModal(UserResponse{Allow: false})
		}
		return m, nil
	}

	// Question modal: arrow/j-k to move, space to toggle (multi), enter to
	// confirm, esc to cancel.
	switch msg.Type {
	case tea.KeyEsc:
		m.answerModal(UserResponse{Cancelled: true})
	case tea.KeyUp:
		if st.cursor > 0 {
			st.cursor--
		}
	case tea.KeyDown:
		if st.cursor < len(st.options)-1 {
			st.cursor++
		}
	case tea.KeySpace:
		if st.prompt.MultiSelect {
			st.selected[st.cursor] = !st.selected[st.cursor]
		}
	case tea.KeyEnter:
		m.confirmQuestion(st)
	case tea.KeyRunes:
		switch string(msg.Runes) {
		case "j":
			if st.cursor < len(st.options)-1 {
				st.cursor++
			}
		case "k":
			if st.cursor > 0 {
				st.cursor--
			}
		}
	}
	return m, nil
}

// confirmQuestion maps the modal selection onto a UserResponse. The trailing
// "Other" slot can't be resolved to free text without a second input step; for
// now picking it cancels (the model can ask again or pick a default). A future
// pass can add an inline free-text field.
func (m *tuiModel) confirmQuestion(st *modalState) {
	otherIdx := len(st.options) - 1

	if st.prompt.MultiSelect {
		var picks []string
		for i := range st.options {
			if i == otherIdx {
				continue
			}
			if st.selected[i] {
				picks = append(picks, st.prompt.Options[i])
			}
		}
		if len(picks) == 0 {
			m.answerModal(UserResponse{Cancelled: true})
			return
		}
		m.answerModal(UserResponse{Choices: picks})
		return
	}

	if st.cursor == otherIdx {
		m.answerModal(UserResponse{Cancelled: true})
		return
	}
	m.answerModal(UserResponse{Choices: []string{st.prompt.Options[st.cursor]}})
}

// keyIs reports whether msg is a single-rune press of r (case-insensitive).
func keyIs(msg tea.KeyMsg, r rune) bool {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return false
	}
	got := msg.Runes[0]
	return got == r || got == r-32 || got == r+32
}

// ── View ──

func (m *tuiModel) View() string {
	if m.quit {
		return ""
	}
	if m.modal != nil {
		return m.modalView()
	}

	var b strings.Builder

	// Live partial assistant line (committed lines already scrolled up).
	if p := m.partial.String(); p != "" {
		b.WriteString(p)
		b.WriteByte('\n')
	} else if m.turnRunning && !m.streaming {
		b.WriteString(hintStyle.Render("thinking…"))
		b.WriteByte('\n')
	}

	// Queue panel.
	if len(m.queue) > 0 {
		b.WriteString(queueStyle.Render(fmt.Sprintf("queue (%d):", len(m.queue))))
		b.WriteByte('\n')
		for i, q := range m.queue {
			b.WriteString(queueStyle.Render(fmt.Sprintf("  %d. %s", i+1, q.text)))
			b.WriteByte('\n')
		}
	}

	// Input line.
	b.WriteString(promptStyle.Render("you> "))
	b.WriteString(string(m.input))
	b.WriteString("▏")

	// Context hint.
	b.WriteByte('\n')
	if m.turnRunning {
		b.WriteString(hintStyle.Render("Enter steer · Alt+Enter queue · Esc interrupt"))
	} else {
		b.WriteString(hintStyle.Render("Enter send · /exit quit · Ctrl+D quit"))
	}
	return b.String()
}

func (m *tuiModel) modalView() string {
	st := m.modal
	var b strings.Builder

	if st.prompt.Kind == KindPermission {
		b.WriteString(modalStyle.Render("⚠ permission"))
		b.WriteByte('\n')
		b.WriteString(fmt.Sprintf("  %s wants to run\n", st.prompt.ToolName))
		b.WriteString(fmt.Sprintf("    %s\n", summariseInput(st.prompt.ToolInput)))
		b.WriteString(hintStyle.Render("  [y]es · [a]lways this session · [n]o/Esc"))
		return b.String()
	}

	header := st.prompt.Header
	if header == "" {
		header = "question"
	}
	b.WriteString(modalStyle.Render("[" + header + "]"))
	b.WriteByte('\n')
	b.WriteString("  " + st.prompt.Question + "\n")
	for i, opt := range st.options {
		cursor := "  "
		if i == st.cursor {
			cursor = "▸ "
		}
		mark := ""
		if st.prompt.MultiSelect {
			if st.selected[i] {
				mark = "[x] "
			} else {
				mark = "[ ] "
			}
		}
		b.WriteString(fmt.Sprintf("  %s%s%s\n", cursor, mark, opt))
	}
	hint := "↑/↓ move · Enter select · Esc cancel"
	if st.prompt.MultiSelect {
		hint = "↑/↓ move · Space toggle · Enter confirm · Esc cancel"
	}
	b.WriteString(hintStyle.Render("  " + hint))
	return b.String()
}

// cacheLine formats the per-turn cache footer, or "" when nothing to show.
// Mirrors plainView's rule: shown when cache moved, always in verbose.
func cacheLine(v verbosity, reply agent.Reply) string {
	if v.quiet() {
		return ""
	}
	show := reply.CacheReadTokens > 0 || reply.CacheWriteTokens > 0
	if v.verbose() {
		show = true
	}
	if !show {
		return ""
	}
	return noticeStyle.Render(fmt.Sprintf("  ⓘ cache: %d read, %d write (in %d / out %d)",
		reply.CacheReadTokens, reply.CacheWriteTokens, reply.InputTokens, reply.OutputTokens))
}
