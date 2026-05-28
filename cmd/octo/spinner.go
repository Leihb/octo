package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattn/go-isatty"
)

// spinner is a one-line "still thinking" indicator shown during pauses
// between turn start and the first byte of model output. It's gated on
// stdout being an interactive terminal so piped or redirected stdout
// stays free of escape sequences (the user can still see exactly what
// the model printed).
//
// Use:
//
//	sp := newSpinner(stdout, "thinking…")
//	sp.Start(250 * time.Millisecond)     // delay before first frame
//	defer sp.Stop()                       // clears the line
//
// Stop is idempotent — calling it after the first event has stopped the
// spinner is a no-op, so the EventHandler closure can call it on every
// event without bookkeeping.
type spinner struct {
	out     io.Writer
	label   string
	enabled bool

	// Two-stage state: armed (Start called, waiting for delay to elapse) and
	// running (delay elapsed, frames being drawn). Both transition to "done"
	// when Stop is called. running is atomic.Bool so the EventHandler can
	// call Stop concurrently with the goroutine without locking.
	running atomic.Bool
	stop    chan struct{}
	once    sync.Once
}

// spinnerFrames is a 10-frame Braille spinner — visually pleasant in dark and
// light terminals, doesn't depend on Unicode-Emoji rendering.
var spinnerFrames = []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}

// newSpinner builds a spinner that paints to out. Auto-disables itself when
// out isn't an interactive terminal so non-tty consumers (pipes, tests, CI
// logs) get clean output. The Start / Stop methods then become no-ops.
func newSpinner(out io.Writer, label string) *spinner {
	return &spinner{
		out:     out,
		label:   label,
		enabled: writerIsTTY(out),
	}
}

// Start kicks off the spinner. If delay > 0, the first frame is drawn after
// that delay — useful so a sub-100ms reply never blinks a spinner at the
// user. Subsequent Start calls (while still running) are silently ignored.
func (s *spinner) Start(delay time.Duration) {
	if s == nil || !s.enabled {
		return
	}
	if !s.running.CompareAndSwap(false, true) {
		return
	}
	s.stop = make(chan struct{})
	s.once = sync.Once{}
	go s.loop(delay)
}

// Stop halts the spinner and clears the painted line. Idempotent.
func (s *spinner) Stop() {
	if s == nil || !s.enabled {
		return
	}
	if !s.running.CompareAndSwap(true, false) {
		return
	}
	s.once.Do(func() { close(s.stop) })
}

func (s *spinner) loop(delay time.Duration) {
	// Stage 1: wait out the silent grace period. If Stop fires here we just
	// exit — no frame ever painted, so nothing to clear.
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-s.stop:
			return
		}
	}
	// Stage 2: paint frames at ~12.5 Hz until Stop arrives.
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	i := 0
	// Draw the first frame immediately so the user sees something the moment
	// the grace period elapses, instead of waiting up to 80ms more.
	s.draw(i)
	i++
	for {
		select {
		case <-s.stop:
			s.clear()
			return
		case <-ticker.C:
			s.draw(i)
			i++
		}
	}
}

func (s *spinner) draw(i int) {
	// \r returns to column 0; \x1b[K clears from cursor to end of line. Result
	// is each frame overwrites the previous one without leaving a trail.
	fmt.Fprintf(s.out, "\r\x1b[K%c %s", spinnerFrames[i%len(spinnerFrames)], s.label)
}

func (s *spinner) clear() {
	fmt.Fprint(s.out, "\r\x1b[K")
}

// writerIsTTY reports whether w is an *os.File backed by an interactive
// terminal. Anything else returns false — including bytes.Buffer (used by
// tests), os.Pipe (used by shell redirection), and io.MultiWriter.
func writerIsTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd())
}
