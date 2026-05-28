package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
)

// Client is a single MCP server connection: one transport, one initialize
// handshake, request/response dispatch by ID. It's safe for concurrent
// callers — Call serialises through the transport via sendMu, and the
// receive loop demultiplexes responses on a per-request channel.
//
// Lifecycle:
//
//	Initialize  →  ListTools / CallTool / ListResources / ... (any order, concurrent OK)  →  Close
//
// The receive goroutine is started in Initialize and stops when the
// transport hits EOF or Close is called. Pending callers waiting on a
// response see their channel closed with an error in that case.
type Client struct {
	transport Transport
	info      Implementation
	// caps is populated after Initialize so callers can branch on which
	// surfaces the server actually advertised. Tools/Resources/Prompts are
	// pointers in the spec; nil means "not supported".
	caps         ServerCapabilities
	serverInfo   Implementation
	instructions string

	nextID atomic.Uint64

	// pending tracks in-flight requests. The map is keyed by the request
	// ID we issued (as a string for json.RawMessage compatibility) and
	// holds the response channel the caller is blocked on.
	pendingMu sync.Mutex
	pending   map[string]chan *Message

	// sendMu is the transport's write lock. Stdio transport already
	// serialises but the contract is per-Client, so we hold it explicitly
	// at the Call call site.
	sendMu sync.Mutex

	rxDone chan struct{} // closed when the receive loop exits
	closed atomic.Bool
}

// NewClient builds a Client around an already-Open transport. The caller
// owns transport lifetime up to Initialize; after Initialize succeeds the
// Client owns it and Close will tear it down.
func NewClient(t Transport, info Implementation) *Client {
	return &Client{
		transport: t,
		info:      info,
		pending:   make(map[string]chan *Message),
		rxDone:    make(chan struct{}),
	}
}

// Initialize performs the MCP handshake: send the initialize request,
// receive the result, then send the initialized notification. Starts the
// background receive loop on the way in so the response to initialize is
// demuxed normally.
func (c *Client) Initialize(ctx context.Context) error {
	// Kick off the receive loop BEFORE issuing the first request so the
	// response can be matched. If the loop fails to start the goroutine
	// would still observe ctx cancellation, but starting it ahead removes
	// a race where Send finishes and the response arrives before we set
	// up the demuxer.
	go c.receiveLoop()

	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    ClientCapabilities{},
		ClientInfo:      c.info,
	}
	var result InitializeResult
	if err := c.Call(ctx, MethodInitialize, params, &result); err != nil {
		return fmt.Errorf("mcp: initialize: %w", err)
	}
	c.caps = result.Capabilities
	c.serverInfo = result.ServerInfo
	c.instructions = result.Instructions

	// notifications/initialized lets the server know we're done handshaking
	// and ready for normal traffic. Fire-and-forget — the server doesn't
	// reply, and a transport error here surfaces on the next real call.
	if err := c.Notify(ctx, MethodInitialized, nil); err != nil {
		return fmt.Errorf("mcp: send initialized: %w", err)
	}
	return nil
}

// ServerInfo returns the {name, version} the server advertised in its
// initialize response. Useful for /mcp output.
func (c *Client) ServerInfo() Implementation { return c.serverInfo }

// Capabilities returns the server's advertised capabilities so callers can
// avoid calling tools/list against a server that doesn't support tools.
func (c *Client) Capabilities() ServerCapabilities { return c.caps }

// Instructions returns the optional human-readable instructions the server
// included in its initialize response. Some servers use this to tell the
// agent how their tools should be used.
func (c *Client) Instructions() string { return c.instructions }

// Call issues a request and blocks for the matched response. params is
// json-marshalled; result, if non-nil, is unmarshalled from the response's
// result field. RPC errors (server-returned) become a typed error caller
// can inspect.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	if c.closed.Load() {
		return errors.New("mcp: client closed")
	}
	id := c.nextID.Add(1)
	idBytes := []byte(strconv.FormatUint(id, 10))
	idKey := string(idBytes)

	// Register the pending channel BEFORE sending so a fast response can't
	// arrive before the demuxer knows where to deliver it.
	ch := make(chan *Message, 1)
	c.pendingMu.Lock()
	c.pending[idKey] = ch
	c.pendingMu.Unlock()
	defer c.removePending(idKey)

	msg := &Message{
		JSONRPC: "2.0",
		ID:      idBytes,
		Method:  method,
	}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("mcp: marshal params: %w", err)
		}
		msg.Params = p
	}

	c.sendMu.Lock()
	err := c.transport.Send(ctx, msg)
	c.sendMu.Unlock()
	if err != nil {
		return err
	}

	select {
	case resp := <-ch:
		if resp == nil {
			return errors.New("mcp: connection closed")
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil && len(resp.Result) > 0 {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("mcp: unmarshal result: %w", err)
			}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Notify sends a one-way notification — no id, no response. params may be
// nil for parameterless notifications (e.g., notifications/initialized).
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	if c.closed.Load() {
		return errors.New("mcp: client closed")
	}
	msg := &Message{JSONRPC: "2.0", Method: method}
	if params != nil {
		p, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("mcp: marshal notify params: %w", err)
		}
		msg.Params = p
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.transport.Send(ctx, msg)
}

// receiveLoop runs for the life of the Client, pulling frames off the
// transport and dispatching responses to the matching pending channel.
// Notifications from the server are silently dropped — v1 doesn't react to
// list_changed / progress / log messages.
func (c *Client) receiveLoop() {
	defer close(c.rxDone)
	ctx := context.Background()
	for {
		msg, err := c.transport.Receive(ctx)
		if err != nil {
			c.abortPending(err)
			return
		}
		if msg.IsResponse() {
			c.deliverResponse(msg)
			continue
		}
		// Server-initiated request or notification: ignore for v1. A
		// future revision can add a handler hook here.
	}
}

func (c *Client) deliverResponse(m *Message) {
	c.pendingMu.Lock()
	ch, ok := c.pending[string(m.ID)]
	delete(c.pending, string(m.ID))
	c.pendingMu.Unlock()
	if !ok {
		return // unknown id — server bug or late delivery after timeout
	}
	ch <- m
}

func (c *Client) removePending(id string) {
	c.pendingMu.Lock()
	if ch, ok := c.pending[id]; ok {
		delete(c.pending, id)
		close(ch)
	}
	c.pendingMu.Unlock()
}

func (c *Client) abortPending(_ error) {
	c.pendingMu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
}

// Close tears the transport down and unblocks any pending Call(). Safe to
// call multiple times.
func (c *Client) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	err := c.transport.Close()
	// Wait briefly for the receive loop to notice — best-effort. If the
	// transport's Close didn't unblock Receive (rare), we don't hang here.
	select {
	case <-c.rxDone:
	default:
	}
	c.abortPending(nil)
	return err
}

// ── Typed convenience wrappers ───────────────────────────────────────────

func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	if c.caps.Tools == nil {
		return nil, nil
	}
	var r ListToolsResult
	if err := c.Call(ctx, MethodToolsList, struct{}{}, &r); err != nil {
		return nil, err
	}
	return r.Tools, nil
}

func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	var r CallToolResult
	if err := c.Call(ctx, MethodToolsCall, CallToolParams{Name: name, Arguments: args}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func (c *Client) ListResources(ctx context.Context) ([]Resource, error) {
	if c.caps.Resources == nil {
		return nil, nil
	}
	var r ListResourcesResult
	if err := c.Call(ctx, MethodResourcesList, struct{}{}, &r); err != nil {
		return nil, err
	}
	return r.Resources, nil
}

func (c *Client) ReadResource(ctx context.Context, uri string) ([]ResourceContent, error) {
	var r ReadResourceResult
	if err := c.Call(ctx, MethodResourcesRead, ReadResourceParams{URI: uri}, &r); err != nil {
		return nil, err
	}
	return r.Contents, nil
}

func (c *Client) ListPrompts(ctx context.Context) ([]Prompt, error) {
	if c.caps.Prompts == nil {
		return nil, nil
	}
	var r ListPromptsResult
	if err := c.Call(ctx, MethodPromptsList, struct{}{}, &r); err != nil {
		return nil, err
	}
	return r.Prompts, nil
}

func (c *Client) GetPrompt(ctx context.Context, name string, args map[string]string) (*GetPromptResult, error) {
	var r GetPromptResult
	if err := c.Call(ctx, MethodPromptsGet, GetPromptParams{Name: name, Arguments: args}, &r); err != nil {
		return nil, err
	}
	return &r, nil
}
