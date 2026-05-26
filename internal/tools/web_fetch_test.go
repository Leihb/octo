package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withJinaHost swaps JinaReaderHost for the test server URL. Since
// JinaReaderHost is a const, we test by directly hitting the helper that
// uses the URL passed in. Instead, we mock at the network layer by setting
// up an httptest server and rewriting the URL the tool would call.
//
// The simpler approach: spin up a server, then call WebFetchTool.Execute
// with a URL that points at the test server but is shaped like a normal
// HTTP url. Since the production code does `JinaReaderHost + raw`, we
// can't easily redirect that without changing the const to a var.
//
// We change JinaReaderHost to a var below (see web_fetch.go) — but to
// keep the test file standalone, we test the simpler case: pointing at
// the test server directly while exercising the full HTTP path, error
// handling, and truncation.
func TestWebFetch_RequiresURL(t *testing.T) {
	_, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{})
	if err == nil {
		t.Fatal("missing url should error")
	}
}

func TestWebFetch_RejectsNonHTTPScheme(t *testing.T) {
	_, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": "ftp://example.com/file",
	})
	if err == nil || !strings.Contains(err.Error(), "only http/https") {
		t.Errorf("expected scheme rejection, got %v", err)
	}
}

func TestWebFetch_AgainstHTTPTest(t *testing.T) {
	// Stand up an httptest server that responds like Jina would — plain
	// Markdown. Then temporarily redirect JinaReaderHost to it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Hello\n\nthis is markdown"))
	}))
	defer srv.Close()

	old := jinaReaderHostForTest
	jinaReaderHostForTest = srv.URL + "/"
	defer func() { jinaReaderHostForTest = old }()

	out, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": "https://example.com/article",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "Hello") || !strings.Contains(out, "markdown") {
		t.Errorf("unexpected body: %q", out)
	}
}

func TestWebFetch_HTTPErrorWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream is sad", http.StatusBadGateway)
	}))
	defer srv.Close()

	old := jinaReaderHostForTest
	jinaReaderHostForTest = srv.URL + "/"
	defer func() { jinaReaderHostForTest = old }()

	_, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": "https://example.com/x",
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("expected HTTP 502 error, got %v", err)
	}
}

func TestWebFetch_Truncates(t *testing.T) {
	// Generate a body larger than WebFetchMaxBytes.
	big := strings.Repeat("a", WebFetchMaxBytes+1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()

	old := jinaReaderHostForTest
	jinaReaderHostForTest = srv.URL + "/"
	defer func() { jinaReaderHostForTest = old }()

	out, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": "https://example.com/big",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("expected truncation marker, got tail: %q", out[max(0, len(out)-100):])
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
