package channel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
)

// mockAdapter is a test double that records sent messages.
type mockAdapter struct {
	platform      string
	mu            sync.Mutex
	sentTexts     []sentText
	sentFiles     []sentFile
	updatedMsgs   []updatedMsg
	started       bool
	stopped       bool
	validateErrs  []string
	onMessageFunc func(InboundEvent)
}

type sentText struct {
	chatID  string
	text    string
	replyTo string
}
type sentFile struct {
	chatID  string
	path    string
	name    string
	replyTo string
}
type updatedMsg struct {
	chatID    string
	messageID string
	text      string
}

func (m *mockAdapter) Platform() string { return m.platform }
func (m *mockAdapter) Start(ctx context.Context, onMessage func(InboundEvent)) error {
	m.started = true
	m.onMessageFunc = onMessage
	<-ctx.Done()
	return ctx.Err()
}
func (m *mockAdapter) Stop() error {
	m.stopped = true
	return nil
}
func (m *mockAdapter) SendText(chatID, text, replyTo string) SendResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentTexts = append(m.sentTexts, sentText{chatID, text, replyTo})
	return SendResult{OK: true}
}
func (m *mockAdapter) SendFile(chatID, path, name, replyTo string) SendResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentFiles = append(m.sentFiles, sentFile{chatID, path, name, replyTo})
	return SendResult{OK: true}
}
func (m *mockAdapter) UpdateMessage(chatID, messageID, text string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updatedMsgs = append(m.updatedMsgs, updatedMsg{chatID, messageID, text})
	return true
}
func (m *mockAdapter) SupportsMessageUpdates() bool               { return true }
func (m *mockAdapter) ValidateConfig(cfg PlatformConfig) []string { return m.validateErrs }

func (m *mockAdapter) sentTextCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sentTexts)
}
func (m *mockAdapter) lastSentText() sentText {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sentTexts) == 0 {
		return sentText{}
	}
	return m.sentTexts[len(m.sentTexts)-1]
}

func fakeAgentFactory() *agent.Agent {
	return agent.New(fakeSender{}, "test-model")
}

type fakeSender struct{}

func (f fakeSender) SendMessages(ctx context.Context, model, system string, messages []agent.Message, maxTokens int) (agent.Reply, error) {
	return agent.Reply{Content: "ok"}, nil
}

func TestManager_StartStop(t *testing.T) {
	Register("mock", func(pc PlatformConfig) (Adapter, error) {
		return &mockAdapter{platform: "mock"}, nil
	})

	cfg := &Config{
		Channels: map[string]PlatformConfig{
			"mock": {"enabled": true},
		},
	}
	mgr := NewManager(cfg, fakeAgentFactory, BindByChatUser)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Start blocks; run it in a goroutine.
	done := make(chan error, 1)
	go func() { done <- mgr.Start(ctx) }()

	time.Sleep(50 * time.Millisecond)
	if !mgr.IsRunning() {
		t.Fatal("expected manager to be running")
	}

	mgr.Stop()
	<-done

	if mgr.IsRunning() {
		t.Fatal("expected manager to be stopped")
	}
}

func TestManager_SessionBindingModes(t *testing.T) {
	ev := InboundEvent{Platform: "mock", ChatID: "chat1", UserID: "user1"}

	tests := []struct {
		mode BindingMode
		want SessionKey
	}{
		{BindByChatUser, "mock:chat1:user1"},
		{BindByChat, "mock:chat1"},
		{BindByUser, "mock:user1"},
	}

	for _, tt := range tests {
		got := sessionKeyFor(tt.mode, ev)
		if got != tt.want {
			t.Errorf("mode %q: got %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestManager_CommandRouter(t *testing.T) {
	Register("mock", func(pc PlatformConfig) (Adapter, error) {
		return &mockAdapter{platform: "mock"}, nil
	})

	cfg := &Config{
		Channels: map[string]PlatformConfig{
			"mock": {"enabled": true},
		},
	}
	mgr := NewManager(cfg, fakeAgentFactory, BindByChatUser)

	// /bind
	ev := InboundEvent{Platform: "mock", ChatID: "c1", UserID: "u1", Text: "/bind"}
	reply := mgr.commandRouter(ev)
	if reply == "" {
		t.Fatal("expected non-empty reply for /bind")
	}
	if mgr.SessionCount() != 1 {
		t.Fatalf("expected 1 session, got %d", mgr.SessionCount())
	}

	// /status
	ev.Text = "/status"
	reply = mgr.commandRouter(ev)
	if reply == "" {
		t.Fatal("expected non-empty reply for /status")
	}

	// /list
	ev.Text = "/list"
	reply = mgr.commandRouter(ev)
	if reply == "" {
		t.Fatal("expected non-empty reply for /list")
	}

	// /stop (does not delete)
	ev.Text = "/stop"
	reply = mgr.commandRouter(ev)
	if reply == "" {
		t.Fatal("expected non-empty reply for /stop")
	}

	// /unbind (deletes)
	ev.Text = "/unbind"
	reply = mgr.commandRouter(ev)
	if mgr.SessionCount() != 0 {
		t.Fatalf("expected 0 sessions after /unbind, got %d", mgr.SessionCount())
	}
}

func TestManager_AutoSessionCreation(t *testing.T) {
	Register("mock2", func(pc PlatformConfig) (Adapter, error) {
		return &mockAdapter{platform: "mock2"}, nil
	})

	cfg := &Config{
		Channels: map[string]PlatformConfig{
			"mock2": {"enabled": true},
		},
	}
	mgr := NewManager(cfg, fakeAgentFactory, BindByChatUser)

	ev := InboundEvent{Platform: "mock2", ChatID: "c1", UserID: "u1", Text: "hello"}
	mgr.handleSessionMessage(context.Background(), ev)

	if mgr.SessionCount() != 1 {
		t.Fatalf("expected 1 auto-created session, got %d", mgr.SessionCount())
	}

	sess := mgr.GetOrCreateSession(ev)
	if sess == nil {
		t.Fatal("expected session to exist")
	}
	if sess.ChatID != "c1" || sess.UserID != "u1" {
		t.Fatalf("unexpected session chat/user: %s/%s", sess.ChatID, sess.UserID)
	}
}

func TestManager_SendReply(t *testing.T) {
	mock := &mockAdapter{platform: "mock3"}
	Register("mock3", func(pc PlatformConfig) (Adapter, error) {
		return mock, nil
	})

	cfg := &Config{
		Channels: map[string]PlatformConfig{
			"mock3": {"enabled": true},
		},
	}
	mgr := NewManager(cfg, fakeAgentFactory, BindByChatUser)

	// Manually store the adapter so sendReply works without Start().
	mgr.adapters.Store("mock3", mock)

	ev := InboundEvent{Platform: "mock3", ChatID: "c1", MessageID: "m1"}
	mgr.sendReply(ev, "hello back")

	if mock.sentTextCount() != 1 {
		t.Fatalf("expected 1 sent text, got %d", mock.sentTextCount())
	}
	last := mock.lastSentText()
	if last.text != "hello back" || last.chatID != "c1" {
		t.Fatalf("unexpected sent text: %+v", last)
	}
}

func TestManager_UnknownCommand(t *testing.T) {
	mgr := NewManager(&Config{}, fakeAgentFactory, BindByChatUser)
	ev := InboundEvent{Text: "/foobar"}
	reply := mgr.commandRouter(ev)
	if reply == "" {
		t.Fatal("expected error reply for unknown command")
	}
	if !contains(reply, "Unknown command") {
		t.Fatalf("expected 'Unknown command' in reply, got: %s", reply)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}
func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
