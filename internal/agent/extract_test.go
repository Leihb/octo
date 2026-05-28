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

func TestParseFacts(t *testing.T) {
	good := `[{"type":"user","description":"prefers Go","content":"User likes Go."},{"type":"feedback","description":"run tests first","content":"Always run tests before committing."}]`
	facts, err := parseFacts(good)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(facts) != 2 || facts[0].Type != "user" || facts[1].Description != "run tests first" {
		t.Errorf("parseFacts wrong: %+v", facts)
	}
}

func TestParseFacts_FencedAndPreamble(t *testing.T) {
	fenced := "```json\n[{\"type\":\"user\",\"description\":\"d\",\"content\":\"c\"}]\n```"
	facts, _ := parseFacts(fenced)
	if len(facts) != 1 {
		t.Errorf("fenced should parse one fact: %+v", facts)
	}
	withPreamble := "Here you go:\n[{\"type\":\"user\",\"description\":\"d\",\"content\":\"c\"}]"
	facts, _ = parseFacts(withPreamble)
	if len(facts) != 1 {
		t.Errorf("preamble should not block parsing: %+v", facts)
	}
}

func TestParseFacts_EmptyAndMissingArray(t *testing.T) {
	if facts, err := parseFacts("[]"); err != nil || facts != nil {
		t.Errorf("[] should yield nil,nil; got %v,%v", facts, err)
	}
	if facts, err := parseFacts("nothing interesting"); err != nil || facts != nil {
		t.Errorf("no array should yield nil,nil; got %v,%v", facts, err)
	}
}

func TestParseFacts_DescriptionFromContent(t *testing.T) {
	in := `[{"type":"user","content":"User prefers Go.\nDetails follow."}]`
	facts, _ := parseFacts(in)
	if len(facts) != 1 || facts[0].Description != "User prefers Go." {
		t.Errorf("description should default to first line of content: %+v", facts)
	}
}

func TestExtractMemory(t *testing.T) {
	s := &stubExtractSender{reply: `[{"type":"feedback","description":"run tests first","content":"Always run tests before committing."}]`}
	a := New(s, "test-model")
	msgs := []Message{NewUserMessage("test")}

	facts, err := a.ExtractMemory(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Description != "run tests first" {
		t.Errorf("ExtractMemory: %+v", facts)
	}
	if !strings.Contains(s.lastSystem, "JSON array") {
		t.Errorf("ExtractMemory should use the extract system prompt, got %q", s.lastSystem)
	}
}

func TestExtractMemory_NoMessages(t *testing.T) {
	a := New(&stubExtractSender{}, "test-model")
	facts, err := a.ExtractMemory(context.Background(), nil)
	if err != nil || facts != nil {
		t.Errorf("no messages → nil,nil; got %v,%v", facts, err)
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
