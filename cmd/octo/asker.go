package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/Leihb/octo-agent/internal/tools"
)

// replAsker implements tools.Asker by prompting on the REPL's shared
// stdin/stdout. Same plumbing as cliPermissionGate — both run synchronously
// inside RunStream, so they share the line reader without racing on it.
//
// UX:
//
//	[ask_user_question · header]
//	  Which library should we use?
//	    1) date-fns
//	    2) Day.js
//	    3) Other (free text)
//	  Select [1-3]: _
//
// On multi-select the prompt accepts a comma-separated list ("1,3"). The
// "Other" tail is always added and lets the user type a free-text answer
// instead of choosing.
type replAsker struct {
	in  lineReader // shared with the REPL loop
	out io.Writer
}

func newREPLAsker(in lineReader, out io.Writer) *replAsker {
	return &replAsker{in: in, out: out}
}

// Ask implements tools.Asker.
func (a *replAsker) Ask(_ context.Context, q tools.AskRequest) (tools.AskResponse, error) {
	if a.in == nil {
		return tools.AskResponse{Cancelled: true}, nil
	}

	prompt := a.printQuestion(q)

	raw, ok := a.in.ReadLine(prompt)
	if !ok {
		// EOF or empty submission → treat as cancellation. An empty answer
		// to a forced-pick prompt isn't a valid selection, and surfacing
		// "(user cancelled)" to the model gives it a chance to ask again
		// or pick a default itself.
		fmt.Fprintln(a.out)
		return tools.AskResponse{Cancelled: true}, nil
	}

	choice := strings.TrimSpace(raw)
	if choice == "" {
		return tools.AskResponse{Cancelled: true}, nil
	}

	// "Other" picks the (N+1)th slot — the free-text tail.
	otherIdx := len(q.Options) + 1
	indices, parseErr := parseSelection(choice, otherIdx, q.MultiSelect)
	if parseErr != nil {
		fmt.Fprintf(a.out, "  (couldn't parse %q, treating as cancellation)\n", choice)
		return tools.AskResponse{Cancelled: true}, nil
	}

	var (
		picks      []string
		pickedSlot int
		wantOther  bool
	)
	for _, idx := range indices {
		if idx == otherIdx {
			wantOther = true
			continue
		}
		if idx < 1 || idx > len(q.Options) {
			continue // ignore out-of-range; parseSelection already filtered
		}
		picks = append(picks, q.Options[idx-1])
		pickedSlot = idx
	}
	_ = pickedSlot

	if wantOther {
		text, ok := a.in.ReadLine("  Other (free text): ")
		if !ok || strings.TrimSpace(text) == "" {
			return tools.AskResponse{Cancelled: true}, nil
		}
		return tools.AskResponse{Custom: strings.TrimSpace(text)}, nil
	}

	if len(picks) == 0 {
		return tools.AskResponse{Cancelled: true}, nil
	}
	return tools.AskResponse{Choices: picks}, nil
}

// printQuestion writes the multi-line question card and returns the final
// inline "Select [...]: " prompt — which is passed to ReadLine so that
// readline can render it correctly (e.g. preserving line position for
// history navigation).
func (a *replAsker) printQuestion(q tools.AskRequest) string {
	header := q.Header
	if header == "" {
		header = "question"
	}
	fmt.Fprintf(a.out, "\n[ask_user_question · %s]\n", header)
	fmt.Fprintf(a.out, "  %s\n", q.Question)
	otherIdx := len(q.Options) + 1
	for i, opt := range q.Options {
		fmt.Fprintf(a.out, "    %d) %s\n", i+1, opt)
	}
	fmt.Fprintf(a.out, "    %d) Other (free text)\n", otherIdx)

	hint := fmt.Sprintf("[1-%d]", otherIdx)
	if q.MultiSelect {
		hint = "[comma-separated, e.g. 1,3]"
	}
	return fmt.Sprintf("  Select %s: ", hint)
}

// parseSelection converts the user's typed answer into a list of 1-based
// option indices. Empty / non-numeric input returns nil with no error;
// genuinely malformed input (e.g. mixing numbers and prose) errors so the
// caller can treat it as cancellation rather than guessing.
//
// In single-select mode, only the first index is honored even if the user
// typed multiple.
func parseSelection(raw string, maxIdx int, multi bool) ([]int, error) {
	parts := strings.Split(raw, ",")
	var out []int
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("not a number: %q", p)
		}
		if n < 1 || n > maxIdx {
			return nil, fmt.Errorf("out of range: %d", n)
		}
		out = append(out, n)
	}
	if !multi && len(out) > 1 {
		out = out[:1]
	}
	return out, nil
}
