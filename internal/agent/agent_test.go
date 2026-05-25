package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// fakeSender implements Sender for tests, recording its inputs and returning
// canned replies.
type fakeSender struct {
	gotModel    string
	gotSystem   string
	gotMessages []Message
	gotMaxToks  int

	reply Reply
	err   error
}

func (f *fakeSender) SendMessages(_ context.Context, model, system string, messages []Message, maxTokens int) (Reply, error) {
	f.gotModel = model
	f.gotSystem = system
	f.gotMessages = append([]Message(nil), messages...) // defensive copy
	f.gotMaxToks = maxTokens
	if f.err != nil {
		return Reply{}, f.err
	}
	return f.reply, nil
}

func TestAgent_Turn_HappyPath(t *testing.T) {
	send := &fakeSender{reply: Reply{Content: "hi from agent", Model: "m", StopReason: "end_turn"}}
	a := New(send, "claude-test")
	a.System = "you are octo"

	reply, err := a.Turn(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Turn: %v", err)
	}

	if reply.Content != "hi from agent" {
		t.Errorf("reply.Content = %q", reply.Content)
	}

	// Sender saw model + system + the single user message
	if send.gotModel != "claude-test" {
		t.Errorf("Sender saw model %q", send.gotModel)
	}
	if send.gotSystem != "you are octo" {
		t.Errorf("Sender saw system %q", send.gotSystem)
	}
	if len(send.gotMessages) != 1 || send.gotMessages[0].Role != RoleUser {
		t.Errorf("Sender saw messages %+v", send.gotMessages)
	}

	// History now has [user, assistant]
	snap := a.History.Snapshot()
	if len(snap) != 2 || snap[0].Role != RoleUser || snap[1].Role != RoleAssistant {
		t.Errorf("History after Turn = %+v", snap)
	}
}

func TestAgent_Turn_MultiTurnSendsFullHistory(t *testing.T) {
	send := &fakeSender{reply: Reply{Content: "ok"}}
	a := New(send, "m")

	for _, msg := range []string{"first", "second", "third"} {
		if _, err := a.Turn(context.Background(), msg); err != nil {
			t.Fatalf("Turn(%q): %v", msg, err)
		}
	}

	// On the third call the Sender must have seen [user, asst, user, asst, user].
	if got := len(send.gotMessages); got != 5 {
		t.Fatalf("len(msgs) on 3rd turn = %d, want 5", got)
	}
	wantRoles := []Role{RoleUser, RoleAssistant, RoleUser, RoleAssistant, RoleUser}
	for i, want := range wantRoles {
		if send.gotMessages[i].Role != want {
			t.Errorf("messages[%d].Role = %q, want %q", i, send.gotMessages[i].Role, want)
		}
	}
}

func TestAgent_Turn_SenderError_RestoresHistory(t *testing.T) {
	send := &fakeSender{err: errors.New("upstream 500")}
	a := New(send, "m")

	if _, err := a.Turn(context.Background(), "hello"); err == nil {
		t.Fatal("Turn: expected error, got nil")
	}

	// User message must be rolled back so the next attempt isn't a dup.
	if n := a.History.Len(); n != 0 {
		t.Errorf("History.Len after failed Turn = %d, want 0", n)
	}
}

func TestAgent_Turn_Validation(t *testing.T) {
	a := New(&fakeSender{}, "")
	if _, err := a.Turn(context.Background(), "hi"); err == nil {
		t.Error("empty model should error")
	}

	a = New(nil, "m")
	if _, err := a.Turn(context.Background(), "hi"); err == nil {
		t.Error("nil sender should error")
	}

	a = New(&fakeSender{}, "m")
	if _, err := a.Turn(context.Background(), ""); err == nil {
		t.Error("empty input should error")
	}
}

// fakeStreamSender implements StreamingSender, emitting a canned slice of
// deltas before returning the aggregated reply.
type fakeStreamSender struct {
	fakeSender
	chunks       []string
	gotCallback  bool
	emittedReply Reply
}

func (f *fakeStreamSender) StreamMessages(
	ctx context.Context,
	model, system string,
	messages []Message,
	maxTokens int,
	onChunk func(string),
) (Reply, error) {
	// Record inputs through the embedded fakeSender so the existing
	// assertions on gotModel/gotMessages still apply.
	f.fakeSender.gotModel = model
	f.fakeSender.gotSystem = system
	f.fakeSender.gotMessages = append([]Message(nil), messages...)
	f.fakeSender.gotMaxToks = maxTokens

	if f.fakeSender.err != nil {
		return Reply{}, f.fakeSender.err
	}
	for _, c := range f.chunks {
		if onChunk != nil {
			f.gotCallback = true
			onChunk(c)
		}
	}
	return f.emittedReply, nil
}

func TestAgent_TurnStream_HappyPath(t *testing.T) {
	send := &fakeStreamSender{
		chunks:       []string{"hi ", "there"},
		emittedReply: Reply{Content: "hi there", Model: "m", StopReason: "end_turn"},
	}
	a := New(send, "m")
	a.System = "you are octo"

	var got []string
	reply, err := a.TurnStream(context.Background(), "hello", func(d string) {
		got = append(got, d)
	})
	if err != nil {
		t.Fatalf("TurnStream: %v", err)
	}
	if reply.Content != "hi there" {
		t.Errorf("reply.Content = %q", reply.Content)
	}
	if got[0] != "hi " || got[1] != "there" {
		t.Errorf("chunks = %v", got)
	}
	if !send.gotCallback {
		t.Errorf("StreamMessages received nil callback")
	}

	// History: user + assistant, same as the buffered path.
	snap := a.History.Snapshot()
	if len(snap) != 2 || snap[0].Role != RoleUser || snap[1].Role != RoleAssistant {
		t.Errorf("History = %+v", snap)
	}
}

func TestAgent_TurnStream_NilCallback(t *testing.T) {
	send := &fakeStreamSender{
		chunks:       []string{"a", "b"},
		emittedReply: Reply{Content: "ab"},
	}
	a := New(send, "m")

	reply, err := a.TurnStream(context.Background(), "hi", nil)
	if err != nil {
		t.Fatalf("TurnStream: %v", err)
	}
	if reply.Content != "ab" {
		t.Errorf("reply.Content = %q", reply.Content)
	}
}

func TestAgent_TurnStream_BufferedFallback(t *testing.T) {
	// A plain Sender (no StreamingSender) should still work — TurnStream
	// must fall back to SendMessages and synthesise a single onChunk call.
	send := &fakeSender{reply: Reply{Content: "buffered reply"}}
	a := New(send, "m")

	var got []string
	reply, err := a.TurnStream(context.Background(), "hi", func(d string) {
		got = append(got, d)
	})
	if err != nil {
		t.Fatalf("TurnStream: %v", err)
	}
	if reply.Content != "buffered reply" {
		t.Errorf("reply.Content = %q", reply.Content)
	}
	if len(got) != 1 || got[0] != "buffered reply" {
		t.Errorf("fallback should emit one chunk with the full content, got %v", got)
	}
}

func TestAgent_TurnStream_Error_RestoresHistory(t *testing.T) {
	send := &fakeStreamSender{fakeSender: fakeSender{err: errors.New("boom")}}
	a := New(send, "m")

	if _, err := a.TurnStream(context.Background(), "hi", nil); err == nil {
		t.Fatal("expected error")
	}
	if n := a.History.Len(); n != 0 {
		t.Errorf("History.Len = %d after failed TurnStream, want 0", n)
	}
}

// ─── Run() agentic loop tests ─────────────────────────────────────────────

// fakeToolSender implements both Sender and ToolSender. On each call it pops
// the next reply from the replies slice so callers can script multi-turn
// sequences.
type fakeToolSender struct {
	replies []Reply
	calls   int
}

func (f *fakeToolSender) SendMessages(_ context.Context, _, _ string, _ []Message, _ int) (Reply, error) {
	return f.nextReply()
}

func (f *fakeToolSender) SendMessagesWithTools(_ context.Context, _, _ string, _ []Message, _ int, _ []ToolDefinition) (Reply, error) {
	return f.nextReply()
}

func (f *fakeToolSender) nextReply() (Reply, error) {
	if f.calls >= len(f.replies) {
		return Reply{}, fmt.Errorf("fakeToolSender: no more replies (call %d)", f.calls)
	}
	r := f.replies[f.calls]
	f.calls++
	return r, nil
}

// fakeExecutor implements ToolExecutor, returning canned results keyed by name.
type fakeExecutor struct {
	results map[string]string
	called  []string
}

func (f *fakeExecutor) Execute(_ context.Context, name string, _ map[string]any) (string, error) {
	f.called = append(f.called, name)
	if f.results != nil {
		if v, ok := f.results[name]; ok {
			return v, nil
		}
	}
	return "ok", nil
}

func TestAgent_Run_NoTools_FallsBackToTurn(t *testing.T) {
	send := &fakeToolSender{
		replies: []Reply{{Content: "plain reply", StopReason: "end_turn"}},
	}
	a := New(send, "m")

	reply, err := a.Run(context.Background(), "hello", nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reply.Content != "plain reply" {
		t.Errorf("Content = %q", reply.Content)
	}
}

func TestAgent_Run_ToolUse_ThenEndTurn(t *testing.T) {
	// Round 1: model returns tool_use
	// Round 2: model returns end_turn with final answer
	send := &fakeToolSender{
		replies: []Reply{
			{
				StopReason: "tool_use",
				Blocks: []ContentBlock{
					NewToolUseBlock("call-1", "bash", map[string]any{"command": "echo hi"}),
				},
			},
			{
				Content:    "The output was: hi",
				StopReason: "end_turn",
			},
		},
	}
	exec := &fakeExecutor{results: map[string]string{"bash": "hi"}}

	a := New(send, "m")
	defs := []ToolDefinition{{Name: "bash", Description: "run shell"}}

	reply, err := a.Run(context.Background(), "run echo hi", defs, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reply.Content != "The output was: hi" {
		t.Errorf("Content = %q", reply.Content)
	}
	if reply.StopReason != "end_turn" {
		t.Errorf("StopReason = %q", reply.StopReason)
	}

	// Executor was called once with "bash".
	if len(exec.called) != 1 || exec.called[0] != "bash" {
		t.Errorf("executor called = %v", exec.called)
	}

	// History should be: user, assistant(tool_use), user(tool_result), assistant(final)
	snap := a.History.Snapshot()
	if len(snap) != 4 {
		t.Fatalf("History len = %d, want 4 (%v)", len(snap), snap)
	}
	if snap[0].Role != RoleUser {
		t.Errorf("snap[0] role = %q", snap[0].Role)
	}
	if snap[1].Role != RoleAssistant || len(snap[1].Blocks) == 0 {
		t.Errorf("snap[1] should be assistant with blocks: %+v", snap[1])
	}
	if snap[2].Role != RoleUser || len(snap[2].Blocks) == 0 {
		t.Errorf("snap[2] should be user with tool results: %+v", snap[2])
	}
	if snap[3].Role != RoleAssistant || snap[3].Content != "The output was: hi" {
		t.Errorf("snap[3] = %+v", snap[3])
	}
}

func TestAgent_Run_MultipleToolCalls(t *testing.T) {
	// Round 1: two tool calls in one response
	// Round 2: end_turn
	send := &fakeToolSender{
		replies: []Reply{
			{
				StopReason: "tool_use",
				Blocks: []ContentBlock{
					NewToolUseBlock("c1", "bash", map[string]any{"command": "echo a"}),
					NewToolUseBlock("c2", "bash", map[string]any{"command": "echo b"}),
				},
			},
			{Content: "done", StopReason: "end_turn"},
		},
	}
	exec := &fakeExecutor{}
	a := New(send, "m")
	defs := []ToolDefinition{{Name: "bash"}}

	_, err := a.Run(context.Background(), "go", defs, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Both tool calls executed.
	if len(exec.called) != 2 {
		t.Errorf("executor called %d times, want 2", len(exec.called))
	}
}

func TestAgent_Run_ExceedsMaxIterations(t *testing.T) {
	// Every reply is tool_use — loop should hit the cap.
	replies := make([]Reply, maxToolIterations+5)
	for i := range replies {
		replies[i] = Reply{
			StopReason: "tool_use",
			Blocks:     []ContentBlock{NewToolUseBlock("c", "bash", nil)},
		}
	}
	send := &fakeToolSender{replies: replies}
	exec := &fakeExecutor{}
	a := New(send, "m")
	defs := []ToolDefinition{{Name: "bash"}}

	_, err := a.Run(context.Background(), "go", defs, exec)
	if err == nil {
		t.Fatal("expected error when max iterations exceeded")
	}
}

func TestAgent_Run_SenderWithoutToolSupport_FallsBackToTurn(t *testing.T) {
	// A plain fakeSender (no ToolSender) should just do a single Turn.
	send := &fakeSender{reply: Reply{Content: "plain", StopReason: "end_turn"}}
	a := New(send, "m")
	defs := []ToolDefinition{{Name: "bash"}}
	exec := &fakeExecutor{}

	reply, err := a.Run(context.Background(), "hi", defs, exec)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if reply.Content != "plain" {
		t.Errorf("Content = %q", reply.Content)
	}
	// Executor should NOT have been called.
	if len(exec.called) != 0 {
		t.Errorf("executor should not be called when sender has no ToolSender support")
	}
}
