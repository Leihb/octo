package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
)

// HTTPTransport speaks JSON-RPC 2.0 over a single HTTP endpoint, per the
// MCP "Streamable HTTP" transport in spec 2024-11-05.
//
// What we implement
//   - POST one JSON-RPC frame per request, body Content-Type application/json.
//   - Each response body holds exactly one JSON-RPC frame (the response to
//     the request we just sent). We treat that as a synchronous RPC.
//   - The server-issued Mcp-Session-Id header is captured on the first
//     response and echoed on every subsequent request, so the server can
//     resume per-client state.
//
// What we don't implement (v1 omission)
//   - The SSE upgrade path (server responding with text/event-stream and
//     streaming multiple frames). Almost no server requires it for the
//     tools/list / tools/call request-response flow we care about.
//   - The optional GET endpoint for server-initiated notifications.
//   - Bidirectional concurrent sends. The client serialises one request at
//     a time anyway.
//
// Receive is wired through an in-memory channel: each Send queues the
// parsed response, Receive pops the next one. This keeps the same
// "Send / Receive interleaved" interface stdio uses, so the client logic
// is transport-agnostic.
type HTTPTransport struct {
	url     string
	headers map[string]string
	hc      *http.Client
	oauth   OAuthProvider // nil = no OAuth; static headers only

	// Session id is empty until the first server response sets it via
	// Mcp-Session-Id. Subsequent requests echo it back.
	sessionMu sync.Mutex
	sessionID string

	// inbox queues the response of each Send so Receive can hand it back.
	// Buffered so a Send never blocks (paired with one Receive per Send,
	// the channel never grows unboundedly).
	inbox  chan *Message
	closed atomic.Bool
}

// HTTPConfig is the configuration end-users pass via mcp.json — URL plus
// optional static headers (Authorization, custom auth, etc.). Headers
// values are sent verbatim; we don't expand env-var placeholders here, that
// happens in the config layer before we land on this struct.
type HTTPConfig struct {
	URL     string
	Headers map[string]string
	// OAuth, when non-nil, drives bearer-token injection + 401 retry.
	// The transport calls OAuth.Token before each request and
	// OAuth.Invalidate on a 401 response, then retries once.
	OAuth OAuthProvider
}

// NewHTTPTransport builds an HTTPTransport ready to Send / Receive.
// No network I/O happens until the first Send.
func NewHTTPTransport(cfg HTTPConfig) (*HTTPTransport, error) {
	if cfg.URL == "" {
		return nil, errors.New("mcp: http transport: empty URL")
	}
	return &HTTPTransport{
		url:     cfg.URL,
		headers: cfg.Headers,
		oauth:   cfg.OAuth,
		hc:      &http.Client{},
		// 16 is generous — the synchronous Send/Receive pattern only ever
		// has one outstanding response, but headroom protects against any
		// pipelining we might add later.
		inbox: make(chan *Message, 16),
	}, nil
}

// Send POSTs msg to the configured URL and queues the response for the
// next Receive call. Honors the documented Send-then-Receive pairing — one
// Send does exactly one HTTP round-trip and yields exactly one frame.
//
// When an OAuth provider is configured: every request injects the cached
// access token; a 401 response invalidates the cache and triggers exactly
// one retry (so the user doesn't see two device-flow prompts in a row).
func (t *HTTPTransport) Send(ctx context.Context, msg *Message) error {
	if t.closed.Load() {
		return errors.New("mcp: http transport: closed")
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp: marshal: %w", err)
	}

	// At most one retry on 401 — the retry uses a freshly-acquired token.
	for attempt := 0; attempt < 2; attempt++ {
		retry, err := t.doRequest(ctx, body, attempt > 0)
		if !retry {
			return err
		}
		// retry==true means 401 was observed; loop to retry once.
	}
	return errors.New("mcp: http: still unauthorized after retry")
}

// doRequest performs one HTTP round-trip. Returns (retry, err):
//
//   - retry=true means "401 observed, OAuth was invalidated, caller should
//     loop one more time with a freshly-acquired token". err is non-nil
//     here but only as breadcrumb context.
//   - retry=false means "this attempt is terminal — either the response
//     was queued for Receive or err describes a hard failure".
//
// forceFreshToken=true on the retry attempt forces a token refresh path
// even if the cached token still looks unexpired.
func (t *HTTPTransport) doRequest(ctx context.Context, body []byte, forceFreshToken bool) (retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("mcp: request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// MCP spec: clients MUST include Accept with both JSON and SSE so the
	// server can pick. We hand-pick the order so a server that supports
	// both prefers plain JSON (which we know how to decode synchronously).
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	t.sessionMu.Lock()
	if t.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", t.sessionID)
	}
	t.sessionMu.Unlock()

	// Bearer-token injection from OAuth provider, if configured.
	if t.oauth != nil {
		if forceFreshToken {
			t.oauth.Invalidate()
		}
		tok, terr := t.oauth.Token(ctx)
		if terr != nil {
			return false, fmt.Errorf("mcp: oauth token: %w", terr)
		}
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}

	resp, err := t.hc.Do(req)
	if err != nil {
		return false, fmt.Errorf("mcp: do: %w", err)
	}
	defer resp.Body.Close()

	// Capture session id on first response (and tolerate the server
	// rotating it on subsequent calls — spec lets them).
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.sessionMu.Lock()
		t.sessionID = sid
		t.sessionMu.Unlock()
	}

	if resp.StatusCode == http.StatusNoContent {
		// 204: server accepted a notification. No body to parse, no inbox
		// queue — the client doesn't expect a response for notifications.
		return false, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		// If we have OAuth and this isn't already our second attempt, ask
		// the caller to retry with a fresh token. The provider's
		// Invalidate is what makes the next Token() do real work.
		if t.oauth != nil && !forceFreshToken {
			return true, errors.New("mcp: http 401, will retry")
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, fmt.Errorf("mcp: http 401: %s", bytes.TrimSpace(b))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return false, fmt.Errorf("mcp: http %d: %s", resp.StatusCode, bytes.TrimSpace(b))
	}

	// We only handle plain-JSON responses for v1. If the server insists on
	// text/event-stream we surface a clear error rather than silently
	// hanging — the user can pick a different server or file an issue.
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !isJSONContentType(ct) {
		return false, fmt.Errorf("mcp: http: unsupported response Content-Type %q (v1 needs application/json)", ct)
	}

	var m Message
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return false, fmt.Errorf("mcp: decode response: %w", err)
	}
	select {
	case t.inbox <- &m:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	return false, nil
}

// Receive blocks for the next queued response. The pairing with Send is
// strict: one Send produces one inbox entry, one Receive consumes it.
func (t *HTTPTransport) Receive(ctx context.Context) (*Message, error) {
	select {
	case m, ok := <-t.inbox:
		if !ok {
			return nil, io.EOF
		}
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Close tears the transport down. Idempotent. After Close, any pending
// Receive caller is unblocked with io.EOF; further Sends return an error.
func (t *HTTPTransport) Close() error {
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(t.inbox)
	return nil
}

// isJSONContentType reports whether ct (a Content-Type header value)
// indicates a JSON-RPC body. Tolerant of the "; charset=utf-8" suffix and
// the legacy "application/json-rpc" alias some servers use.
func isJSONContentType(ct string) bool {
	for _, prefix := range []string{"application/json", "application/json-rpc"} {
		if len(ct) >= len(prefix) && ct[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
