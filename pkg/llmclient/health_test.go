package llmclient

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"
)

func newTestHealth() *ProviderHealth {
	return NewProviderHealth()
}

func availErr(statusCode int) error {
	return &UpstreamError{StatusCode: statusCode}
}

// TestAvailabilityBackoffSequence asserts the 1.5× backoff:
// threshold=5 → 30s, 45s, 67.5s, 101.25s, 151.875s, 227.8125s, 341.71875s,
// 512.578125s, 768.8671875s, 1153.30078125s, 1729.951171875s (≈28m50s),
// capped at 15m=900s.
func TestAvailabilityBackoffSequence(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"
	threshold := h.Policy().AvailabilityThreshold // 5

	// Below threshold: still healthy, no cooldown.
	for i := 1; i < threshold; i++ {
		h.RecordFailure(provider, model, availErr(503))
		snap := h.HealthSnapshot()[unitKey(provider, model)]
		if snap.State != HealthStateHealthy {
			t.Fatalf("failure %d: state = %s, want %s", i, snap.State, HealthStateHealthy)
		}
		if snap.ConsecutiveFailures != i {
			t.Fatalf("failure %d: consecutiveFailures = %d, want %d", i, snap.ConsecutiveFailures, i)
		}
	}

	base := h.Policy().BaseCooldown    // 30s
	factor := h.Policy().BackoffFactor // 1.5
	maxCD := h.Policy().MaxCooldown    // 15m

	// At threshold and beyond: verify the backoff sequence.
	expect := base
	failure := threshold
	for {
		h.RecordFailure(provider, model, availErr(503))
		snap := h.HealthSnapshot()[unitKey(provider, model)]
		if snap.State != HealthStateCoolingDown {
			t.Fatalf("failure %d: state = %s, want %s", failure, snap.State, HealthStateCoolingDown)
		}
		remaining := snap.Remaining
		// Allow 1s slack for test execution time.
		if remaining < expect-1*time.Second || remaining > expect+1*time.Second {
			t.Fatalf("failure %d: remaining = %v, want ~%v", failure, remaining, expect)
		}

		// Check if we've hit the cap.
		if expect >= maxCD {
			break
		}

		failure++
		expect = time.Duration(float64(expect) * factor)
		if expect > maxCD {
			expect = maxCD
		}
		if failure > threshold+20 {
			t.Fatal("did not reach max cooldown cap within reasonable iterations")
		}
	}
}

func TestAvailabilityBackoffCapsAtMaxCooldown(t *testing.T) {
	h := newTestHealth()
	const provider, model = "anthropic", "claude-3"

	// Pre-seed with threshold failures at healthy state, then keep going
	// past the cap.
	p := h.Policy()
	for i := 0; i < p.AvailabilityThreshold+20; i++ {
		h.RecordFailure(provider, model, availErr(503))
	}

	snap := h.HealthSnapshot()[unitKey(provider, model)]
	if snap.State != HealthStateCoolingDown {
		t.Fatalf("state = %s, want %s", snap.State, HealthStateCoolingDown)
	}
	if snap.Remaining > p.MaxCooldown+2*time.Second {
		t.Fatalf("remaining = %v, want <= %v (+slack)", snap.Remaining, p.MaxCooldown)
	}
}

func TestRateLimitImmediateCooldown(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	// 429 without Retry-After: uses RateLimitFloor.
	h.RecordFailure(provider, model, &UpstreamError{StatusCode: 429})
	snap := h.HealthSnapshot()[unitKey(provider, model)]
	if snap.State != HealthStateCoolingDown {
		t.Fatalf("state = %s, want %s", snap.State, HealthStateCoolingDown)
	}
	if snap.Reason != HealthReasonRateLimit {
		t.Fatalf("reason = %s, want %s", snap.Reason, HealthReasonRateLimit)
	}
	floor := h.Policy().RateLimitFloor
	if snap.Remaining < floor-2*time.Second || snap.Remaining > floor+2*time.Second {
		t.Fatalf("remaining = %v, want ~%v", snap.Remaining, floor)
	}
}

func TestRateLimitRetryAfterClampedToFloor(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	// Retry-After below floor: should be clamped up to floor.
	h.RecordFailure(provider, model, &UpstreamError{StatusCode: 429, RetryAfter: 10 * time.Second})
	snap := h.HealthSnapshot()[unitKey(provider, model)]
	floor := h.Policy().RateLimitFloor
	if snap.Remaining < floor-2*time.Second {
		t.Fatalf("remaining = %v, want >= ~%v", snap.Remaining, floor)
	}
}

func TestRateLimitRetryAfterClampedToCeiling(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	// Retry-After above ceiling: should be clamped down to ceiling.
	h.RecordFailure(provider, model, &UpstreamError{StatusCode: 429, RetryAfter: 30 * time.Minute})
	snap := h.HealthSnapshot()[unitKey(provider, model)]
	ceiling := h.Policy().RateLimitCeiling
	if snap.Remaining > ceiling+2*time.Second {
		t.Fatalf("remaining = %v, want <= ~%v", snap.Remaining, ceiling)
	}
}

func TestQuotaFixedCooldown(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	h.RecordFailure(provider, model, &UpstreamError{StatusCode: 403, Code: "insufficient_user_quota"})
	snap := h.HealthSnapshot()[unitKey(provider, model)]
	if snap.State != HealthStateCoolingDown {
		t.Fatalf("state = %s, want %s", snap.State, HealthStateCoolingDown)
	}
	if snap.Reason != HealthReasonQuotaExhausted {
		t.Fatalf("reason = %s, want %s", snap.Reason, HealthReasonQuotaExhausted)
	}
	quota := h.Policy().QuotaCooldown
	if snap.Remaining < quota-2*time.Second || snap.Remaining > quota+2*time.Second {
		t.Fatalf("remaining = %v, want ~%v", snap.Remaining, quota)
	}
}

// TestModelDeprecatedCooldown verifies a relay-wrapped "model has been
// deprecated" failure (outer 400 [server_error], upstream 404) cools the unit
// with the model_deprecated reason instead of being ignored as a stop-class
// configuration error.
func TestModelDeprecatedCooldown(t *testing.T) {
	h := newTestHealth()
	const provider, model = "console-go", "xiaomi/mimo-v2"

	h.RecordFailure(provider, model, errors.New("gospore.handler.error: aiaggregator.dispatch: stream open: http 400 [server_error/]: Error from provider (Console Go): Upstream request failed: [404] This model has been deprecated. It is recommended to migrate to xiaomi/mimo-v2.5"))
	snap := h.HealthSnapshot()[unitKey(provider, model)]
	if snap.State != HealthStateCoolingDown {
		t.Fatalf("state = %s, want %s", snap.State, HealthStateCoolingDown)
	}
	if snap.Reason != HealthReasonModelDeprecated {
		t.Fatalf("reason = %s, want %s", snap.Reason, HealthReasonModelDeprecated)
	}
	want := h.Policy().ModelDeprecatedCooldown
	if snap.Remaining < want-2*time.Second || snap.Remaining > want+2*time.Second {
		t.Fatalf("remaining = %v, want ~%v", snap.Remaining, want)
	}
	if h.IsAvailable(provider, model, time.Now()) {
		t.Error("unit should be unavailable during model-deprecated cooldown")
	}
}

func TestDisabledMarkForAuthError(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	// 401 → authentication_failed → disabled
	h.RecordFailure(provider, model, &UpstreamError{StatusCode: 401})
	snap := h.HealthSnapshot()[unitKey(provider, model)]
	if snap.State != HealthStateDisabled {
		t.Fatalf("state = %s, want %s", snap.State, HealthStateDisabled)
	}
	if snap.Reason != HealthReasonAuthenticationFailed {
		t.Fatalf("reason = %s, want %s", snap.Reason, HealthReasonAuthenticationFailed)
	}
	if h.IsAvailable(provider, model, time.Now()) {
		t.Error("disabled unit should not be available")
	}

	// Success should NOT clear disabled state.
	h.RecordSuccess(provider, model)
	snap2 := h.HealthSnapshot()[unitKey(provider, model)]
	if snap2.State != HealthStateDisabled {
		t.Fatalf("after success: state = %s, want %s (disabled persists)", snap2.State, HealthStateDisabled)
	}
}

func TestDisabledMarkForAuthorizationError(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	// 403 (non-quota) → authorization_failed → disabled
	h.RecordFailure(provider, model, &UpstreamError{StatusCode: 403, Code: "permission_denied"})
	snap := h.HealthSnapshot()[unitKey(provider, model)]
	if snap.State != HealthStateDisabled {
		t.Fatalf("state = %s, want %s", snap.State, HealthStateDisabled)
	}
	if snap.Reason != HealthReasonAuthorizationFailed {
		t.Fatalf("reason = %s, want %s", snap.Reason, HealthReasonAuthorizationFailed)
	}
}

func TestStopClassNoHealthChange(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	// 400 → classStop → no health entry created
	h.RecordFailure(provider, model, &UpstreamError{StatusCode: 400})
	snap, ok := h.HealthSnapshot()[unitKey(provider, model)]
	if ok {
		t.Fatalf("expected no health entry for stop-class error, got %+v", snap)
	}
	if !h.IsAvailable(provider, model, time.Now()) {
		t.Error("stop-class error should not affect availability")
	}
}

func TestRecordSuccessClearsFailureCount(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	// Record 3 availability failures (below threshold=5).
	for i := 0; i < 3; i++ {
		h.RecordFailure(provider, model, availErr(503))
	}
	snap := h.HealthSnapshot()[unitKey(provider, model)]
	if snap.ConsecutiveFailures != 3 {
		t.Fatalf("consecutiveFailures = %d, want 3", snap.ConsecutiveFailures)
	}

	// Success clears failure count.
	h.RecordSuccess(provider, model)
	snap2 := h.HealthSnapshot()[unitKey(provider, model)]
	if snap2.ConsecutiveFailures != 0 {
		t.Fatalf("after success: consecutiveFailures = %d, want 0", snap2.ConsecutiveFailures)
	}
}

func TestRecordSuccessDoesNotShortenCooldown(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	// Trigger cooldown via 429.
	h.RecordFailure(provider, model, &UpstreamError{StatusCode: 429})
	before := h.HealthSnapshot()[unitKey(provider, model)]
	if before.State != HealthStateCoolingDown {
		t.Fatal("expected cooling_down")
	}

	// Success should not clear the cooldown.
	h.RecordSuccess(provider, model)
	after := h.HealthSnapshot()[unitKey(provider, model)]
	if after.State != HealthStateCoolingDown {
		t.Fatalf("state = %s, want %s (cooldown persists)", after.State, HealthStateCoolingDown)
	}
	if after.Remaining < before.Remaining-2*time.Second {
		t.Fatalf("remaining dropped from %v to %v (should not shorten)", before.Remaining, after.Remaining)
	}
}

func TestPolicyChangeDoesNotResetState(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	// Trigger cooldown via 429 with default policy.
	h.RecordFailure(provider, model, &UpstreamError{StatusCode: 429})
	before := h.HealthSnapshot()[unitKey(provider, model)]
	if before.State != HealthStateCoolingDown {
		t.Fatal("expected cooling_down")
	}
	beforeUntil := before.CooldownUntil

	// Change policy.
	h.SetCooldownPolicy(CooldownPolicy{
		AvailabilityThreshold: 3,
		BaseCooldown:          10 * time.Second,
		BackoffFactor:         2.0,
		MaxCooldown:           5 * time.Minute,
		RateLimitFloor:        30 * time.Second,
		RateLimitCeiling:      5 * time.Minute,
		QuotaCooldown:         2 * time.Minute,
	})

	// Existing cooldown should be untouched.
	after := h.HealthSnapshot()[unitKey(provider, model)]
	if after.State != HealthStateCoolingDown {
		t.Fatalf("state = %s, want %s", after.State, HealthStateCoolingDown)
	}
	if !after.CooldownUntil.Equal(beforeUntil) {
		t.Fatalf("cooldownUntil changed from %v to %v (should be unchanged)", beforeUntil, after.CooldownUntil)
	}
}

func TestPolicyChangeAffectsNewJudgements(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	// Set a custom policy with threshold=2, base=10s, factor=2.0, max=1m.
	h.SetCooldownPolicy(CooldownPolicy{
		AvailabilityThreshold: 2,
		BaseCooldown:          10 * time.Second,
		BackoffFactor:         2.0,
		MaxCooldown:           1 * time.Minute,
		RateLimitFloor:        30 * time.Second,
		RateLimitCeiling:      5 * time.Minute,
		QuotaCooldown:         2 * time.Minute,
	})

	// First failure (below threshold): healthy.
	h.RecordFailure(provider, model, availErr(503))
	snap := h.HealthSnapshot()[unitKey(provider, model)]
	if snap.State != HealthStateHealthy {
		t.Fatalf("failure 1: state = %s, want %s", snap.State, HealthStateHealthy)
	}

	// Second failure (at threshold=2): cooldown with 10s (10 * 2^0).
	h.RecordFailure(provider, model, availErr(503))
	snap = h.HealthSnapshot()[unitKey(provider, model)]
	if snap.State != HealthStateCoolingDown {
		t.Fatalf("failure 2: state = %s, want %s", snap.State, HealthStateCoolingDown)
	}
	if snap.Remaining > 11*time.Second || snap.Remaining < 9*time.Second {
		t.Fatalf("failure 2: remaining = %v, want ~10s", snap.Remaining)
	}

	// Third failure: 20s (10 * 2^1).
	h.RecordFailure(provider, model, availErr(503))
	snap = h.HealthSnapshot()[unitKey(provider, model)]
	if snap.Remaining > 21*time.Second || snap.Remaining < 19*time.Second {
		t.Fatalf("failure 3: remaining = %v, want ~20s", snap.Remaining)
	}
}

func TestIsAvailableUnknownUnitIsHealthy(t *testing.T) {
	h := newTestHealth()
	if !h.IsAvailable("unknown", "model", time.Now()) {
		t.Error("unknown unit should be available")
	}
}

func TestIsAvailableAfterCooldownExpires(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	// Short cooldown via 429 with tiny RetryAfter clamped to floor.
	h.RecordFailure(provider, model, &UpstreamError{StatusCode: 429})
	now := time.Now()
	if h.IsAvailable(provider, model, now) {
		t.Error("unit in cooldown should not be available")
	}

	// After cooldown expires, should be available again.
	floor := h.Policy().RateLimitFloor
	future := now.Add(floor + 1*time.Second)
	if !h.IsAvailable(provider, model, future) {
		t.Error("unit should be available after cooldown expires")
	}
}

func TestHealthSnapshotConcurrent(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	var wg sync.WaitGroup
	// Writers: record failures and successes concurrently.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if n%2 == 0 {
				h.RecordFailure(provider, model, availErr(429+n%100))
			} else {
				h.RecordSuccess(provider, model)
			}
		}(i)
	}
	// Readers: take snapshots concurrently.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = h.HealthSnapshot()
			_ = h.IsAvailable(provider, model, time.Now())
		}()
	}
	wg.Wait()
	// If we got here without panicking or data races (with -race), we pass.
}

func TestHealthSnapshotReturnsImmutableCopy(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	h.RecordFailure(provider, model, &UpstreamError{StatusCode: 429})
	snap1 := h.HealthSnapshot()
	entry := snap1[unitKey(provider, model)]

	// Mutate the returned entry — should not affect internal state.
	entry.Reason = "tampered"
	entry.State = "tampered"

	snap2 := h.HealthSnapshot()
	entry2 := snap2[unitKey(provider, model)]
	if entry2.Reason == "tampered" || entry2.State == "tampered" {
		t.Fatal("snapshot mutation leaked into internal state")
	}
}

func TestDisabledUnitStaysDisabledOnNewFailures(t *testing.T) {
	h := newTestHealth()
	const provider, model = "openai", "gpt-5"

	// Disable via 401.
	h.RecordFailure(provider, model, &UpstreamError{StatusCode: 401})
	snap := h.HealthSnapshot()[unitKey(provider, model)]
	if snap.State != HealthStateDisabled {
		t.Fatal("expected disabled after 401")
	}

	// New availability failure should not change disabled state.
	h.RecordFailure(provider, model, availErr(503))
	snap2 := h.HealthSnapshot()[unitKey(provider, model)]
	if snap2.State != HealthStateDisabled {
		t.Fatalf("state = %s, want %s (disabled persists)", snap2.State, HealthStateDisabled)
	}
}

func TestRecordFailureNilErrorIsNoop(t *testing.T) {
	h := newTestHealth()
	h.RecordFailure("openai", "gpt-5", nil)
	if len(h.HealthSnapshot()) != 0 {
		t.Fatal("nil error should not create a health entry")
	}
}

func TestRecordFailureEmptyProviderIsNoop(t *testing.T) {
	h := newTestHealth()
	h.RecordFailure("", "gpt-5", errors.New("boom"))
	if len(h.HealthSnapshot()) != 0 {
		t.Fatal("empty provider should not create a health entry")
	}
}

// TestClearProvider_RemovesOnlyTargetProvider verifies the per-provider clear
// primitive: units of the target provider (disabled and cooling_down alike)
// are removed, while other providers' units — including disabled ones — stay
// untouched. This is the primitive backing aimanager's provider reset, and the
// guarantee that a reset of one provider never leaks into another's health.
func TestClearProvider_RemovesOnlyTargetProvider(t *testing.T) {
	h := newTestHealth()

	// Target provider: one disabled (401) + one cooling (429) unit.
	h.RecordFailure("openai", "gpt-5", &UpstreamError{StatusCode: 401})
	h.RecordFailure("openai", "gpt-4o", &UpstreamError{StatusCode: 429})
	// Other provider: one disabled + one cooling unit.
	h.RecordFailure("anthropic", "claude-3", &UpstreamError{StatusCode: 403, Code: "permission_denied"})
	h.RecordFailure("anthropic", "claude-sonnet", &UpstreamError{StatusCode: 429})

	h.ClearProvider("openai")

	snap := h.HealthSnapshot()
	if _, ok := snap["openai::gpt-5"]; ok {
		t.Error("disabled unit of cleared provider must be removed")
	}
	if _, ok := snap["openai::gpt-4o"]; ok {
		t.Error("cooling unit of cleared provider must be removed")
	}
	if e, ok := snap["anthropic::claude-3"]; !ok || e.State != HealthStateDisabled {
		t.Errorf("other provider's disabled unit must survive the clear, got %+v", e)
	}
	if e, ok := snap["anthropic::claude-sonnet"]; !ok || e.State != HealthStateCoolingDown {
		t.Errorf("other provider's cooling unit must survive the clear, got %+v", e)
	}
	if !h.IsAvailable("openai", "gpt-5", time.Now()) {
		t.Error("cleared provider unit must be selectable again")
	}
	if h.IsAvailable("anthropic", "claude-3", time.Now()) {
		t.Error("other provider's disabled unit must stay unavailable")
	}

	// Clearing an unknown provider is a no-op.
	h.ClearProvider("ghost")
	if len(h.HealthSnapshot()) != 2 {
		t.Errorf("unknown-provider clear must be a no-op, snapshot has %d entries", len(h.HealthSnapshot()))
	}
}

// TestClearProvider_ConcurrentWithReaders verifies ClearProvider is safe to
// call while other goroutines record failures and take snapshots (no data
// races under -race).
func TestClearProvider_ConcurrentWithReaders(t *testing.T) {
	h := newTestHealth()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			p := "prov"
			if n%2 == 0 {
				p = "other"
			}
			h.RecordFailure(p, "m", &UpstreamError{StatusCode: 429})
			_ = h.HealthSnapshot()
			if n%5 == 0 {
				h.ClearProvider(p)
			}
		}(i)
	}
	wg.Wait()
	// No panic / no race is the pass condition; just verify a consistent
	// snapshot can still be taken afterwards.
	_ = h.HealthSnapshot()
}

func TestAvailabilityBackoffDurationDirect(t *testing.T) {
	h := newTestHealth()
	h.SetCooldownPolicy(CooldownPolicy{
		AvailabilityThreshold: 5,
		BaseCooldown:          30 * time.Second,
		BackoffFactor:         1.5,
		MaxCooldown:           15 * time.Minute,
	})

	cases := []struct {
		failures int
		want     time.Duration
	}{
		{5, 30 * time.Second},
		{6, 45 * time.Second},
		{7, 67500 * time.Millisecond},
		{8, 101250 * time.Millisecond},
		{9, 151875 * time.Millisecond},
		{10, 227812*time.Millisecond + 500*time.Microsecond},
		{11, 341718*time.Millisecond + 750*time.Microsecond},
		{12, 512578*time.Millisecond + 125*time.Microsecond},
		{13, 768867*time.Millisecond + 187*time.Microsecond + 500*time.Nanosecond},
		{14, 900 * time.Second}, // capped at 15m
		{15, 900 * time.Second},
	}
	for _, tc := range cases {
		got := h.availabilityDuration(tc.failures)
		if got != tc.want {
			t.Errorf("failures=%d: got %v, want %v", tc.failures, got, tc.want)
		}
	}
}

// netErr is a minimal net.Error for classification tests.
type netErr struct{}

func (netErr) Error() string   { return "network error" }
func (netErr) Timeout() bool   { return false }
func (netErr) Temporary() bool { return true }

func TestClassifyStreamOpenError_NetError(t *testing.T) {
	got := ClassifyStreamOpenError(netErr{})
	if got.Class != ClassFailover || got.Reason != HealthReasonAvailability || !got.Rotatable {
		t.Errorf("net.Error classification = %+v, want failover/availability", got)
	}
}

func TestSplitUnitKeyMalformed(t *testing.T) {
	provider, model := splitUnitKey("nomodelkey")
	if provider != "nomodelkey" || model != "" {
		t.Errorf("splitUnitKey('nomodelkey') = (%q, %q), want ('nomodelkey', '')", provider, model)
	}
}

func TestUnitKeyAndSplit(t *testing.T) {
	cases := []struct {
		provider, model string
	}{
		{"openai", "gpt-5"},
		{"anthropic", "claude-3-opus"},
		{"a", "b"},
	}
	for _, c := range cases {
		key := unitKey(c.provider, c.model)
		p, m := splitUnitKey(key)
		if p != c.provider || m != c.model {
			t.Errorf("splitUnitKey(%q) = (%q, %q), want (%q, %q)", key, p, m, c.provider, c.model)
		}
	}
}

func TestDefaultCooldownPolicy(t *testing.T) {
	p := DefaultCooldownPolicy()
	if p.AvailabilityThreshold != 5 {
		t.Errorf("AvailabilityThreshold = %d, want 5", p.AvailabilityThreshold)
	}
	if p.BaseCooldown != 30*time.Second {
		t.Errorf("BaseCooldown = %v, want 30s", p.BaseCooldown)
	}
	if p.BackoffFactor != 1.5 {
		t.Errorf("BackoffFactor = %v, want 1.5", p.BackoffFactor)
	}
	if p.MaxCooldown != 15*time.Minute {
		t.Errorf("MaxCooldown = %v, want 15m", p.MaxCooldown)
	}
	if p.RateLimitFloor != 60*time.Second {
		t.Errorf("RateLimitFloor = %v, want 60s", p.RateLimitFloor)
	}
	if p.RateLimitCeiling != 15*time.Minute {
		t.Errorf("RateLimitCeiling = %v, want 15m", p.RateLimitCeiling)
	}
	if p.QuotaCooldown != 10*time.Minute {
		t.Errorf("QuotaCooldown = %v, want 10m", p.QuotaCooldown)
	}
}

// Ensure package-level wrappers delegate to DefaultProviderHealth.
func TestPackageLevelWrappers(t *testing.T) {
	// Save/restore default.
	orig := DefaultProviderHealth
	defer func() { DefaultProviderHealth = orig }()
	DefaultProviderHealth = NewProviderHealth()

	SetCooldownPolicy(CooldownPolicy{
		AvailabilityThreshold: 1,
		BaseCooldown:          5 * time.Second,
		BackoffFactor:         2.0,
		MaxCooldown:           1 * time.Minute,
		RateLimitFloor:        10 * time.Second,
		RateLimitCeiling:      1 * time.Minute,
		QuotaCooldown:         30 * time.Second,
	})

	RecordFailure("test", "m1", &UpstreamError{StatusCode: 429})
	if IsAvailable("test", "m1", time.Now()) {
		t.Error("expected unavailable after 429")
	}

	snap := HealthSnapshot()
	if _, ok := snap["test::m1"]; !ok {
		t.Fatal("expected health snapshot entry for test::m1")
	}

	RecordSuccess("test", "m1")
	// Cooldown should persist even after success.
	snap2 := HealthSnapshot()
	if entry, ok := snap2["test::m1"]; ok && entry.State == HealthStateHealthy {
		t.Error("cooldown should persist after success")
	}
}

// Verify that the default global instance is non-nil and usable.
func TestDefaultProviderHealthNotNil(t *testing.T) {
	if DefaultProviderHealth == nil {
		t.Fatal("DefaultProviderHealth is nil")
	}
	_ = DefaultProviderHealth.Policy()
}

// ──────────────────────────────────────────────────────────────────────────────
// Aggregator-level health registry
// ──────────────────────────────────────────────────────────────────────────────

// TestAggregatorHealthRegisterUnavailable verifies that an unavailable
// registration is visible in the snapshot with a positive remaining TTL and
// makes the aggregator unavailable for routing.
func TestAggregatorHealthRegisterUnavailable(t *testing.T) {
	r := NewAggregatorHealthRegistry()
	r.Register("child", AggregatorHealthUnavailable)

	snap := r.Snapshot()
	e, ok := snap["child"]
	if !ok {
		t.Fatal("expected snapshot entry for child")
	}
	if e.State != AggregatorHealthUnavailable {
		t.Errorf("state = %s, want %s", e.State, AggregatorHealthUnavailable)
	}
	if e.AggregatorID != "child" {
		t.Errorf("AggregatorID = %q, want child", e.AggregatorID)
	}
	if e.Remaining <= 0 {
		t.Errorf("Remaining = %v, want > 0", e.Remaining)
	}
	if r.IsAvailable("child", time.Now()) {
		t.Error("child must be unavailable while registered")
	}
}

// TestAggregatorHealthExplicitClear verifies that registering the available
// state removes the entry — the recovery signal.
func TestAggregatorHealthExplicitClear(t *testing.T) {
	r := NewAggregatorHealthRegistry()
	r.Register("child", AggregatorHealthUnavailable)
	if r.IsAvailable("child", time.Now()) {
		t.Fatal("child should be unavailable before clear")
	}
	r.Register("child", AggregatorHealthAvailable)
	if len(r.Snapshot()) != 0 {
		t.Errorf("snapshot after clear = %+v, want empty", r.Snapshot())
	}
	if !r.IsAvailable("child", time.Now()) {
		t.Error("child should be available after clear")
	}
}

// TestAggregatorHealthUnavailableUntilNeverShortens verifies the explicit
// deadline registration never shortens an existing unavailable deadline — a
// parent-side default-TTL write must not cut short a longer deadline the
// child derived from its own unit cooldowns — while a later, longer deadline
// still extends it.
func TestAggregatorHealthUnavailableUntilNeverShortens(t *testing.T) {
	r := NewAggregatorHealthRegistry()
	now := time.Now()
	long := now.Add(90 * time.Second)
	r.RegisterUnavailableUntil("child", long)

	r.RegisterUnavailableUntil("child", now.Add(10*time.Second))
	e, ok := r.Snapshot()["child"]
	if !ok {
		t.Fatal("expected entry to survive shorter write")
	}
	if !e.UnavailableUntil.Equal(long) {
		t.Errorf("deadline = %v, want unchanged %v", e.UnavailableUntil, long)
	}

	longer := now.Add(120 * time.Second)
	r.RegisterUnavailableUntil("child", longer)
	e, ok = r.Snapshot()["child"]
	if !ok || !e.UnavailableUntil.Equal(longer) {
		t.Errorf("deadline = %v, want extended %v", e.UnavailableUntil, longer)
	}

	// A plain Register (registry TTL) also must not shorten the explicit
	// deadline.
	r.Register("child", AggregatorHealthUnavailable)
	e, ok = r.Snapshot()["child"]
	if !ok || !e.UnavailableUntil.Equal(longer) {
		t.Errorf("deadline after plain Register = %v, want unchanged %v", e.UnavailableUntil, longer)
	}
}

// TestAggregatorHealthTTLExpiry verifies that an unavailable registration
// auto-invalidates once its TTL passes — the snapshot omits it and routing
// sees the aggregator available again.
func TestAggregatorHealthTTLExpiry(t *testing.T) {
	r := NewAggregatorHealthRegistry()
	r.SetTTL(20 * time.Millisecond)
	r.Register("child", AggregatorHealthUnavailable)

	if _, ok := r.Snapshot()["child"]; !ok {
		t.Fatal("expected entry before TTL expiry")
	}
	if r.IsAvailable("child", time.Now()) {
		t.Fatal("child should be unavailable before TTL expiry")
	}

	time.Sleep(60 * time.Millisecond)
	if len(r.Snapshot()) != 0 {
		t.Errorf("snapshot after TTL = %+v, want empty (auto-invalidated)", r.Snapshot())
	}
	if !r.IsAvailable("child", time.Now()) {
		t.Error("child should be available after TTL expiry")
	}
}

// TestAggregatorHealthEmptyIDIgnored verifies that an empty aggregator id is
// never registered — unknown/empty ids are always available.
func TestAggregatorHealthEmptyIDIgnored(t *testing.T) {
	r := NewAggregatorHealthRegistry()
	r.Register("", AggregatorHealthUnavailable)
	if len(r.Snapshot()) != 0 {
		t.Errorf("snapshot = %+v, want empty for empty id", r.Snapshot())
	}
	if !r.IsAvailable("", time.Now()) {
		t.Error("empty id must always be available")
	}
}

// TestAggregatorHealthUnknownIDAvailable verifies that aggregators never
// registered are implicitly available.
func TestAggregatorHealthUnknownIDAvailable(t *testing.T) {
	r := NewAggregatorHealthRegistry()
	if !r.IsAvailable("never-registered", time.Now()) {
		t.Error("never-registered aggregator must be available")
	}
}

// TestAggregatorHealthConcurrentAccess exercises the registry from multiple
// goroutines to catch data races under -race.
func TestAggregatorHealthConcurrentAccess(t *testing.T) {
	r := NewAggregatorHealthRegistry()
	r.SetTTL(5 * time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := "agg-" + string(rune('a'+n))
			for j := 0; j < 200; j++ {
				r.Register(id, AggregatorHealthUnavailable)
				_ = r.Snapshot()
				_ = r.IsAvailable(id, time.Now())
				r.Register(id, AggregatorHealthAvailable)
			}
		}(i)
	}
	wg.Wait()
	if len(r.Snapshot()) != 0 {
		t.Errorf("snapshot after concurrent churn = %+v, want empty", r.Snapshot())
	}
}

// TestAggregatorHealthPackageWrappers verifies the package-level functions
// delegate to the process-wide DefaultAggregatorHealth.
func TestAggregatorHealthPackageWrappers(t *testing.T) {
	orig := DefaultAggregatorHealth
	defer func() { DefaultAggregatorHealth = orig }()
	DefaultAggregatorHealth = NewAggregatorHealthRegistry()
	defer RegisterAggregatorHealth("child", AggregatorHealthAvailable) // cleanup

	RegisterAggregatorHealth("child", AggregatorHealthUnavailable)
	if IsAggregatorAvailable("child", time.Now()) {
		t.Error("expected child unavailable via package-level wrapper")
	}
	snap := AggregatorHealthSnapshot()
	if e, ok := snap["child"]; !ok || e.State != AggregatorHealthUnavailable {
		t.Errorf("snapshot = %+v, want child/unavailable", snap)
	}
	RegisterAggregatorHealth("child", AggregatorHealthAvailable)
	if len(AggregatorHealthSnapshot()) != 0 {
		t.Errorf("snapshot after clear = %+v, want empty", AggregatorHealthSnapshot())
	}
	if !IsAggregatorAvailable("child", time.Now()) {
		t.Error("expected child available after clear")
	}
}

// TestRecordFailure_DifferentClassDoesNotShortenCooldown pins the no-shrink
// rule: an error arriving while the unit is already cooling to a LATER
// deadline must back off from that deadline, not restart from its own base.
// Alternating error classes (e.g. 429 landing on a quota cooldown, or
// availability backoff landing on a long Retry-After) previously overwrote the
// deadline with their own shorter base, so the cooldown kept collapsing toward
// the shortest value and routers re-probed an unreachable unit at dispatch
// rate.
func TestRecordFailure_DifferentClassDoesNotShortenCooldown(t *testing.T) {
	t.Run("429 does not shrink a quota cooldown", func(t *testing.T) {
		h := newTestHealth()
		const provider, model = "openai", "gpt-5"

		h.RecordFailure(provider, model, &UpstreamError{StatusCode: 403, Code: "insufficient_user_quota"})
		h.RecordFailure(provider, model, &UpstreamError{StatusCode: 429})

		snap := h.HealthSnapshot()[unitKey(provider, model)]
		if snap.State != HealthStateCoolingDown {
			t.Fatalf("state = %s, want %s", snap.State, HealthStateCoolingDown)
		}
		quota := h.Policy().QuotaCooldown // 10m
		if snap.Remaining < quota-2*time.Second {
			t.Fatalf("remaining = %v, want ~%v: a 429 shrunk the quota cooldown", snap.Remaining, quota)
		}
		if snap.Reason != HealthReasonQuotaExhausted {
			t.Fatalf("reason = %s, want %s", snap.Reason, HealthReasonQuotaExhausted)
		}
	})

	t.Run("availability backoff does not shrink a Retry-After cooldown", func(t *testing.T) {
		h := newTestHealth()
		const provider, model = "kimi", "k2"
		retryAfter := 14 * time.Minute

		h.RecordFailure(provider, model, &UpstreamError{StatusCode: 429, RetryAfter: retryAfter})
		// Availability failures past the threshold compute a 30s base
		// backoff — far shorter than the active Retry-After deadline.
		for i := 0; i <= h.Policy().AvailabilityThreshold; i++ {
			h.RecordFailure(provider, model, availErr(503))
		}

		snap := h.HealthSnapshot()[unitKey(provider, model)]
		if snap.Remaining < retryAfter-2*time.Second {
			t.Fatalf("remaining = %v, want ~%v: availability backoff shrunk the Retry-After cooldown", snap.Remaining, retryAfter)
		}
	})

	t.Run("a longer deadline still extends", func(t *testing.T) {
		h := newTestHealth()
		const provider, model = "deepseek", "v3"
		floor := h.Policy().RateLimitFloor

		h.RecordFailure(provider, model, &UpstreamError{StatusCode: 429}) // floor cooldown
		retryAfter := 5 * time.Minute
		h.RecordFailure(provider, model, &UpstreamError{StatusCode: 429, RetryAfter: retryAfter})

		snap := h.HealthSnapshot()[unitKey(provider, model)]
		if snap.Remaining < retryAfter-2*time.Second {
			t.Fatalf("remaining = %v, want ~%v: longer deadline must extend", snap.Remaining, retryAfter)
		}
		if snap.Remaining < floor {
			t.Fatalf("sanity: remaining = %v below floor %v", snap.Remaining, floor)
		}
	})
}

// TestConnectStageClassification verifies connect-stage detection: dial
// refused/timeout and TLS handshake failures happened before any HTTP
// response; HTTP-status errors (5xx/429) did not.
func TestConnectStageClassification(t *testing.T) {
	connRefused := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}
	if c := ClassifyStreamOpenError(connRefused); !c.ConnectStage || c.Class != ClassFailover {
		t.Fatalf("dial refused: class=%s connectStage=%v, want ClassFailover/true", c.Class, c.ConnectStage)
	}
	if c := ClassifyStreamOpenError(fmt.Errorf("post https://x/y: %w", connRefused)); !c.ConnectStage {
		t.Fatal("url.Error-wrapped dial refused must still be connect-stage")
	}
	tlsTimeout := &net.OpError{Op: "remote error", Net: "tcp", Err: errors.New("tls: handshake timeout")}
	if c := ClassifyStreamOpenError(tlsTimeout); !c.ConnectStage {
		t.Fatal("TLS handshake timeout must be connect-stage")
	}
	if c := ClassifyStreamOpenError(&UpstreamError{StatusCode: 503}); c.ConnectStage {
		t.Fatal("HTTP 503 means the connection succeeded: not connect-stage")
	}
	if c := ClassifyStreamOpenError(&UpstreamError{StatusCode: 429}); c.ConnectStage {
		t.Fatal("HTTP 429 means the connection succeeded: not connect-stage")
	}
}

// TestConnectFailureImmediateCooldown pins the first-failure rule for
// connect-stage errors: an unreachable endpoint cools immediately (default
// 15s) instead of waiting for the availability threshold, so routers stop
// re-dialing it at dispatch rate. Non-connect availability failures keep the
// threshold semantics.
func TestConnectFailureImmediateCooldown(t *testing.T) {
	t.Run("first connect failure cools immediately", func(t *testing.T) {
		h := newTestHealth()
		const provider, model = "unreachable", "m"

		connRefused := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED}}
		h.RecordFailure(provider, model, connRefused)

		snap := h.HealthSnapshot()[unitKey(provider, model)]
		if snap.State != HealthStateCoolingDown {
			t.Fatalf("state = %s, want %s on first connect failure", snap.State, HealthStateCoolingDown)
		}
		d := h.Policy().ConnectFailureCooldown
		if snap.Remaining < d-2*time.Second || snap.Remaining > d+2*time.Second {
			t.Fatalf("remaining = %v, want ~%v", snap.Remaining, d)
		}
		if snap.ConsecutiveFailures != 1 {
			t.Fatalf("consecutiveFailures = %d, want 1", snap.ConsecutiveFailures)
		}
	})

	t.Run("repeated connect failures extend, never shorten", func(t *testing.T) {
		h := newTestHealth()
		const provider, model = "unreachable2", "m"

		dial := func() { h.RecordFailure(provider, model, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("i/o timeout")}) }
		dial()
		first := h.HealthSnapshot()[unitKey(provider, model)]
		dial()
		second := h.HealthSnapshot()[unitKey(provider, model)]
		if second.CooldownUntil.Before(first.CooldownUntil) {
			t.Fatalf("second connect failure shortened the deadline: %v < %v", second.CooldownUntil, first.CooldownUntil)
		}
	})

	t.Run("non-connect availability failures keep the threshold", func(t *testing.T) {
		h := newTestHealth()
		const provider, model = "flaky5xx", "m"

		h.RecordFailure(provider, model, availErr(503))

		snap := h.HealthSnapshot()[unitKey(provider, model)]
		if snap.State != HealthStateHealthy {
			t.Fatalf("state = %s, want %s below the availability threshold", snap.State, HealthStateHealthy)
		}
	})
}
