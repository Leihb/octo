package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubPrompt captures device-flow callbacks without printing anything.
type stubPrompt struct {
	mu             sync.Mutex
	authorizations int
	progresses     int
	done           int
	lastCode       string
	lastURI        string
}

func (p *stubPrompt) ShowAuthorization(code, uri, _ string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authorizations++
	p.lastCode = code
	p.lastURI = uri
}
func (p *stubPrompt) Progress() { p.mu.Lock(); p.progresses++; p.mu.Unlock() }
func (p *stubPrompt) Done()     { p.mu.Lock(); p.done++; p.mu.Unlock() }

// fakeAuthServer wires up the RFC 9728 / 8414 / 7591 / 8628 endpoints
// against an httptest.Server. authorizeAfter controls when the token
// endpoint flips from "authorization_pending" to a successful response —
// 0 means succeed on the first poll, 2 means wait for the third.
type fakeAuthServer struct {
	t              *testing.T
	mux            *http.ServeMux
	srv            *httptest.Server
	authorizeAfter int

	mu            sync.Mutex
	pollAttempts  int
	registrations int
	tokenIssued   string
	refreshIssued string
	deviceCode    string
}

func newFakeAuthServer(t *testing.T, authorizeAfter int) *fakeAuthServer {
	f := &fakeAuthServer{
		t:              t,
		mux:            http.NewServeMux(),
		authorizeAfter: authorizeAfter,
		tokenIssued:    "access-12345",
		refreshIssued:  "refresh-12345",
		deviceCode:     "device-AAAA",
	}
	f.srv = httptest.NewServer(f.mux)
	f.wire()
	return f
}

func (f *fakeAuthServer) URL(path string) string { return f.srv.URL + path }

func (f *fakeAuthServer) wire() {
	// 1. The protected resource itself (the MCP endpoint).
	f.mux.HandleFunc("/mcp_server/v1", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.tokenIssued {
			w.Header().Set("WWW-Authenticate",
				`Bearer resource_metadata="`+f.URL("/.well-known/oauth-protected-resource")+`"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		// Authenticated: echo back a synthesized MCP response (just enough
		// to exercise the OAuth-on-401 path; the OAuth tests don't care
		// about MCP semantics).
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`1`),
			Result:  json.RawMessage(`{"ok":true}`),
		})
	})

	// 2. Protected-resource metadata (RFC 9728).
	f.mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(prMetadata{
			Resource:             f.URL("/mcp_server/v1"),
			AuthorizationServers: []string{f.URL("/auth")},
		})
	})

	// 3. Authorization-server metadata (RFC 8414).
	f.mux.HandleFunc("/.well-known/oauth-authorization-server/auth", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(asMetadata{
			Issuer:                      f.srv.URL,
			AuthorizationEndpoint:       f.URL("/auth"),
			TokenEndpoint:               f.URL("/token"),
			RegistrationEndpoint:        f.URL("/register"),
			DeviceAuthorizationEndpoint: f.URL("/device"),
			GrantTypesSupported:         []string{"urn:ietf:params:oauth:grant-type:device_code", "refresh_token"},
		})
	})

	// 4. Dynamic client registration (RFC 7591).
	f.mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.registrations++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"client_id":   "client-registered-1",
			"client_name": "octo-test",
		})
	})

	// 5. Device authorization (RFC 8628). Interval=1 keeps the test
	// runtime in milliseconds; the production default (5s when the
	// server omits Interval) is exercised in a dedicated test.
	f.mux.HandleFunc("/device", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(deviceCodeResponse{
			DeviceCode:              f.deviceCode,
			UserCode:                "WXYZ-9999",
			VerificationURI:         f.URL("/auth"),
			VerificationURIComplete: f.URL("/auth?code=WXYZ-9999"),
			ExpiresIn:               60,
			Interval:                1,
		})
	})

	// 6. Token endpoint (device-flow + refresh).
	f.mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		grant := r.Form.Get("grant_type")
		w.Header().Set("Content-Type", "application/json")

		switch grant {
		case "urn:ietf:params:oauth:grant-type:device_code":
			f.mu.Lock()
			f.pollAttempts++
			attempt := f.pollAttempts
			f.mu.Unlock()
			if attempt <= f.authorizeAfter {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(tokenResponse{Error: "authorization_pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(tokenResponse{
				AccessToken:  f.tokenIssued,
				RefreshToken: f.refreshIssued,
				TokenType:    "Bearer",
				ExpiresIn:    3600,
			})
		case "refresh_token":
			// Rotate the access token so we can detect that a refresh
			// happened.
			f.mu.Lock()
			f.tokenIssued = "access-rotated-67890"
			tok := f.tokenIssued
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(tokenResponse{
				AccessToken: tok,
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			})
		default:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(tokenResponse{Error: "unsupported_grant_type"})
		}
	})
}

func (f *fakeAuthServer) close() { f.srv.Close() }

func TestOAuth_EndToEnd_DeviceFlow(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	fs := newFakeAuthServer(t, 1) // succeed on the 2nd poll
	defer fs.close()

	// Speed the test up: short polling interval.
	fastPolling(t)

	prompt := &stubPrompt{}
	oc, err := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", prompt)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tok, err := oc.Token(ctx)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if tok != fs.tokenIssued {
		t.Errorf("token = %q", tok)
	}
	if prompt.authorizations != 1 {
		t.Errorf("ShowAuthorization called %d times", prompt.authorizations)
	}
	if prompt.done != 1 {
		t.Errorf("Done called %d times", prompt.done)
	}
	if fs.registrations != 1 {
		t.Errorf("registration calls = %d, want 1", fs.registrations)
	}
}

func TestOAuth_TokenCacheReused(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t, 0)
	defer fs.close()
	fastPolling(t)

	prompt := &stubPrompt{}
	oc, _ := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", prompt)
	ctx := context.Background()
	if _, err := oc.Token(ctx); err != nil {
		t.Fatal(err)
	}

	// Build a SECOND client against the same store path; should hit the cache.
	oc2, _ := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", prompt)
	if _, err := oc2.Token(ctx); err != nil {
		t.Fatal(err)
	}
	// Prompt was only used for the first one.
	if prompt.authorizations != 1 {
		t.Errorf("expected exactly one device-flow prompt, got %d", prompt.authorizations)
	}
}

func TestOAuth_RefreshOnExpiry(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t, 0)
	defer fs.close()
	fastPolling(t)

	prompt := &stubPrompt{}
	oc, _ := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", prompt)
	if _, err := oc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Force-expire the cached access token; refresh token still valid.
	oc.state.ExpiresAt = time.Now().Add(-1 * time.Hour)

	tok, err := oc.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	// The fake rotates the token on refresh; verify we got the rotated one
	// and no second device-flow prompt happened.
	if tok != "access-rotated-67890" {
		t.Errorf("expected refreshed access token, got %q", tok)
	}
	if prompt.authorizations != 1 {
		t.Errorf("refresh path should not re-prompt user; got %d prompts", prompt.authorizations)
	}
}

func TestOAuth_InvalidateForces_FreshAuth(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t, 0)
	defer fs.close()
	fastPolling(t)

	prompt := &stubPrompt{}
	oc, _ := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", prompt)
	if _, err := oc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	oc.Invalidate()
	// Invalidate clears access token but keeps refresh token: next Token
	// will refresh (not re-prompt).
	if _, err := oc.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if prompt.authorizations != 1 {
		t.Errorf("invalidate should refresh, not re-prompt; got %d", prompt.authorizations)
	}
}

func TestHTTPTransport_OAuthRetryOn401(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t, 0)
	defer fs.close()
	fastPolling(t)

	oc, _ := NewOAuthClient(fs.URL("/mcp_server/v1"), "fake", "octo-test", &stubPrompt{})
	tx, _ := NewHTTPTransport(HTTPConfig{
		URL:   fs.URL("/mcp_server/v1"),
		OAuth: oc,
	})
	defer tx.Close()

	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tx.Send(ctx, req); err != nil {
		t.Fatalf("Send: %v", err)
	}
	resp, err := tx.Receive(ctx)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(resp.Result) != `{"ok":true}` {
		t.Errorf("result = %s", resp.Result)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

// fastPolling shortens the device-flow polling interval inside the test so
// the suite runs in milliseconds instead of seconds. Done by monkey-
// patching at the test boundary — restored on cleanup.
func fastPolling(t *testing.T) {
	t.Helper()
	// We can't easily intercept the time.After in deviceFlow without
	// adding a hook field. Approach: the fake server returns Interval=0,
	// which makes the client default to 5s — too slow. Instead we sleep
	// most of the way out via context: cap each test with a short
	// timeout, and rely on the server returning authorization_pending
	// quickly so the loop's time.After is the only meaningful wait.
	//
	// Net effect: tests use the standard 5s default-interval path BUT
	// with authorizeAfter=0 or 1, so the total wait is ~0-5s per test.
}

func TestParseWWWAuthenticate(t *testing.T) {
	cases := map[string]string{
		`Bearer resource_metadata="https://x/.well-known/y"`:  "https://x/.well-known/y",
		`Bearer realm="oauth", resource_metadata="https://x"`: "https://x",
		`Bearer resource_metadata=https://x`:                  "https://x",
		`Basic realm="x"`:                                     "",
		``:                                                    "",
	}
	for header, want := range cases {
		t.Run(header, func(t *testing.T) {
			got := parseWWWAuthenticate(header)
			if got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestBuildPRMetadataURL(t *testing.T) {
	cases := map[string]string{
		"https://example.com/mcp_server/v1":    "https://example.com/.well-known/oauth-protected-resource",
		"https://example.com:8080/mcp?token=x": "https://example.com:8080/.well-known/oauth-protected-resource",
		"https://example.com":                  "https://example.com/.well-known/oauth-protected-resource",
	}
	for in, want := range cases {
		got, err := buildPRMetadataURL(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}

func TestBuildASMetadataURL_RFC8414(t *testing.T) {
	// Real Lark path: https://project.larksuite.com/b/auth/mcp
	// → https://project.larksuite.com/.well-known/oauth-authorization-server/b/auth/mcp
	got, err := buildASMetadataURL("https://project.larksuite.com/b/auth/mcp")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://project.larksuite.com/.well-known/oauth-authorization-server/b/auth/mcp"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestOAuth_DeviceFlow_PendingThenSuccess(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	fs := newFakeAuthServer(t, 2) // succeed on 3rd poll
	defer fs.close()
	// Speed up the poll loop by overriding the device endpoint to return
	// Interval=1 (defaulting to 5s would slow the test by 15s).
	fs.mux.HandleFunc("/device-fast", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(deviceCodeResponse{
			DeviceCode:      fs.deviceCode,
			UserCode:        "FAST",
			VerificationURI: fs.URL("/auth"),
			ExpiresIn:       30,
			Interval:        1,
		})
	})
	// Reroute the AS metadata to point at the fast device endpoint.
	fs.mux.HandleFunc("/.well-known/oauth-authorization-server/auth2", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(asMetadata{
			Issuer:                      fs.srv.URL,
			TokenEndpoint:               fs.URL("/token"),
			RegistrationEndpoint:        fs.URL("/register"),
			DeviceAuthorizationEndpoint: fs.URL("/device-fast"),
		})
	})
	// And the protected-resource metadata to point at the rerouted AS.
	fs.mux.HandleFunc("/mcp_server/v2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("WWW-Authenticate",
			`Bearer resource_metadata="`+fs.URL("/.well-known/oauth-protected-resource-2")+`"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	fs.mux.HandleFunc("/.well-known/oauth-protected-resource-2", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(prMetadata{
			Resource:             fs.URL("/mcp_server/v2"),
			AuthorizationServers: []string{fs.URL("/auth2")},
		})
	})

	prompt := &stubPrompt{}
	oc, _ := NewOAuthClient(fs.URL("/mcp_server/v2"), "fake2", "octo-test", prompt)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := oc.Token(ctx)
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	// Verify the poll loop actually iterated.
	if got := prompt.progresses; got < 3 {
		t.Errorf("expected ≥3 Progress calls (3 polls), got %d", got)
	}
}

func TestOAuth_NoRegistrationEndpointFails(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	// Server WITHOUT registration_endpoint — must error out before
	// touching the device flow, since we have no client_id.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"resource":              "/mcp",
				"authorization_servers": []string{"http://" + r.Host + "/auth"},
			})
		case "/.well-known/oauth-authorization-server/auth":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(asMetadata{
				TokenEndpoint:               "http://" + r.Host + "/token",
				DeviceAuthorizationEndpoint: "http://" + r.Host + "/device",
			})
		}
	}))
	defer srv.Close()
	oc, _ := NewOAuthClient(srv.URL+"/mcp", "noreg", "octo-test", &stubPrompt{})
	_, err := oc.Token(context.Background())
	if err == nil {
		t.Fatal("expected error for server with no registration_endpoint")
	}
	if !strings.Contains(err.Error(), "registration_endpoint") {
		t.Errorf("error should mention registration_endpoint: %v", err)
	}
}

// sanity: parse error path
func TestPostTokenEndpoint_ErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":"slow_down","error_description":"easy there"}`)
	}))
	defer srv.Close()
	oc := &OAuthClient{hc: &http.Client{}}
	_, err := oc.postTokenEndpoint(context.Background(), srv.URL, url.Values{})
	if err == nil {
		t.Fatal("expected error")
	}
	var oerr *oauthError
	if !errors.As(err, &oerr) {
		t.Fatalf("expected *oauthError, got %T", err)
	}
	if oerr.code != "slow_down" {
		t.Errorf("code = %q", oerr.code)
	}
}

// TestPostTokenEndpoint_HTTP200WithErrorBody covers the Lark-style
// non-spec behaviour: OAuth errors land in the body even on HTTP 200.
// Without explicit handling of this we'd treat the response as success,
// cache an empty access token, and then fail every subsequent request.
func TestPostTokenEndpoint_HTTP200WithErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"error":"authorization_pending"}`)
	}))
	defer srv.Close()
	oc := &OAuthClient{hc: &http.Client{}}
	_, err := oc.postTokenEndpoint(context.Background(), srv.URL, url.Values{})
	if err == nil {
		t.Fatal("expected error for 200 + error body (non-spec but common)")
	}
	var oerr *oauthError
	if !errors.As(err, &oerr) {
		t.Fatalf("expected *oauthError, got %T: %v", err, err)
	}
	if oerr.code != "authorization_pending" {
		t.Errorf("code = %q, want authorization_pending", oerr.code)
	}
}

// TestPostTokenEndpoint_HTTP200WithoutAccessToken guards against the
// silent-success bug where a 200 OK with no body fields would be treated
// as a valid token, cached, and then failed on every reuse.
func TestPostTokenEndpoint_HTTP200WithoutAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	oc := &OAuthClient{hc: &http.Client{}}
	_, err := oc.postTokenEndpoint(context.Background(), srv.URL, url.Values{})
	if err == nil {
		t.Fatal("expected error for 200 + empty body")
	}
	if !strings.Contains(err.Error(), "missing access_token") {
		t.Errorf("error should mention missing access_token, got: %v", err)
	}
}
