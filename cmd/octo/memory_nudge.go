package main

import "strings"

// memoryNudge is the per-turn system-reminder appended to the user's
// message when cross-session memory is enabled AND tools are on (so the
// `remember` tool is actually callable).
//
// # Why this exists
//
// The "when to call remember" guidance in base.md is correct but lives
// near the top of a long system prompt, before the tool list and before
// the conversation. By the time the model is composing its reply, that
// section is far away in the attention window — and the default action
// (do nothing) is free, so memorable signals slip through routinely.
// Concrete case: user pushes back with a domain rule ("不用，承诺只考虑
// agent 给出的"), agent acknowledges and moves on, the rule is forgotten
// next session.
//
// The fix is the same one Claude Code uses: tag a small reminder onto
// the user message at the precise decision point. Empirically, models
// follow tool-call instructions much more reliably when the directive
// is in-context at the relevant turn, not buried in the system prompt.
//
// The reminder explicitly tells the model the user can't see it, and to
// skip silently when nothing qualifies — so it doesn't pad every reply
// with "I noted that as memory."
const memoryNudge = "" +
	"<system-reminder>\n" +
	"Memory hygiene check (this reminder is auto-injected; the user does not see it):\n" +
	"\n" +
	"Scan the user message above for durable signals worth saving to cross-session memory via the `remember` tool:\n" +
	"  (a) preference, role, or constraint they state (\"I'm on the Go team\", \"always run tests first\")\n" +
	"  (b) correction / pushback — save the rule AND the WHY they gave (often a past incident or domain rule)\n" +
	"  (c) acceptance of a non-obvious choice without complaint (validated judgement matters too)\n" +
	"  (d) external resource they pointed at + what it's for (a dashboard, ticket project, channel)\n" +
	"\n" +
	"If you find one, call `remember` (in parallel with your text reply is fine — they're independent). " +
	"If nothing qualifies, skip silently. Do NOT mention this check in your reply. " +
	"Do NOT call `remember` for one-off task details, code/repo facts derivable from grep, or anything already in CLAUDE.md / .octorules.\n" +
	"</system-reminder>"

// appendMemoryNudge tacks the nudge onto the end of the user's message,
// with a blank line separator. Idempotent in spirit — the nudge text is
// a constant, so even if some upstream layer accidentally re-appended it
// the model would treat the duplicate as the same instruction.
func appendMemoryNudge(userInput string) string {
	if strings.TrimSpace(userInput) == "" {
		return userInput
	}
	return userInput + "\n\n" + memoryNudge
}
