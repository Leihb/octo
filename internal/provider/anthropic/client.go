package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Leihb/octo/internal/agent"
	"github.com/Leihb/octo/internal/provider"
)

// DefaultBaseURL is Anthropic's API base. The actual Messages endpoint is
// BaseURL + "/v1/messages". Override Client.BaseURL when pointing at an
// Anthropic-compatible third-party (DeepSeek, Kimi, OpenRouter's Anthropic
// shim, …) — for example DeepSeek exposes the protocol at
// `https://api.deepseek.com/anthropic`.
const DefaultBaseURL = "https://api.anthropic.com"

// MessagesPath is the path appended to BaseURL for every send.
const MessagesPath = "/v1/messages"

// DefaultAPIVersion is the value sent as the `anthropic-version` header.
// Pinned to the GA version used by the Messages API since 2023-06-01; bump
// only when a new feature requires it.
const DefaultAPIVersion = "2023-06-01"

// DefaultMaxTokens caps response length when a caller doesn't specify one.
// 4096 is generous for chat-style replies and well below the model ceilings.
const DefaultMaxTokens = 4096

// Client talks to an Anthropic-compatible Messages API. Construct via New();
// zero values are not valid because APIKey is required.
//
// BaseURL is the host + protocol-prefix only (no /v1/messages suffix); the
// client appends MessagesPath itself. This makes pointing at compatible
// endpoints painless — set `BaseURL = "https://api.deepseek.com/anthropic"`
// and the rest works unchanged.
type Client struct {
	APIKey     string
	BaseURL    string       // optional override; defaults to DefaultBaseURL
	APIVersion string       // optional override; defaults to DefaultAPIVersion
	HTTPClient *http.Client // optional; defaults to http.Client with a 60s timeout
}

// New constructs a Client with the given API key and the standard defaults.
// Returns an error when apiKey is empty so misconfiguration is caught at
// startup rather than at the first request.
func New(apiKey string) (*Client, error) {
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("anthropic: API key is required")
	}
	return &Client{
		APIKey:     apiKey,
		BaseURL:    DefaultBaseURL,
		APIVersion: DefaultAPIVersion,
		HTTPClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// Name implements provider.Provider.
func (c *Client) Name() string { return "anthropic-messages" }

// Send implements provider.Provider against Anthropic's Messages API.
//
// Non-2xx responses are decoded as apiError and wrapped into a descriptive
// error containing the HTTP status and the upstream error message.
func (c *Client) Send(ctx context.Context, req provider.Request) (provider.Response, error) {
	if req.Model == "" {
		return provider.Response{}, errors.New("anthropic: req.Model is required")
	}
	if len(req.Messages) == 0 {
		return provider.Response{}, errors.New("anthropic: at least one message is required")
	}

	msgs, err := toAPIMessages(req.Messages)
	if err != nil {
		return provider.Response{}, fmt.Errorf("anthropic: serialize messages: %w", err)
	}

	body := apiRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.SystemPrompt,
		Messages:  msgs,
		Tools:     toAPITools(req.Tools),
	}
	if body.MaxTokens <= 0 {
		body.MaxTokens = DefaultMaxTokens
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return provider.Response{}, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL(), bytes.NewReader(payload))
	if err != nil {
		return provider.Response{}, fmt.Errorf("anthropic: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.APIKey)
	apiVer := c.APIVersion
	if apiVer == "" {
		apiVer = DefaultAPIVersion
	}
	httpReq.Header.Set("anthropic-version", apiVer)

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return provider.Response{}, fmt.Errorf("anthropic: send: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return provider.Response{}, fmt.Errorf("anthropic: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr apiError
		if jerr := json.Unmarshal(respBody, &apiErr); jerr == nil && apiErr.Error.Message != "" {
			return provider.Response{}, fmt.Errorf(
				"anthropic: HTTP %d (%s): %s",
				resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message,
			)
		}
		return provider.Response{}, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResp apiResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return provider.Response{}, fmt.Errorf("anthropic: decode response: %w", err)
	}

	blocks := fromAPIContentBlocks(apiResp.Content)
	return provider.Response{
		Content:      joinTextBlocks(apiResp.Content),
		Blocks:       blocks,
		Model:        apiResp.Model,
		StopReason:   apiResp.StopReason,
		InputTokens:  apiResp.Usage.InputTokens,
		OutputTokens: apiResp.Usage.OutputTokens,
	}, nil
}

// endpointURL returns BaseURL + MessagesPath, applying defaults and trimming
// any trailing slash on BaseURL so the join is exactly one slash.
func (c *Client) endpointURL() string {
	base := c.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	return strings.TrimRight(base, "/") + MessagesPath
}

// toAPITools converts []agent.ToolDefinition to []apiTool.
// Anthropic uses "input_schema" where the agent layer uses "parameters".
func toAPITools(defs []agent.ToolDefinition) []apiTool {
	if len(defs) == 0 {
		return nil
	}
	out := make([]apiTool, len(defs))
	for i, d := range defs {
		out[i] = apiTool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.Parameters,
		}
	}
	return out
}

// toAPIMessages converts agent.Message slice into the Anthropic wire format.
// The system role is filtered out here because Anthropic carries it as a
// top-level Request.SystemPrompt — sending role:"system" inside the
// messages array would 400.
//
// Messages with Blocks are serialized as a []apiContentBlock JSON array;
// plain-text messages are serialized as a JSON string for compatibility.
func toAPIMessages(in []agent.Message) ([]apiMessage, error) {
	out := make([]apiMessage, 0, len(in))
	for _, m := range in {
		if m.Role == agent.RoleSystem {
			continue
		}

		if len(m.Blocks) > 0 {
			// Serialize as a content-block array.
			blocks, err := marshalBlocks(m.Blocks)
			if err != nil {
				return nil, err
			}
			raw, err := json.Marshal(blocks)
			if err != nil {
				return nil, err
			}
			out = append(out, apiMessage{Role: string(m.Role), Content: raw})
		} else {
			// Plain string content.
			raw, err := json.Marshal(m.Content)
			if err != nil {
				return nil, err
			}
			out = append(out, apiMessage{Role: string(m.Role), Content: raw})
		}
	}
	return out, nil
}

// marshalBlocks converts agent.ContentBlock slice to []apiContentBlock.
// tool_result blocks are serialized with a nested "content" string field
// per the Anthropic wire format.
func marshalBlocks(blocks []agent.ContentBlock) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, map[string]any{
				"type": "text",
				"text": b.Text,
			})
		case "tool_use":
			m := map[string]any{
				"type":  "tool_use",
				"id":    b.ID,
				"name":  b.Name,
				"input": b.Input,
			}
			out = append(out, m)
		case "tool_result":
			m := map[string]any{
				"type":        "tool_result",
				"tool_use_id": b.ToolUseID,
				"content":     b.Result,
			}
			if b.IsError {
				m["is_error"] = true
			}
			out = append(out, m)
		default:
			return nil, fmt.Errorf("anthropic: unknown block type %q", b.Type)
		}
	}
	return out, nil
}

// fromAPIContentBlocks converts the response content blocks to agent.ContentBlock.
func fromAPIContentBlocks(blocks []apiContentBlock) []agent.ContentBlock {
	out := make([]agent.ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case "text":
			out = append(out, agent.NewTextBlock(b.Text))
		case "tool_use":
			out = append(out, agent.NewToolUseBlock(b.ID, b.Name, b.Input))
		}
	}
	return out
}

// joinTextBlocks concatenates the text from every "text" content block.
// Non-text blocks (tool_use) are skipped.
func joinTextBlocks(blocks []apiContentBlock) string {
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}
