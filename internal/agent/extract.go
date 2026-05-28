package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// extractMaxTokens caps the memory-extraction side-call. Like the compaction
// summary, an extraction is a short structured carry-forward, not a transcript.
const extractMaxTokens = 1024

// extractSystem instructs the extraction side-call to mine a finished
// conversation for durable, cross-session facts and emit them as JSON. It is
// the boundary counterpart to the immediate `remember` tool: remember catches
// explicit signals mid-session, this sweeps for what the model didn't record.
const extractSystem = `You extract durable, cross-session memories from a coding agent's finished
conversation. Output ONLY a JSON array; each element:

  {"type": "user|feedback|project|reference", "description": "<one line>", "content": "<the fact>"}

Record ONLY load-bearing facts worth recalling in FUTURE sessions:
- user: who the user is; durable preferences.
- feedback: how to work — corrections and confirmed approaches (say why).
- project: ongoing goals/constraints not derivable from the code or git history.
- reference: pointers to external resources (URLs, dashboards, tickets).

Do NOT record: one-off task details, things already in the repo / CLAUDE.md,
transient state, or anything likely to change next session. Quality over
quantity — most turns yield nothing. If nothing qualifies, output exactly [].
No prose, no code fences around the array unless you must — just the JSON.`

// consolidateSystem instructs the consolidation side-call to maintain (rather
// than rebuild) a summary: it gets the current summary plus any new notes since
// the last pass and emits the updated summary. This keeps the input bounded as
// the memory store grows.
const consolidateSystem = `You maintain a coding agent's cross-session memory summary. You will receive
the current consolidated summary (which may be empty on the first pass) and
any new memory notes added since the last consolidation. Produce the UPDATED
summary: fold the new notes into the existing summary, dedupe, drop anything
stale or trivial, and keep the load-bearing facts. Be terse — bullet points,
grouped loosely by kind (who the user is, how they like to work, ongoing
project context, useful references). Output only the updated summary text.`

// MemoryFact is one extracted fact (the JSON shape the extraction side-call
// returns). type maps to memory.Type at the call site.
type MemoryFact struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

// ExtractMemory runs the extraction side-call over msgs (a finished session's
// messages) and returns the durable facts found. It does not write anything —
// the caller persists the facts. A nil slice means "nothing worth keeping".
func (a *Agent) ExtractMemory(ctx context.Context, msgs []Message) ([]MemoryFact, error) {
	if a.Sender == nil {
		return nil, fmt.Errorf("agent: no Sender configured")
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	req := make([]Message, 0, len(msgs)+1)
	req = append(req, msgs...)
	req = append(req, NewUserMessage(
		"Extract durable memories from the conversation above per your instructions. Output only the JSON array."))

	reply, err := a.Sender.SendMessages(ctx, a.Model, extractSystem, req, extractMaxTokens)
	if err != nil {
		return nil, err
	}
	a.sessionInputTokens += reply.InputTokens
	a.sessionOutputTokens += reply.OutputTokens
	return parseFacts(reply.Content)
}

// ConsolidateMemory runs the (incremental) consolidation side-call: it folds
// newNotes into priorSummary and returns the updated summary. Either argument
// may be empty — empty priorSummary means "first pass"; empty newNotes means
// "no new material" and the call short-circuits.
func (a *Agent) ConsolidateMemory(ctx context.Context, priorSummary, newNotes string) (string, error) {
	if a.Sender == nil {
		return "", fmt.Errorf("agent: no Sender configured")
	}
	priorSummary = strings.TrimSpace(priorSummary)
	newNotes = strings.TrimSpace(newNotes)
	if priorSummary == "" && newNotes == "" {
		return "", nil
	}

	var prompt strings.Builder
	if priorSummary != "" {
		prompt.WriteString("Current consolidated summary:\n\n")
		prompt.WriteString(priorSummary)
		prompt.WriteString("\n\n")
	}
	if newNotes != "" {
		prompt.WriteString("New memory notes since last consolidation:\n\n")
		prompt.WriteString(newNotes)
		prompt.WriteString("\n\n")
	}
	prompt.WriteString("Produce the updated consolidated summary per your instructions.")

	req := []Message{NewUserMessage(prompt.String())}
	reply, err := a.Sender.SendMessages(ctx, a.Model, consolidateSystem, req, extractMaxTokens)
	if err != nil {
		return "", err
	}
	a.sessionInputTokens += reply.InputTokens
	a.sessionOutputTokens += reply.OutputTokens
	return strings.TrimSpace(reply.Content), nil
}

// parseFacts extracts the JSON array from a side-call reply (tolerating a code
// fence or surrounding prose) and normalizes each fact. A reply with no array
// yields nil, nil — "nothing to record" is not an error.
func parseFacts(s string) ([]MemoryFact, error) {
	s = strings.TrimSpace(stripCodeFence(s))
	i := strings.Index(s, "[")
	j := strings.LastIndex(s, "]")
	if i < 0 || j < 0 || j < i {
		return nil, nil
	}
	var facts []MemoryFact
	if err := json.Unmarshal([]byte(s[i:j+1]), &facts); err != nil {
		return nil, fmt.Errorf("agent: parse memory facts: %w", err)
	}
	out := make([]MemoryFact, 0, len(facts))
	for _, f := range facts {
		f.Content = strings.TrimSpace(f.Content)
		f.Description = strings.TrimSpace(f.Description)
		if f.Content == "" && f.Description == "" {
			continue
		}
		if f.Content == "" {
			f.Content = f.Description
		}
		if f.Description == "" {
			f.Description = firstLineOf(f.Content)
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// stripCodeFence removes a leading ```… fence and trailing ``` if present, so a
// fenced JSON block parses.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if nl := strings.IndexByte(s, '\n'); nl >= 0 {
		s = s[nl+1:] // drop the ```lang line
	}
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// firstLineOf returns the first non-empty line, capped, for a fallback description.
func firstLineOf(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 80 {
			line = strings.TrimSpace(line[:80])
		}
		return line
	}
	return ""
}
