package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
)

// JinaReaderHost is the public Jina AI Reader endpoint. Sending a URL via
// `https://r.jina.ai/<URL>` returns the page rendered to Markdown — handles
// JavaScript-rendered pages, paywalls (where Jina has access), and most
// of the noisy chrome a raw curl would dump on the LLM.
const JinaReaderHost = "https://r.jina.ai/"

// jinaReaderHostForTest is the actual host the tool uses. Tests swap it
// out to a local httptest server; production reads the const default.
var jinaReaderHostForTest = JinaReaderHost

// WebFetchMaxBytes caps the response size returned to the LLM. Past this,
// the body is truncated with a clear marker so the LLM can ask for more
// targeted slices via grep / a follow-up fetch.
const WebFetchMaxBytes = 200_000

// WebFetchTool fetches a URL and returns its body as Markdown (via the
// Jina AI Reader proxy). The LLM is expected to read the returned text
// directly — there's no second-stage extraction model in this tool.
type WebFetchTool struct{}

func (WebFetchTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "web_fetch",
		Description: "Fetch a URL and return its content as Markdown. Uses the Jina " +
			"Reader proxy to handle JS-rendered pages and convert HTML cleanly. " +
			"Response is truncated at ~200 KB; for long pages, fetch a more specific URL " +
			"or grep the returned text. Public web only — no authentication.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "Full URL to fetch (http or https).",
				},
			},
			"required": []string{"url"},
		},
	}
}

func (WebFetchTool) Execute(ctx context.Context, _ string, input map[string]any) (string, error) {
	raw, _ := input["url"].(string)
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("web_fetch: url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("web_fetch: parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("web_fetch: only http/https URLs are allowed (got %q)", u.Scheme)
	}

	jinaURL := jinaReaderHostForTest + raw

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jinaURL, nil)
	if err != nil {
		return "", fmt.Errorf("web_fetch: build request: %w", err)
	}
	req.Header.Set("Accept", "text/markdown,text/plain,*/*")
	// Identify ourselves so Jina can reach us about issues.
	req.Header.Set("User-Agent", "octo-agent/web_fetch")

	resp, err := webHTTPClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("web_fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("web_fetch: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, WebFetchMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("web_fetch: read body: %w", err)
	}
	truncated := false
	if len(body) > WebFetchMaxBytes {
		body = body[:WebFetchMaxBytes]
		truncated = true
	}

	out := string(body)
	if truncated {
		out += "\n\n…[truncated at " + strconvItoa(WebFetchMaxBytes) + " bytes]"
	}
	return out, nil
}

// strconvItoa is a tiny local alias to avoid adding an import just for one
// integer-to-string call in the truncation message. (Same as strconv.Itoa.)
func strconvItoa(n int) string {
	// fmt.Sprintf is fine; the simplicity beats a manual loop here.
	return fmt.Sprintf("%d", n)
}

// webHTTPClient is the shared http.Client used by web_fetch and the
// network backends of web_search. Default Go client has no timeout — we
// set 30s to keep agents responsive.
func webHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}
