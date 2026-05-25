// Package anthropic implements the provider.Provider interface against
// Anthropic's native Messages API (POST /v1/messages).
//
// API reference: https://docs.anthropic.com/en/api/messages
package anthropic

// apiRequest is the wire-level JSON body of POST /v1/messages.
//
// Tool definitions and request metadata arrive in later milestones.
type apiRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    string       `json:"system,omitempty"`
	Messages  []apiMessage `json:"messages"`
	Stream    bool         `json:"stream,omitempty"`
}

// apiMessage is one element of apiRequest.Messages.
//
// Anthropic accepts either a plain string Content or a slice of content blocks
// (text, image, tool_use, tool_result). M1.2 always sends strings; the
// adapter converts agent.Message → apiMessage one-to-one.
type apiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// apiResponse is the wire-level JSON body of a successful 200 response.
type apiResponse struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Role       string            `json:"role"`
	Model      string            `json:"model"`
	Content    []apiContentBlock `json:"content"`
	StopReason string            `json:"stop_reason"`
	Usage      apiUsageBlock     `json:"usage"`
}

// apiContentBlock is a single element of apiResponse.Content. M1.2 only
// reads text blocks; tool_use blocks are returned in later milestones once
// the tool layer can dispatch them.
type apiContentBlock struct {
	Type string `json:"type"`           // "text" or "tool_use" in M2+
	Text string `json:"text,omitempty"` // populated when Type == "text"
}

// apiUsageBlock is the token-count block Anthropic returns on every message.
type apiUsageBlock struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// apiError is the body of an Anthropic error response (4xx/5xx).
type apiError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// streamEvent is the per-event payload Anthropic sends on the SSE stream.
// Reference: https://docs.anthropic.com/en/api/messages-streaming
//
// The union covers message_start, content_block_start, content_block_delta,
// content_block_stop, message_delta, message_stop, ping, and error events.
// We only act on a subset; the rest is ignored.
type streamEvent struct {
	Type    string         `json:"type"`
	Index   int            `json:"index,omitempty"`   // content_block_*
	Message *streamMessage `json:"message,omitempty"` // message_start
	Delta   *streamDelta   `json:"delta,omitempty"`   // content_block_delta, message_delta
	Usage   *apiUsageBlock `json:"usage,omitempty"`   // message_delta
}

// streamMessage is the abridged message snapshot inside a message_start event.
// Only fields we currently read are kept; the rest of the JSON is allowed
// to flow past untouched.
type streamMessage struct {
	ID    string        `json:"id,omitempty"`
	Model string        `json:"model,omitempty"`
	Usage apiUsageBlock `json:"usage"`
}

// streamDelta is the per-delta payload inside content_block_delta and
// message_delta events.
//   - content_block_delta: Type=="text_delta", Text holds the new bytes
//   - message_delta:        StopReason set when the model stops
type streamDelta struct {
	Type       string `json:"type,omitempty"`
	Text       string `json:"text,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
}
