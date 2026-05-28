package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// extractMaxTokens caps the memory-extraction side-call. The cap covers all
// three artifacts the side-call produces in one shot (slug + summary + facts),
// so it's larger than a fact-only extraction would need.
const extractMaxTokens = 4096

// extractSystem instructs the extraction side-call to mine a finished
// conversation for three artifacts:
//
//   - facts: typed durable memories (the entries injected into future prompts)
//   - rollout_summary: a narrative reference of what this session was about
//   - rollout_slug: a filesystem-safe handle naming this session
//
// It's the boundary counterpart to the immediate `remember` tool: remember
// catches explicit signals mid-session, this sweeps for what the model didn't
// record AND produces the per-rollout reference doc Codex calls
// `rollout_summaries/<…>.md`.
//
// The prompt borrows the shape of Codex's stage_one writer: an explicit no-op
// gate, a reading-priority rule (user > tool > assistant), outcome triage,
// and evidence→implication wording for feedback.
const extractSystem = `You extract durable, cross-session memories from a coding agent's finished
conversation. Output ONE JSON object with three top-level fields:

  {
    "rollout_slug":    "<short kebab-case handle for this session, <= 60 chars>",
    "rollout_summary": "<narrative reference doc, see format below>",
    "facts":           [ { "type": "user|feedback|project|reference", "description": "<one line>", "content": "<the fact>" }, ... ]
  }

================ GOAL ================
Help future sessions:
- act on what the user already taught us, so they don't have to repeat themselves
- skip dead ends we already proved wrong
- reuse what was validated

Optimize for future USER time saved (fewer corrections, fewer interruptions,
fewer re-specifications), not just future agent time saved.

================ NO-OP GATE ================
Before each candidate fact, ask: "Will a future session plausibly do better
because of THIS line?" If no, drop it.

If the whole session has nothing worth carrying — random one-off questions,
quick fixes already in the code, brainstorming with no adopted conclusion —
return the all-empty form exactly:

  {"rollout_slug": "", "rollout_summary": "", "facts": []}

Most turns yield nothing. Quality beats quantity. Empty is correct, not lazy.

================ READING PRIORITY ================
Read the conversation in this order of trust:
1. User messages — primary source for preferences, corrections, constraints,
   dissatisfaction, "what should have been anticipated".
2. Tool outputs / verification evidence — primary source for repo facts, what
   actually worked or failed.
3. Assistant messages — useful for what was attempted, NOT primary source of
   truth. Do NOT promote assistant proposals to durable memory unless the user
   clearly adopted them (implemented, agreed, or reinforced repeatedly).

If the user spent keystrokes specifying something a good future session could
have inferred or volunteered, that is a candidate for a remembered default.

================ OUTCOME TRIAGE ================
For each topic, classify the outcome before deciding what to record:
- success: user confirmed, tests/tools verified, user moved on without unresolved issues.
- partial: progress but unverified, workaround only, user kept iterating.
- fail: user rejected the result, errors unresolved, contradictions unresolved.
- uncertain: no clear signal, or only the assistant claimed success.

For success → capture the reusable shortcut or the user-preferred default.
For fail/partial → capture the failure shield: "symptom → cause → fix" or
"when X, do Y not Z", with the WHY.
For uncertain → usually skip; wait for stronger signal in a future session.

================ WHAT TO RECORD (by type) ================
- user: who the user is; durable role / expertise / responsibilities / preferences.
  Not transient mood. Frame as facts that shape how future sessions talk to them
  (e.g. "deep Go expertise, new to React" lets future sessions calibrate explanations).

- feedback: how to work with this user. Includes:
    * explicit corrections ("don't do X", "stop doing Y")
    * confirmed approaches the user accepted without pushback when the choice
      was non-obvious (save both — saving only corrections drifts you cautious).
  Lead with the rule, then say WHY (the user's reason, often a past incident or
  strong preference). Prefer evidence → implication shape on the same fact:
    "User said '<short quote / near-verbatim>' → implies <default behavior next time>"
  Split distinct defaults into separate facts; do not merge several concrete
  requests into one umbrella preference.

- project: ongoing goals, deadlines, constraints, decisions, who is doing what
  and why — that are NOT derivable from the code or git history. Convert
  relative dates to absolute ("Thursday" → "2026-05-30") so the fact stays
  legible after time passes.

- reference: pointers to external systems (dashboards, tickets, channels, repos)
  with what they are for, so future sessions know where to look.

================ DO NOT RECORD ================
- One-off task details, in-progress work, current conversation state.
- Anything derivable from the code, git log, CLAUDE.md, .octorules, or repo structure.
- Debug recipes / one-time fix commands — the fix is already in the code; the
  commit message has the context.
- Generic advice ("be careful", "check docs", "write tests").
- Pure brainstorming / options the user discussed but did not commit to.
- Secrets, tokens, API keys, credentials. Redact if quoting.

================ FACTS STYLE ================
- One fact per array element. Do not merge distinct defaults.
- Terse, concrete, actionable. Preserve short user quotes when they sharpen the rule.
- description: one line, <=80 chars, the rule itself (not a topic label).
- content: the rule, then for feedback/project a "Why: ..." clause, then for
  feedback an optional "How to apply: ..." clause naming when this kicks in.

================ rollout_slug ================
A filesystem-safe, lowercase, hyphen-separated handle naming THIS session.
Examples: "tune-extract-prompt", "fix-bing-search-encoding", "design-m11".
Cap at 60 chars. Avoid generic slugs ("debug", "fix", "task") — be specific
enough that a future search lands on the right session. If you can't pick a
specific name, use "".

================ rollout_summary ================
A narrative reference doc future sessions can grep when they ask "did we do
anything in this area before?". It is NOT auto-injected into the next session
(the consolidated memory_summary is) — these summaries live on disk for
on-demand reading, so they can be detailed.

Length: as much as the session deserves. Single-task sessions: ~30-80 lines.
Multi-task sessions: longer, one section per task. Use Markdown.

Suggested structure (omit sections that are empty):

  # <one-sentence summary of the session>

  Rollout context: <what the user wanted, environment, constraints>

  ## Task <n>: <task name>

  Outcome: <success|partial|fail|uncertain>

  Preference signals:
  - when <situation>, the user said "<short quote>" → implies <default for next time>

  Key steps: <only steps that produced a durable result>

  Failures and how to do differently: <what failed, what worked instead, why>

  Reusable knowledge: <validated repo facts, high-leverage shortcuts>

  References: <file paths, commands, error strings worth preserving verbatim>

For fail / partial / uncertain outcomes: emphasize what didn't work, the pivot
that did, and the prevention rule.

================ OUTPUT ================
ONE JSON object only. No prose, no code fences. Honor the no-op gate — the
all-empty form is correct when there's nothing durable to carry.`

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

// ExtractResult is the three-piece artifact a session-end extraction produces:
// a typed-facts array (the durable memory entries), a narrative rollout
// summary (the on-disk reference doc), and a slug (the filename handle).
//
// Any field can be empty. An all-empty result means the no-op gate fired and
// the session had nothing worth carrying forward.
type ExtractResult struct {
	Slug    string       `json:"rollout_slug"`
	Summary string       `json:"rollout_summary"`
	Facts   []MemoryFact `json:"facts"`
}

// ExtractMemory runs the extraction side-call over msgs (a finished session's
// messages) and returns the three-piece result. It does not write anything —
// the caller persists each piece (entries via the memory Store, the rollout
// summary via Store.SaveRolloutSummary). A zero ExtractResult means "nothing
// worth keeping".
func (a *Agent) ExtractMemory(ctx context.Context, msgs []Message) (ExtractResult, error) {
	if a.Sender == nil {
		return ExtractResult{}, fmt.Errorf("agent: no Sender configured")
	}
	if len(msgs) == 0 {
		return ExtractResult{}, nil
	}
	req := make([]Message, 0, len(msgs)+1)
	req = append(req, msgs...)
	req = append(req, NewUserMessage(
		"Extract durable memories from the conversation above per your instructions. Output only the JSON object."))

	reply, err := a.Sender.SendMessages(ctx, a.Model, extractSystem, req, extractMaxTokens)
	if err != nil {
		return ExtractResult{}, err
	}
	a.sessionInputTokens += reply.InputTokens
	a.sessionOutputTokens += reply.OutputTokens
	return parseExtractResult(reply.Content)
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

// parseExtractResult parses the side-call reply into an ExtractResult,
// tolerating a code fence or surrounding prose. The model is supposed to emit
// a single JSON object with rollout_slug / rollout_summary / facts; for safety
// we also accept a bare array (old shape from before this PR) and treat it as
// a facts-only result with empty slug/summary. An empty / unparseable reply
// yields a zero ExtractResult with no error — "nothing to record" is normal.
func parseExtractResult(s string) (ExtractResult, error) {
	s = strings.TrimSpace(stripCodeFence(s))
	if s == "" {
		return ExtractResult{}, nil
	}

	// Decide which top-level shape we're looking at by the first significant
	// JSON character. An object inside an array (legacy array shape) must NOT
	// be confused with the new top-level object — we'd lose the facts.
	first, _ := firstJSONChar(s)

	if first == '{' {
		if obj := sliceBetween(s, '{', '}'); obj != "" {
			var r ExtractResult
			if err := json.Unmarshal([]byte(obj), &r); err == nil {
				r.Slug = sanitizeSlug(r.Slug)
				r.Summary = strings.TrimSpace(r.Summary)
				r.Facts = normalizeFacts(r.Facts)
				return r, nil
			}
		}
	}

	if first == '[' {
		if arr := sliceBetween(s, '[', ']'); arr != "" {
			var facts []MemoryFact
			if err := json.Unmarshal([]byte(arr), &facts); err == nil {
				return ExtractResult{Facts: normalizeFacts(facts)}, nil
			}
		}
	}

	return ExtractResult{}, nil
}

// firstJSONChar returns the first '{' or '[' byte in s, scanning past
// surrounding prose. Used to choose between object/array parse paths so an
// array of objects isn't misread as a bare object.
func firstJSONChar(s string) (byte, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			return s[i], true
		}
	}
	return 0, false
}

// sliceBetween returns the substring from the first open rune to the last
// close rune (inclusive), or "" if either is missing. Used to forgive a model
// that wraps the JSON in surrounding prose.
func sliceBetween(s string, open, close byte) string {
	i := strings.IndexByte(s, open)
	j := strings.LastIndexByte(s, close)
	if i < 0 || j < 0 || j < i {
		return ""
	}
	return s[i : j+1]
}

// normalizeFacts trims whitespace, fills missing description from content's
// first line (and vice versa), and drops entries that are empty after that.
func normalizeFacts(in []MemoryFact) []MemoryFact {
	if len(in) == 0 {
		return nil
	}
	out := make([]MemoryFact, 0, len(in))
	for _, f := range in {
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
		return nil
	}
	return out
}

// sanitizeSlug folds an LLM-supplied slug to the same kebab-case shape we use
// elsewhere and caps the length. Empty → empty (the caller falls back to the
// session id or a default).
func sanitizeSlug(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	return out
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
