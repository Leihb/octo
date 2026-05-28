package mcp

import "context"

// Transport is the framed-message connection an MCP client speaks over.
// Two impls today: stdio (subprocess pipes) and Streamable HTTP. Send is
// fire-and-forget — the client matches responses to requests by ID, not by
// Send/Receive ordering. Receive blocks until either a frame arrives or ctx
// is done; transports must honor ctx cancellation promptly so a stalled
// remote can be torn down on /exit.
type Transport interface {
	// Send writes one JSON-RPC frame. Implementations serialise it; the
	// caller hands over an already-encoded Message.
	Send(ctx context.Context, msg *Message) error
	// Receive blocks until the next frame arrives. Returns (nil, io.EOF) on
	// a clean close. Recoverable parse errors are returned as a non-nil
	// error; callers may retry, but in practice we tear the transport down.
	Receive(ctx context.Context) (*Message, error)
	// Close releases the transport. After Close, Send / Receive return an
	// error. Idempotent.
	Close() error
}
