package agent

import "strings"

// Steer queues a mid-turn "steer" message — text the user typed while a turn
// was running and wants folded into the in-flight turn rather than run as a
// fresh turn. It is safe to call from a different goroutine than the one
// running the loop; the loop drains the queue at each tool-batch boundary.
//
// Empty / whitespace-only messages are ignored. Multiple steers that arrive
// before the next boundary accumulate and are injected together.
func (a *Agent) Steer(msg string) {
	if strings.TrimSpace(msg) == "" {
		return
	}
	a.steerMu.Lock()
	a.steerBuf = append(a.steerBuf, msg)
	a.steerMu.Unlock()
}

// drainSteer returns all queued steer messages joined by a blank line and
// clears the queue. Returns "" when nothing is queued. Called from the loop
// goroutine at a tool-batch boundary (and by the caller after a turn ends, to
// drain any steer that never found a boundary so it can degrade to a queued
// turn — see dev-docs/tui-input-modes-design.md §8).
func (a *Agent) drainSteer() string {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	if len(a.steerBuf) == 0 {
		return ""
	}
	joined := strings.Join(a.steerBuf, "\n\n")
	a.steerBuf = nil
	return joined
}

// DrainSteer is the exported drain used by the REPL after a turn ends: any
// steer text that never hit a tool-batch boundary (e.g. a no-tool turn, or
// text typed during the final answer) is returned so the caller can run it as
// the next turn. Returns "" when nothing is pending.
func (a *Agent) DrainSteer() string {
	return a.drainSteer()
}

// HasPendingSteer reports whether any steer text is queued. Lets the REPL
// decide, after a turn, whether to spin up a degraded queued turn.
func (a *Agent) HasPendingSteer() bool {
	a.steerMu.Lock()
	defer a.steerMu.Unlock()
	return len(a.steerBuf) > 0
}
