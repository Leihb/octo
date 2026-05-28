package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockTransport is a paired-channel transport for unit tests. The mock
// server runs in a goroutine, reading inbound frames off the in channel and
// pushing replies / notifications to the out channel.
type mockTransport struct {
	out    chan *Message
	in     chan *Message
	closed atomic.Bool
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		out: make(chan *Message, 16),
		in:  make(chan *Message, 16),
	}
}

func (m *mockTransport) Send(ctx context.Context, msg *Message) error {
	if m.closed.Load() {
		return errors.New("closed")
	}
	select {
	case m.in <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *mockTransport) Receive(ctx context.Context) (*Message, error) {
	select {
	case msg, ok := <-m.out:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *mockTransport) Close() error {
	if !m.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(m.out)
	return nil
}

// mockServer is the goroutine driving mockTransport from the server side.
// It mirrors a real MCP server: handles initialize + lists + tool calls
// with canned responses. The optional handlers map lets tests override per-
// method behaviour.
type mockServer struct {
	t        *testing.T
	tx       *mockTransport
	caps     ServerCapabilities
	tools    []Tool
	resmap   map[string][]ResourceContent
	promap   map[string]GetPromptResult
	wg       sync.WaitGroup
	stopped  chan struct{}
	stopOnce sync.Once
}

func newMockServer(t *testing.T) *mockServer {
	return &mockServer{
		t:       t,
		tx:      newMockTransport(),
		stopped: make(chan struct{}),
		// Default: all surfaces enabled. Tests can override before start.
		caps: ServerCapabilities{
			Tools:     &ToolsCapability{},
			Resources: &ResourcesCapability{},
			Prompts:   &PromptsCapability{},
		},
	}
}

// reply sends a success response for the given request id and result.
func (s *mockServer) reply(id json.RawMessage, result any) {
	b, _ := json.Marshal(result)
	s.tx.out <- &Message{JSONRPC: "2.0", ID: id, Result: b}
}

func (s *mockServer) replyError(id json.RawMessage, code int, msg string) {
	s.tx.out <- &Message{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	}
}

func (s *mockServer) start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-s.stopped:
				return
			case req, ok := <-s.tx.in:
				if !ok {
					return
				}
				if req.IsNotification() {
					continue // ignore notifications/initialized etc.
				}
				switch req.Method {
				case MethodInitialize:
					s.reply(req.ID, InitializeResult{
						ProtocolVersion: ProtocolVersion,
						Capabilities:    s.caps,
						ServerInfo:      Implementation{Name: "mockserver", Version: "0.0.1"},
						Instructions:    "use the echo tool",
					})
				case MethodToolsList:
					s.reply(req.ID, ListToolsResult{Tools: s.tools})
				case MethodToolsCall:
					var p CallToolParams
					_ = json.Unmarshal(req.Params, &p)
					s.reply(req.ID, CallToolResult{
						Content: []Content{{Type: "text", Text: "echo:" + p.Name}},
					})
				case MethodResourcesList:
					var rs []Resource
					for uri := range s.resmap {
						rs = append(rs, Resource{URI: uri, Name: uri})
					}
					s.reply(req.ID, ListResourcesResult{Resources: rs})
				case MethodResourcesRead:
					var p ReadResourceParams
					_ = json.Unmarshal(req.Params, &p)
					contents, ok := s.resmap[p.URI]
					if !ok {
						s.replyError(req.ID, -32602, "no such resource")
						continue
					}
					s.reply(req.ID, ReadResourceResult{Contents: contents})
				case MethodPromptsList:
					var ps []Prompt
					for name := range s.promap {
						ps = append(ps, Prompt{Name: name})
					}
					s.reply(req.ID, ListPromptsResult{Prompts: ps})
				case MethodPromptsGet:
					var p GetPromptParams
					_ = json.Unmarshal(req.Params, &p)
					r, ok := s.promap[p.Name]
					if !ok {
						s.replyError(req.ID, -32602, "no such prompt")
						continue
					}
					s.reply(req.ID, r)
				default:
					s.replyError(req.ID, -32601, "method not found: "+req.Method)
				}
			}
		}
	}()
}

func (s *mockServer) stop() {
	s.stopOnce.Do(func() {
		close(s.stopped)
		_ = s.tx.Close()
	})
	s.wg.Wait()
}

func TestClient_InitializeRoundtrip(t *testing.T) {
	srv := newMockServer(t)
	srv.start()
	defer srv.stop()

	c := NewClient(srv.tx, Implementation{Name: "test", Version: "0.1"})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if got := c.ServerInfo().Name; got != "mockserver" {
		t.Errorf("ServerInfo.Name = %q", got)
	}
	if c.Capabilities().Tools == nil {
		t.Error("expected Tools capability")
	}
	if c.Instructions() != "use the echo tool" {
		t.Errorf("Instructions = %q", c.Instructions())
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestClient_ListAndCallTool(t *testing.T) {
	srv := newMockServer(t)
	srv.tools = []Tool{
		{Name: "echo", Description: "echoes back", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	srv.start()
	defer srv.stop()

	c := NewClient(srv.tx, Implementation{Name: "test", Version: "0.1"})
	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("ListTools = %v", tools)
	}
	res, err := c.CallTool(ctx, "echo", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "echo:echo" {
		t.Errorf("CallTool result = %+v", res)
	}
}

func TestClient_ReadResource(t *testing.T) {
	srv := newMockServer(t)
	srv.resmap = map[string][]ResourceContent{
		"file:///etc/hosts": {
			{URI: "file:///etc/hosts", MIMEType: "text/plain", Text: "127.0.0.1 localhost"},
		},
	}
	srv.start()
	defer srv.stop()

	c := NewClient(srv.tx, Implementation{Name: "test", Version: "0.1"})
	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	resources, err := c.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("ListResources = %v", resources)
	}
	contents, err := c.ReadResource(ctx, "file:///etc/hosts")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(contents) != 1 || contents[0].Text != "127.0.0.1 localhost" {
		t.Errorf("ReadResource = %+v", contents)
	}
}

func TestClient_GetPrompt(t *testing.T) {
	srv := newMockServer(t)
	srv.promap = map[string]GetPromptResult{
		"summarise": {
			Description: "summarise a text",
			Messages: []PromptMessage{
				{Role: "user", Content: Content{Type: "text", Text: "Please summarise."}},
			},
		},
	}
	srv.start()
	defer srv.stop()

	c := NewClient(srv.tx, Implementation{Name: "test", Version: "0.1"})
	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	prompts, err := c.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if len(prompts) != 1 {
		t.Fatalf("ListPrompts = %v", prompts)
	}
	r, err := c.GetPrompt(ctx, "summarise", nil)
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if r.Description != "summarise a text" {
		t.Errorf("Description = %q", r.Description)
	}
}

func TestClient_RPCErrorPropagates(t *testing.T) {
	srv := newMockServer(t)
	srv.start()
	defer srv.stop()

	c := NewClient(srv.tx, Implementation{Name: "test", Version: "0.1"})
	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	// Unknown resource → server replies with code -32602.
	_, err := c.ReadResource(ctx, "file:///does-not-exist")
	if err == nil {
		t.Fatal("expected RPC error")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Errorf("expected *RPCError, got %T", err)
	}
}

func TestClient_CallAfterCloseFails(t *testing.T) {
	srv := newMockServer(t)
	srv.start()
	defer srv.stop()

	c := NewClient(srv.tx, Implementation{Name: "test", Version: "0.1"})
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, err := c.ListTools(context.Background()); err == nil {
		t.Error("expected error after Close")
	}
}

func TestClient_ContextCancellation(t *testing.T) {
	srv := newMockServer(t)
	srv.start()
	defer srv.stop()

	c := NewClient(srv.tx, Implementation{Name: "test", Version: "0.1"})
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Drain the server briefly so the next call sees the cancellation, not
	// a normal reply.
	srv.stop()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.ListTools(ctx)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
