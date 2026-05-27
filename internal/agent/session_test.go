package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// setTempHome redirects both HOME (Unix) and USERPROFILE (Windows) to tmp so
// sessionsDir() stays inside the test sandbox on all platforms.
func setTempHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp) // Windows: os.UserHomeDir() reads USERPROFILE
	return tmp
}

func TestNewSession(t *testing.T) {
	s := NewSession("claude-haiku-4-5-20251001", "be helpful")
	if s.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if s.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("model = %q, want %q", s.Model, "claude-haiku-4-5-20251001")
	}
	if s.System != "be helpful" {
		t.Errorf("system = %q, want %q", s.System, "be helpful")
	}
	if s.CreatedAt.IsZero() {
		t.Error("CreatedAt must be set")
	}
}

func TestSessionIDFormat(t *testing.T) {
	s := NewSession("m", "")
	// YYYYMMDD-HHMMSS-xxxxxxxx — 24 chars (15 timestamp + '-' + 8 hex suffix).
	if len(s.ID) != 24 {
		t.Errorf("ID len = %d, want 24 (got %q)", len(s.ID), s.ID)
	}
	if s.ID[8] != '-' || s.ID[15] != '-' {
		t.Errorf("ID separators wrong: %q", s.ID)
	}
}

func TestSessionID_NoCollisionSameSecond(t *testing.T) {
	// Two sessions created back-to-back (same second) must get distinct IDs,
	// so their save files don't clobber each other.
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := NewSession("m", "").ID
		if seen[id] {
			t.Fatalf("duplicate session ID within the same second: %q", id)
		}
		seen[id] = true
	}
}

func TestTurnCount(t *testing.T) {
	s := NewSession("m", "")
	if s.TurnCount() != 0 {
		t.Errorf("TurnCount = %d, want 0", s.TurnCount())
	}
	s.Messages = []Message{
		NewUserMessage("hi"),
		NewAssistantMessage("hello"),
		NewUserMessage("bye"),
		NewAssistantMessage("goodbye"),
	}
	if s.TurnCount() != 2 {
		t.Errorf("TurnCount = %d, want 2", s.TurnCount())
	}
}

func TestSaveAndLoad(t *testing.T) {
	// Point sessions dir at a temp directory so we don't pollute ~/.octo.
	setTempHome(t)

	s := NewSession("model-x", "sys")
	s.Messages = []Message{
		NewUserMessage("ping"),
		NewAssistantMessage("pong"),
	}

	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadSession(s.ID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	if got.ID != s.ID {
		t.Errorf("ID: got %q, want %q", got.ID, s.ID)
	}
	if got.Model != "model-x" {
		t.Errorf("Model: got %q, want %q", got.Model, "model-x")
	}
	if len(got.Messages) != 2 {
		t.Errorf("Messages len: got %d, want 2", len(got.Messages))
	}
	if got.Messages[0].Content != "ping" {
		t.Errorf("Messages[0].Content: got %q, want %q", got.Messages[0].Content, "ping")
	}
}

func TestLoadSession_NotFound(t *testing.T) {
	setTempHome(t)

	_, err := LoadSession("19991231-235959")
	if err == nil {
		t.Fatal("expected error for missing session")
	}
}

func TestLoadSession_StripDotJSON(t *testing.T) {
	setTempHome(t)

	s := NewSession("m", "")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadSession(s.ID + ".json")
	if err != nil {
		t.Fatalf("LoadSession with .json suffix: %v", err)
	}
	if got.ID != s.ID {
		t.Errorf("ID mismatch: got %q, want %q", got.ID, s.ID)
	}
}

func TestListSessions(t *testing.T) {
	tmp := setTempHome(t)

	// Write 3 sessions with distinct UpdatedAt so ordering is deterministic.
	for i := 0; i < 3; i++ {
		s := &Session{
			ID:        "20260101-00000" + string(rune('0'+i)),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now().Add(time.Duration(i) * time.Second),
			Model:     "m",
		}
		dir := filepath.Join(tmp, ".octo", "sessions")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(s)
		if err := os.WriteFile(filepath.Join(dir, s.ID+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	sessions, err := ListSessions(10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("got %d sessions, want 3", len(sessions))
	}
	// Newest first — UpdatedAt +2s is index 2 in creation but index 0 in list.
	if sessions[0].ID != "20260101-000002" {
		t.Errorf("sessions[0].ID = %q, want %q", sessions[0].ID, "20260101-000002")
	}
}

func TestListSessions_Limit(t *testing.T) {
	tmp := setTempHome(t)

	dir := filepath.Join(tmp, ".octo", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		s := &Session{
			ID:        "20260101-00000" + string(rune('0'+i)),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			Model:     "m",
		}
		data, _ := json.Marshal(s)
		os.WriteFile(filepath.Join(dir, s.ID+".json"), data, 0o600)
	}

	sessions, err := ListSessions(3)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 3 {
		t.Errorf("got %d sessions with limit 3", len(sessions))
	}
}

func TestSyncFrom(t *testing.T) {
	s := NewSession("m", "")
	h := NewHistory()
	h.Append(NewUserMessage("hello"))
	h.Append(NewAssistantMessage("world"))

	s.SyncFrom(h)

	if len(s.Messages) != 2 {
		t.Errorf("Messages len = %d, want 2", len(s.Messages))
	}
}

func TestToHistory(t *testing.T) {
	s := NewSession("m", "")
	s.Messages = []Message{
		NewUserMessage("a"),
		NewAssistantMessage("b"),
	}
	h := s.ToHistory()
	if h.Len() != 2 {
		t.Errorf("History.Len() = %d, want 2", h.Len())
	}
	snap := h.Snapshot()
	if snap[0].Content != "a" {
		t.Errorf("snap[0].Content = %q, want %q", snap[0].Content, "a")
	}
}
