package agent

import (
	"context"
	"errors"
	"testing"
)

func TestHasToolUse(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want bool
	}{
		{
			name: "plain text message",
			msg:  NewAssistantMessage("hello"),
			want: false,
		},
		{
			name: "message with tool_use block",
			msg:  NewToolUseMessage([]ContentBlock{NewToolUseBlock("c1", "read_file", nil)}),
			want: true,
		},
		{
			name: "message with mixed blocks",
			msg: Message{
				Role: RoleAssistant,
				Blocks: []ContentBlock{
					NewTextBlock("thinking..."),
					NewToolUseBlock("c1", "read_file", nil),
				},
			},
			want: true,
		},
		{
			name: "tool_result message",
			msg:  NewToolResultMessage([]ContentBlock{NewToolResultBlock("c1", "result", false)}),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasToolUse(tt.msg); got != tt.want {
				t.Errorf("hasToolUse() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSynthesizeInterruptedToolResults(t *testing.T) {
	blocks := []ContentBlock{
		NewTextBlock("Let me read that file"),
		NewToolUseBlock("c1", "read_file", map[string]any{"path": "foo.go"}),
		NewToolUseBlock("c2", "grep", map[string]any{"pattern": "bar"}),
	}

	results := synthesizeInterruptedToolResults(blocks)

	if len(results) != 2 {
		t.Fatalf("synthesizeInterruptedToolResults() returned %d results, want 2", len(results))
	}
	for i, r := range results {
		if r.Type != "tool_result" {
			t.Errorf("result[%d].Type = %q, want tool_result", i, r.Type)
		}
		if !r.IsError {
			t.Errorf("result[%d].IsError = false, want true", i)
		}
		if r.Result != interruptNote {
			t.Errorf("result[%d].Result = %q, want %q", i, r.Result, interruptNote)
		}
	}
	if results[0].ToolUseID != "c1" {
		t.Errorf("results[0].ToolUseID = %q, want c1", results[0].ToolUseID)
	}
	if results[1].ToolUseID != "c2" {
		t.Errorf("results[1].ToolUseID = %q, want c2", results[1].ToolUseID)
	}
}

func TestEnsureToolPairing(t *testing.T) {
	t.Run("no orphaned tool_use", func(t *testing.T) {
		a := New(&summarizeFake{}, "m")
		a.History.Append(NewUserMessage("read it"))
		a.History.Append(NewToolUseMessage([]ContentBlock{NewToolUseBlock("c1", "read_file", nil)}))
		a.History.Append(NewToolResultMessage([]ContentBlock{NewToolResultBlock("c1", "content", false)}))
		a.History.Append(NewAssistantMessage("done"))

		a.ensureToolPairing()

		// History should be unchanged
		if a.History.Len() != 4 {
			t.Errorf("history len = %d, want 4", a.History.Len())
		}
	})

	t.Run("orphaned tool_use gets synthesized result", func(t *testing.T) {
		a := New(&summarizeFake{}, "m")
		a.History.Append(NewUserMessage("read it"))
		a.History.Append(NewToolUseMessage([]ContentBlock{NewToolUseBlock("c1", "read_file", nil)}))
		// Missing tool_result!

		a.ensureToolPairing()

		if a.History.Len() != 3 {
			t.Fatalf("history len = %d, want 3", a.History.Len())
		}
		msgs := a.History.Snapshot()
		last := msgs[2]
		if last.Role != RoleUser {
			t.Fatalf("last message role = %q, want user", last.Role)
		}
		if len(last.Blocks) != 1 {
			t.Fatalf("last message blocks = %d, want 1", len(last.Blocks))
		}
		if last.Blocks[0].Type != "tool_result" {
			t.Errorf("last block type = %q, want tool_result", last.Blocks[0].Type)
		}
		if last.Blocks[0].ToolUseID != "c1" {
			t.Errorf("last block ToolUseID = %q, want c1", last.Blocks[0].ToolUseID)
		}
		if !last.Blocks[0].IsError {
			t.Error("last block IsError = false, want true")
		}
	})

	t.Run("multiple orphaned tool_use blocks", func(t *testing.T) {
		a := New(&summarizeFake{}, "m")
		a.History.Append(NewUserMessage("read files"))
		a.History.Append(NewToolUseMessage([]ContentBlock{
			NewToolUseBlock("c1", "read_file", nil),
			NewToolUseBlock("c2", "read_file", nil),
		}))
		// Only one tool_result
		a.History.Append(NewToolResultMessage([]ContentBlock{NewToolResultBlock("c1", "content", false)}))

		a.ensureToolPairing()

		if a.History.Len() != 4 {
			t.Fatalf("history len = %d, want 4", a.History.Len())
		}
		msgs := a.History.Snapshot()
		last := msgs[3]
		if len(last.Blocks) != 1 {
			t.Fatalf("last message blocks = %d, want 1 (only c2 orphaned)", len(last.Blocks))
		}
		if last.Blocks[0].ToolUseID != "c2" {
			t.Errorf("last block ToolUseID = %q, want c2", last.Blocks[0].ToolUseID)
		}
	})

	t.Run("orphaned tool_use with trailing user message gets merged", func(t *testing.T) {
		// This is the critical bug fix: when inbox drain adds a user message
		// after an orphaned tool_use, the synthetic tool_result must be
		// MERGED into that user message to preserve the API's tool_use/
		// tool_result pairing requirement.
		a := New(&summarizeFake{}, "m")
		a.History.Append(NewUserMessage("read it"))
		a.History.Append(NewToolUseMessage([]ContentBlock{NewToolUseBlock("c1", "read_file", nil)}))
		// Simulate inbox drain adding a steer message
		a.History.Append(NewUserMessage("also handle errors"))
		// Missing tool_result for c1!

		a.ensureToolPairing()

		// History should NOT grow - the last message should be REPLACED (merged)
		if a.History.Len() != 3 {
			t.Fatalf("history len = %d, want 3 (merged, not appended)", a.History.Len())
		}
		msgs := a.History.Snapshot()
		last := msgs[2]
		if last.Role != RoleUser {
			t.Fatalf("last message role = %q, want user", last.Role)
		}
		// Should have: tool_result block + text block (merged)
		if len(last.Blocks) != 2 {
			t.Fatalf("last message blocks = %d, want 2 (tool_result + text)", len(last.Blocks))
		}
		// First block should be the synthetic tool_result
		if last.Blocks[0].Type != "tool_result" {
			t.Errorf("last.Blocks[0].Type = %q, want tool_result", last.Blocks[0].Type)
		}
		if last.Blocks[0].ToolUseID != "c1" {
			t.Errorf("last.Blocks[0].ToolUseID = %q, want c1", last.Blocks[0].ToolUseID)
		}
		if !last.Blocks[0].IsError {
			t.Error("last.Blocks[0].IsError = false, want true")
		}
		// Second block should be the original steer text
		if last.Blocks[1].Type != "text" {
			t.Errorf("last.Blocks[1].Type = %q, want text", last.Blocks[1].Type)
		}
		if last.Blocks[1].Text != "also handle errors" {
			t.Errorf("last.Blocks[1].Text = %q, want %q", last.Blocks[1].Text, "also handle errors")
		}
	})
}

func TestFinishInterrupted_OrphanedToolUse(t *testing.T) {
	a := New(&summarizeFake{}, "m")
	a.History.Append(NewUserMessage("read it"))
	// Assistant wants to use a tool, but dispatchTools never ran
	a.History.Append(NewToolUseMessage([]ContentBlock{NewToolUseBlock("c1", "read_file", nil)}))

	_, err := a.finishInterrupted(nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}

	msgs := a.History.Snapshot()
	// Should have: user, assistant(tool_use), user(tool_result), assistant(interrupt note)
	if len(msgs) != 4 {
		t.Fatalf("history len = %d, want 4", len(msgs))
	}
	// Check synthesized tool_result
	if msgs[2].Role != RoleUser {
		t.Fatalf("msgs[2].Role = %q, want user", msgs[2].Role)
	}
	if len(msgs[2].Blocks) != 1 || msgs[2].Blocks[0].Type != "tool_result" {
		t.Fatalf("msgs[2] should have tool_result block")
	}
	if !msgs[2].Blocks[0].IsError {
		t.Error("synthesized tool_result should be is_error=true")
	}
	// Check interrupt note
	if msgs[3].Role != RoleAssistant || msgs[3].Content != interruptNote {
		t.Errorf("msgs[3] should be assistant interrupt note")
	}
}
