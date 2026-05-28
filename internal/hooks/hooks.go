// Package hooks implements C9 Phase 3 turn-boundary hooks. External
// retrieval layers (notably Hindsight) plug into octo by registering a
// pre-turn command (called before every REPL turn; stdout is injected
// as additional context) and/or a post-turn command (called after,
// receives the user input + assistant reply for retention). The
// surface is the same shell-out pattern Claude Code's UserPromptSubmit
// hook uses, so existing Hindsight scripts work with octo unchanged.
//
// Configuration is env-var driven in v1:
//
//	OCTO_HOOK_PRE_TURN   = /path/to/pre-script   (optional)
//	OCTO_HOOK_POST_TURN  = /path/to/post-script  (optional)
//	OCTO_HOOK_TIMEOUT    = 5s                    (optional, default below)
//
// A future PR may add ~/.octo/hooks.yml for layered config, but env vars
// cover the v1 "advanced user wiring Hindsight" use case without YAML
// parsing surface area.
//
// Hook protocol (stdin → script → stdout):
//
//	pre-turn:  stdin JSON {"user_input": "..."}
//	           stdout JSON {"additional_context": "..."}   OR  plain text
//	           non-empty stdout → injected into the user message
//	           exit non-zero → ignored (logged), turn proceeds without injection
//	post-turn: stdin JSON {"user_input": "...", "assistant_reply": "..."}
//	           stdout ignored (fire-and-forget on success);
//	           non-zero exit → logged but never blocks the next prompt.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout caps how long the REPL is willing to wait for a hook.
// Pre-turn hooks block the user's prompt response, so the budget is
// tight; post-turn hooks block the next prompt, also tight. 5s is
// generous for an embedding lookup or a write to a local DB.
const DefaultTimeout = 5 * time.Second

// timeoutCeiling caps the env-supplied OCTO_HOOK_TIMEOUT so a fat-
// fingered value doesn't hang the REPL indefinitely. 30 seconds is
// well past any reasonable retrieval call.
const timeoutCeiling = 30 * time.Second

// Runner executes the configured pre-turn / post-turn hooks. The zero
// Runner is a no-op — pre/post calls return cleanly with no work done,
// so callers don't have to branch on "is a hook configured" themselves.
//
// One Runner per REPL session; safe to share across the loop.
type Runner struct {
	PreTurnCmd  string        // empty → no pre-turn hook
	PostTurnCmd string        // empty → no post-turn hook
	Timeout     time.Duration // zero → DefaultTimeout
}

// LoadFromEnv reads the hook configuration from the standard env vars.
// Always returns a Runner; an unconfigured environment yields a zero
// Runner whose Pre and Post are no-ops.
func LoadFromEnv() *Runner {
	r := &Runner{
		PreTurnCmd:  strings.TrimSpace(os.Getenv("OCTO_HOOK_PRE_TURN")),
		PostTurnCmd: strings.TrimSpace(os.Getenv("OCTO_HOOK_POST_TURN")),
	}
	if raw := strings.TrimSpace(os.Getenv("OCTO_HOOK_TIMEOUT")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			if d > timeoutCeiling {
				d = timeoutCeiling
			}
			r.Timeout = d
		}
	}
	return r
}

// Configured reports whether at least one hook is wired. Used by the
// REPL to skip the "running hook…" trace lines when no hooks are
// configured (the common case for users who don't run Hindsight).
func (r *Runner) Configured() bool {
	if r == nil {
		return false
	}
	return r.PreTurnCmd != "" || r.PostTurnCmd != ""
}

// timeout returns the configured per-hook deadline.
func (r *Runner) timeout() time.Duration {
	if r != nil && r.Timeout > 0 {
		return r.Timeout
	}
	return DefaultTimeout
}

// Pre runs the pre-turn hook and returns the additional context to
// inject into the user's message. Returns "" with no error when no
// hook is configured (the no-op path). Hook failures are non-fatal:
// they return an error so the caller can log it, but the user's turn
// proceeds as if no context was added.
//
// Output protocol: stdout is interpreted as JSON first; if it parses
// as {"additional_context": "..."}, that field is used. If it doesn't
// parse, stdout is used verbatim. This lets scripts emit either a
// structured response (recommended) or a quick `echo "extra context"`.
func (r *Runner) Pre(ctx context.Context, userInput string) (string, error) {
	if r == nil || r.PreTurnCmd == "" {
		return "", nil
	}
	payload, err := json.Marshal(map[string]any{"user_input": userInput})
	if err != nil {
		return "", err
	}
	stdout, err := r.run(ctx, r.PreTurnCmd, payload)
	if err != nil {
		return "", err
	}
	return parsePreOutput(stdout), nil
}

// Post runs the post-turn hook in a fire-and-forget fashion: errors are
// returned so the caller can log them but never propagated up the chain
// — a flaky retain step shouldn't block the next user prompt. The hook
// still runs synchronously (within the timeout) so a misbehaving script
// can't pile up unbounded background goroutines.
//
// Output is ignored: the post hook is for side effects (writing to a
// retrieval index), not for shaping the next turn.
func (r *Runner) Post(ctx context.Context, userInput, assistantReply string) error {
	if r == nil || r.PostTurnCmd == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"user_input":      userInput,
		"assistant_reply": assistantReply,
	})
	if err != nil {
		return err
	}
	_, err = r.run(ctx, r.PostTurnCmd, payload)
	return err
}

// run executes cmd, piping payload to its stdin, with a deadline. The
// command is invoked via "sh -c" so users can write `OCTO_HOOK_PRE_TURN="./script | filter"`
// pipelines without quoting the shell themselves. On Windows the user
// is responsible for an sh-compatible runner being on PATH (Git Bash,
// WSL, msys2) — same constraint as the terminal tool.
func (r *Runner) run(ctx context.Context, cmd string, stdin []byte) ([]byte, error) {
	deadline := r.timeout()
	rctx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	c := exec.CommandContext(rctx, "sh", "-c", cmd)
	c.Stdin = bytes.NewReader(stdin)
	var out, errBuf bytes.Buffer
	c.Stdout = &out
	c.Stderr = &errBuf
	// WaitDelay (Go 1.20+) bounds how long Run blocks after the process is
	// killed waiting for its stdio pipes to close. Without it, a `sh -c`
	// whose child (e.g. `sleep`) keeps the pipes open survives the SIGKILL
	// on the shell — Run hangs until the child terminates naturally. 1s
	// is plenty for the kernel to tear down the process group.
	c.WaitDelay = time.Second
	if err := c.Run(); err != nil {
		// Distinguish timeout from a script that just exit-1'd. Either
		// way the caller will surface the message but the wording
		// helps the user debug Hindsight wiring.
		if errors.Is(rctx.Err(), context.DeadlineExceeded) {
			return out.Bytes(), fmt.Errorf("hooks: %s timed out after %s", cmd, deadline)
		}
		stderrTail := strings.TrimSpace(errBuf.String())
		if stderrTail != "" {
			return out.Bytes(), fmt.Errorf("hooks: %s: %w (stderr: %s)", cmd, err, oneLineCap(stderrTail, 200))
		}
		return out.Bytes(), fmt.Errorf("hooks: %s: %w", cmd, err)
	}
	return out.Bytes(), nil
}

// parsePreOutput accepts either a JSON object with an additional_context
// field, or a raw text payload. Empty / whitespace-only stdout yields "".
func parsePreOutput(stdout []byte) string {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return ""
	}
	// Try the structured form first.
	if strings.HasPrefix(trimmed, "{") {
		var obj struct {
			AdditionalContext string `json:"additional_context"`
		}
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
			return strings.TrimSpace(obj.AdditionalContext)
		}
	}
	// Fall through to raw stdout. The script that printed it gets to
	// choose its own formatting — newlines are preserved.
	return trimmed
}

// InjectContext wraps additional context (from a pre-turn hook) and the
// user's original message into a single string to send to the model.
// Centralized here so cmd/octo doesn't reinvent the framing each turn —
// and so the surface stays stable if we decide to relocate the hook
// output (e.g. into the system prompt instead of the user message).
//
// Format: original message, then a divider, then a labelled block so
// the model knows where the user stopped speaking and the retrieval
// layer started.
func InjectContext(userInput, additionalContext string) string {
	additionalContext = strings.TrimSpace(additionalContext)
	if additionalContext == "" {
		return userInput
	}
	var b strings.Builder
	b.WriteString(userInput)
	b.WriteString("\n\n---\nAdditional context (from pre-turn hook):\n")
	b.WriteString(additionalContext)
	return b.String()
}

// oneLineCap collapses s to a single line and truncates to max runes
// for stderr-tail rendering. Multi-line script errors would otherwise
// spam the REPL.
func oneLineCap(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// devNull is a writer that discards everything. Some test helpers want
// to silence the runner without touching its public Out surface.
var _ io.Writer = devNull{}

type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }
