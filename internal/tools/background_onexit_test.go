package tools

import (
	"strings"
	"testing"
	"time"
)

// TestBackgroundManager_OnExitHookFires verifies the completion hook fires once
// with the final status and the still-unread output, and that consuming it via
// readNew dedups against a later poll (terminal_output sees no new bytes).
func TestBackgroundManager_OnExitHookFires(t *testing.T) {
	m := NewBackgroundManager()

	fired := make(chan BgExit, 1)
	m.SetOnExit(func(e BgExit) {
		// Channel send happens-after this assignment and happens-before the
		// receiver's read, so no extra synchronisation is needed.
		select {
		case fired <- e:
		default:
		}
	})

	id, err := m.Start("echo hi-from-bg")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var got BgExit
	select {
	case got = <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("onExit hook did not fire within 3s")
	}

	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
	if got.Command != "echo hi-from-bg" {
		t.Errorf("Command = %q, want %q", got.Command, "echo hi-from-bg")
	}
	if got.Status != "exited: 0" {
		t.Errorf("Status = %q, want 'exited: 0'", got.Status)
	}
	if !strings.Contains(got.NewOutput, "hi-from-bg") {
		t.Errorf("NewOutput = %q, want it to contain 'hi-from-bg'", got.NewOutput)
	}

	// Dedup: the hook already consumed the output via readNew, so a subsequent
	// terminal_output-style poll must not re-report the same bytes.
	o, _, found, _ := m.Read(id)
	if !found {
		t.Fatal("process should still be known after exit")
	}
	if strings.Contains(o, "hi-from-bg") {
		t.Errorf("output should have been consumed by the hook; Read re-reported %q", o)
	}
}

// TestBackgroundManager_NoHookByDefault confirms a nil hook (the default)
// leaves the original poll-only behaviour intact — Start/Read still work.
func TestBackgroundManager_NoHookByDefault(t *testing.T) {
	m := NewBackgroundManager()
	id, err := m.Start("echo plain")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	var out string
	waitFor(t, "process to exit", func() bool {
		o, s, f, _ := m.Read(id)
		out += o
		return f && strings.HasPrefix(s, "exited")
	})
	if !strings.Contains(out, "plain") {
		t.Errorf("output = %q, want 'plain'", out)
	}
}

// TestBackgroundManager_OnExitNotFiredForInvisibleProcess verifies that a
// sync-started process which finishes *before* the timeout (and therefore
// never gets Promoted to visible) does NOT fire the onExit hook. This
// prevents a spurious "background finished" system-reminder for a command
// whose result was already returned synchronously.
func TestBackgroundManager_OnExitNotFiredForInvisibleProcess(t *testing.T) {
	m := NewBackgroundManager()

	fired := make(chan BgExit, 1)
	m.SetOnExit(func(e BgExit) {
		select {
		case fired <- e:
		default:
		}
	})

	// Start invisible (visible=false), exactly like the sync path does.
	id, err := m.Start("echo sync-done", WithVisible(false))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait for the process to finish.
	waitFor(t, "process to exit", func() bool {
		_, s, f, _ := m.Read(id)
		return f && strings.HasPrefix(s, "exited")
	})

	// Give the waiter goroutine time to evaluate the visible flag.
	select {
	case got := <-fired:
		t.Fatalf("onExit should NOT fire for an invisible sync process, but got %+v", got)
	case <-time.After(200 * time.Millisecond):
		// Expected: no hook fired.
	}
}

// TestBackgroundManager_OnExitFiresAfterPromote verifies that a sync-started
// process which times out (gets Promoted to visible=true) and then finishes
// DOES fire the onExit hook, because it became a true background task.
func TestBackgroundManager_OnExitFiresAfterPromote(t *testing.T) {
	m := NewBackgroundManager()

	fired := make(chan BgExit, 1)
	m.SetOnExit(func(e BgExit) {
		select {
		case fired <- e:
		default:
		}
	})

	// Start invisible, then promote before it finishes.
	id, err := m.Start("sleep 0.1", WithVisible(false))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Promote immediately (simulating the timeout path).
	m.Promote(id)

	var got BgExit
	select {
	case got = <-fired:
	case <-time.After(3 * time.Second):
		t.Fatal("onExit hook did not fire after Promote within 3s")
	}

	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
	if got.Status != "exited: 0" {
		t.Errorf("Status = %q, want 'exited: 0'", got.Status)
	}
}
