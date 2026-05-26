package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/provider"
)

// TestSend_ToolDefinitions_WireFormat verifies that ToolDefinition slices are
// translated into Anthropic's `tools` wire format (using "input_schema" rather
// than "parameters").
func TestSend_ToolDefinitions_WireFormat(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		// Return a minimal valid response so the client doesn't error.
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"x","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	c, _ := New("k")
	c.BaseURL = srv.URL

	_, err := c.Send(context.Background(), provider.Request{
		Model:    "x",
		Messages: []agent.Message{agent.NewUserMessage("hi")},
		Tools: []agent.ToolDefinition{
			{
				Name:        "bash",
				Description: "Run shell command",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{"type": "string"},
					},
					"required": []string{"command"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var wireReq struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"input_schema"`
			Parameters  map[string]any `json:"parameters"` // should NOT be present
		} `json:"tools"`
	}
	if err := json.Unmarshal(capturedBody, &wireReq); err != nil {
		t.Fatalf("decode wire body: %v", err)
	}
	if len(wireReq.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(wireReq.Tools))
	}
	tool := wireReq.Tools[0]
	if tool.Name != "bash" {
		t.Errorf("tool.name = %q, want bash", tool.Name)
	}
	if tool.InputSchema == nil {
		t.Error("tool.input_schema should be set")
	}
	if tool.Parameters != nil {
		t.Error("tool.parameters should NOT be present (Anthropic uses input_schema)")
	}
}

// TestSend_ToolUse_Response verifies that tool_use content blocks in the
// response are converted to agent.ContentBlock correctly.
func TestSend_ToolUse_Response(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"id": "msg_01",
			"type": "message",
			"role": "assistant",
			"model": "x",
			"content": [
				{"type":"text","text":"Let me run that."},
				{"type":"tool_use","id":"call-1","name":"bash","input":{"command":"echo hi"}}
			],
			"stop_reason": "tool_use",
			"usage": {"input_tokens":10,"output_tokens":20}
		}`))
	}))
	defer srv.Close()

	c, _ := New("k")
	c.BaseURL = srv.URL

	resp, err := c.Send(context.Background(), provider.Request{
		Model:    "x",
		Messages: []agent.Message{agent.NewUserMessage("run echo hi")},
		Tools:    []agent.ToolDefinition{{Name: "bash"}},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	if resp.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
	}
	if resp.Content != "Let me run that." {
		t.Errorf("Content = %q", resp.Content)
	}
	if len(resp.Blocks) != 2 {
		t.Fatalf("Blocks len = %d, want 2", len(resp.Blocks))
	}
	textBlock := resp.Blocks[0]
	if textBlock.Type != "text" || textBlock.Text != "Let me run that." {
		t.Errorf("Blocks[0] = %+v", textBlock)
	}
	toolBlock := resp.Blocks[1]
	if toolBlock.Type != "tool_use" {
		t.Errorf("Blocks[1].Type = %q, want tool_use", toolBlock.Type)
	}
	if toolBlock.ID != "call-1" || toolBlock.Name != "bash" {
		t.Errorf("Blocks[1] = %+v", toolBlock)
	}
	if toolBlock.Input["command"] != "echo hi" {
		t.Errorf("Blocks[1].Input = %v", toolBlock.Input)
	}
}

// TestSend_ToolResultMessage verifies that an agent message containing
// tool_result blocks is serialized correctly (Anthropic format: role=user,
// content=[{type:tool_result, ...}]).
func TestSend_ToolResultMessage(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"x","content":[{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	c, _ := New("k")
	c.BaseURL = srv.URL

	msgs := []agent.Message{
		agent.NewUserMessage("run echo hi"),
		agent.NewToolUseMessage([]agent.ContentBlock{
			agent.NewToolUseBlock("call-1", "bash", map[string]any{"command": "echo hi"}),
		}),
		agent.NewToolResultMessage([]agent.ContentBlock{
			agent.NewToolResultBlock("call-1", "hi", false),
		}),
	}

	_, err := c.Send(context.Background(), provider.Request{
		Model:    "x",
		Messages: msgs,
		Tools:    []agent.ToolDefinition{{Name: "bash"}},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	var wireReq struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(capturedBody, &wireReq); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// Should be 3 messages: user text, assistant(tool_use), user(tool_result)
	if len(wireReq.Messages) != 3 {
		t.Fatalf("messages len = %d, want 3", len(wireReq.Messages))
	}

	// Message 1: user text (plain string)
	if wireReq.Messages[0].Role != "user" {
		t.Errorf("messages[0].role = %q", wireReq.Messages[0].Role)
	}

	// Message 2: assistant with tool_use blocks
	if wireReq.Messages[1].Role != "assistant" {
		t.Errorf("messages[1].role = %q", wireReq.Messages[1].Role)
	}
	var assistantContent []map[string]any
	if err := json.Unmarshal(wireReq.Messages[1].Content, &assistantContent); err != nil {
		t.Fatalf("decode assistant content: %v", err)
	}
	if len(assistantContent) != 1 || assistantContent[0]["type"] != "tool_use" {
		t.Errorf("assistant content = %v", assistantContent)
	}

	// Message 3: user with tool_result blocks
	if wireReq.Messages[2].Role != "user" {
		t.Errorf("messages[2].role = %q", wireReq.Messages[2].Role)
	}
	var userContent []map[string]any
	if err := json.Unmarshal(wireReq.Messages[2].Content, &userContent); err != nil {
		t.Fatalf("decode user content: %v", err)
	}
	if len(userContent) != 1 || userContent[0]["type"] != "tool_result" {
		t.Errorf("user content = %v", userContent)
	}
	if userContent[0]["tool_use_id"] != "call-1" {
		t.Errorf("tool_use_id = %v", userContent[0]["tool_use_id"])
	}
}

// TestSendStream_ToolUse verifies that tool_use blocks emitted during a stream
// are accumulated and returned in resp.Blocks, and that input_json_delta
// fragments are correctly assembled into the final Input map.
func TestSendStream_ToolUse(t *testing.T) {
	// Anthropic stream with one text block and one tool_use block.
	toolStream := "" +
		`data: {"type":"message_start","message":{"id":"m","model":"x","usage":{"input_tokens":10,"output_tokens":0}}}` + "\n\n" +
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","id":"","name":""}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Thinking..."}}` + "\n\n" +
		`data: {"type":"content_block_stop","index":0}` + "\n\n" +
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call-99","name":"bash"}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"command\":"}}` + "\n\n" +
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"echo test\"}"}}` + "\n\n" +
		`data: {"type":"content_block_stop","index":1}` + "\n\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"input_tokens":0,"output_tokens":15}}` + "\n\n" +
		`data: {"type":"message_stop"}` + "\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, toolStream)
	}))
	defer srv.Close()

	c, _ := New("k")
	c.BaseURL = srv.URL

	var textChunks []string
	resp, err := c.SendStream(context.Background(), provider.Request{
		Model:    "x",
		Messages: []agent.Message{agent.NewUserMessage("run echo test")},
		Tools:    []agent.ToolDefinition{{Name: "bash"}},
	}, func(d string) { textChunks = append(textChunks, d) })
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}

	if resp.StopReason != "tool_use" {
		t.Errorf("StopReason = %q, want tool_use", resp.StopReason)
	}
	if resp.Content != "Thinking..." {
		t.Errorf("Content = %q", resp.Content)
	}
	if len(textChunks) != 1 || textChunks[0] != "Thinking..." {
		t.Errorf("textChunks = %v", textChunks)
	}
	if len(resp.Blocks) != 2 {
		t.Fatalf("Blocks len = %d, want 2", len(resp.Blocks))
	}
	if resp.Blocks[0].Type != "text" || resp.Blocks[0].Text != "Thinking..." {
		t.Errorf("Blocks[0] = %+v", resp.Blocks[0])
	}
	toolBlock := resp.Blocks[1]
	if toolBlock.Type != "tool_use" || toolBlock.ID != "call-99" || toolBlock.Name != "bash" {
		t.Errorf("Blocks[1] = %+v", toolBlock)
	}
	if toolBlock.Input["command"] != "echo test" {
		t.Errorf("Blocks[1].Input = %v", toolBlock.Input)
	}
}

// Ensure tools field is absent from wire request when no tools are specified.
func TestSend_NoTools_FieldAbsent(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"id":"m","type":"message","role":"assistant","model":"x","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer srv.Close()

	c, _ := New("k")
	c.BaseURL = srv.URL

	_, err := c.Send(context.Background(), provider.Request{
		Model:    "x",
		Messages: []agent.Message{agent.NewUserMessage("hi")},
	})
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(capturedBody), `"tools"`) {
		t.Errorf("tools field should be absent when no tools: %s", capturedBody)
	}
}
