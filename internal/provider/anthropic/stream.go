package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Leihb/octo/internal/provider"
)

// SendStream implements provider.StreamingProvider against Anthropic's
// Messages API with `stream: true`.
//
// Each text delta (content_block_delta of type text_delta) is forwarded to
// onChunk synchronously. The aggregated Content, Model, StopReason, and
// token usage are returned in the final Response.
//
// Cancellation is via ctx; no HTTP-level timeout is set, because streaming
// responses can legitimately run for minutes.
func (c *Client) SendStream(ctx context.Context, req provider.Request, onChunk func(string)) (provider.Response, error) {
	if req.Model == "" {
		return provider.Response{}, errors.New("anthropic: req.Model is required")
	}
	if len(req.Messages) == 0 {
		return provider.Response{}, errors.New("anthropic: at least one message is required")
	}

	body := apiRequest{
		Model:     req.Model,
		MaxTokens: req.MaxTokens,
		System:    req.SystemPrompt,
		Messages:  toAPIMessages(req.Messages),
		Stream:    true,
	}
	if body.MaxTokens <= 0 {
		body.MaxTokens = DefaultMaxTokens
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return provider.Response{}, fmt.Errorf("anthropic: marshal stream request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointURL(), bytes.NewReader(payload))
	if err != nil {
		return provider.Response{}, fmt.Errorf("anthropic: build stream request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", c.APIKey)
	apiVer := c.APIVersion
	if apiVer == "" {
		apiVer = DefaultAPIVersion
	}
	httpReq.Header.Set("anthropic-version", apiVer)

	resp, err := c.streamingHTTPClient().Do(httpReq)
	if err != nil {
		return provider.Response{}, fmt.Errorf("anthropic: send stream: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		var apiErr apiError
		if jerr := json.Unmarshal(respBody, &apiErr); jerr == nil && apiErr.Error.Message != "" {
			return provider.Response{}, fmt.Errorf(
				"anthropic: HTTP %d (%s): %s",
				resp.StatusCode, apiErr.Error.Type, apiErr.Error.Message,
			)
		}
		return provider.Response{}, fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var (
		contentB strings.Builder
		result   provider.Response
	)

	scanner := bufio.NewScanner(resp.Body)
	// Default Scanner buffer is 64 KiB which can be undersized for long
	// `data:` lines if Anthropic ever ships an unusually large delta.
	// Cap at 1 MiB.
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		// SSE frames: "data: <json>" lines, separated by blank lines, with
		// optional "event: <type>" markers we ignore (the JSON payload
		// always carries its own `type` field).
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "" {
			continue
		}

		var ev streamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return result, fmt.Errorf("anthropic: parse stream event: %w", err)
		}

		switch ev.Type {
		case "message_start":
			if ev.Message != nil {
				result.Model = ev.Message.Model
				result.InputTokens = ev.Message.Usage.InputTokens
				result.OutputTokens = ev.Message.Usage.OutputTokens
			}
		case "content_block_delta":
			if ev.Delta != nil && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				contentB.WriteString(ev.Delta.Text)
				if onChunk != nil {
					onChunk(ev.Delta.Text)
				}
			}
		case "message_delta":
			if ev.Delta != nil && ev.Delta.StopReason != "" {
				result.StopReason = ev.Delta.StopReason
			}
			// Output token count refines as the stream progresses; the
			// final message_delta carries the authoritative total.
			if ev.Usage != nil {
				result.OutputTokens = ev.Usage.OutputTokens
			}
		case "error":
			// Server-side errors mid-stream are surfaced as `event: error`.
			// The payload reuses apiError shape.
			var apiErr apiError
			_ = json.Unmarshal([]byte(data), &apiErr)
			return result, fmt.Errorf("anthropic: stream error: %s", apiErr.Error.Message)
		}
	}
	if err := scanner.Err(); err != nil {
		return result, fmt.Errorf("anthropic: stream read: %w", err)
	}

	result.Content = contentB.String()
	return result, nil
}

// streamingHTTPClient returns an http.Client suitable for long-lived SSE
// reads. When the caller has injected c.HTTPClient (typically a test using
// httptest), that client is reused — httptest responses are fast enough
// that their default behaviour is fine. Otherwise we synthesise a fresh
// client with NO end-to-end Timeout so multi-minute generations complete;
// cancellation falls back to the request context.
func (c *Client) streamingHTTPClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{}
}

// Compile-time assertion: *Client also satisfies provider.StreamingProvider.
var _ provider.StreamingProvider = (*Client)(nil)
