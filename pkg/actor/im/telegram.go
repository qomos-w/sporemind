package im

// Telegram Bot API provider adapter. Inbound traffic arrives through a
// getUpdates long-polling loop (Run), outbound text leaves via sendMessage
// (Send), and Probe validates the token with getMe. The HTTP layer is a
// plain *http.Client, so tests inject a fake RoundTripper and never touch
// the real API. Inbound defense: the account's AllowUsers list gates every
// message (an empty list denies everyone and answers with one fixed notice)
// and a per-chat token bucket (default 1 msg/s) bounds floods.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	telegramProviderID   = "telegram"
	telegramProviderName = "Telegram"

	// telegramDefaultAPIBase is the public Bot API endpoint unless the
	// account overrides APIBase (e.g. a self-hosted Bot API server).
	telegramDefaultAPIBase = "https://api.telegram.org"

	// telegramPollTimeout is the server-side long-poll window requested via
	// getUpdates' timeout field.
	telegramPollTimeout = 50 * time.Second
	// telegramRequestTimeout caps one getUpdates HTTP call. It must exceed
	// telegramPollTimeout so the server can finish the long poll before the
	// client gives up.
	telegramRequestTimeout = 60 * time.Second
	// telegramAPITimeout caps one getMe / sendMessage call.
	telegramAPITimeout = 30 * time.Second
	// telegramMaxBackoff bounds the retry delay after failed polls.
	telegramMaxBackoff = 30 * time.Second

	// Default inbound gate: one message per chat per second, single-token
	// burst (a second message in the same second is dropped).
	telegramChatRate  = 1.0
	telegramChatBurst = 1.0

	// telegramRejectNotice is the fixed reply to senders that fail the
	// account allowlist. An empty AllowUsers denies everyone by design, so
	// until the owner configures the list this notice is all the bot says.
	telegramRejectNotice = "⛔ 抱歉，您不在该机器人的授权用户列表中，暂无法对话。\nSorry, you are not authorized to chat with this bot."
)

// ---------------------------------------------------------------------------
// Wire payloads
// ---------------------------------------------------------------------------

// telegramEnvelope is the common Bot API response wrapper.
type telegramEnvelope struct {
	OK          bool            `json:"ok"`
	ErrorCode   int             `json:"error_code,omitempty"`
	Description string          `json:"description,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

type telegramGetUpdatesReq struct {
	Offset  int64 `json:"offset,omitempty"`
	Timeout int   `json:"timeout,omitempty"`
}

type telegramSendMessageReq struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview,omitempty"`
}

type telegramUpdate struct {
	UpdateID int64            `json:"update_id"`
	Message  *telegramMessage `json:"message,omitempty"`
}

type telegramMessage struct {
	MessageID int64         `json:"message_id"`
	From      *telegramUser `json:"from,omitempty"`
	Chat      *telegramChat `json:"chat,omitempty"`
	Date      int64         `json:"date"`
	Text      string        `json:"text,omitempty"`
}

type telegramUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

type telegramChat struct {
	ID int64 `json:"id"`
}

// telegramBot is getMe's result.
type telegramBot struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

// ---------------------------------------------------------------------------
// Provider
// ---------------------------------------------------------------------------

// telegramProvider implements Provider against the Telegram Bot API.
type telegramProvider struct {
	// client is the HTTP layer; tests inject a fake RoundTripper through
	// client.Transport so no request ever leaves the process.
	client *http.Client

	pollTimeout    time.Duration    // server-side long-poll window
	requestTimeout time.Duration    // per-call cap for getUpdates
	apiTimeout     time.Duration    // per-call cap for getMe / sendMessage
	retryBackoff   time.Duration    // base delay after a failed poll
	chatRate       float64          // inbound tokens per second per chat
	chatBurst      float64          // inbound token bucket capacity per chat
	now            func() time.Time // rate-limiter clock (test seam)

	// disableWebPreview asks Telegram not to render link previews in
	// outbound messages.
	disableWebPreview bool

	mu          sync.Mutex
	botUsername string // backfilled by the last successful Probe
}

// newTelegramProvider returns a provider with production defaults.
func newTelegramProvider() *telegramProvider {
	return &telegramProvider{
		client:            &http.Client{},
		pollTimeout:       telegramPollTimeout,
		requestTimeout:    telegramRequestTimeout,
		apiTimeout:        telegramAPITimeout,
		retryBackoff:      time.Second,
		chatRate:          telegramChatRate,
		chatBurst:         telegramChatBurst,
		now:               time.Now,
		disableWebPreview: true,
	}
}

func init() {
	RegisterProvider(telegramProviderID, func() Provider { return newTelegramProvider() })
}

// Interface conformance.
var (
	_ Provider     = (*telegramProvider)(nil)
	_ BotUsernamer = (*telegramProvider)(nil)
)

// BotUsernamer is an optional Provider extension for adapters whose Probe
// discovers the bot's own account name. The actor reads it after a
// successful Probe to populate ImAccountView.BotUsername.
type BotUsernamer interface {
	// BotUsername returns the username recorded by the last successful
	// Probe on this provider instance ("" before the first success).
	BotUsername() string
}

func (p *telegramProvider) ID() string   { return telegramProviderID }
func (p *telegramProvider) Name() string { return telegramProviderName }

// BotUsername returns the username captured by the last successful Probe.
func (p *telegramProvider) BotUsername() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.botUsername
}

// apiBase resolves the Bot API base URL for one account.
func telegramAPIBase(account gen.ImAccount) string {
	if account.APIBase != "" {
		return account.APIBase
	}
	return telegramDefaultAPIBase
}

// Probe verifies the token with GET getMe and, on success, records the bot
// username for ImAccountView.BotUsername (see BotUsernamer).
func (p *telegramProvider) Probe(ctx context.Context, account gen.ImAccount) error {
	if account.Token == "" {
		return fmt.Errorf("telegram: probe: account token is required")
	}
	callCtx, cancel := context.WithTimeout(ctx, p.apiTimeout)
	defer cancel()
	var bot telegramBot
	if err := p.call(callCtx, http.MethodGet, telegramAPIBase(account), account.Token, "getMe", nil, &bot); err != nil {
		return fmt.Errorf("telegram: probe: %w", err)
	}
	p.mu.Lock()
	p.botUsername = bot.Username
	p.mu.Unlock()
	return nil
}

// Send pushes one outbound text message via POST sendMessage. Web previews
// are disabled by default (disableWebPreview).
func (p *telegramProvider) Send(ctx context.Context, account gen.ImAccount, chatID string, text string) error {
	if account.Token == "" {
		return fmt.Errorf("telegram: send: account token is required")
	}
	if chatID == "" || text == "" {
		return fmt.Errorf("telegram: send: chat id and text are required")
	}
	req := telegramSendMessageReq{ChatID: chatID, Text: text}
	if p.disableWebPreview {
		req.DisableWebPagePreview = true
	}
	callCtx, cancel := context.WithTimeout(ctx, p.apiTimeout)
	defer cancel()
	var sent telegramMessage
	if err := p.call(callCtx, http.MethodPost, telegramAPIBase(account), account.Token, "sendMessage", req, &sent); err != nil {
		return fmt.Errorf("telegram: send: %w", err)
	}
	return nil
}

// Run drives the getUpdates long-polling loop until ctx is cancelled and
// forwards allowed text messages to inbound. Poll failures are retried with
// exponential backoff (bounded by telegramMaxBackoff); only cancellation or
// an unrecoverable per-update failure terminates the loop.
func (p *telegramProvider) Run(ctx context.Context, account gen.ImAccount, inbound chan<- InboundMessage) error {
	if account.Token == "" {
		return fmt.Errorf("telegram: run: account token is required")
	}
	limiter := newChatRateLimiter(p.chatRate, p.chatBurst, p.now)
	var offset int64
	backoff := p.retryBackoff
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		updates, err := p.poll(ctx, account, offset)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !sleepContext(ctx, backoff) {
				return ctx.Err()
			}
			backoff = min(backoff*2, telegramMaxBackoff)
			continue
		}
		backoff = p.retryBackoff
		for _, upd := range updates {
			if err := p.deliver(ctx, account, limiter, upd, inbound); err != nil {
				return err
			}
			// Acknowledge every update — including skipped, rate-limited
			// and rejected ones — so Telegram drops it from the queue.
			offset = upd.UpdateID + 1
		}
	}
}

// deliver applies the inbound defenses to one update and forwards surviving
// text messages: per-chat rate limit first (bounding both floods and the
// rejection notices), then the account allowlist (empty list denies
// everyone, answering with one fixed notice), then the channel send.
func (p *telegramProvider) deliver(ctx context.Context, account gen.ImAccount, limiter *chatRateLimiter, upd telegramUpdate, inbound chan<- InboundMessage) error {
	msg := upd.Message
	if msg == nil || msg.Text == "" || msg.Chat == nil {
		// Non-text updates (photos, joins, edits...) are acknowledged but
		// never forwarded.
		return nil
	}
	chatID := strconv.FormatInt(msg.Chat.ID, 10)
	from := telegramFromUser(msg.From)
	if !limiter.allow(chatID) {
		// Over the per-chat rate: drop silently. Replying here would let a
		// flood generate a reply flood.
		return nil
	}
	if !telegramAllowed(account.AllowUsers, from) {
		if err := p.Send(ctx, account, chatID, telegramRejectNotice); err != nil {
			return fmt.Errorf("telegram: run: reject notice: %w", err)
		}
		return nil
	}
	select {
	case inbound <- InboundMessage{
		AccountID: account.ID,
		ChatID:    chatID,
		FromUser:  from,
		Text:      msg.Text,
	}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// poll performs one getUpdates long-poll call and decodes the updates.
func (p *telegramProvider) poll(ctx context.Context, account gen.ImAccount, offset int64) ([]telegramUpdate, error) {
	callCtx, cancel := context.WithTimeout(ctx, p.requestTimeout)
	defer cancel()
	body := telegramGetUpdatesReq{Offset: offset, Timeout: int(p.pollTimeout.Seconds())}
	var updates []telegramUpdate
	if err := p.call(callCtx, http.MethodPost, telegramAPIBase(account), account.Token, "getUpdates", body, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// call performs one Bot API request: it builds {apiBase}/bot{token}/{method},
// serializes body as JSON (nil means no body), enforces the HTTP status and
// the envelope's ok flag, and decodes result into out when non-nil.
func (p *telegramProvider) call(ctx context.Context, httpMethod, apiBase, token, method string, body, out any) error {
	url := strings.TrimSuffix(apiBase, "/") + "/bot" + token + "/" + method
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, httpMethod, url, reader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(respBody), 200))
	}
	var envelope telegramEnvelope
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	if !envelope.OK {
		return fmt.Errorf("API %d: %s", envelope.ErrorCode, truncate(envelope.Description, 200))
	}
	if out != nil && len(envelope.Result) > 0 {
		if err := json.Unmarshal(envelope.Result, out); err != nil {
			return fmt.Errorf("parse result: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Inbound helpers
// ---------------------------------------------------------------------------

// telegramFromUser normalizes the sender: the @username when present, else
// the numeric Telegram user id as a string ("" for messages without a from
// actor, e.g. channel posts).
func telegramFromUser(u *telegramUser) string {
	if u == nil {
		return ""
	}
	if u.Username != "" {
		return u.Username
	}
	return strconv.FormatInt(u.ID, 10)
}

// telegramAllowed gates one sender against the account allowlist: an empty
// list denies everyone (explicit opt-in only) and a missing sender is never
// allowed.
func telegramAllowed(allow []string, from string) bool {
	if len(allow) == 0 || from == "" {
		return false
	}
	for _, u := range allow {
		if u == from {
			return true
		}
	}
	return false
}

// sleepContext waits for d or ctx termination; it reports false when ctx
// ended the wait early.
func sleepContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// truncate shortens a string to maxLen, appending "..." if truncated. Used
// on upstream response fragments carried into errors (long log fields must
// be truncated before crossing actor boundaries).
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ---------------------------------------------------------------------------
// Per-chat rate limiting (token bucket)
// ---------------------------------------------------------------------------

// chatRateLimiter gates inbound messages per ChatID with independent token
// buckets. Buckets start full (burst capacity); failed takes still advance
// the refill clock, so a flood cannot bank credit.
type chatRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rate    float64
	burst   float64
	now     func() time.Time
}

func newChatRateLimiter(rate, burst float64, now func() time.Time) *chatRateLimiter {
	return &chatRateLimiter{
		buckets: map[string]*tokenBucket{},
		rate:    rate,
		burst:   burst,
		now:     now,
	}
}

// allow reports whether one message from chatID passes the rate limit.
func (l *chatRateLimiter) allow(chatID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[chatID]
	if !ok {
		b = &tokenBucket{tokens: l.burst, rate: l.rate, burst: l.burst}
		l.buckets[chatID] = b
	}
	return b.take(l.now())
}

// tokenBucket is a minimal token bucket (rate tokens per second, burst
// capacity).
type tokenBucket struct {
	tokens float64
	last   time.Time
	rate   float64
	burst  float64
}

// take consumes one token if available and reports success.
func (b *tokenBucket) take(now time.Time) bool {
	if !b.last.IsZero() {
		b.tokens = min(b.tokens+now.Sub(b.last).Seconds()*b.rate, b.burst)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}
