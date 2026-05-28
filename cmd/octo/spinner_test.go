package main

import (
	"bytes"
	"testing"
	"time"
)

// bytes.Buffer is not an *os.File and isn't a tty, so newSpinner returns one
// that's permanently disabled. Start / Stop become no-ops — exactly what we
// want for test fixtures, but it also means we can't easily exercise the
// frame-drawing path through the public surface. The tests below assert the
// public no-op behaviour; the loop+timer path is exercised through manual
// smoke testing.

func TestSpinner_NonTTYDisabled(t *testing.T) {
	var buf bytes.Buffer
	s := newSpinner(&buf, "thinking…")
	if s.enabled {
		t.Fatal("spinner should be disabled when output isn't a tty")
	}
	s.Start(0)
	// Give the no-op goroutine a chance to do nothing.
	time.Sleep(20 * time.Millisecond)
	s.Stop()
	if buf.Len() != 0 {
		t.Errorf("non-tty spinner must produce no output, got %q", buf.String())
	}
}

func TestSpinner_StopIsIdempotent(t *testing.T) {
	var buf bytes.Buffer
	s := newSpinner(&buf, "x")
	// Multiple Stops with no Start must not panic.
	s.Stop()
	s.Stop()
	s.Start(0)
	s.Stop()
	s.Stop()
}

func TestSpinner_DoubleStartIgnored(t *testing.T) {
	var buf bytes.Buffer
	s := newSpinner(&buf, "x")
	s.Start(0)
	// Second Start should silently return; if it kicked off a second loop
	// goroutine and we Stop()'d only once, the loop would leak. Detected
	// indirectly: the running flag stays true after the first Stop call
	// makes it false, then the second Start would set it back true — but
	// since we still expect Stop to fully shut it down, we just verify it
	// doesn't panic on repeated start/stop cycles.
	s.Start(0)
	s.Stop()
	s.Start(0)
	s.Stop()
}

func TestSpinner_NilReceiverIsSafe(t *testing.T) {
	// Both methods are guarded with `if s == nil` so callers don't have to
	// branch when verbosity.quiet() suppresses spinner creation.
	var s *spinner
	s.Start(time.Millisecond)
	s.Stop()
}

// TestSpinner_StopWaitsForLoopExit pins down the regression fix for the
// race that ate the first character of streamed assistant replies. Before
// the fix, Stop() closed the stop channel and returned immediately —
// leaving the goroutine to race against the next caller's Fprint. The
// fix wires up a `done` channel and Stop() blocks on it.
//
// We can't directly observe the goroutine, but we can prove Stop is
// synchronous by checking that after Stop returns the loop's state (the
// `running` flag plus the `done` channel) is fully settled.
func TestSpinner_StopWaitsForLoopExit(t *testing.T) {
	var buf bytes.Buffer
	s := newSpinner(&buf, "x")
	// Force-enable even though buf isn't a tty so the goroutine actually
	// runs. This is a white-box test.
	s.enabled = true
	s.Start(0)
	s.Stop()
	// done channel must be closed by the time Stop returns. select with a
	// 0-length sleep proves we don't block (closed channels are
	// immediately receivable).
	select {
	case <-s.done:
	default:
		t.Error("Stop returned before the loop goroutine signaled done")
	}
	// running flag must be false too — Stop CompareAndSwap flipped it.
	if s.running.Load() {
		t.Error("running flag should be false after Stop")
	}
}
