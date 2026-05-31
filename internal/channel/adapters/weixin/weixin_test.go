package weixin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Leihb/octo-agent/internal/channel"
)

func TestAdapter_ValidateConfig(t *testing.T) {
	a := &Adapter{appID: "", appSecret: ""}
	errs := a.ValidateConfig(channel.PlatformConfig{})
	if len(errs) != 2 {
		t.Fatalf("expected 2 validation errors, got %d: %v", len(errs), errs)
	}

	a = &Adapter{appID: "app1", appSecret: "secret1"}
	errs = a.ValidateConfig(channel.PlatformConfig{})
	if len(errs) != 0 {
		t.Fatalf("expected 0 validation errors, got %d: %v", len(errs), errs)
	}
}

func TestAdapter_SendText(t *testing.T) {
	var received []map[string]any
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/message/custom/send" {
			body, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(body, &payload)
			mu.Lock()
			received = append(received, payload)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok", "msgid": "mid123"})
		}
	}))
	defer ts.Close()

	a := &Adapter{
		baseURL:    ts.URL,
		appID:      "app1",
		appSecret:  "secret1",
		client:     &http.Client{Timeout: 5 * time.Second},
		sendQueue:  make(chan sendJob, 10),
		typingStop: make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.sendWorker(ctx)

	res := a.SendText("user1", "hello world", "")
	if !res.OK {
		t.Fatalf("expected send OK, got error: %s", res.Error)
	}
	if res.MessageID != "mid123" {
		t.Fatalf("unexpected message ID: %q", res.MessageID)
	}

	// Wait for async send.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if len(received) != 1 {
		t.Fatalf("expected 1 received request, got %d", len(received))
	}
	payload := received[0]
	mu.Unlock()

	if payload["touser"] != "user1" {
		t.Errorf("unexpected touser: %v", payload["touser"])
	}
	if payload["msgtype"] != "text" {
		t.Errorf("unexpected msgtype: %v", payload["msgtype"])
	}
}

func TestAdapter_SendText_Retry(t *testing.T) {
	attempts := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errcode": -1, "errmsg": "system busy"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok", "msgid": "mid456"})
	}))
	defer ts.Close()

	a := &Adapter{
		baseURL:    ts.URL,
		appID:      "app1",
		appSecret:  "secret1",
		client:     &http.Client{Timeout: 5 * time.Second},
		sendQueue:  make(chan sendJob, 10),
		typingStop: make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.sendWorker(ctx)

	res := a.SendText("user1", "retry test", "")
	if !res.OK {
		t.Fatalf("expected send OK after retry, got error: %s", res.Error)
	}
	if attempts < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", attempts)
	}
}

func TestAdapter_SendQueueFull(t *testing.T) {
	a := &Adapter{
		baseURL:    "http://localhost:1",
		appID:      "app1",
		appSecret:  "secret1",
		client:     &http.Client{Timeout: 100 * time.Millisecond},
		sendQueue:  make(chan sendJob, 1),
		typingStop: make(chan struct{}),
	}

	// Fill the queue without a worker.
	a.sendQueue <- sendJob{result: make(chan channel.SendResult, 1)}

	res := a.SendText("user1", "overflow", "")
	if res.OK {
		t.Fatal("expected failure when queue full")
	}
	if !strings.Contains(res.Error, "full") {
		t.Fatalf("expected 'full' in error, got: %s", res.Error)
	}
}

func TestAdapter_PollOnce(t *testing.T) {
	var received []channel.InboundEvent
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cgi-bin/message/custom/polling" {
			resp := pollResponse{
				Messages: []weixinMessage{
					{
						FromUserName: "user1",
						MsgType:      "text",
						Content:      "hello bot",
						MsgID:        "msg123",
						ContextToken: "ctx456",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer ts.Close()

	a := &Adapter{
		baseURL:    ts.URL,
		appID:      "app1",
		appSecret:  "secret1",
		client:     &http.Client{Timeout: 5 * time.Second},
		sendQueue:  make(chan sendJob, 10),
		typingStop: make(chan struct{}),
	}

	onMessage := func(ev channel.InboundEvent) {
		mu.Lock()
		received = append(received, ev)
		mu.Unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Run pollOnce manually.
	a.onMessage = onMessage
	a.pollOnce(ctx)

	mu.Lock()
	if len(received) != 1 {
		t.Fatalf("expected 1 received event, got %d", len(received))
	}
	ev := received[0]
	mu.Unlock()

	if ev.Platform != "weixin" {
		t.Errorf("unexpected platform: %q", ev.Platform)
	}
	if ev.ChatID != "user1" {
		t.Errorf("unexpected chatID: %q", ev.ChatID)
	}
	if ev.Text != "hello bot" {
		t.Errorf("unexpected text: %q", ev.Text)
	}
	if ev.MessageID != "msg123" {
		t.Errorf("unexpected messageID: %q", ev.MessageID)
	}
	if ev.ContextToken != "ctx456" {
		t.Errorf("unexpected contextToken: %q", ev.ContextToken)
	}
	if ev.ChatType != "direct" {
		t.Errorf("unexpected chatType: %q", ev.ChatType)
	}
}

func TestAdapter_StopNotRunning(t *testing.T) {
	a := &Adapter{
		sendQueue:  make(chan sendJob, 1),
		typingStop: make(chan struct{}),
	}
	err := a.Stop()
	if err != nil {
		t.Fatalf("expected nil error when stopping non-running adapter, got: %v", err)
	}
}

func TestAdapter_StartAlreadyRunning(t *testing.T) {
	a := &Adapter{
		baseURL:    "http://localhost:1",
		appID:      "app1",
		appSecret:  "secret1",
		client:     &http.Client{Timeout: 100 * time.Millisecond},
		sendQueue:  make(chan sendJob, 1),
		typingStop: make(chan struct{}),
	}

	// Simulate already running.
	a.running = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := a.Start(ctx, func(ev channel.InboundEvent) {})
	if err == nil {
		t.Fatal("expected error when starting already-running adapter")
	}
}

func TestAdapter_SupportsMessageUpdates(t *testing.T) {
	a := &Adapter{}
	if a.SupportsMessageUpdates() {
		t.Error("expected SupportsMessageUpdates to be false")
	}
}

func TestAdapter_SendFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok", "msgid": "mid789"})
	}))
	defer ts.Close()

	a := &Adapter{
		baseURL:    ts.URL,
		appID:      "app1",
		appSecret:  "secret1",
		client:     &http.Client{Timeout: 5 * time.Second},
		sendQueue:  make(chan sendJob, 10),
		typingStop: make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.sendWorker(ctx)

	res := a.SendFile("user1", "/tmp/test.txt", "test.txt", "")
	if !res.OK {
		t.Fatalf("expected send OK, got error: %s", res.Error)
	}
}

func TestNew(t *testing.T) {
	cfg := channel.PlatformConfig{
		"app_id":      "test_app",
		"app_secret":  "test_secret",
		"base_url":    "https://custom.example.com",
		"timeout_sec": 60,
	}
	ad, err := New(cfg)
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	wa := ad.(*Adapter)
	if wa.appID != "test_app" {
		t.Errorf("unexpected appID: %q", wa.appID)
	}
	if wa.appSecret != "test_secret" {
		t.Errorf("unexpected appSecret: %q", wa.appSecret)
	}
	if wa.baseURL != "https://custom.example.com" {
		t.Errorf("unexpected baseURL: %q", wa.baseURL)
	}
	if wa.client.Timeout != 60*time.Second {
		t.Errorf("unexpected timeout: %v", wa.client.Timeout)
	}
}

func TestNew_Defaults(t *testing.T) {
	ad, err := New(channel.PlatformConfig{})
	if err != nil {
		t.Fatalf("New error: %v", err)
	}
	wa := ad.(*Adapter)
	if wa.baseURL != "https://api.weixin.qq.com" {
		t.Errorf("unexpected default baseURL: %q", wa.baseURL)
	}
	if wa.client.Timeout != 30*time.Second {
		t.Errorf("unexpected default timeout: %v", wa.client.Timeout)
	}
}
