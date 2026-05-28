package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// StdioTransport speaks JSON-RPC 2.0 over a subprocess's stdin/stdout. One
// JSON object per line, UTF-8, no chunking. The subprocess inherits stderr
// from the parent so its logs reach the user (and don't fill an unbounded
// pipe). The transport owns the *exec.Cmd lifecycle: Start runs it, Close
// closes stdin (signalling EOF to the child) and Wait()s for exit.
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	dec    *json.Decoder

	closeMu sync.Mutex
	closed  atomic.Bool

	// sendMu serialises writes so concurrent Send calls don't interleave
	// JSON bytes on stdin.
	sendMu sync.Mutex
}

// StdioConfig is the wire-format-agnostic spawn config. Env overrides extend
// (not replace) the parent's environment so PATH / HOME stay available to
// the child by default.
type StdioConfig struct {
	Command string
	Args    []string
	Env     map[string]string
}

// NewStdioTransport spawns the configured subprocess and wires its stdin /
// stdout to a line-delimited JSON-RPC pipe. stderr is inherited so the
// child's diagnostic logs appear directly under the parent's stderr.
//
// The returned transport is already running — Close will signal shutdown
// by closing stdin (the conventional MCP "I'm done, please exit") and then
// Wait()ing for exit so resources are released.
func NewStdioTransport(ctx context.Context, cfg StdioConfig) (*StdioTransport, error) {
	if cfg.Command == "" {
		return nil, errors.New("mcp: stdio transport: empty command")
	}
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	// Inherit env + add/override the configured entries. Building a fresh
	// slice keeps os.Environ() intact for the parent.
	if len(cfg.Env) > 0 {
		env := os.Environ()
		for k, v := range cfg.Env {
			env = append(env, k+"="+v)
		}
		cmd.Env = env
	}
	cmd.Stderr = os.Stderr // child's logs flow through to user terminal

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %s: %w", cfg.Command, err)
	}

	t := &StdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		// bufio.Reader on stdout lets us read line-by-line; json.Decoder
		// accepts a reader and tokenises whitespace-separated JSON values.
		// Using a Decoder over a raw scanner lets us hand back nested
		// objects without re-marshalling.
		dec: json.NewDecoder(bufio.NewReaderSize(stdout, 64*1024)),
	}
	return t, nil
}

// Send writes msg as one line of JSON to the subprocess stdin. Concurrent
// callers are serialised via sendMu so frames don't interleave.
func (t *StdioTransport) Send(ctx context.Context, msg *Message) error {
	if t.closed.Load() {
		return errors.New("mcp: stdio transport: closed")
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("mcp: marshal: %w", err)
	}
	t.sendMu.Lock()
	defer t.sendMu.Unlock()
	// We append a newline so older MCP servers that read line-by-line (and
	// many community implementations do) see a clean delimiter; newer
	// JSON-aware decoders don't care either way.
	b = append(b, '\n')
	if _, err := t.stdin.Write(b); err != nil {
		return fmt.Errorf("mcp: write: %w", err)
	}
	return nil
}

// Receive blocks for the next JSON object on stdout. The Decoder skips
// whitespace between objects, so the wire format can be either one-object-
// per-line or stream-with-no-delimiters — both work.
//
// Context cancellation is handled in two layers: (1) if ctx is already done
// we return immediately; (2) cancelling ctx after Receive has blocked has
// no direct hook (Decoder doesn't take ctx), so the canonical interrupt is
// Close, which closes stdout and surfaces here as io.EOF.
func (t *StdioTransport) Receive(ctx context.Context) (*Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if t.closed.Load() {
		return nil, io.EOF
	}
	var m Message
	if err := t.dec.Decode(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Close shuts the subprocess down. The order matters: close stdin first so
// the child sees EOF and can exit on its own (the polite shutdown signal
// MCP servers expect), then Wait so process resources are reclaimed.
// Idempotent — repeat calls return nil.
func (t *StdioTransport) Close() error {
	t.closeMu.Lock()
	defer t.closeMu.Unlock()
	if !t.closed.CompareAndSwap(false, true) {
		return nil
	}
	// stdin.Close() can race with the Send path; we hold closeMu but Send
	// only checks closed atomically before acquiring sendMu. The window is
	// tiny and a failed write surfaces as an error to the caller — fine.
	_ = t.stdin.Close()
	_ = t.stdout.Close()
	// Wait is best-effort: if the child is being killed by ctx (via
	// CommandContext) it might already be reaped.
	_ = t.cmd.Wait()
	return nil
}
