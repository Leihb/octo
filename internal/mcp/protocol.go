// Package mcp implements a Model Context Protocol client for octo. It speaks
// MCP spec version 2024-11-05 over either stdio (subprocess) or Streamable
// HTTP transports, and exposes the tools / resources / prompts surfaces a
// server can advertise.
//
// What this package does NOT cover (deliberately, for v1):
//   - server-initiated notifications (list_changed, progress, log) — connect,
//     list once, call;
//   - resource subscriptions;
//   - sampling (server asking client to call the LLM);
//   - roots (client advertising filesystem roots to the server).
//
// The package is pure protocol + transport — adapting an MCP server's surface
// into octo's tool layer is done in cmd/octo so this package stays free of
// any agent / tool dependencies.
package mcp

import (
	"encoding/json"
)

// ProtocolVersion is the MCP spec revision this client implements. Sent in
// the initialize request; the server is expected to echo it back (or a
// compatible newer/older version it can downgrade to). We don't currently
// negotiate — if the server returns a different version we accept it and
// hope for the best.
const ProtocolVersion = "2024-11-05"

// ── JSON-RPC 2.0 frame ───────────────────────────────────────────────────
//
// A single Message struct covers all three frame shapes — request,
// notification, response — because the wire format is the same and Go's
// optional-field encoding (omitempty + nil pointers / empty slices) lets us
// distinguish them at marshal time. Sentinel helpers IsRequest /
// IsNotification / IsResponse classify a decoded message at read time.
//
// ID is held as json.RawMessage because the JSON-RPC spec permits numbers,
// strings, or null. We always issue numeric IDs ourselves, but a strict
// server might echo back the literal we sent (e.g. "1" vs 1) and matching
// on the byte representation keeps us tolerant.

type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the standard JSON-RPC 2.0 error object. Code follows the spec
// reserved ranges: -32700 parse, -32600 invalid request, -32601 method not
// found, -32602 invalid params, -32603 internal, -32000..-32099 server.
// MCP-specific codes (if any) ride in the same field.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// IsRequest reports whether the message is a request (method set, id set).
func (m *Message) IsRequest() bool { return m.Method != "" && len(m.ID) > 0 }

// IsNotification reports whether the message is a notification (method set,
// no id). Servers send these when they don't expect a response (e.g.
// notifications/initialized from the client, or notifications/progress from
// the server).
func (m *Message) IsNotification() bool { return m.Method != "" && len(m.ID) == 0 }

// IsResponse reports whether the message is a response — either result or
// error, never both, always with an id.
func (m *Message) IsResponse() bool { return m.Method == "" && len(m.ID) > 0 }

// ── MCP method names ─────────────────────────────────────────────────────

const (
	MethodInitialize    = "initialize"
	MethodInitialized   = "notifications/initialized"
	MethodToolsList     = "tools/list"
	MethodToolsCall     = "tools/call"
	MethodResourcesList = "resources/list"
	MethodResourcesRead = "resources/read"
	MethodPromptsList   = "prompts/list"
	MethodPromptsGet    = "prompts/get"
	MethodShutdown      = "shutdown"
)

// ── initialize handshake ─────────────────────────────────────────────────

// InitializeParams is the payload of the client → server initialize request.
// We advertise an empty capabilities object because v1 doesn't implement
// sampling or roots — both are server → client features. ClientInfo is just
// nameplate metadata the server may log.
type InitializeParams struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ClientCapabilities `json:"capabilities"`
	ClientInfo      Implementation     `json:"clientInfo"`
}

type ClientCapabilities struct {
	// Future: Roots, Sampling. Left empty for v1.
	Experimental map[string]any `json:"experimental,omitempty"`
}

type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeResult comes back from the server. ServerInfo is informational
// (we log it in /mcp output). Capabilities tells us which surfaces the
// server actually supports — we use those flags to decide whether to call
// tools/list / resources/list / prompts/list at connect time.
type InitializeResult struct {
	ProtocolVersion string             `json:"protocolVersion"`
	Capabilities    ServerCapabilities `json:"capabilities"`
	ServerInfo      Implementation     `json:"serverInfo"`
	Instructions    string             `json:"instructions,omitempty"`
}

type ServerCapabilities struct {
	Tools     *ToolsCapability     `json:"tools,omitempty"`
	Resources *ResourcesCapability `json:"resources,omitempty"`
	Prompts   *PromptsCapability   `json:"prompts,omitempty"`
	Logging   *struct{}            `json:"logging,omitempty"`
}

type ToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type ResourcesCapability struct {
	Subscribe   bool `json:"subscribe,omitempty"`
	ListChanged bool `json:"listChanged,omitempty"`
}

type PromptsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

// ── tools ────────────────────────────────────────────────────────────────

type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

type ListToolsResult struct {
	Tools      []Tool `json:"tools"`
	NextCursor string `json:"nextCursor,omitempty"`
}

type CallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// CallToolResult carries the tool's output as a list of content blocks,
// matching the agent's existing multi-content shape. IsError signals that
// the server ran the tool but it failed — distinct from an RPC error,
// which means the call itself didn't reach the tool.
type CallToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Content covers the four block types MCP defines: text, image, audio, and
// embedded resource. Only Type and the type-specific fields are populated
// per block; the other fields stay zero.
type Content struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`     // base64; image/audio
	MIMEType string `json:"mimeType,omitempty"` // image/audio/resource
	// Embedded resource shape (type == "resource"): the nested Resource holds
	// uri + name + mimeType + text/blob.
	Resource *EmbeddedResource `json:"resource,omitempty"`
}

type EmbeddedResource struct {
	URI      string `json:"uri"`
	Name     string `json:"name,omitempty"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// ── resources ────────────────────────────────────────────────────────────

type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mimeType,omitempty"`
}

type ListResourcesResult struct {
	Resources  []Resource `json:"resources"`
	NextCursor string     `json:"nextCursor,omitempty"`
}

type ReadResourceParams struct {
	URI string `json:"uri"`
}

type ReadResourceResult struct {
	Contents []ResourceContent `json:"contents"`
}

// ResourceContent is a single chunk of a resource read. The server returns
// at least one — for a plain file it's typically one Text chunk; for a
// multi-part resource it can be several. Text and Blob are mutually
// exclusive (blob is base64-encoded binary).
type ResourceContent struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// ── prompts ──────────────────────────────────────────────────────────────

type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type ListPromptsResult struct {
	Prompts    []Prompt `json:"prompts"`
	NextCursor string   `json:"nextCursor,omitempty"`
}

type GetPromptParams struct {
	Name      string            `json:"name"`
	Arguments map[string]string `json:"arguments,omitempty"`
}

// GetPromptResult is the materialised prompt — a list of role-tagged messages
// the server expanded from its template. The user-facing tool drops them
// into the conversation; the role distinction (user / assistant) matters
// for chains-of-thought prompts.
type GetPromptResult struct {
	Description string          `json:"description,omitempty"`
	Messages    []PromptMessage `json:"messages"`
}

type PromptMessage struct {
	Role    string  `json:"role"`
	Content Content `json:"content"`
}
