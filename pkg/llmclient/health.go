package llmclient

import (
	"errors"
	"math"
	"sync"
	"time"
)

// Health state values projected via health snapshots. These mirror the
// state strings already used by aiaggregator and aimanager so existing UI and
// telemetry consumers continue to work when they switch to reading from this
// layer.
const (
	HealthStateHealthy     = "healthy"
	HealthStateCoolingDown = "cooling_down"
	HealthStateDisabled    = "disabled"
)

// Health reason values for the disabled and cooling-down states. These mirror
// the reason strings already used by aiaggregator and aimanager.
const (
	HealthReasonRateLimit            = "rate_limit"
	HealthReasonAvailability         = "availability"
	HealthReasonQuotaExhausted       = "quota_exhausted"
	HealthReasonAuthenticationFailed = "authentication_failed"
	HealthReasonAuthorizationFailed  = "authorization_failed"
	HealthReasonConfigurationError   = "configuration_error"
	HealthReasonModelDeprecated      = "model_deprecated"
)

// Aggregator-level health state values registered via RegisterAggregatorHealth.
// Only the unavailable state is ever stored (low-frequency pool-down events);
// every other state, including AggregatorHealthAvailable, is the explicit
// clear signal that removes the entry.
const (
	AggregatorHealthAvailable   = "available"
	AggregatorHealthUnavailable = "unavailable"
)

// DefaultAggregatorUnavailableTTL is the time-to-live of an unavailable
// aggregator registration. The pool-down event is low-frequency by design: a
// child aggregator re-registers while its pool stays down and explicitly
// clears on recovery, so the TTL only guards against stale registrations
// (e.g. a child that disappeared without clearing). While a child is
// registered unavailable, its parents skip it; once the TTL expires the
// parents naturally re-probe it.
const DefaultAggregatorUnavailableTTL = 30 * time.Second

// AggregatorHealth is an immutable per-aggregator health snapshot returned by
// AggregatorHealthSnapshot. It is safe to retain and inspect concurrently.
type AggregatorHealth struct {
	AggregatorID     string
	State            string
	UnavailableUntil time.Time
	Remaining        time.Duration
}

// aggHealth is the mutable per-aggregator state protected by
// AggregatorHealthRegistry.mu.
type aggHealth struct {
	state            string
	unavailableUntil time.Time
}

// AggregatorHealthRegistry tracks aggregator-level pool health at the process
// level, parallel to the unit-level ProviderHealth. The key is the aggregator
// id. Only the unavailable state is stored (with a TTL); availability is
// implicit for absent or expired entries. It is goroutine-safe. A single
// process-wide instance is available as DefaultAggregatorHealth; child
// aggregators register themselves after each selection/failure handling and
// parent aggregators read snapshots when routing to aggregator-ref entries.
type AggregatorHealthRegistry struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]*aggHealth
}

// NewAggregatorHealthRegistry returns a fresh AggregatorHealthRegistry with
// the default unavailable TTL.
func NewAggregatorHealthRegistry() *AggregatorHealthRegistry {
	return &AggregatorHealthRegistry{
		ttl:     DefaultAggregatorUnavailableTTL,
		entries: make(map[string]*aggHealth),
	}
}

// SetAggregatorUnavailableTTL updates the TTL of the process-wide
// DefaultAggregatorHealth. The change only affects new registrations — already
// active unavailable entries keep their original deadline.
func SetAggregatorUnavailableTTL(ttl time.Duration) {
	DefaultAggregatorHealth.SetTTL(ttl)
}

// SetTTL updates the TTL. The change only affects new registrations.
func (r *AggregatorHealthRegistry) SetTTL(ttl time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ttl > 0 {
		r.ttl = ttl
	}
}

// RegisterAggregatorHealth records an aggregator-level health state on the
// process-wide registry. The unavailable state is stored with the registry TTL
// and auto-expires; any other state (AggregatorHealthAvailable) clears the
// entry explicitly — the recovery signal. An empty aggregator id is ignored.
func RegisterAggregatorHealth(aggID, state string) {
	DefaultAggregatorHealth.Register(aggID, state)
}

func (r *AggregatorHealthRegistry) Register(aggID, state string) {
	if aggID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if state != AggregatorHealthUnavailable {
		delete(r.entries, aggID)
		return
	}
	deadline := time.Now().Add(r.ttl)
	// Never shorten an existing unavailable deadline (see
	// RegisterUnavailableUntil): the longest known recovery estimate wins.
	if e := r.entries[aggID]; e != nil && e.state == AggregatorHealthUnavailable && deadline.Before(e.unavailableUntil) {
		return
	}
	r.entries[aggID] = &aggHealth{
		state:            AggregatorHealthUnavailable,
		unavailableUntil: deadline,
	}
}

// RegisterAggregatorUnavailableUntil registers the aggregator unavailable with
// an explicit deadline instead of the registry TTL. An existing unavailable
// deadline is never shortened — the longest known recovery estimate wins, so
// a parent-side default-TTL write cannot cut short a longer deadline the
// child derived from its own unit cooldowns. An empty aggregator id is
// ignored.
func RegisterAggregatorUnavailableUntil(aggID string, until time.Time) {
	DefaultAggregatorHealth.RegisterUnavailableUntil(aggID, until)
}

func (r *AggregatorHealthRegistry) RegisterUnavailableUntil(aggID string, until time.Time) {
	if aggID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.entries[aggID]; e != nil && e.state == AggregatorHealthUnavailable && until.Before(e.unavailableUntil) {
		return
	}
	r.entries[aggID] = &aggHealth{
		state:            AggregatorHealthUnavailable,
		unavailableUntil: until,
	}
}

// IsAggregatorAvailable reports whether the aggregator is currently available
// for routing: absent entries (never registered or TTL-expired) are available,
// as are entries whose unavailable deadline has passed.
func IsAggregatorAvailable(aggID string, now time.Time) bool {
	return DefaultAggregatorHealth.IsAvailable(aggID, now)
}

func (r *AggregatorHealthRegistry) IsAvailable(aggID string, now time.Time) bool {
	if aggID == "" {
		return true
	}
	// Full lock (not RLock): an expired entry is deleted on read so the
	// map does not accumulate dead aggregator IDs forever (entries are
	// otherwise only cleared by an explicit available registration).
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.entries[aggID]
	if e == nil {
		return true
	}
	if !now.Before(e.unavailableUntil) {
		delete(r.entries, aggID)
		return true
	}
	return false
}

// AggregatorHealthSnapshot returns an immutable copy of all currently
// unavailable aggregator registrations. Entries whose TTL has expired are
// omitted (auto-invalidation). The returned map is safe to retain.
func AggregatorHealthSnapshot() map[string]AggregatorHealth {
	return DefaultAggregatorHealth.Snapshot()
}

func (r *AggregatorHealthRegistry) Snapshot() map[string]AggregatorHealth {
	r.mu.RLock()
	defer r.mu.RUnlock()
	now := time.Now()
	out := make(map[string]AggregatorHealth, len(r.entries))
	for aggID, e := range r.entries {
		if e.state != AggregatorHealthUnavailable {
			continue
		}
		if now.Before(e.unavailableUntil) {
			out[aggID] = AggregatorHealth{
				AggregatorID:     aggID,
				State:            e.state,
				UnavailableUntil: e.unavailableUntil,
				Remaining:        e.unavailableUntil.Sub(now),
			}
		}
	}
	return out
}

// CooldownPolicy defines the tuning parameters for process-level health
// tracking. A single policy governs all units (provider::model pairs);
// reason-specific behaviour is derived from the classification functions.
//
//   - Availability failures (5xx/network): once consecutiveFailures reaches
//     AvailabilityThreshold, the unit enters cooldown for
//     BaseCooldown × BackoffFactor^(failures - threshold), capped at
//     MaxCooldown. Each subsequent availability failure multiplies the
//     duration by BackoffFactor.
//   - Rate-limit failures (429): immediate cooldown using Retry-After when
//     available, clamped to [RateLimitFloor, RateLimitCeiling]; defaults to
//     RateLimitFloor when Retry-After is absent or zero.
//   - Quota failures (403 + quota signal): fixed QuotaCooldown.
//   - Auth/authorization/config failures: unit is marked disabled (no
//     automatic recovery; requires an explicit recovery event or config
//     refresh — wired by a future persistence bridge).
//   - Model-deprecated failures (400/404 + deprecation signal): fixed
//     ModelDeprecatedCooldown. A retired model never self-recovers, but a
//     cooldown (rather than a sticky disable) keeps a misclassification
//     self-healing and lets a config refresh naturally re-probe the unit.
type CooldownPolicy struct {
	AvailabilityThreshold   int
	BaseCooldown            time.Duration
	BackoffFactor           float64
	MaxCooldown             time.Duration
	RateLimitFloor          time.Duration
	RateLimitCeiling        time.Duration
	QuotaCooldown           time.Duration
	ModelDeprecatedCooldown time.Duration
	// ConnectFailureCooldown is applied on the FIRST connect-stage failure
	// (dial refused/timeout, DNS, TLS handshake) rather than waiting for
	// AvailabilityThreshold: an endpoint that cannot be reached is skipped
	// immediately so routers stop re-dialing it at dispatch rate. Pinned
	// (unit-locked) selections bypass this cooldown. <=0 falls back to
	// BaseCooldown.
	ConnectFailureCooldown time.Duration
}

// DefaultCooldownPolicy returns the default policy, consolidating constants
// previously scattered across aiaggregator (unitCooldownDuration=30s,
// unitFailureThreshold=5, unitQuotaCooldownDuration=5min) and aimanager
// (providerBaseCooldown=30s, providerMaxCooldown=10min→raised to 15m,
// rateLimitDefaultCooldown=60s, rateLimitMaxCooldown=15min,
// quotaCooldown=10min).
func DefaultCooldownPolicy() CooldownPolicy {
	return CooldownPolicy{
		AvailabilityThreshold:   5,
		BaseCooldown:            30 * time.Second,
		BackoffFactor:           1.5,
		MaxCooldown:             15 * time.Minute,
		RateLimitFloor:          60 * time.Second,
		RateLimitCeiling:        15 * time.Minute,
		QuotaCooldown:           10 * time.Minute,
		ModelDeprecatedCooldown: 30 * time.Minute,
		ConnectFailureCooldown:  15 * time.Second,
	}
}

// unitHealth is the mutable per-unit state protected by ProviderHealth.mu.
type unitHealth struct {
	state               string
	reason              string
	cooldownUntil       time.Time
	consecutiveFailures int
}

// UnitHealthSnapshot is an immutable per-unit health snapshot returned by
// HealthSnapshot. It is safe to retain and inspect concurrently.
type UnitHealthSnapshot struct {
	Provider            string
	Model               string
	State               string
	Reason              string
	CooldownUntil       time.Time
	Remaining           time.Duration
	ConsecutiveFailures int
}

// ProviderHealth tracks per-unit (provider::model) health state at the
// process level. It is goroutine-safe. A single process-wide instance is
// available as DefaultProviderHealth; callers record failures and successes
// at the dispatch point and read snapshots for routing/projection.
type ProviderHealth struct {
	mu     sync.RWMutex
	policy CooldownPolicy
	units  map[string]*unitHealth
}

// NewProviderHealth returns a fresh ProviderHealth with the default cooldown
// policy.
func NewProviderHealth() *ProviderHealth {
	return &ProviderHealth{
		policy: DefaultCooldownPolicy(),
		units:  make(map[string]*unitHealth),
	}
}

// unitKey produces the canonical "provider::model" lookup key.
func unitKey(provider, model string) string {
	return provider + "::" + model
}

// splitUnitKey reverses unitKey into (provider, model).
func splitUnitKey(key string) (provider, model string) {
	// Use strings.IndexByte via a manual scan to avoid importing strings just
	// for this (strings is already imported elsewhere in the package, but this
	// helper is self-contained).
	for i := 0; i < len(key); i++ {
		if key[i] == ':' && i+1 < len(key) && key[i+1] == ':' {
			return key[:i], key[i+2:]
		}
	}
	return key, ""
}

// SetCooldownPolicy updates the policy of the process-wide
// DefaultProviderHealth. The policy change only affects new failure
// judgements — it does not reset or rescale already-active cooldowns.
func SetCooldownPolicy(p CooldownPolicy) {
	DefaultProviderHealth.SetCooldownPolicy(p)
}

// SetCooldownPolicy updates the policy. The change only affects new failure
// judgements — existing cooldowns and disabled states are untouched.
func (h *ProviderHealth) SetCooldownPolicy(p CooldownPolicy) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.policy = p
}

// Policy returns the current cooldown policy (useful for tests and debugging).
func (h *ProviderHealth) Policy() CooldownPolicy {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.policy
}

// RecordFailure records a failed dispatch for the given provider::model unit,
// classifies the error, and applies the appropriate health effect (cooldown,
// disable, or no-op for stop-class errors). It is goroutine-safe.
func RecordFailure(provider, model string, err error) {
	DefaultProviderHealth.RecordFailure(provider, model, err)
}

func (h *ProviderHealth) RecordFailure(provider, model string, err error) {
	if provider == "" || err == nil {
		return
	}
	c := ClassifyStreamOpenError(err)
	if c.Class == ClassStop {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	key := unitKey(provider, model)
	u := h.units[key]
	if u == nil {
		u = &unitHealth{state: HealthStateHealthy}
		h.units[key] = u
	}

	// A disabled unit stays disabled — new failures don't change its state.
	if u.state == HealthStateDisabled {
		return
	}

	now := time.Now()

	switch c.Class {
	case ClassLongDisable:
		u.state = HealthStateDisabled
		u.reason = c.Reason
		// No cooldown deadline; disabled requires explicit recovery.

	case ClassShortCooldown:
		u.extendCooldown(c.Reason, now.Add(h.rateLimitDuration(err)))

	case ClassQuotaCooldown:
		u.extendCooldown(c.Reason, now.Add(h.policy.QuotaCooldown))

	case ClassModelCooldown:
		u.extendCooldown(c.Reason, now.Add(h.policy.ModelDeprecatedCooldown))

	case ClassFailover:
		u.consecutiveFailures++
		switch {
		case c.ConnectStage:
			// Connect-stage failure (endpoint unreachable): cool immediately
			// so auto-selection stops re-dialing it every dispatch. Repeated
			// probes extend the deadline (never shorten), and once the
			// availability threshold is also crossed the longer backoff
			// deadline wins via extendCooldown.
			d := h.policy.ConnectFailureCooldown
			if d <= 0 {
				d = h.policy.BaseCooldown
			}
			u.extendCooldown(c.Reason, now.Add(d))
		case u.consecutiveFailures >= h.policy.AvailabilityThreshold:
			u.extendCooldown(c.Reason, now.Add(h.availabilityDuration(u.consecutiveFailures)))
		}
	}
}

// extendCooldown moves the unit into (or keeps it in) cooling_down with the
// given deadline, backing off FROM any active deadline instead of replacing
// it: when the unit is already cooling to a later deadline, that deadline (and
// its reason) wins. Without this, a different error class arriving
// mid-cooldown — e.g. a 429 (60s floor) landing on a unit in a quota (10min)
// or availability backoff (up to 15min) cooldown — would shrink the deadline
// back to its own base, so alternating errors pin the cooldown near the
// shortest value and routers keep re-probing an unreachable unit.
func (u *unitHealth) extendCooldown(reason string, until time.Time) {
	if u.state == HealthStateCoolingDown && u.cooldownUntil.After(until) {
		return
	}
	u.state = HealthStateCoolingDown
	u.reason = reason
	u.cooldownUntil = until
}

// RecordSuccess clears the failure counter for the given unit. It does NOT
// shorten an already-active cooldown or clear a disabled state — cooldowns
// expire naturally and disabled units require explicit recovery.
func RecordSuccess(provider, model string) {
	DefaultProviderHealth.RecordSuccess(provider, model)
}

func (h *ProviderHealth) RecordSuccess(provider, model string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	u := h.units[unitKey(provider, model)]
	if u == nil {
		return
	}
	u.consecutiveFailures = 0
}

// ClearProvider removes every unit of the given provider from the process-wide
// health layer, clearing both cooldowns and disabled states. Other providers'
// units are untouched — a per-provider recovery (manual reset, config change)
// must not give unrelated providers a fresh chance. Unknown providers are a
// no-op. This is the primitive backing aimanager's provider_reset_health and
// provider config changes; it replaces the previous var-swap of the whole
// DefaultProviderHealth instance, which had a race window with in-flight
// dispatches and silently dropped other providers' active cooldowns.
func ClearProvider(provider string) {
	DefaultProviderHealth.ClearProvider(provider)
}

func (h *ProviderHealth) ClearProvider(provider string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for key := range h.units {
		p, _ := splitUnitKey(key)
		if p == provider {
			delete(h.units, key)
		}
	}
}

// IsAvailable reports whether the given unit is currently available for
// selection (healthy or cooldown expired). Disabled units are never available.
func IsAvailable(provider, model string, now time.Time) bool {
	return DefaultProviderHealth.IsAvailable(provider, model, now)
}

func (h *ProviderHealth) IsAvailable(provider, model string, now time.Time) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	u := h.units[unitKey(provider, model)]
	if u == nil {
		return true
	}
	switch u.state {
	case HealthStateDisabled:
		return false
	case HealthStateCoolingDown:
		return !now.Before(u.cooldownUntil)
	default:
		return true
	}
}

// HealthSnapshot returns an immutable copy of all tracked units' health state.
// Units that have never been recorded are absent (implicitly healthy). The
// returned map is safe to retain.
func HealthSnapshot() map[string]UnitHealthSnapshot {
	return DefaultProviderHealth.HealthSnapshot()
}

func (h *ProviderHealth) HealthSnapshot() map[string]UnitHealthSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	now := time.Now()
	out := make(map[string]UnitHealthSnapshot, len(h.units))
	for key, u := range h.units {
		provider, model := splitUnitKey(key)
		snap := UnitHealthSnapshot{
			Provider:            provider,
			Model:               model,
			State:               u.state,
			Reason:              u.reason,
			CooldownUntil:       u.cooldownUntil,
			ConsecutiveFailures: u.consecutiveFailures,
		}
		if u.state == HealthStateCoolingDown && now.Before(u.cooldownUntil) {
			snap.Remaining = u.cooldownUntil.Sub(now)
		}
		out[key] = snap
	}
	return out
}

// availabilityDuration computes the cooldown duration for an availability
// failure given the current consecutive failure count. The duration is
// BaseCooldown × BackoffFactor^(failures - threshold), capped at MaxCooldown.
func (h *ProviderHealth) availabilityDuration(consecutiveFailures int) time.Duration {
	p := h.policy
	exp := consecutiveFailures - p.AvailabilityThreshold
	if exp < 0 {
		exp = 0
	}
	d := time.Duration(float64(p.BaseCooldown) * math.Pow(p.BackoffFactor, float64(exp)))
	if d > p.MaxCooldown {
		d = p.MaxCooldown
	}
	return d
}

// rateLimitDuration computes the cooldown duration for a rate-limit (429)
// failure. If the error carries a Retry-After value, that is used (clamped to
// [Floor, Ceiling]); otherwise the Floor duration is used.
func (h *ProviderHealth) rateLimitDuration(err error) time.Duration {
	p := h.policy
	d := p.RateLimitFloor
	var ue *UpstreamError
	if errors.As(err, &ue) && ue.RetryAfter > 0 {
		d = ue.RetryAfter
	}
	if d < p.RateLimitFloor {
		d = p.RateLimitFloor
	}
	if d > p.RateLimitCeiling {
		d = p.RateLimitCeiling
	}
	return d
}
