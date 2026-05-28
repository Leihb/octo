package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/chzyer/readline"
	"github.com/mattn/go-isatty"
)

// lineReader is the REPL's input abstraction. Two implementations:
//
//   - scannerLineReader  — wraps bufio.Scanner. Used for piped stdin, test
//     fixtures (strings.Reader), and any non-tty input.
//   - readlineLineReader — wraps chzyer/readline. Used in interactive
//     terminals; provides history, line editing, Ctrl-C / Ctrl-D handling.
//
// The interface intentionally keeps prompt printing inside the reader so
// scanner mode and readline mode produce the same stdout sequence (important
// for tests that assert against stdout). Callers that draw multi-line prompts
// — the asker's question card — print the body themselves and pass only the
// final inline prompt ("Select [1-3]: ") to ReadLine.
type lineReader interface {
	// ReadLine reads one logical line. prompt, if non-empty, is rendered
	// just before the cursor; the trailing newline produced by the user
	// is stripped from the returned string. ok is false on EOF or any
	// terminal error.
	ReadLine(prompt string) (line string, ok bool)
	// Interrupted reports whether the most recent ReadLine ended via
	// Ctrl-C at the prompt (readline only). Scanner mode never sets it.
	Interrupted() bool
	// Close flushes history (readline) and releases the terminal.
	Close() error
}

// scannerLineReader is the non-tty fallback. Tests rely on this — they pass
// stdin as a strings.Reader and expect deterministic stdout including the
// "you> " prompts.
type scannerLineReader struct {
	in  *bufio.Scanner
	out io.Writer
}

func newScannerLineReader(in io.Reader, out io.Writer) *scannerLineReader {
	return &scannerLineReader{in: bufio.NewScanner(in), out: out}
}

func (s *scannerLineReader) ReadLine(prompt string) (string, bool) {
	if prompt != "" {
		fmt.Fprint(s.out, prompt)
	}
	if !s.in.Scan() {
		return "", false
	}
	return strings.TrimRight(s.in.Text(), "\r"), true
}

func (*scannerLineReader) Interrupted() bool { return false }
func (*scannerLineReader) Close() error      { return nil }

// readlineLineReader is the interactive-tty implementation.
type readlineLineReader struct {
	rl          *readline.Instance
	interrupted bool
}

func newReadlineReader(historyFile string) (*readlineLineReader, error) {
	if historyFile != "" {
		if err := os.MkdirAll(filepath.Dir(historyFile), 0o755); err != nil {
			// Non-fatal: fall through with history disabled rather than
			// blowing up the REPL because ~/.octo isn't writable.
			historyFile = ""
		}
	}
	rl, err := readline.NewEx(&readline.Config{
		Prompt:                 "you> ",
		HistoryFile:            historyFile,
		HistoryLimit:           5000,
		HistorySearchFold:      true,
		DisableAutoSaveHistory: false,
		InterruptPrompt:        "^C",
		EOFPrompt:              "",
	})
	if err != nil {
		return nil, err
	}
	return &readlineLineReader{rl: rl}, nil
}

func (r *readlineLineReader) ReadLine(prompt string) (string, bool) {
	r.interrupted = false
	if prompt == "" {
		// readline always renders *some* prompt; an empty one is fine.
		r.rl.SetPrompt("")
	} else {
		r.rl.SetPrompt(prompt)
	}
	line, err := r.rl.Readline()
	switch {
	case errors.Is(err, readline.ErrInterrupt):
		r.interrupted = true
		return "", false
	case errors.Is(err, io.EOF):
		return "", false
	case err != nil:
		return "", false
	}
	return line, true
}

func (r *readlineLineReader) Interrupted() bool { return r.interrupted }
func (r *readlineLineReader) Close() error      { return r.rl.Close() }

// stdinIsTTY reports whether r is an *os.File backed by an interactive
// terminal. Anything else (pipe, file, strings.Reader) returns false.
func stdinIsTTY(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd())
}

// defaultHistoryFile resolves the path used for persistent REPL history.
// OCTO_HISTORY_FILE wins so users (and tests) can redirect it. Empty
// return disables history persistence.
func defaultHistoryFile() string {
	if env := os.Getenv("OCTO_HISTORY_FILE"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".octo", "history")
}

// readPromptLine reads one user-facing input line, expanding `\` line
// continuations into a single multi-line string (joined with "\n"). The
// continuation prompt is used for any line after the first. Both modes
// support continuation; in scanner mode a trailing `\` in piped input
// glues two lines together, which is consistent with bash-style heredocs.
func readPromptLine(r lineReader, prompt, contPrompt string) (string, bool) {
	first, ok := r.ReadLine(prompt)
	if !ok {
		return "", false
	}
	if !endsWithContinuation(first) {
		return first, true
	}
	var sb strings.Builder
	sb.WriteString(strings.TrimSuffix(first, "\\"))
	for {
		next, ok := r.ReadLine(contPrompt)
		if !ok {
			// EOF mid-continuation — return what we have so the user's
			// keystrokes aren't silently discarded.
			return sb.String(), true
		}
		if !endsWithContinuation(next) {
			sb.WriteString("\n")
			sb.WriteString(next)
			return sb.String(), true
		}
		sb.WriteString("\n")
		sb.WriteString(strings.TrimSuffix(next, "\\"))
	}
}

// endsWithContinuation is true when the line ends in a single unescaped
// backslash. A doubled `\\` at the end is treated as a literal backslash,
// not a continuation marker — same rule bash uses.
func endsWithContinuation(line string) bool {
	if !strings.HasSuffix(line, "\\") {
		return false
	}
	// Count trailing backslashes; odd count → continuation.
	n := 0
	for i := len(line) - 1; i >= 0 && line[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}
