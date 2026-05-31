package channel

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/Leihb/octo-agent/internal/agent"
)

// UIController bridges agent.AgentEvent stream to IM platform messages.
// It suppresses noisy tool-call events, buffers file previews, and forwards
// meaningful events (text deltas, tool results, completion) to the adapter.
type UIController struct {
	adapter Adapter
	chatID  string
	replyTo string

	// mu guards the builder state below.
	mu sync.Mutex

	// textBuf accumulates text deltas until a turn completes or a tool event
	// forces a flush. This batches rapid deltas into fewer IM messages.
	textBuf strings.Builder

	// pendingTextMsgID is the message ID of the last text message we sent,
	// used for in-place updates on platforms that support it.
	pendingTextMsgID string

	// fileBuf accumulates file paths mentioned in tool results for batch preview.
	fileBuf []string

	// toolCount tracks how many tools have started (for suppression heuristics).
	toolCount int

	// inTool is true while we're between EventToolStarted and its matching done/error.
	inTool bool

	// sentTyping tracks whether we've already sent a typing indicator this turn.
	sentTyping bool
}

// NewUIController creates a controller bound to one chat conversation.
func NewUIController(adapter Adapter, chatID, replyTo string) *UIController {
	return &UIController{
		adapter: adapter,
		chatID:  chatID,
		replyTo: replyTo,
	}
}

// Handler returns an agent.EventHandler that forwards events to the adapter.
func (u *UIController) Handler() agent.EventHandler {
	return func(ev agent.AgentEvent) {
		u.handleEvent(ev)
	}
}

// handleEvent routes each AgentEvent to the appropriate handler.
func (u *UIController) handleEvent(ev agent.AgentEvent) {
	switch ev.Kind {
	case agent.EventTextDelta:
		u.onTextDelta(ev.Text)
	case agent.EventToolStarted:
		u.onToolStarted(ev.ToolName)
	case agent.EventToolProgress:
		// Suppress progress noise in IM — too chatty.
	case agent.EventToolDone:
		u.onToolDone(ev.ToolName, ev.Output)
	case agent.EventToolError:
		u.onToolError(ev.ToolName, ev.Err, ev.Output)
	case agent.EventTurnDone:
		u.onTurnDone(ev.Reply)
	case agent.EventToolInputDelta:
		// Tool input deltas are UI-only (web TUI) — suppress in IM.
	}
}

// onTextDelta accumulates text and periodically flushes to the adapter.
func (u *UIController) onTextDelta(text string) {
	if text == "" {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()

	u.textBuf.WriteString(text)

	// Flush when we hit a natural boundary or the buffer grows large.
	buf := u.textBuf.String()
	if shouldFlush(buf) {
		u.flushTextLocked()
	}
}

// onToolStarted notes that a tool has started. We may send a brief "working on X"
// indicator on platforms that support updates, but generally suppress tool-start
// noise in IM.
func (u *UIController) onToolStarted(toolName string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.toolCount++
	u.inTool = true
}

// onToolDone handles successful tool completion. We extract file references from
// the output and buffer them for a batched preview at turn end.
func (u *UIController) onToolDone(toolName, output string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.inTool = false

	// Extract file paths from tool output for potential preview.
	files := extractFilePaths(output)
	u.fileBuf = append(u.fileBuf, files...)
}

// onToolError handles failed tool execution. We flush any pending text, then
// send a concise error summary.
func (u *UIController) onToolError(toolName, errMsg, output string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.inTool = false

	u.flushTextLocked()

	msg := fmt.Sprintf("❌ Tool %q failed: %s", toolName, truncate(errMsg, 200))
	u.sendText(msg)
}

// onTurnDone finalizes the turn: flushes remaining text, sends file previews,
// and resets state for the next turn.
func (u *UIController) onTurnDone(reply *agent.Reply) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.flushTextLocked()
	u.flushFilesLocked()
	u.resetLocked()
}

// flushTextLocked sends the accumulated text buffer to the adapter.
// Must be called with mu held.
func (u *UIController) flushTextLocked() {
	text := strings.TrimSpace(u.textBuf.String())
	if text == "" {
		return
	}
	u.textBuf.Reset()

	// If the platform supports message updates and we already have a pending
	// message, update it in place rather than sending a new one.
	if u.adapter.SupportsMessageUpdates() && u.pendingTextMsgID != "" {
		if u.adapter.UpdateMessage(u.chatID, u.pendingTextMsgID, text) {
			return
		}
		// Update failed — fall through to sending a new message.
	}

	res := u.adapter.SendText(u.chatID, text, u.replyTo)
	if res.OK {
		u.pendingTextMsgID = res.MessageID
	}
}

// flushFilesLocked sends batched file previews. Must be called with mu held.
func (u *UIController) flushFilesLocked() {
	if len(u.fileBuf) == 0 {
		return
	}
	// Deduplicate.
	seen := make(map[string]bool, len(u.fileBuf))
	unique := make([]string, 0, len(u.fileBuf))
	for _, f := range u.fileBuf {
		if !seen[f] {
			seen[f] = true
			unique = append(unique, f)
		}
	}
	u.fileBuf = u.fileBuf[:0]

	// Send a summary message with file references.
	if len(unique) > 0 {
		var sb strings.Builder
		sb.WriteString("📎 Files:\n")
		for _, f := range unique {
			sb.WriteString("• ")
			sb.WriteString(f)
			sb.WriteByte('\n')
		}
		u.sendText(strings.TrimSpace(sb.String()))
	}
}

// sendText sends a text message through the adapter (unlocked helper).
func (u *UIController) sendText(text string) {
	if text == "" {
		return
	}
	u.adapter.SendText(u.chatID, text, u.replyTo)
}

// resetLocked clears per-turn state. Must be called with mu held.
func (u *UIController) resetLocked() {
	u.textBuf.Reset()
	u.fileBuf = u.fileBuf[:0]
	u.pendingTextMsgID = ""
	u.toolCount = 0
	u.inTool = false
	u.sentTyping = false
}

// shouldFlush returns true when the buffer should be sent.
// It triggers on paragraph boundaries or when the buffer exceeds a threshold.
func shouldFlush(buf string) bool {
	const maxBuf = 800
	if len(buf) >= maxBuf {
		return true
	}
	// Flush on paragraph break (double newline).
	if strings.HasSuffix(buf, "\n\n") {
		return true
	}
	// Flush on sentence-ending punctuation followed by space or newline.
	if n := len(buf); n > 2 {
		c := buf[n-2]
		if (c == '.' || c == '!' || c == '?') && (buf[n-1] == ' ' || buf[n-1] == '\n') {
			return true
		}
	}
	return false
}

// extractFilePaths scans tool output for likely file path references.
// This is a heuristic — it looks for lines that look like file operations.
func extractFilePaths(output string) []string {
	var paths []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		// Simple heuristic: lines containing "/" that don't look like URLs.
		if strings.Contains(line, "/") && !strings.HasPrefix(line, "http") {
			// Take the last word as the path candidate.
			fields := strings.Fields(line)
			if len(fields) > 0 {
				candidate := fields[len(fields)-1]
				if strings.Contains(candidate, "/") && !strings.Contains(candidate, "://") {
					paths = append(paths, candidate)
				}
			}
		}
	}
	return paths
}

// truncate limits a string to maxLen, adding an ellipsis if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

// RunAgent executes the agent run loop for a session and bridges events to the IM adapter.
func RunAgent(ctx context.Context, sess *Session, tools []agent.ToolDefinition, executor agent.ToolExecutor, ctrl *UIController, userInput string) (agent.Reply, error) {
	return sess.Agent.RunStream(ctx, userInput, tools, executor, ctrl.Handler())
}
