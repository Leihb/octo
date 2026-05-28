package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubExtractSender returns a canned reply for both extract and consolidate
// side-calls. It captures the system prompt so tests can assert which side-call
// fired.
type stubExtractSender struct {
	reply        string
	err          error
	lastSystem   string
	lastMessages []Message
}

func (s *stubExtractSender) SendMessages(_ context.Context, _, system string, msgs []Message, _ int) (Reply, error) {
	s.lastSystem = system
	s.lastMessages = msgs
	if s.err != nil {
		return Reply{}, s.err
	}
	return Reply{Content: s.reply, InputTokens: 10, OutputTokens: 20}, nil
}

func (s *stubExtractSender) StreamMessages(_ context.Context, _, _ string, _ []Message, _ int, _ func(string)) (Reply, error) {
	return s.SendMessages(context.Background(), "", "", nil, 0)
}

func TestParseExtractResult_Object(t *testing.T) {
	good := `{
	  "rollout_slug": "tune-extract-prompt",
	  "rollout_summary": "# tuned the extract prompt\n\nDetails…",
	  "facts": [
	    {"type":"user","description":"prefers Go","content":"User likes Go."},
	    {"type":"feedback","description":"run tests first","content":"Always run tests before committing."}
	  ]
	}`
	r, err := parseExtractResult(good)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if r.Slug != "tune-extract-prompt" {
		t.Errorf("slug = %q", r.Slug)
	}
	if !strings.Contains(r.Summary, "tuned the extract prompt") {
		t.Errorf("summary lost: %q", r.Summary)
	}
	if len(r.Facts) != 2 || r.Facts[1].Description != "run tests first" {
		t.Errorf("facts wrong: %+v", r.Facts)
	}
}

func TestParseExtractResult_FencedAndPreamble(t *testing.T) {
	fenced := "```json\n{\"rollout_slug\":\"x\",\"rollout_summary\":\"\",\"facts\":[{\"type\":\"user\",\"description\":\"d\",\"content\":\"c\"}]}\n```"
	r, _ := parseExtractResult(fenced)
	if len(r.Facts) != 1 || r.Slug != "x" {
		t.Errorf("fenced object should parse: %+v", r)
	}
	withPreamble := "Here you go:\n{\"rollout_slug\":\"y\",\"rollout_summary\":\"\",\"facts\":[{\"type\":\"user\",\"description\":\"d\",\"content\":\"c\"}]}"
	r, _ = parseExtractResult(withPreamble)
	if len(r.Facts) != 1 || r.Slug != "y" {
		t.Errorf("preamble should not block parsing: %+v", r)
	}
}

func TestParseExtractResult_EmptyAndNoop(t *testing.T) {
	if r, err := parseExtractResult(`{"rollout_slug":"","rollout_summary":"","facts":[]}`); err != nil || r.Slug != "" || r.Summary != "" || r.Facts != nil {
		t.Errorf("no-op object → zero result; got %+v, %v", r, err)
	}
	if r, err := parseExtractResult("nothing interesting"); err != nil || r.Slug != "" || r.Facts != nil {
		t.Errorf("no JSON → zero result; got %+v, %v", r, err)
	}
}

func TestParseExtractResult_AcceptsLegacyArray(t *testing.T) {
	// Defensive: a model that ignored the new schema and returned a bare array
	// (the pre-this-PR shape) should still yield usable facts. Slug/summary
	// stay empty in that case.
	legacy := `[{"type":"feedback","description":"run tests first","content":"…"}]`
	r, err := parseExtractResult(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if r.Slug != "" || r.Summary != "" {
		t.Errorf("legacy array should leave slug/summary empty, got %+v", r)
	}
	if len(r.Facts) != 1 || r.Facts[0].Description != "run tests first" {
		t.Errorf("legacy array should still yield facts: %+v", r.Facts)
	}
}

func TestParseExtractResult_DescriptionFromContent(t *testing.T) {
	in := `{"rollout_slug":"","rollout_summary":"","facts":[{"type":"user","content":"User prefers Go.\nDetails follow."}]}`
	r, _ := parseExtractResult(in)
	if len(r.Facts) != 1 || r.Facts[0].Description != "User prefers Go." {
		t.Errorf("description should default to first line of content: %+v", r.Facts)
	}
}

func TestSanitizeSlug(t *testing.T) {
	cases := map[string]string{
		"Tune Extract Prompt":           "tune-extract-prompt",
		"  fix Bing-encoding  ":         "fix-bing-encoding",
		"___debug___":                   "debug",
		"":                              "",
		"非英文 slug fallback":             "slug-fallback",
		strings.Repeat("very-long-", 8): "very-long-very-long-very-long-very-long-very-long-very-long",
	}
	for in, want := range cases {
		if got := sanitizeSlug(in); got != want {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractMemory(t *testing.T) {
	s := &stubExtractSender{reply: `{"rollout_slug":"tune","rollout_summary":"# tune\n","facts":[{"type":"feedback","description":"run tests first","content":"Always run tests before committing."}]}`}
	a := New(s, "test-model")
	msgs := []Message{NewUserMessage("test")}

	r, err := a.ExtractMemory(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if r.Slug != "tune" || !strings.Contains(r.Summary, "tune") {
		t.Errorf("ExtractMemory slug/summary: %+v", r)
	}
	if len(r.Facts) != 1 || r.Facts[0].Description != "run tests first" {
		t.Errorf("ExtractMemory facts: %+v", r.Facts)
	}
	if !strings.Contains(s.lastSystem, "JSON object") {
		t.Errorf("ExtractMemory should use the extract system prompt, got %q", s.lastSystem)
	}
}

func TestExtractMemory_NoMessages(t *testing.T) {
	a := New(&stubExtractSender{}, "test-model")
	r, err := a.ExtractMemory(context.Background(), nil)
	if err != nil || r.Slug != "" || r.Summary != "" || r.Facts != nil {
		t.Errorf("no messages → zero result; got %+v, %v", r, err)
	}
}

func TestExtractMemory_SenderError(t *testing.T) {
	s := &stubExtractSender{err: errors.New("boom")}
	a := New(s, "test-model")
	if _, err := a.ExtractMemory(context.Background(), []Message{NewUserMessage("x")}); err == nil {
		t.Error("expected error to propagate")
	}
}

func TestConsolidateMemory(t *testing.T) {
	s := &stubExtractSender{reply: "Consolidated summary."}
	a := New(s, "test-model")
	out, err := a.ConsolidateMemory(context.Background(), "", "- [user] prefers Go")
	if err != nil || out != "Consolidated summary." {
		t.Errorf("ConsolidateMemory out=%q err=%v", out, err)
	}
	if !strings.Contains(s.lastSystem, "summary") {
		t.Errorf("should use consolidate system prompt, got %q", s.lastSystem)
	}
	// Both arguments empty → short-circuit to empty, no call.
	s.lastMessages = nil
	out, _ = a.ConsolidateMemory(context.Background(), "", "")
	if out != "" || s.lastMessages != nil {
		t.Errorf("empty inputs should short-circuit; out=%q msgs=%v", out, s.lastMessages)
	}
}

func TestConsolidateMemory_IncrementalPrompt(t *testing.T) {
	s := &stubExtractSender{reply: "merged"}
	a := New(s, "test-model")
	if _, err := a.ConsolidateMemory(context.Background(), "PRIOR_SUMMARY_XYZ", "NEW_NOTE_ABC"); err != nil {
		t.Fatal(err)
	}
	if len(s.lastMessages) == 0 {
		t.Fatal("expected a SendMessages call")
	}
	prompt := s.lastMessages[0].Content
	if !strings.Contains(prompt, "PRIOR_SUMMARY_XYZ") || !strings.Contains(prompt, "NEW_NOTE_ABC") {
		t.Errorf("prompt should carry both prior summary and new notes:\n%s", prompt)
	}
}
