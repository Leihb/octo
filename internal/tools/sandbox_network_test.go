//go:build darwin || linux

package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Leihb/octo-agent/internal/sandbox"
)

// TestWebFetch_SandboxBlocksNetwork verifies that when a sandbox with
// AllowNetwork=false is active, web_fetch returns a clear error instead of
// attempting the request.
func TestWebFetch_SandboxBlocksNetwork(t *testing.T) {
	if !sandbox.Available() {
		t.Skip("no OS sandbox available on this host")
	}
	cwd := t.TempDir()
	p := sandbox.DefaultPolicy(cwd)
	p.AllowNetwork = false
	SetSandbox(&p)
	defer SetSandbox(nil)

	_, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": "https://example.com",
	})
	if err == nil {
		t.Fatal("expected error when network is sandboxed")
	}
	if !strings.Contains(err.Error(), "network access is disabled by sandbox") {
		t.Errorf("expected sandbox error, got: %v", err)
	}
}

// TestWebFetch_SandboxAllowsNetwork verifies that when a sandbox with
// AllowNetwork=true is active, web_fetch works normally.
func TestWebFetch_SandboxAllowsNetwork(t *testing.T) {
	if !sandbox.Available() {
		t.Skip("no OS sandbox available on this host")
	}
	cwd := t.TempDir()
	p := sandbox.DefaultPolicy(cwd)
	p.AllowNetwork = true
	SetSandbox(&p)
	defer SetSandbox(nil)

	// Point Jina at a local server so we don't need real network.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# allowed"))
	}))
	defer srv.Close()

	old := jinaReaderHostForTest
	jinaReaderHostForTest = srv.URL + "/"
	defer func() { jinaReaderHostForTest = old }()

	out, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": "https://example.com",
	})
	if err != nil {
		t.Fatalf("expected success when network is allowed, got: %v", err)
	}
	if !strings.Contains(out.Text, "allowed") {
		t.Errorf("expected 'allowed' in response, got: %q", out.Text)
	}
}

// TestWebSearch_SandboxBlocksNetwork verifies that when a sandbox with
// AllowNetwork=false is active, web_search returns a clear error.
func TestWebSearch_SandboxBlocksNetwork(t *testing.T) {
	if !sandbox.Available() {
		t.Skip("no OS sandbox available on this host")
	}
	cwd := t.TempDir()
	p := sandbox.DefaultPolicy(cwd)
	p.AllowNetwork = false
	SetSandbox(&p)
	defer SetSandbox(nil)

	_, err := WebSearchTool{}.Execute(context.Background(), "web_search", map[string]any{
		"query": "test",
	})
	if err == nil {
		t.Fatal("expected error when network is sandboxed")
	}
	if !strings.Contains(err.Error(), "network access is disabled by sandbox") {
		t.Errorf("expected sandbox error, got: %v", err)
	}
}
