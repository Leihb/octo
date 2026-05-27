package agent

// ContentBlock is a single element of a multi-part message. It unifies the
// three roles a block can play in an LLM conversation:
//
//   - "text"        — plain assistant or user text
//   - "tool_use"    — the model requesting a tool call (assistant turn)
//   - "tool_result" — the result of a tool call (user turn)
//
// The zero value is not valid; use the New*Block helpers instead.
type ContentBlock struct {
	// Type distinguishes the block variant: "text", "tool_use", "tool_result".
	Type string `json:"type"`

	// Text is the text payload (type=="text").
	Text string `json:"text,omitempty"`

	// ID is the unique call identifier supplied by the model (type=="tool_use").
	// ToolUseID on a tool_result block must match the ID of the corresponding
	// tool_use block.
	ID string `json:"id,omitempty"`

	// Name is the tool name the model wants to invoke (type=="tool_use").
	Name string `json:"name,omitempty"`

	// Input is the parsed argument map the model passes to the tool
	// (type=="tool_use"). Keys and value types are defined by the tool's
	// JSON Schema Parameters.
	Input map[string]any `json:"input,omitempty"`

	// ToolUseID links this result back to its originating tool_use block
	// (type=="tool_result"). Must equal the ID field of the paired block.
	ToolUseID string `json:"tool_use_id,omitempty"`

	// Result is the textual output of the tool execution (type=="tool_result").
	Result string `json:"result,omitempty"`

	// IsError signals that the tool execution failed (type=="tool_result").
	// The LLM can inspect Result for the error message and recover gracefully.
	IsError bool `json:"is_error,omitempty"`

	// Reasoning carries a thinking model's reasoning trace that must be echoed
	// back on the next request (type=="tool_use"). OpenAI thinking models such
	// as deepseek-v4 return reasoning_content alongside a tool call and reject
	// the follow-up unless it's resent; the OpenAI adapter stashes it here so it
	// round-trips through history. Providers that don't need it ignore the field.
	Reasoning string `json:"reasoning,omitempty"`
}

// NewTextBlock creates a ContentBlock with Type=="text".
func NewTextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

// NewToolUseBlock creates a ContentBlock with Type=="tool_use".
// id must be unique within the conversation turn (supplied by the LLM).
func NewToolUseBlock(id, name string, input map[string]any) ContentBlock {
	return ContentBlock{
		Type:  "tool_use",
		ID:    id,
		Name:  name,
		Input: input,
	}
}

// NewToolResultBlock creates a ContentBlock with Type=="tool_result".
// toolUseID must match the ID of the corresponding tool_use block.
// isError should be true when the tool execution failed; result carries the
// error message in that case.
func NewToolResultBlock(toolUseID, result string, isError bool) ContentBlock {
	return ContentBlock{
		Type:      "tool_result",
		ToolUseID: toolUseID,
		Result:    result,
		IsError:   isError,
	}
}
