package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session is a named conversation that can be saved to and loaded from disk.
// It wraps History with metadata needed to resume later.
type Session struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Model     string    `json:"model"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
}

// NewSession creates a Session with an ID derived from the current time plus
// a short random suffix: YYYYMMDD-HHMMSS-xxxx. The timestamp keeps IDs
// roughly sortable and human-readable; the suffix removes the same-second
// collision that would otherwise let two sessions overwrite each other's
// file (e.g. two quick `octo chat` launches, or anything non-interactive).
func NewSession(model, system string) *Session {
	now := time.Now()
	return &Session{
		ID:        now.Format("20060102-150405") + "-" + randomSuffix(now),
		CreatedAt: now,
		UpdatedAt: now,
		Model:     model,
		System:    system,
	}
}

// randomSuffix returns 4 hex chars from crypto/rand. If the system RNG is
// somehow unavailable it falls back to the sub-second nanosecond fraction,
// which still disambiguates same-second IDs from a single process.
func randomSuffix(now time.Time) string {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%04x", now.Nanosecond()&0xffff)
	}
	return hex.EncodeToString(b[:])
}

// TurnCount returns the number of complete user+assistant turn pairs.
func (s *Session) TurnCount() int {
	return len(s.Messages) / 2
}

// sessionsDir returns (and creates if needed) ~/.octo/sessions.
func sessionsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("session: home dir: %w", err)
	}
	dir := filepath.Join(home, ".octo", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("session: mkdir %s: %w", dir, err)
	}
	return dir, nil
}

// SavePath returns the path where this session would be saved.
func (s *Session) SavePath() (string, error) {
	dir, err := sessionsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, s.ID+".json"), nil
}

// Save writes the session to ~/.octo/sessions/<id>.json.
func (s *Session) Save() error {
	s.UpdatedAt = time.Now()
	path, err := s.SavePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("session: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("session: write %s: %w", path, err)
	}
	return nil
}

// LoadSession reads ~/.octo/sessions/<id>.json.
// The id may be the bare YYYYMMDD-HHMMSS string or an absolute path.
func LoadSession(id string) (*Session, error) {
	var path string
	if filepath.IsAbs(id) {
		path = id
	} else {
		// Strip .json suffix if user passed it by mistake.
		id = strings.TrimSuffix(id, ".json")
		dir, err := sessionsDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(dir, id+".json")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("session %q not found", id)
		}
		return nil, fmt.Errorf("session: read %s: %w", path, err)
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("session: parse %s: %w", path, err)
	}
	return &s, nil
}

// ListSessions returns up to n most-recently-updated sessions from
// ~/.octo/sessions/, newest first.
func ListSessions(n int) ([]*Session, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("session: readdir %s: %w", dir, err)
	}

	var sessions []*Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := LoadSession(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip corrupt files
		}
		sessions = append(sessions, s)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	if n > 0 && len(sessions) > n {
		sessions = sessions[:n]
	}
	return sessions, nil
}

// ToHistory converts the session's message slice into an agent History.
func (s *Session) ToHistory() *History {
	h := NewHistory()
	for _, m := range s.Messages {
		h.Append(m)
	}
	return h
}

// SyncFrom copies the current messages from h into the session.
// Call this before Save to flush the latest turns.
func (s *Session) SyncFrom(h *History) {
	s.Messages = h.Snapshot()
}
