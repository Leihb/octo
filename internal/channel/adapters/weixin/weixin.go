// Package weixin implements the Weixin (微信) iLink HTTP adapter.
//
// It uses long-polling HTTP to receive messages and a send queue with
// rate limiting and retry for outbound messages.
package weixin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Leihb/octo-agent/internal/channel"
)

const platformName = "weixin"

func init() {
	channel.Register(platformName, New)
}

// Config keys expected in channels.yml.
const (
	cfgBaseURL    = "base_url"
	cfgAppID      = "app_id"
	cfgAppSecret  = "app_secret"
	cfgWebhookURL = "webhook_url"
	cfgTimeoutSec = "timeout_sec"
)

// Adapter implements channel.Adapter for Weixin iLink.
type Adapter struct {
	baseURL   string
	appID     string
	appSecret string
	client    *http.Client

	mu        sync.Mutex
	running   bool
	cancel    context.CancelFunc
	onMessage func(channel.InboundEvent)

	// sendQueue serializes outbound messages with rate limiting.
	sendQueue chan sendJob

	// typingKeepalive sends periodic typing indicators while processing.
	typingKeepalive *time.Ticker
	typingStop      chan struct{}
}

// sendJob is one outbound message queued for sending.
type sendJob struct {
	chatID   string
	text     string
	filePath string
	fileName string
	replyTo  string
	result   chan channel.SendResult
}

// New creates a Weixin adapter from platform config.
func New(cfg channel.PlatformConfig) (channel.Adapter, error) {
	baseURL, _ := cfg[cfgBaseURL].(string)
	if baseURL == "" {
		baseURL = "https://api.weixin.qq.com"
	}
	timeoutSec, _ := cfg[cfgTimeoutSec].(int)
	if timeoutSec <= 0 {
		timeoutSec = 30
	}

	return &Adapter{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		appID:      stringOr(cfg[cfgAppID], ""),
		appSecret:  stringOr(cfg[cfgAppSecret], ""),
		client:     &http.Client{Timeout: time.Duration(timeoutSec) * time.Second},
		sendQueue:  make(chan sendJob, 100),
		typingStop: make(chan struct{}),
	}, nil
}

// Platform returns "weixin".
func (a *Adapter) Platform() string { return platformName }

// Start begins the long-poll receive loop and the send queue worker.
func (a *Adapter) Start(ctx context.Context, onMessage func(channel.InboundEvent)) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("weixin adapter already running")
	}
	a.running = true
	a.onMessage = onMessage
	ctx, a.cancel = context.WithCancel(ctx)
	a.mu.Unlock()

	// Start the send queue worker.
	go a.sendWorker(ctx)

	// Start long-polling for messages.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			a.pollOnce(ctx)
		}
	}
}

// Stop shuts down the adapter.
func (a *Adapter) Stop() error {
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return nil
	}
	a.running = false
	if a.cancel != nil {
		a.cancel()
	}
	close(a.typingStop)
	a.mu.Unlock()
	return nil
}

// SendText enqueues a text message for delivery.
func (a *Adapter) SendText(chatID, text, replyTo string) channel.SendResult {
	resCh := make(chan channel.SendResult, 1)
	select {
	case a.sendQueue <- sendJob{chatID: chatID, text: text, replyTo: replyTo, result: resCh}:
		return <-resCh
	default:
		return channel.SendResult{OK: false, Error: "send queue full"}
	}
}

// SendFile enqueues a file message for delivery.
func (a *Adapter) SendFile(chatID, path, name, replyTo string) channel.SendResult {
	resCh := make(chan channel.SendResult, 1)
	select {
	case a.sendQueue <- sendJob{chatID: chatID, filePath: path, fileName: name, replyTo: replyTo, result: resCh}:
		return <-resCh
	default:
		return channel.SendResult{OK: false, Error: "send queue full"}
	}
}

// UpdateMessage is not supported by Weixin iLink.
func (a *Adapter) UpdateMessage(chatID, messageID, text string) bool { return false }

// SupportsMessageUpdates returns false.
func (a *Adapter) SupportsMessageUpdates() bool { return false }

// ValidateConfig checks required fields.
func (a *Adapter) ValidateConfig(cfg channel.PlatformConfig) []string {
	var errs []string
	if a.appID == "" {
		errs = append(errs, "weixin: app_id is required")
	}
	if a.appSecret == "" {
		errs = append(errs, "weixin: app_secret is required")
	}
	return errs
}

// pollOnce performs one long-poll request for inbound messages.
func (a *Adapter) pollOnce(ctx context.Context) {
	u, err := url.Parse(a.baseURL + "/cgi-bin/message/custom/polling")
	if err != nil {
		return
	}
	q := u.Query()
	q.Set("appid", a.appID)
	q.Set("appsecret", a.appSecret)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return
	}

	resp, err := a.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var pollResp pollResponse
	if err := json.Unmarshal(body, &pollResp); err != nil {
		return
	}

	for _, msg := range pollResp.Messages {
		ev := channel.InboundEvent{
			Type:         "message",
			Platform:     platformName,
			ChatID:       msg.FromUserName,
			UserID:       msg.FromUserName,
			Text:         msg.Content,
			MessageID:    msg.MsgID,
			ChatType:     chatType(msg.MsgType),
			ContextToken: msg.ContextToken,
			Raw:          msg,
		}
		if a.onMessage != nil {
			a.onMessage(ev)
		}
	}
}

// sendWorker processes the send queue with rate limiting and retry.
func (a *Adapter) sendWorker(ctx context.Context) {
	const (
		maxRetries = 3
		baseDelay  = 500 * time.Millisecond
	)

	for {
		select {
		case <-ctx.Done():
			return
		case job, ok := <-a.sendQueue:
			if !ok {
				return
			}
			var res channel.SendResult
			for i := 0; i < maxRetries; i++ {
				if i > 0 {
					time.Sleep(baseDelay * time.Duration(i))
				}
				if job.filePath != "" {
					res = a.doSendFile(ctx, job)
				} else {
					res = a.doSendText(ctx, job)
				}
				if res.OK {
					break
				}
			}
			if job.result != nil {
				job.result <- res
			}
			// Rate limit: sleep between sends.
			time.Sleep(200 * time.Millisecond)
		}
	}
}

// doSendText sends a text message via the Weixin API.
func (a *Adapter) doSendText(ctx context.Context, job sendJob) channel.SendResult {
	payload := map[string]any{
		"touser":  job.chatID,
		"msgtype": "text",
		"text":    map[string]string{"content": job.text},
	}
	if job.replyTo != "" {
		payload["context_token"] = job.replyTo
	}
	return a.postJSON(ctx, "/cgi-bin/message/custom/send", payload)
}

// doSendFile sends a file message via the Weixin API.
func (a *Adapter) doSendFile(ctx context.Context, job sendJob) channel.SendResult {
	// File upload is a two-step process: upload media, then send by media_id.
	// For simplicity in the adapter skeleton, we send the file path as a text hint.
	payload := map[string]any{
		"touser":  job.chatID,
		"msgtype": "text",
		"text":    map[string]string{"content": fmt.Sprintf("📎 File: %s", job.fileName)},
	}
	return a.postJSON(ctx, "/cgi-bin/message/custom/send", payload)
}

// postJSON sends a JSON POST to the Weixin API.
func (a *Adapter) postJSON(ctx context.Context, path string, payload map[string]any) channel.SendResult {
	data, err := json.Marshal(payload)
	if err != nil {
		return channel.SendResult{OK: false, Error: err.Error()}
	}

	u := a.baseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return channel.SendResult{OK: false, Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return channel.SendResult{OK: false, Error: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return channel.SendResult{OK: false, Error: err.Error()}
	}

	if resp.StatusCode != http.StatusOK {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))}
	}

	var apiResp apiResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return channel.SendResult{OK: true, MessageID: ""}
	}
	if apiResp.ErrCode != 0 {
		return channel.SendResult{OK: false, Error: fmt.Sprintf("weixin error %d: %s", apiResp.ErrCode, apiResp.ErrMsg)}
	}
	return channel.SendResult{OK: true, MessageID: apiResp.MsgID}
}

// pollResponse is the shape of the long-poll response.
type pollResponse struct {
	Messages []weixinMessage `json:"messages"`
}

// weixinMessage is a single inbound message from Weixin.
type weixinMessage struct {
	ToUserName   string `json:"ToUserName"`
	FromUserName string `json:"FromUserName"`
	CreateTime   int64  `json:"CreateTime"`
	MsgType      string `json:"MsgType"`
	Content      string `json:"Content"`
	MsgID        string `json:"MsgId"`
	ContextToken string `json:"ContextToken,omitempty"`
}

// apiResponse is the common Weixin API response envelope.
type apiResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	MsgID   string `json:"msgid,omitempty"`
}

// chatType maps Weixin msg types to our chat type.
func chatType(msgType string) string {
	switch msgType {
	case "text", "image", "voice", "video", "file":
		return "direct"
	default:
		return "group"
	}
}

// stringOr returns v if it's a string, otherwise fallback.
func stringOr(v any, fallback string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fallback
}
