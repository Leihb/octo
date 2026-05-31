package agent

import (
	"strings"
	"sync"
)

// Inbox is a thread-safe queue for user messages that arrive while a turn is
// running. It mirrors Ruby octo's @inbox: messages accumulate here and are
// drained into history at the start of each loop iteration, before the LLM
// call. This keeps mid-turn input handling simple and avoids the complexity of
// merging steer text into tool_result messages.
type Inbox struct {
	mu   sync.Mutex
	msgs []string
}

// Enqueue adds a message to the inbox. Empty/whitespace-only messages are
// ignored. Safe to call from any goroutine (e.g. the UI goroutine while the
// agent loop is running).
func (ib *Inbox) Enqueue(msg string) {
	if strings.TrimSpace(msg) == "" {
		return
	}
	ib.mu.Lock()
	ib.msgs = append(ib.msgs, msg)
	ib.mu.Unlock()
}

// Drain returns all queued messages and clears the inbox. Returns nil when
// nothing is queued. Called from the loop goroutine at iteration start.
func (ib *Inbox) Drain() []string {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	if len(ib.msgs) == 0 {
		return nil
	}
	out := ib.msgs
	ib.msgs = nil
	return out
}

// HasPending reports whether any messages are queued.
func (ib *Inbox) HasPending() bool {
	ib.mu.Lock()
	defer ib.mu.Unlock()
	return len(ib.msgs) > 0
}
