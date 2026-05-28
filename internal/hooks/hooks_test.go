package hooks

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// makeScript writes a shell script with the given body to a tempdir and
// returns its absolute path. Skip on Windows: the hook runner uses
// "sh -c" which isn't reliably available there. The runner contract
// matches the terminal tool's, so this is the same trade-off.
func makeScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("hook scripts use sh -c; not portable to Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "hook.sh")
	body = "#!/bin/sh\n" + body
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// ── LoadFromEnv ─────────────────────────────────────────────────────────

func TestLoadFromEnv_EmptyByDefault(t *testing.T) {
	t.Setenv("OCTO_HOOK_PRE_TURN", "")
	t.Setenv("OCTO_HOOK_POST_TURN", "")
	t.Setenv("OCTO_HOOK_TIMEOUT", "")
	r := LoadFromEnv()
	if r.Configured() {
		t.Errorf("empty env → not Configured, got %+v", r)
	}
}

func TestLoadFromEnv_ReadsPreAndPost(t *testing.T) {
	t.Setenv("OCTO_HOOK_PRE_TURN", "/usr/bin/pre")
	t.Setenv("OCTO_HOOK_POST_TURN", "/usr/bin/post")
	r := LoadFromEnv()
	if r.PreTurnCmd != "/usr/bin/pre" {
		t.Errorf("PreTurnCmd = %q", r.PreTurnCmd)
	}
	if r.PostTurnCmd != "/usr/bin/post" {
		t.Errorf("PostTurnCmd = %q", r.PostTurnCmd)
	}
	if !r.Configured() {
		t.Error("Runner with both hooks should be Configured")
	}
}

func TestLoadFromEnv_ParsesTimeout(t *testing.T) {
	t.Setenv("OCTO_HOOK_PRE_TURN", "/bin/echo")
	t.Setenv("OCTO_HOOK_TIMEOUT", "2s")
	r := LoadFromEnv()
	if r.Timeout != 2*time.Second {
		t.Errorf("Timeout = %v, want 2s", r.Timeout)
	}
}

func TestLoadFromEnv_TimeoutCeilingApplied(t *testing.T) {
	t.Setenv("OCTO_HOOK_PRE_TURN", "/bin/echo")
	t.Setenv("OCTO_HOOK_TIMEOUT", "10m") // way over ceiling
	r := LoadFromEnv()
	if r.Timeout > timeoutCeiling {
		t.Errorf("Timeout %v exceeded ceiling %v", r.Timeout, timeoutCeiling)
	}
}

func TestLoadFromEnv_InvalidTimeoutIgnored(t *testing.T) {
	t.Setenv("OCTO_HOOK_PRE_TURN", "/bin/echo")
	t.Setenv("OCTO_HOOK_TIMEOUT", "not-a-duration")
	r := LoadFromEnv()
	if r.Timeout != 0 {
		t.Errorf("invalid timeout should fall through to default, got %v", r.Timeout)
	}
}

func TestRunner_NilSafe(t *testing.T) {
	var r *Runner
	if r.Configured() {
		t.Error("nil Runner.Configured should be false")
	}
	out, err := r.Pre(context.Background(), "hi")
	if err != nil || out != "" {
		t.Errorf("nil Runner.Pre = (%q,%v); want ('',nil)", out, err)
	}
	if err := r.Post(context.Background(), "u", "a"); err != nil {
		t.Errorf("nil Runner.Post = %v; want nil", err)
	}
}

func TestRunner_NoHook_NoOp(t *testing.T) {
	r := &Runner{}
	out, err := r.Pre(context.Background(), "hi")
	if err != nil || out != "" {
		t.Errorf("unconfigured Pre should be no-op; got (%q,%v)", out, err)
	}
	if err := r.Post(context.Background(), "u", "a"); err != nil {
		t.Errorf("unconfigured Post should be no-op; got %v", err)
	}
}

// ── Pre-turn ─────────────────────────────────────────────────────────────

func TestPre_PlainTextStdout(t *testing.T) {
	script := makeScript(t, "echo 'hello from hook'")
	r := &Runner{PreTurnCmd: script}
	out, err := r.Pre(context.Background(), "user msg")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello from hook" {
		t.Errorf("Pre = %q", out)
	}
}

func TestPre_StructuredJSONStdout(t *testing.T) {
	script := makeScript(t, `echo '{"additional_context": "structured ctx"}'`)
	r := &Runner{PreTurnCmd: script}
	out, _ := r.Pre(context.Background(), "user msg")
	if out != "structured ctx" {
		t.Errorf("Pre with JSON = %q", out)
	}
}

func TestPre_EmptyStdoutNoContext(t *testing.T) {
	script := makeScript(t, "exit 0")
	r := &Runner{PreTurnCmd: script}
	out, err := r.Pre(context.Background(), "user msg")
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Errorf("empty stdout should yield empty context, got %q", out)
	}
}

func TestPre_StdinPayloadAvailable(t *testing.T) {
	// Echo stdin with a leading marker so parsePreOutput treats it as
	// raw text (it'd try JSON-decode if stdout started with '{').
	script := makeScript(t, `echo "RAW:"; cat`)
	r := &Runner{PreTurnCmd: script}
	out, _ := r.Pre(context.Background(), "the user's message")
	if !strings.Contains(out, "the user's message") {
		t.Errorf("stdin payload not delivered to hook: %q", out)
	}
}

func TestPre_ScriptFailureSurfacesAsError(t *testing.T) {
	script := makeScript(t, "echo oops >&2; exit 1")
	r := &Runner{PreTurnCmd: script}
	_, err := r.Pre(context.Background(), "user msg")
	if err == nil {
		t.Fatal("script failure should error")
	}
	if !strings.Contains(err.Error(), "stderr: oops") {
		t.Errorf("stderr tail should be folded into the error: %v", err)
	}
}

func TestPre_TimeoutKillsScript(t *testing.T) {
	script := makeScript(t, "sleep 5")
	r := &Runner{PreTurnCmd: script, Timeout: 100 * time.Millisecond}
	start := time.Now()
	_, err := r.Pre(context.Background(), "user msg")
	dur := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("slow script should time out, got %v", err)
	}
	// timeout (100ms) + WaitDelay (1s for pipe-close fallback) → ~1.1s
	// ceiling. Anything past that means kill/cleanup isn't working.
	if dur > 2*time.Second {
		t.Errorf("timeout took too long: %v (deadline was 100ms; WaitDelay adds ≤1s)", dur)
	}
}

// ── Post-turn ────────────────────────────────────────────────────────────

func TestPost_FireAndForget(t *testing.T) {
	// Use a script that writes a marker file so we can prove it ran.
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	script := makeScript(t, "touch "+marker)
	r := &Runner{PostTurnCmd: script}
	if err := r.Post(context.Background(), "u", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("post hook should have created marker file: %v", err)
	}
}

func TestPost_FailureReturnsErrorButDoesntPanic(t *testing.T) {
	script := makeScript(t, "exit 7")
	r := &Runner{PostTurnCmd: script}
	if err := r.Post(context.Background(), "u", "a"); err == nil {
		t.Error("Post script failure should error")
	}
}

func TestPost_PayloadIncludesReply(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "captured.json")
	script := makeScript(t, "cat > "+out)
	r := &Runner{PostTurnCmd: script}
	if err := r.Post(context.Background(), "the user msg", "the assistant reply"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, want := range []string{"the user msg", "the assistant reply"} {
		if !strings.Contains(body, want) {
			t.Errorf("captured payload missing %q:\n%s", want, body)
		}
	}
}

// ── InjectContext ────────────────────────────────────────────────────────

func TestInjectContext_EmptyAdditional(t *testing.T) {
	if got := InjectContext("just user", ""); got != "just user" {
		t.Errorf("empty additional → user passes through; got %q", got)
	}
	if got := InjectContext("just user", "   "); got != "just user" {
		t.Errorf("whitespace-only additional should be no-op; got %q", got)
	}
}

func TestInjectContext_PreservesUserAndAppendsContext(t *testing.T) {
	got := InjectContext("hi there", "extra info from Hindsight")
	if !strings.HasPrefix(got, "hi there") {
		t.Errorf("user input should lead: %q", got)
	}
	if !strings.Contains(got, "extra info from Hindsight") {
		t.Errorf("additional context should be appended: %q", got)
	}
	if !strings.Contains(got, "---") {
		t.Errorf("divider should be present: %q", got)
	}
}
