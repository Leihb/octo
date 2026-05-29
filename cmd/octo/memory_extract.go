package main

import (
	"context"
	"strings"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/memory"
	"github.com/Leihb/octo-agent/internal/tools"
)

// Consolidation trigger thresholds. Memory is captured live via the `remember`
// tool; this folds those accumulated entries into the summary when due.
const (
	consolidateMinEntries = 5
	consolidateMinAge     = 24 * time.Hour
)

// maybeProcessMemory runs the consolidation trigger at session start. It folds
// the live-captured entries (from the `remember` tool) into the summary when
// due. Best-effort: any side-call error is swallowed so a memory hiccup never
// blocks the session.
func maybeProcessMemory(a *agent.Agent, store *memory.Store) {
	if store == nil {
		return
	}
	ctx := context.Background()
	st := store.LoadState()

	consolidateIfDue(ctx, a, store, &st)

	_ = store.SaveState(st)
}

func consolidateIfDue(ctx context.Context, a *agent.Agent, store *memory.Store, st *memory.State) {
	if !consolidateDue(*st, store) {
		return
	}
	newNotes, err := store.ExportNotes()
	if err != nil || newNotes == "" {
		return // no new active entries — nothing to fold in
	}
	priorSummary := store.ReadSummary()

	// Prefer the sub-agent path (#6) when a Spawner is registered: a child
	// agent runs in its own context with read-only filesystem tools, can
	// grep / read_file across the memory dir to look at the actual entry
	// files, and returns the new summary text. Falls back to the side-call
	// (priorSummary + newNotes only, no tools) when no Spawner is wired or
	// the sub-agent declines.
	summary := consolidateViaSubAgent(ctx, store, priorSummary, newNotes)
	if summary == "" {
		summary, err = a.ConsolidateMemory(ctx, priorSummary, newNotes)
		if err != nil || summary == "" {
			return
		}
	}

	if err := store.WriteSummary(summary); err != nil {
		return
	}
	// Archive the active entries that were just folded into the summary, so
	// neither the next consolidation's input nor the injection fallback grows
	// unbounded. A failure here leaves them active and they'll be re-folded
	// next time — idempotent (same facts in, same summary out).
	if err := store.ArchiveAll(); err != nil {
		return
	}
	st.LastConsolidated = time.Now().Format("2006-01-02")
	// Record the new git baseline so the next consolidation can diff against
	// it. HeadSHA returns "" when git is off, which is fine — it just leaves
	// the field empty.
	if sha, err := store.HeadSHA(); err == nil && sha != "" {
		st.LastConsolidatedSHA = sha
	}
}

// consolidationToolAllowlist restricts the consolidator sub-agent to read-only
// research. It must not have write_file / edit_file (the parent calls
// Store.WriteSummary, which adds the v1 marker and commits via git), terminal
// (out of scope), remember (would mutate memory mid-consolidation), or
// launch_agent (recursion is filtered out by the spawner anyway, listing it
// explicitly is defense in depth).
var consolidationToolAllowlist = []string{"read_file", "grep", "glob"}

// consolidateViaSubAgent runs the consolidation pass as a sub-agent with
// read-only filesystem tools. Returns the new summary text on success or ""
// when no spawner is configured / the sub-agent errored / the sub-agent
// returned nothing. The empty case is non-fatal — caller falls back to the
// side-call.
//
// The sub-agent's win over the side-call: it can read the actual <slug>.md
// frontmatter for context that didn't make it into newNotes, and check
// MEMORY.md for cross-references — autonomously, in its
// own context window.
func consolidateViaSubAgent(ctx context.Context, store *memory.Store, priorSummary, newNotes string) string {
	spawner := tools.ActiveSpawner()
	if spawner == nil {
		return ""
	}

	res, err := spawner.Spawn(ctx, tools.SpawnRequest{
		Description: "Consolidate cross-session memory",
		Prompt:      buildConsolidationPrompt(store.Dir(), priorSummary, newNotes),
		Tools:       consolidationToolAllowlist,
	})
	if err != nil {
		return ""
	}
	out := strings.TrimSpace(res.Reply)
	// Strip a leading code fence the sub-agent might wrap the summary in.
	if strings.HasPrefix(out, "```") {
		if nl := strings.IndexByte(out, '\n'); nl >= 0 {
			out = out[nl+1:]
		}
		if idx := strings.LastIndex(out, "```"); idx >= 0 {
			out = out[:idx]
		}
		out = strings.TrimSpace(out)
	}
	return out
}

// buildConsolidationPrompt assembles the sub-agent's instructions. The prompt
// is self-contained: the sub-agent doesn't see the parent's conversation, so
// the current summary, the new notes, and pointers to the on-disk memory dir
// must all be inline here.
func buildConsolidationPrompt(memDir, priorSummary, newNotes string) string {
	var b strings.Builder
	b.WriteString("You are consolidating cross-session memory for the octo coding agent.\n\n")
	b.WriteString("Memory layout (read-only access via read_file / grep / glob):\n")
	if memDir != "" {
		b.WriteString("- Root: " + memDir + "\n")
		b.WriteString("- " + memDir + "/MEMORY.md (index of slugs)\n")
		b.WriteString("- " + memDir + "/<slug>.md (one fact per file, with frontmatter)\n")
		b.WriteString("- " + memDir + "/memory_summary.md (the file you're updating; current contents below)\n")
	}
	b.WriteString("\nCurrent consolidated summary (may be empty on first pass):\n\n")
	if priorSummary != "" {
		b.WriteString(priorSummary)
	} else {
		b.WriteString("(empty — this is the first consolidation pass)")
	}
	b.WriteString("\n\nNew memory entries since the last consolidation (the index digest):\n\n")
	b.WriteString(newNotes)
	b.WriteString("\n\nYour task: produce the UPDATED consolidated summary. Fold the new entries into the current summary, dedupe, drop anything stale or trivial, and keep load-bearing facts. Be terse — bullet points, grouped loosely by kind (who the user is, how they like to work, ongoing project context, useful references). If you need more context than the digest gives you (a specific quote, the rationale behind a feedback fact), use read_file / grep / glob to look at the actual files.\n\n")
	b.WriteString("Output ONLY the new summary text. No preamble, no code fences, no commentary about what you changed. The parent will write your output to memory_summary.md (it adds the protocol marker automatically — you don't need to).\n")
	return b.String()
}

func consolidateDue(st memory.State, store *memory.Store) bool {
	entries, err := store.List()
	if err != nil || len(entries) < consolidateMinEntries {
		return false
	}
	if st.LastConsolidated == "" {
		return true
	}
	last, err := time.Parse("2006-01-02", st.LastConsolidated)
	if err != nil {
		return true
	}
	return time.Since(last) >= consolidateMinAge
}
