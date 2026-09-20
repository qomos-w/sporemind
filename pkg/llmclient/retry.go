package llmclient

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"time"
)

// RetryClient wraps a Client with transient-error retry logic for the
// stream-open phase (HTTP POST + response-header read). Once the stream is
// successfully opened, events are forwarded without further retry.
type RetryClient struct {
	inner      Client
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
}

// NewRetryClient wraps inner with retry for stream-open failures.
func NewRetryClient(inner Client) *RetryClient {
	return &RetryClient{
		inner:      inner,
		maxRetries: 5,
		baseDelay:  500 * time.Millisecond,
		maxDelay:   5 * time.Second,
	}
}

func (c *RetryClient) Stream(ctx context.Context, req Request) (Stream, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := c.backoff(attempt)
			slog.Info("llmclient: retrying stream open", "attempt", attempt, "delay", delay, "error", lastErr.Error())
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}
		s, err := c.inner.Stream(ctx, req)
		if err == nil {
			return s, nil
		}
		lastErr = err
		if !IsRetryableError(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *RetryClient) backoff(attempt int) time.Duration {
	d := c.baseDelay * (1 << (attempt - 1))
	if d > c.maxDelay {
		return c.maxDelay
	}
	return d
}

// IsRetryableError reports whether an error represents a transient provider failure.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	// Never retry explicit cancellation.
	if errors.Is(err, context.Canceled) {
		return false
	}
	// Don't retry context deadlines that the caller set.
	if errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	status := ExtractHTTPStatus(err)
	if status != 0 {
		// Most 429 rate limits are handled by the aggregator through cooldown
		// and unit rotation. A provider's "try again" response is transient,
		// so retry it like a timeout.
		if status == 429 {
			return strings.Contains(strings.ToLower(err.Error()), "try again")
		}
		return IsProviderFailureStatus(status)
	}

	// Retry network-level timeouts and temporary errors.
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		if netErr.Temporary() {
			return true
		}
	}

	// Fallback: match known transient error strings.
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "context canceled") || strings.Contains(msg, "context cancelled") || strings.Contains(msg, "context deadline exceeded") {
		return false
	}
	transientStrings := []string{
		"timeout awaiting response headers",
		"connection refused",
		"connection reset",
		"tls handshake timeout",
		"no such host",
		"broken pipe",
		"unexpected eof",
		// HTTP/2 keepalive-detected dead pooled connection: the transport
		// fails the request with "http2: client connection lost" once a
		// ping goes unanswered. The next dial builds a fresh connection.
		"http2: client connection lost",
		// Windows socket-level failures (WSAETIMEDOUT/WSAECONNRESET/etc.)
		// surface as "wsarecv: ..." from internal/poll. The trailing prose
		// is localized by FormatMessage, so match only the stable op prefix.
		"wsarecv:",
		"wsasend:",
	}
	for _, s := range transientStrings {
		if strings.Contains(msg, s) {
			return true
		}
	}

	return false
}
