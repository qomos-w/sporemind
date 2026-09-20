package llmclient

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/qomos-w/sporemind/pkg/version"
)

// Error sentinels

var (
	ErrStreamClosed      = errors.New("llmclient: stream closed unexpectedly")
	ErrStreamIdleTimeout = errors.New("llmclient: stream idle timeout")
)

// Lookup tables

// Quota-exhaustion signals: structured provider error code/type values that,
// combined with HTTP 403, identify credential-level quota exhaustion (billing
// cycle / usage limit) as opposed to a generic authorization failure.
//   - OpenAI / one-api / new-api proxies: error.code = "insufficient_user_quota"
//   - Kimi For Coding: error.type = "access_terminated_error" (usage limit
//     for the billing cycle)
//
// Shared by aiaggregator (unit-level cooldown) and aimanager (provider-level
// health) so both layers classify the same failure identically.
var quotaErrorCodes = map[string]bool{
	"insufficient_user_quota": true,
	"insufficient_quota":      true,
	"quota_exceeded":          true,
	"quota_exhausted":         true,
	"usage_limit_exceeded":    true,
	"billing_limit_reached":   true,
}

var quotaErrorTypes = map[string]bool{
	"access_terminated_error": true,
	"insufficient_quota":      true,
	"quota_exceeded":          true,
	"usage_limit_reached":     true,
}

// quotaMessageMarkers are message substrings that, on a 403, indicate quota
// exhaustion when no structured code/type matched. A false positive here only
// costs a cooldown (self-healing); a false negative hard-disables the unit and
// requires manual recovery, so 403 classification deliberately leans toward
// cooldown when the message mentions quota/usage/billing.
var quotaMessageMarkers = []string{
	"usage limit",
	"quota",
	"billing",
	"insufficient balance",
	"额度",
	"配额",
	"余额不足",
	// Usage-window / token-plan exhaustion is delivered as 429 by several
	// relays; without these markers it rides the short rate-limit window and
	// the failover loop grinds the dead unit until the dispatch budget dies.
	"使用上限",
	"token plan",
}

// Model-deprecation signals: structured provider error code/type values, and
// message substrings, that identify a retired model on a 400/404. Relays
// (one-api / new-api "Console Go") frequently wrap an upstream 404 "model has
// been deprecated" into an outer 400 [server_error], so the message scan is
// what carries detection in practice. A hit redirects the failure from
// ClassStop (terminate) to ClassModelCooldown (cool the unit, rotate).
var modelDeprecatedCodes = map[string]bool{
	"model_deprecated": true,
}

var modelDeprecatedTypes = map[string]bool{
	"model_deprecated": true,
}

var modelDeprecatedMarkers = []string{
	"model has been deprecated",
	"model is deprecated",
}

// Image-input-unsupported signals: message substrings that, on a 400/404,
// identify a text-only model that rejected an image block. Providers and
// relays phrase this in many ways ("this model does not support image
// input", "images are not supported with this model", …) and none define a
// structured code for it, so detection is message-scan only. A false
// positive costs one stripped retry that falls back to the original stop
// semantics when it fails again, so the list may stay moderately broad.
var imageUnsupportedMarkers = []string{
	"does not support image",
	"not support image input",
	"image input is not supported",
	"images are not supported",
	"image content is not supported",
	"does not support multimodal",
	"不支持图片",
	"不支持图像",
	// Chinese relays (one-api / new-api style) reject a text-only model with
	// "messages.content.type 参数非法，取值范围 ['text']" or "非图像模型" instead
	// of an image-specific phrase; both name the content-type constraint itself.
	"content.type",
	"非图像模型",
}

// cacheEphemeral is the Anthropic cache-control marker for ephemeral
// prompt-caching entries.
var cacheEphemeral = map[string]string{"type": "ephemeral"}

// Mutable singletons

// TODO(actor-ownership): migrate to actor-owned state.

// DefaultProviderGate is the process-wide per-provider concurrency limiter
// singleton shared by all aggregators.
var DefaultProviderGate = NewProviderGate()

// DefaultAggregatorHealth is the process-wide aggregator health registry
// singleton shared by all aggregators and routing layers.
var DefaultAggregatorHealth = NewAggregatorHealthRegistry()

// DefaultProviderHealth is the process-wide per-unit health singleton shared
// by all aggregators and aimanager projections.
var DefaultProviderHealth = NewProviderHealth()

// semPool is the legacy per-endpoint semaphore store keyed by
// "endpoint\x00authToken" (kept for backward compat, unused by aggregator).
var semPool sync.Map

// userAgent identifies this client in HTTP requests to LLM providers.
var userAgent = "SporeMind/" + version.Version

// httpClient is the package-default HTTP client with production-grade
// transport tuning to prevent connection pool exhaustion, slowloris-style
// stalls, and half-open connection leaks. Tests overwrite the embedded
// client on a concrete provider to redirect to httptest servers.
//
// No Client.Timeout is set: streaming SSE calls may run arbitrarily long
// (e.g. model reasoning). Idle and cancellation are handled at the
// application layer via context and StreamIdleTimeout.
var httpClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		MaxConnsPerHost:       20,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
		HTTP2: &http.HTTP2Config{
			// A pooled connection silently dropped by the provider's LB
			// must be detected instead of swallowing the next stream-open
			// (x/net/http2 ignores ResponseHeaderTimeout, so without a
			// ping the wait is unbounded). SendPingTimeout starts a health
			// ping after 15s without frames; PingTimeout closes a connection
			// whose ping goes unanswered 10s. Detected death fails the
			// request with "client connection lost", which
			// IsRetryableError retries transparently.
			SendPingTimeout: 15 * time.Second,
			PingTimeout:     10 * time.Second,
		},
	},
}
