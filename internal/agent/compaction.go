package agent

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// compactKeepTurns is how many of the most recent user turns runLoop keeps
// verbatim when it compacts; everything older is folded into one summary.
const compactKeepTurns = 4

// compactThresholdFraction is the share of the model's context window at which
// auto-compaction (CompactThreshold == 0) triggers, leaving headroom for the
// kept tail, the summary side-call, and the next turn's output.
const compactThresholdFraction = 0.75

// defaultContextWindow is the conservative fallback window (in tokens) for
// models not named in contextWindow. Under-estimating only makes us compact
// slightly earlier — never overflow — so unknown models stay safe.
const defaultContextWindow = 128_000

// contextWindow returns the approximate context-window size (in tokens) for a
// model. Values are deliberately conservative; matched case-insensitively by
// substring so dated/aliased names ("claude-haiku-4-5-2025…") still resolve.
// Raise a model's entry when a larger window is confirmed.
func contextWindow(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "claude-3-5"):
		return 200_000
	case strings.Contains(m, "claude-3"):
		return 200_000
	case strings.Contains(m, "claude-opus-4"):
		return 200_000
	case strings.Contains(m, "claude-sonnet-4"):
		return 200_000
	case strings.Contains(m, "claude-haiku-4"):
		return 200_000
	case strings.Contains(m, "claude"):
		// Claude 4.x and newer models with 256K window
		return 256_000
	case strings.Contains(m, "gpt-4o"):
		return 128_000
	case strings.Contains(m, "gpt-4"):
		return 128_000
	default:
		return defaultContextWindow
	}
}

// compactTriggerTokens resolves the effective auto-compaction trigger from
// CompactThreshold:
//
//	< 0  → disabled (0)
//	== 0 → auto: a fraction of the model's context window
//	> 0  → that explicit token count
func (a *Agent) compactTriggerTokens() int {
	switch {
	case a.CompactThreshold < 0:
		return 0
	case a.CompactThreshold == 0:
		return int(float64(contextWindow(a.Model)) * compactThresholdFraction)
	default:
		return a.CompactThreshold
	}
}

// summarizeMaxTokens caps the summary length. A summary is meant to be a
// compact carry-forward of decisions/paths/open tasks, not a transcript.
const summarizeMaxTokens = 1024

// compressionPrompt is the instruction inserted into the conversation for
// insert-then-compress. The LLM sees the full history (cached) plus this
// instruction, and returns a summary. Aligned with Ruby's COMPRESSION_PROMPT.
const compressionPrompt = `═══════════════════════════════════════════════════════════════
CRITICAL: TASK CHANGE - MEMORY COMPRESSION MODE
═══════════════════════════════════════════════════════════════
The conversation above has ENDED. You are now in MEMORY COMPRESSION MODE.

CRITICAL INSTRUCTIONS - READ CAREFULLY:
1. This is NOT a continuation of the conversation
2. DO NOT respond to any requests in the conversation above
3. DO NOT call ANY tools or functions
4. DO NOT use tool_calls in your response
5. Your response MUST be PURE TEXT ONLY

YOUR ONLY TASK: Create a comprehensive summary of the conversation above.

REQUIRED RESPONSE FORMAT:
First output a <topics> line listing 3-6 key topic phrases (comma-separated, concise).
Then output the full summary wrapped in <summary> tags.

Example format:
<topics>Rails setup, database config, deploy pipeline, Tailwind CSS</topics>
<summary>
...full summary text...
</summary>

Focus on:
- User's explicit requests and intents
- Key technical concepts and code changes
- Files examined and modified
- Errors encountered and fixes applied
- Current work status and pending tasks

Begin your response NOW. Remember: PURE TEXT only, starting with <topics> then <summary>.`

// maybeCompact summarizes the older portion of history when the estimated
// total context size (from History.Snapshot) crosses CompactThreshold. It runs
// only at a safe boundary — between turns, splitting on a plain user message —
// so tool_use/tool_result pairs are never severed. A nil return means either
// "compaction disabled / not needed" or "compacted successfully"; an error
// means the summarization side-call failed (the caller logs and proceeds with
// uncompacted history rather than aborting the turn).
func (a *Agent) maybeCompact(ctx context.Context) error {
	trigger := a.compactTriggerTokens()
	if trigger <= 0 {
		return nil
	}

	msgs := a.History.Snapshot()
	if estimateMessages(msgs) < trigger {
		return nil
	}

	split := safeSplitIndex(msgs, compactKeepTurns)
	if split <= 0 {
		return nil // not enough complete turns to safely compact yet
	}

	summary, err := a.summarize(ctx, msgs[:split])
	if err != nil {
		return fmt.Errorf("agent: compact: %w", err)
	}
	if summary == "" {
		return nil // nothing usable came back; leave history alone
	}

	// Rebuild: one summary user-message, then the kept recent turns verbatim.
	a.History.Reset()
	a.History.Append(NewUserMessage("[Earlier conversation summary]\n\n" + summary))
	for _, m := range msgs[split:] {
		a.History.Append(m)
	}
	// Reset the trigger so we don't re-compact until the context grows again.
	a.lastInputTokens = 0
	return nil
}

// summarize asks the Sender to condense the given messages into a summary.
// It uses the insert-then-compress strategy: the compression instruction is
// appended to the messages slice (which ends on an assistant message, so
// alternation holds), and the LLM returns a summary. The caller is responsible
// for rebuilding history with the summary.
//
// Overflow protection: if the estimated token count of msgs exceeds the model's
// context window, messages are popped from the head until it fits. This
// prevents the compression call itself from 400-ing.
func (a *Agent) summarize(ctx context.Context, msgs []Message) (string, error) {
	// ── Overflow protection ──────────────────────────────────────────────
	// If msgs alone exceeds the window, pop from head until it fits.
	// This handles the case where the history is already over the limit
	// (e.g. a single huge tool_result tipped it over).
	window := contextWindow(a.Model)
	for {
		est := estimateMessages(msgs)
		if est < window {
			break
		}
		if len(msgs) <= 2 {
			// Can't pop any more; proceed anyway and let the caller handle 400
			break
		}
		msgs = msgs[1:] // pop from head (preserve system msg at index 0)
	}

	// ── Insert-then-compress ─────────────────────────────────────────────
	req := make([]Message, 0, len(msgs)+1)
	req = append(req, msgs...)
	req = append(req, NewUserMessage(compressionPrompt))

	reply, err := a.Sender.SendMessages(ctx, a.Model, "", req, summarizeMaxTokens)
	if err != nil {
		return "", err
	}

	// Defensive: if the LLM returned tool_calls instead of text (it shouldn't,
	// but some models hallucinate), treat it as a failed compression.
	if reply.StopReason == "tool_use" {
		return "", fmt.Errorf("agent: compact: LLM returned tool_calls instead of summary")
	}

	// Summary tokens count toward the session budget like any other call.
	a.sessionInputTokens += reply.InputTokens
	a.sessionOutputTokens += reply.OutputTokens
	return reply.Content, nil
}

// safeSplitIndex returns the history index at which compaction can split:
// messages before it are summarized, messages from it onward are kept. The
// split always lands on a plain user turn (RoleUser with no tool_result
// blocks), so a tool_use and its tool_result are never separated. It keeps
// the last keepTurns user turns; if there aren't more than that, it returns 0
// (nothing safe to compact).
func safeSplitIndex(msgs []Message, keepTurns int) int {
	var userTurns []int
	for i, m := range msgs {
		if m.Role == RoleUser && !hasToolResult(m) {
			userTurns = append(userTurns, i)
		}
	}
	if len(userTurns) <= keepTurns {
		return 0
	}
	return userTurns[len(userTurns)-keepTurns]
}

// hasToolResult reports whether m carries any tool_result block (i.e. it's a
// synthetic user message returning tool output, not a real user turn).
func hasToolResult(m Message) bool {
	for _, b := range m.Blocks {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

// shouldCompactBetweenBatches reports whether compaction should run after a
// tool batch, before the next LLM call. This catches history growth within a
// turn that lastInputTokens (from the previous provider call) doesn't reflect.
//
// The heuristic is aggressive: if the estimated token count exceeds 90% of the
// model's context window, we compact. This prevents the next send() from 400-ing.
func (a *Agent) shouldCompactBetweenBatches() bool {
	trigger := a.compactTriggerTokens()
	if trigger <= 0 {
		return false
	}
	est := estimateMessages(a.History.Snapshot())
	window := contextWindow(a.Model)
	// Compact if we're within 10% of the window (more aggressive than the
	// between-turns trigger, which uses compactThresholdFraction = 75%).
	return est > int(float64(window)*0.9)
}

// ── Token estimation (fast heuristic) ──────────────────────────────────────

// estimateMessages returns a fast heuristic token count for a message slice.
// Used for overflow protection before compression calls.
func estimateMessages(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateMessage(m)
	}
	return total
}

// estimateMessage counts tokens for a single message.
// Heuristic: ~4 chars/token for ASCII/code, ~1.5 chars/token for CJK.
func estimateMessage(m Message) int {
	tokens := 4 // role overhead
	tokens += estimateText(m.Content)
	for _, b := range m.Blocks {
		switch b.Type {
		case "text":
			tokens += estimateText(b.Text)
		case "tool_use":
			tokens += estimateText(b.Name)
			tokens += estimateMap(b.Input)
		case "tool_result":
			tokens += estimateText(b.Result)
		case "thinking":
			tokens += estimateText(b.Thinking)
		}
	}
	return tokens
}

func estimateText(s string) int {
	if s == "" {
		return 0
	}
	asciiCount := 0
	multiCount := 0
	for _, r := range s {
		if r < 128 {
			asciiCount++
		} else {
			multiCount += utf8.RuneLen(r)
		}
	}
	return (asciiCount / 4) + int(float64(multiCount)/1.5+0.5)
}

func estimateMap(m map[string]any) int {
	if len(m) == 0 {
		return 0
	}
	var b strings.Builder
	for k, v := range m {
		b.WriteString(k)
		b.WriteString(":")
		b.WriteString(fmt.Sprintf("%v", v))
		b.WriteString(",")
	}
	return estimateText(b.String())
}
