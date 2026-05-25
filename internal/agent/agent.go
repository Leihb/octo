package agent

import (
	"context"
	"fmt"
)

// Sender is the minimal slice of provider.Provider the Agent depends on:
// it accepts a model, system prompt, messages, and optional max-tokens, and
// returns the assistant's text reply.
//
// Declaring this interface here (rather than depending on provider.Provider
// directly) keeps the agent package free of an import on provider, which in
// turn keeps the dependency graph one-directional: provider → agent, never
// the other way. The chat subcommand and any future caller is responsible
// for adapting a provider.Provider into a Sender (trivial — provider's
// Request/Response already match).
type Sender interface {
	SendMessages(ctx context.Context, model, system string, messages []Message, maxTokens int) (Reply, error)
}

// StreamingSender extends Sender with the ability to deliver the assistant
// reply chunk-by-chunk via a callback while the upstream stream is open.
//
// The agent type-asserts its Sender to StreamingSender at the start of
// TurnStream; if the underlying provider doesn't implement it, TurnStream
// falls back to the buffered Sender.SendMessages and invokes onChunk once
// with the full content for callers that don't want to branch on capability.
type StreamingSender interface {
	Sender
	StreamMessages(
		ctx context.Context,
		model, system string,
		messages []Message,
		maxTokens int,
		onChunk func(textDelta string),
	) (Reply, error)
}

// Reply is the agent-level view of a provider response. It deliberately
// mirrors provider.Response field-for-field (same names, same types) but
// lives in this package so users of the agent API don't have to import
// provider.
type Reply struct {
	Content      string
	Model        string
	StopReason   string
	InputTokens  int
	OutputTokens int
}

// Agent owns one conversation: the system prompt, the history of turns, the
// model name, and the LLM transport (Sender).
//
// M1.2 covers a single send-receive turn; tool calls, streaming, and inbox
// queuing arrive in M2 / M3.
type Agent struct {
	System    string
	Model     string
	MaxTokens int
	History   *History
	Sender    Sender
}

// New constructs an Agent with a fresh History.
//
// Required: sender (otherwise Turn returns an error), model (otherwise the
// provider rejects the request). System and MaxTokens are optional.
func New(sender Sender, model string) *Agent {
	return &Agent{
		Model:   model,
		History: NewHistory(),
		Sender:  sender,
	}
}

// Turn appends the user's input to history, asks the Sender for a reply,
// appends the reply to history, and returns it. Errors leave History
// unchanged from before the call.
func (a *Agent) Turn(ctx context.Context, userInput string) (Reply, error) {
	if a.Sender == nil {
		return Reply{}, fmt.Errorf("agent: no Sender configured")
	}
	if a.Model == "" {
		return Reply{}, fmt.Errorf("agent: Model is required")
	}
	if userInput == "" {
		return Reply{}, fmt.Errorf("agent: userInput must be non-empty")
	}

	// Append user message first so the snapshot the Sender sees includes it.
	a.History.Append(NewUserMessage(userInput))

	reply, err := a.Sender.SendMessages(ctx, a.Model, a.System, a.History.Snapshot(), a.MaxTokens)
	if err != nil {
		// Pop the user message we just appended so retrying with the same
		// History doesn't duplicate it. Cheaper than transactional locking
		// since History is only mutated from this goroutine in M1.2.
		a.History.popLast()
		return Reply{}, fmt.Errorf("agent: send: %w", err)
	}

	a.History.Append(NewAssistantMessage(reply.Content))
	return reply, nil
}

// TurnStream is the streaming counterpart of Turn. It appends the user input
// to history, calls the Sender (streaming if supported, otherwise falling
// back to SendMessages), invokes onChunk for each text delta, appends the
// final assistant reply to history, and returns it.
//
// onChunk may be nil, in which case the stream is still consumed end-to-end
// but no per-delta callback fires — useful for tests and for callers that
// only want the aggregated Reply.
//
// On error, the user message is popped from History (same contract as Turn),
// so a retry with the same History doesn't duplicate the user turn.
func (a *Agent) TurnStream(
	ctx context.Context,
	userInput string,
	onChunk func(textDelta string),
) (Reply, error) {
	if a.Sender == nil {
		return Reply{}, fmt.Errorf("agent: no Sender configured")
	}
	if a.Model == "" {
		return Reply{}, fmt.Errorf("agent: Model is required")
	}
	if userInput == "" {
		return Reply{}, fmt.Errorf("agent: userInput must be non-empty")
	}

	a.History.Append(NewUserMessage(userInput))

	var (
		reply Reply
		err   error
	)
	if ss, ok := a.Sender.(StreamingSender); ok {
		reply, err = ss.StreamMessages(ctx, a.Model, a.System, a.History.Snapshot(), a.MaxTokens, onChunk)
	} else {
		// Fallback: buffer the call and surface a single "chunk" with the
		// full content. Keeps callers from having to branch on capability
		// at the cost of losing real-time visibility on this backend.
		reply, err = a.Sender.SendMessages(ctx, a.Model, a.System, a.History.Snapshot(), a.MaxTokens)
		if err == nil && onChunk != nil && reply.Content != "" {
			onChunk(reply.Content)
		}
	}
	if err != nil {
		a.History.popLast()
		return Reply{}, fmt.Errorf("agent: stream: %w", err)
	}

	a.History.Append(NewAssistantMessage(reply.Content))
	return reply, nil
}

// popLast is an internal helper used by Turn to undo the user-message append
// when the Sender call fails. Exported users should not need this.
func (h *History) popLast() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if n := len(h.messages); n > 0 {
		h.messages = h.messages[:n-1]
	}
}
