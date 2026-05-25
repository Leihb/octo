package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Leihb/octo/internal/agent"
)

// stubSender is a deterministic Sender for REPL tests — returns a fixed reply
// for every call so tests don't touch the network.
type stubSender struct {
	reply  string
	err    error
	called int
}

func (s *stubSender) SendMessages(_ context.Context, _, _ string, _ []agent.Message, _ int) (agent.Reply, error) {
	s.called++
	return agent.Reply{Content: s.reply, InputTokens: 10, OutputTokens: 5}, s.err
}

func (s *stubSender) StreamMessages(
	_ context.Context,
	_, _ string,
	_ []agent.Message,
	_ int,
	onChunk func(string),
) (agent.Reply, error) {
	s.called++
	if onChunk != nil {
		onChunk(s.reply)
	}
	return agent.Reply{Content: s.reply, InputTokens: 10, OutputTokens: 5}, s.err
}

// makeREPLFixture returns a replConfig wired to a stubSender and in-memory
// buffers. HOME is redirected to a temp dir so session files don't pollute
// ~/.octo.
func makeREPLFixture(t *testing.T, input string) (replConfig, *bytes.Buffer, *bytes.Buffer, *stubSender) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	stub := &stubSender{reply: "pong"}
	a := agent.New(stub, "test-model")
	sess := agent.NewSession("test-model", "")

	var stdout, stderr bytes.Buffer
	cfg := replConfig{
		a:       a,
		session: sess,
		noSave:  false,
		stdin:   strings.NewReader(input),
		stdout:  &stdout,
		stderr:  &stderr,
	}
	return cfg, &stdout, &stderr, stub
}

func TestREPL_SingleTurn(t *testing.T) {
	cfg, stdout, stderr, stub := makeREPLFixture(t, "ping\n/exit\n")

	code := runREPL(cfg)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr.String())
	}
	if stub.called != 1 {
		t.Errorf("Sender called %d times, want 1", stub.called)
	}
	out := stdout.String()
	if !strings.Contains(out, "pong") {
		t.Errorf("stdout does not contain reply %q:\n%s", "pong", out)
	}
}

func TestREPL_MultiTurn(t *testing.T) {
	cfg, _, stderr, stub := makeREPLFixture(t, "one\ntwo\nthree\n/exit\n")

	code := runREPL(cfg)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr.String())
	}
	if stub.called != 3 {
		t.Errorf("Sender called %d times, want 3", stub.called)
	}
}

func TestREPL_EmptyLineSkipped(t *testing.T) {
	cfg, _, _, stub := makeREPLFixture(t, "\n\nhello\n/exit\n")

	runREPL(cfg)
	if stub.called != 1 {
		t.Errorf("Sender called %d times, want 1 (empty lines must be skipped)", stub.called)
	}
}

func TestREPL_HelpCommand(t *testing.T) {
	cfg, stdout, _, stub := makeREPLFixture(t, "/help\n/exit\n")

	runREPL(cfg)
	if stub.called != 0 {
		t.Errorf("Sender called %d times for /help, want 0", stub.called)
	}
	if !strings.Contains(stdout.String(), "/exit") {
		t.Error("stdout does not contain help text")
	}
}

func TestREPL_CostCommand(t *testing.T) {
	cfg, stdout, _, _ := makeREPLFixture(t, "hi\n/cost\n/exit\n")

	runREPL(cfg)
	out := stdout.String()
	if !strings.Contains(out, "Tokens:") {
		t.Errorf("stdout does not contain cost line:\n%s", out)
	}
}

func TestREPL_SaveCommand(t *testing.T) {
	cfg, stdout, stderr, _ := makeREPLFixture(t, "hi\n/save\n/exit\n")

	code := runREPL(cfg)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Saved →") {
		t.Errorf("stdout does not contain save confirmation:\n%s", out)
	}
}

func TestREPL_UnknownSlashCommand(t *testing.T) {
	cfg, stdout, _, stub := makeREPLFixture(t, "/bogus\n/exit\n")

	runREPL(cfg)
	if stub.called != 0 {
		t.Errorf("Sender called for unknown command")
	}
	if !strings.Contains(stdout.String(), "Unknown command") {
		t.Error("expected unknown command message")
	}
}

func TestREPL_EOFExitsCleanly(t *testing.T) {
	cfg, _, stderr, _ := makeREPLFixture(t, "") // EOF immediately

	code := runREPL(cfg)
	if code != 0 {
		t.Fatalf("EOF exit code = %d, stderr: %s", code, stderr.String())
	}
}

func TestREPL_NoSave(t *testing.T) {
	cfg, stdout, _, _ := makeREPLFixture(t, "hi\n/exit\n")
	cfg.noSave = true

	runREPL(cfg)
	if strings.Contains(stdout.String(), "Session saved") {
		t.Error("expected no save message with --no-save")
	}
}

func TestREPL_AutoSaveAfterTurn(t *testing.T) {
	cfg, _, stderr, _ := makeREPLFixture(t, "hi\n/exit\n")

	code := runREPL(cfg)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr.String())
	}
	// If auto-save failed it writes to stderr.
	if strings.Contains(stderr.String(), "auto-save failed") {
		t.Errorf("auto-save failed: %s", stderr.String())
	}
}

func TestREPL_ResumedSessionShowsTurnCount(t *testing.T) {
	cfg, stdout, stderr, _ := makeREPLFixture(t, "/exit\n")
	// Pre-populate two turns in history to simulate a resumed session.
	cfg.a.History.Append(agent.NewUserMessage("old q"))
	cfg.a.History.Append(agent.NewAssistantMessage("old a"))
	cfg.session.SyncFrom(cfg.a.History)

	code := runREPL(cfg)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Resumed") {
		t.Errorf("expected 'Resumed' in output:\n%s", out)
	}
	if !strings.Contains(out, "1 turn") {
		t.Errorf("expected '1 turn' in output:\n%s", out)
	}
}
