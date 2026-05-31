package channel

import (
	"context"
	"fmt"
)

// InboundEvent is the standardized event yielded by every adapter when a
// message arrives from an IM platform.
type InboundEvent struct {
	// Type is always "message" today; reserved for future event kinds.
	Type string

	// Platform identifier, e.g. "feishu", "telegram".
	Platform string

	// ChatID is the conversation identifier (group, DM, etc.).
	ChatID string

	// UserID is the sender's platform identity.
	UserID string

	// Text is the plain-text content of the message.
	Text string

	// Files are attachments delivered with the message.
	Files []FileAttachment

	// MessageID is the platform's native message identifier.
	MessageID string

	// ChatType is either "direct" or "group".
	ChatType string

	// ContextToken is platform-specific reply context (Weixin iLink requires
	// echoing this on every outbound message).
	ContextToken string

	// Raw is the platform-native payload, kept for adapter-internal use.
	Raw any
}

// FileAttachment describes a file delivered with an inbound message.
type FileAttachment struct {
	// Type is "image", "file", "voice", or "video".
	Type string

	// Name is a display filename.
	Name string

	// Path is a local filesystem path (for downloaded files).
	Path string

	// MimeType is the media type.
	MimeType string

	// DataURL is a base64 data URL (for inline images sent to vision models).
	DataURL string
}

// SendResult is returned by outbound send operations.
type SendResult struct {
	// MessageID is the platform's native message ID for the sent message.
	MessageID string

	// OK is false when the send failed.
	OK bool

	// Error carries a human-readable failure reason when OK == false.
	Error string
}

// Adapter is the interface every IM platform must implement.
type Adapter interface {
	// Platform returns the platform identifier, e.g. "feishu".
	Platform() string

	// Start begins receiving messages. It blocks until the context is cancelled
	// or Stop is called. The handler is invoked for every inbound event.
	Start(ctx context.Context, onMessage func(InboundEvent)) error

	// Stop signals the adapter to shut down. It should return promptly;
	// Start will return after cleanup.
	Stop() error

	// SendText sends a plain-text (or Markdown) message to a chat.
	SendText(chatID, text string, replyTo string) SendResult

	// SendFile sends a local file to a chat.
	SendFile(chatID string, path string, name string, replyTo string) SendResult

	// UpdateMessage edits an existing message in-place. Returns true on success.
	UpdateMessage(chatID, messageID, text string) bool

	// SupportsMessageUpdates reports whether the platform can edit sent messages.
	SupportsMessageUpdates() bool

	// ValidateConfig returns a list of human-readable errors; empty means valid.
	ValidateConfig(cfg PlatformConfig) []string
}

// Registry maps platform names to adapter constructors.
var registry = make(map[string]func(PlatformConfig) (Adapter, error))

// Register adds an adapter constructor to the registry.
func Register(name string, ctor func(PlatformConfig) (Adapter, error)) {
	registry[name] = ctor
}

// Find looks up an adapter constructor by name.
func Find(name string) (func(PlatformConfig) (Adapter, error), error) {
	ctor, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("no adapter registered for platform %q", name)
	}
	return ctor, nil
}

// RegisteredPlatforms returns all platform names in the registry.
func RegisteredPlatforms() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}
