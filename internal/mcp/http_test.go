package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPTransport_RoundtripWithSessionHeader(t *testing.T) {
	// Server echoes back a canned "result". On the first request it also
	// sets Mcp-Session-Id; subsequent requests must echo it back.
	var seenSession string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var in Message
		_ = json.Unmarshal(body, &in)

		// Record / verify the session id round-trip.
		if seenSession == "" {
			w.Header().Set("Mcp-Session-Id", "session-42")
			seenSession = "session-42"
		} else if got := r.Header.Get("Mcp-Session-Id"); got != seenSession {
			t.Errorf("expected session header %q, got %q", seenSession, got)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Reply matches the request id.
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0",
			ID:      in.ID,
			Result:  json.RawMessage(`{"ok":true}`),
		})
	}))
	defer srv.Close()

	tx, err := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// First request — server should set the session id.
	req1 := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(ctx, req1); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(ctx); err != nil {
		t.Fatal(err)
	}

	// Second request — client should echo session id; the server above
	// asserts on a mismatch.
	req2 := &Message{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"}
	if err := tx.Send(ctx, req2); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPTransport_ErrorStatusSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("kaboom"))
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()
	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	err := tx.Send(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for non-2xx response")
	}
}

func TestHTTPTransport_HeadersPassedThrough(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Message{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`1`),
			Result:  json.RawMessage(`{}`),
		})
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer abc"},
	})
	defer tx.Close()

	req := &Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/list"}
	if err := tx.Send(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Receive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seenAuth != "Bearer abc" {
		t.Errorf("Authorization header = %q, want Bearer abc", seenAuth)
	}
}

func TestHTTPTransport_NotificationGet204(t *testing.T) {
	// Server returns 204 No Content — this is the path for an accepted
	// notification (no body to parse).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	tx, _ := NewHTTPTransport(HTTPConfig{URL: srv.URL})
	defer tx.Close()

	notif := &Message{JSONRPC: "2.0", Method: "notifications/initialized"}
	if err := tx.Send(context.Background(), notif); err != nil {
		t.Errorf("notification Send returned error: %v", err)
	}
}

func TestNewHTTPTransport_EmptyURLRejected(t *testing.T) {
	_, err := NewHTTPTransport(HTTPConfig{})
	if err == nil {
		t.Error("expected error for empty URL")
	}
}
