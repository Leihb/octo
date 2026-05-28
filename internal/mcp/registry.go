package mcp

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

// Registry owns the live MCP client connections for a session. Built once
// at startup from a Config; tears all clients down on Close. Failures to
// connect or initialize one server degrade — the server is skipped, its
// error logged to stderr, and the registry keeps the survivors.
//
// This separation (Registry here vs. tool-adapter in cmd/octo) lets the
// internal/mcp package stay free of any agent / tool dependencies.
type Registry struct {
	mu     sync.RWMutex
	conns  map[string]*Connection // server name → live client
	closed bool
}

// Connection is the per-server bundle of state the adapters need: the
// client itself plus the pre-fetched tools/resources/prompts lists so the
// agent loop doesn't re-list on every turn.
type Connection struct {
	Name      string
	Client    *Client
	Tools     []Tool
	Resources []Resource
	Prompts   []Prompt
}

// ConnectAll spins up one client per configured server, runs the
// initialize handshake, and pre-fetches the surface lists. Failures are
// reported via the warn writer (typically os.Stderr) and the server is
// simply omitted — the user sees the error but the session still starts.
//
// The handshake + initial lists run with a per-server timeout so a single
// hung server can't stall startup. Servers are connected sequentially to
// keep startup logs readable; the connect cost is dominated by subprocess
// spawn and a single round-trip, so v1 doesn't need parallelism.
func ConnectAll(ctx context.Context, cfg *Config, info Implementation, warn io.Writer) *Registry {
	r := &Registry{conns: map[string]*Connection{}}
	if cfg == nil {
		return r
	}
	for _, name := range cfg.ServerNames() {
		entry := cfg.Servers[name]
		conn, err := connectOne(ctx, name, entry, info)
		if err != nil {
			if warn != nil {
				fmt.Fprintf(warn, "mcp: server %q skipped: %v\n", name, err)
			}
			continue
		}
		r.conns[name] = conn
	}
	return r
}

func connectOne(ctx context.Context, name string, entry ServerEntry, info Implementation) (*Connection, error) {
	// 10s is generous for subprocess spawn + handshake but bounded enough
	// that a misconfigured server doesn't keep a fresh chat blocked.
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var transport Transport
	switch entry.Kind() {
	case "stdio":
		t, err := NewStdioTransport(connectCtx, StdioConfig{
			Command: entry.Command,
			Args:    entry.Args,
			Env:     entry.Env,
		})
		if err != nil {
			return nil, err
		}
		transport = t
	case "http":
		t, err := NewHTTPTransport(HTTPConfig{
			URL:     entry.URL,
			Headers: entry.Headers,
		})
		if err != nil {
			return nil, err
		}
		transport = t
	default:
		return nil, fmt.Errorf("invalid transport for server %q", name)
	}

	client := NewClient(transport, info)
	if err := client.Initialize(connectCtx); err != nil {
		_ = client.Close()
		return nil, err
	}

	conn := &Connection{Name: name, Client: client}
	// Pre-fetch surface listings so per-turn calls don't pay an RPC just
	// to enumerate. We tolerate per-surface failures — a server with
	// broken resources/list still gets its tools/list cached.
	if tools, err := client.ListTools(connectCtx); err == nil {
		conn.Tools = tools
	}
	if resources, err := client.ListResources(connectCtx); err == nil {
		conn.Resources = resources
	}
	if prompts, err := client.ListPrompts(connectCtx); err == nil {
		conn.Prompts = prompts
	}
	return conn, nil
}

// Connections returns the live connections in stable order so /mcp output
// is deterministic.
func (r *Registry) Connections() []*Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Connection, 0, len(r.conns))
	for _, name := range sortedKeys(r.conns) {
		out = append(out, r.conns[name])
	}
	return out
}

// Get returns the named connection, or nil if no such server is live.
func (r *Registry) Get(name string) *Connection {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.conns[name]
}

// Len reports how many servers connected successfully — useful for the
// "Connected N MCP servers" startup line.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.conns)
}

// Close tears every connection down. Idempotent. Errors per server are
// dropped — Close is end-of-life and the user can't do anything with the
// errors anyway.
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	for _, c := range r.conns {
		_ = c.Client.Close()
	}
}

func sortedKeys(m map[string]*Connection) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
