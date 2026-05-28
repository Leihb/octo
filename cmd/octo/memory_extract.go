package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/memory"
)

// Consolidation triggers (Phase 1 fallback; the daemon will own these later).
const (
	consolidateMinEntries = 5
	consolidateMinAge     = 24 * time.Hour
)

// maybeProcessMemory runs the boundary-extraction + consolidation triggers at
// session start (the no-daemon fallback path). It extracts durable facts from
// the most-recent finished session (skipping excludeID, the one we're about to
// resume), then consolidates if due. Best-effort: any side-call error is
// swallowed so a memory hiccup never blocks the session.
func maybeProcessMemory(a *agent.Agent, store *memory.Store, out io.Writer, excludeID string) {
	if store == nil {
		return
	}
	ctx := context.Background()
	st := store.LoadState()

	extractPreviousSession(ctx, a, store, out, excludeID, &st)
	consolidateIfDue(ctx, a, store, &st)

	_ = store.SaveState(st)
}

func extractPreviousSession(ctx context.Context, a *agent.Agent, store *memory.Store, out io.Writer, excludeID string, st *memory.State) {
	sessions, err := agent.ListSessions(2)
	if err != nil {
		return
	}
	for _, sess := range sessions {
		if sess.ID == excludeID {
			continue // the session we're resuming — still ongoing
		}
		if sess.ID == st.LastExtractedSession || sess.TurnCount() == 0 {
			return // reached the already-extracted boundary (or nothing to do)
		}
		facts, err := a.ExtractMemory(ctx, sess.ToHistory().Snapshot())
		if err == nil {
			n := 0
			for _, f := range facts {
				if serr := store.Save(memory.Entry{
					Description: f.Description,
					Type:        memory.Type(f.Type),
					Body:        f.Content,
				}); serr == nil {
					n++
				}
			}
			if n > 0 {
				fmt.Fprintf(out, "  ⓘ remembered %d fact(s) from your last session\n", n)
			}
		}
		st.LastExtractedSession = sess.ID
		return // only the most-recent session, to bound startup cost
	}
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
	summary, err := a.ConsolidateMemory(ctx, priorSummary, newNotes)
	if err != nil || summary == "" {
		return
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
	// Record the new git baseline so the future sub-agent consolidator (#6)
	// knows what point we've folded up to. HeadSHA returns "" when git is off,
	// which is fine — it just leaves the field empty.
	if sha, err := store.HeadSHA(); err == nil && sha != "" {
		st.LastConsolidatedSHA = sha
	}
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
