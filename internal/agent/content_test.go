package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNewTextBlock(t *testing.T) {
	b := NewTextBlock("hello world")
	if b.Type != "text" {
		t.Errorf("Type = %q, want %q", b.Type, "text")
	}
	if b.Text != "hello world" {
		t.Errorf("Text = %q, want %q", b.Text, "hello world")
	}
	// other fields must be zero
	if b.ID != "" || b.Name != "" || b.ToolUseID != "" || b.Result != "" || b.IsError {
		t.Errorf("unexpected non-zero fields in text block: %+v", b)
	}
}

func TestNewToolUseBlock(t *testing.T) {
	input := map[string]any{"command": "echo hi"}
	b := NewToolUseBlock("call-1", "bash", input)
	if b.Type != "tool_use" {
		t.Errorf("Type = %q, want %q", b.Type, "tool_use")
	}
	if b.ID != "call-1" {
		t.Errorf("ID = %q, want %q", b.ID, "call-1")
	}
	if b.Name != "bash" {
		t.Errorf("Name = %q, want %q", b.Name, "bash")
	}
	if b.Input["command"] != "echo hi" {
		t.Errorf("Input = %v", b.Input)
	}
}

func TestNewToolResultBlock(t *testing.T) {
	b := NewToolResultBlock("call-1", "hi\n", false)
	if b.Type != "tool_result" {
		t.Errorf("Type = %q, want %q", b.Type, "tool_result")
	}
	if b.ToolUseID != "call-1" {
		t.Errorf("ToolUseID = %q, want %q", b.ToolUseID, "call-1")
	}
	if b.Result != "hi\n" {
		t.Errorf("Result = %q", b.Result)
	}
	if b.IsError {
		t.Error("IsError should be false")
	}
}

func TestNewToolResultBlock_Error(t *testing.T) {
	b := NewToolResultBlock("call-1", "command not found", true)
	if !b.IsError {
		t.Error("IsError should be true")
	}
	if b.Result != "command not found" {
		t.Errorf("Result = %q", b.Result)
	}
}

func TestContentBlock_JSONRoundtrip(t *testing.T) {
	blocks := []ContentBlock{
		NewTextBlock("some text"),
		NewToolUseBlock("id-1", "bash", map[string]any{"command": "ls"}),
		NewToolResultBlock("id-1", "file1.go\nfile2.go", false),
	}

	data, err := json.Marshal(blocks)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded []ContentBlock
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(decoded) != 3 {
		t.Fatalf("len = %d, want 3", len(decoded))
	}
	if decoded[0].Type != "text" || decoded[0].Text != "some text" {
		t.Errorf("block[0] = %+v", decoded[0])
	}
	if decoded[1].Type != "tool_use" || decoded[1].ID != "id-1" || decoded[1].Name != "bash" {
		t.Errorf("block[1] = %+v", decoded[1])
	}
	if decoded[2].Type != "tool_result" || decoded[2].ToolUseID != "id-1" || decoded[2].Result != "file1.go\nfile2.go" {
		t.Errorf("block[2] = %+v", decoded[2])
	}
}

func TestContentBlock_OmitEmptyFields(t *testing.T) {
	b := NewTextBlock("hi")
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	// id, name, tool_use_id, result, input, is_error should not appear
	for _, field := range []string{`"id"`, `"name"`, `"tool_use_id"`, `"result"`, `"input"`, `"is_error"`} {
		if strings.Contains(s, field) {
			t.Errorf("field %s should be omitted but found in: %s", field, s)
		}
	}
}
