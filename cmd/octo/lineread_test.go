package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestScannerLineReader_PrintsPrompt(t *testing.T) {
	var out bytes.Buffer
	r := newScannerLineReader(strings.NewReader("hi\n"), &out)

	line, ok := r.ReadLine("you> ")
	if !ok || line != "hi" {
		t.Fatalf("ReadLine = (%q, %v); want (\"hi\", true)", line, ok)
	}
	if got := out.String(); got != "you> " {
		t.Errorf("prompt = %q; want %q", got, "you> ")
	}
}

func TestScannerLineReader_EOFReturnsFalse(t *testing.T) {
	var out bytes.Buffer
	r := newScannerLineReader(strings.NewReader(""), &out)

	if _, ok := r.ReadLine(""); ok {
		t.Errorf("expected EOF on empty input")
	}
	if r.Interrupted() {
		t.Errorf("scanner reader should never report interrupt")
	}
}

func TestScannerLineReader_StripsCarriageReturn(t *testing.T) {
	var out bytes.Buffer
	r := newScannerLineReader(strings.NewReader("hi\r\n"), &out)
	line, ok := r.ReadLine("")
	if !ok || line != "hi" {
		t.Errorf("ReadLine = (%q, %v); want (\"hi\", true)", line, ok)
	}
}

func TestReadPromptLine_SingleLine(t *testing.T) {
	var out bytes.Buffer
	r := newScannerLineReader(strings.NewReader("hello\n"), &out)

	got, ok := readPromptLine(r, "you> ", "... ")
	if !ok || got != "hello" {
		t.Errorf("got = (%q, %v); want (\"hello\", true)", got, ok)
	}
	if !strings.Contains(out.String(), "you> ") {
		t.Errorf("expected primary prompt; got %q", out.String())
	}
	if strings.Contains(out.String(), "... ") {
		t.Errorf("did not expect continuation prompt on single line; got %q", out.String())
	}
}

func TestReadPromptLine_BackslashContinuation(t *testing.T) {
	var out bytes.Buffer
	// Two `\`-suffixed lines plus a final unterminated one.
	in := "first\\\nsecond\\\nthird\n"
	r := newScannerLineReader(strings.NewReader(in), &out)

	got, ok := readPromptLine(r, "you> ", "... ")
	if !ok {
		t.Fatal("ReadPromptLine ok=false")
	}
	want := "first\nsecond\nthird"
	if got != want {
		t.Errorf("got = %q; want %q", got, want)
	}
	prompts := out.String()
	if !strings.Contains(prompts, "you> ") {
		t.Errorf("missing primary prompt; got %q", prompts)
	}
	if strings.Count(prompts, "... ") != 2 {
		t.Errorf("expected 2 continuation prompts; got %q", prompts)
	}
}

func TestReadPromptLine_DoubleBackslashIsLiteral(t *testing.T) {
	var out bytes.Buffer
	// `\\` at end means a literal backslash, not a continuation.
	r := newScannerLineReader(strings.NewReader("foo\\\\\n"), &out)
	got, ok := readPromptLine(r, "", "")
	if !ok || got != "foo\\\\" {
		t.Errorf("got = (%q, %v); want (\"foo\\\\\\\\\", true)", got, ok)
	}
}

func TestReadPromptLine_EOFMidContinuation(t *testing.T) {
	var out bytes.Buffer
	r := newScannerLineReader(strings.NewReader("first\\\n"), &out)
	got, ok := readPromptLine(r, "", "")
	if !ok {
		t.Fatal("expected partial line, not EOF")
	}
	if got != "first" {
		t.Errorf("got = %q; want %q", got, "first")
	}
}

func TestEndsWithContinuation(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"foo", false},
		{"foo\\", true},
		{"foo\\\\", false},
		{"foo\\\\\\", true},
		{"", false},
	}
	for _, tc := range cases {
		if got := endsWithContinuation(tc.in); got != tc.want {
			t.Errorf("endsWithContinuation(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}
