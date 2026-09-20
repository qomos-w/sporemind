package aimanager

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

// TestHandleUnitHealthList_EmptySnapshot verifies that an empty health registry
// produces an empty (non-nil) items slice.
func TestHandleUnitHealthList_EmptySnapshot(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{}
	resp, err := a.handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Items == nil {
		t.Fatal("Items should be non-nil")
	}
	if len(resp.Items) != 0 {
		t.Fatalf("expected 0 items, got %d", len(resp.Items))
	}
}

// TestHandleUnitHealthList_CoolingDown verifies that a unit in active cooldown
// is surfaced with the correct health fields.
func TestHandleUnitHealthList_CoolingDown(t *testing.T) {
	resetHealth()
	defer resetHealth()

	deadline := time.Now().Add(5 * time.Minute)
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{
		StatusCode: 429,
		RetryAfter: 5 * time.Minute,
	})

	a := &Actor{}
	resp, err := a.handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}

	item := resp.Items[0]
	if item.ID != "openai::gpt-4o" {
		t.Errorf("ID = %q, want openai::gpt-4o", item.ID)
	}
	if item.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", item.Model)
	}
	if item.ProviderName != "openai" {
		t.Errorf("ProviderName = %q, want openai", item.ProviderName)
	}
	if item.HealthState != llmclient.HealthStateCoolingDown {
		t.Errorf("HealthState = %q, want %q", item.HealthState, llmclient.HealthStateCoolingDown)
	}
	if item.HealthReason != llmclient.HealthReasonRateLimit {
		t.Errorf("HealthReason = %q, want %q", item.HealthReason, llmclient.HealthReasonRateLimit)
	}
	if item.RecoveryMode != recoveryCooldown {
		t.Errorf("RecoveryMode = %q, want %q", item.RecoveryMode, recoveryCooldown)
	}
	if item.CooldownUntil <= 0 {
		t.Errorf("CooldownUntil should be positive, got %d", item.CooldownUntil)
	}
	if item.CooldownUntil < deadline.Add(-10*time.Second).Unix() || item.CooldownUntil > deadline.Add(10*time.Second).Unix() {
		t.Errorf("CooldownUntil = %d, want ~%d", item.CooldownUntil, deadline.Unix())
	}
	if item.DispatchActivity != nil {
		t.Error("DispatchActivity should be nil (aggregator-local)")
	}
}

// TestHandleUnitHealthList_Disabled verifies that a disabled unit is surfaced
// with CooldownUntil=0 and manual recovery mode.
func TestHandleUnitHealthList_Disabled(t *testing.T) {
	resetHealth()
	defer resetHealth()

	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{
		StatusCode: 401,
		Code:       "invalid_api_key",
	})

	a := &Actor{}
	resp, err := a.handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}

	item := resp.Items[0]
	if item.HealthState != llmclient.HealthStateDisabled {
		t.Errorf("HealthState = %q, want %q", item.HealthState, llmclient.HealthStateDisabled)
	}
	if item.HealthReason != llmclient.HealthReasonAuthenticationFailed {
		t.Errorf("HealthReason = %q, want %q", item.HealthReason, llmclient.HealthReasonAuthenticationFailed)
	}
	if item.RecoveryMode != recoveryManualOrBalance {
		t.Errorf("RecoveryMode = %q, want %q", item.RecoveryMode, recoveryManualOrBalance)
	}
	if item.CooldownUntil != 0 {
		t.Errorf("disabled CooldownUntil should be 0, got %d", item.CooldownUntil)
	}
}

// TestHandleUnitHealthList_ExpiredCooldownOmitted verifies that a cooling_down
// unit whose deadline has passed is not surfaced (normalized to healthy).
func TestHandleUnitHealthList_ExpiredCooldownOmitted(t *testing.T) {
	resetHealth()
	defer resetHealth()

	// Use a short floor so the cooldown expires within the test.
	llmclient.DefaultProviderHealth.SetCooldownPolicy(llmclient.CooldownPolicy{
		AvailabilityThreshold: 5,
		BaseCooldown:          30 * time.Second,
		BackoffFactor:         1.5,
		MaxCooldown:           15 * time.Minute,
		RateLimitFloor:        100 * time.Millisecond,
		RateLimitCeiling:      15 * time.Minute,
		QuotaCooldown:         10 * time.Minute,
	})

	// Record a rate-limit failure — cooldown floor is now 100ms.
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{
		StatusCode: 429,
		RetryAfter: 100 * time.Millisecond,
	})

	// Verify the unit is initially surfaced.
	a := &Actor{}
	resp, err := a.handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item before expiry, got %d", len(resp.Items))
	}

	// Wait for the cooldown to expire.
	time.Sleep(200 * time.Millisecond)

	resp, err = a.handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("expired cooldown should be omitted, got %d items", len(resp.Items))
	}
}

// TestHandleUnitHealthList_MixedStates verifies that only non-healthy units are
// surfaced and that the output is sorted by key for determinism.
func TestHandleUnitHealthList_MixedStates(t *testing.T) {
	resetHealth()
	defer resetHealth()

	// openai::gpt-4o → disabled (auth failure)
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{
		StatusCode: 401,
		Code:       "invalid_api_key",
	})
	// anthropic::claude-3 → cooling_down (rate limit)
	llmclient.RecordFailure("anthropic", "claude-3", &llmclient.UpstreamError{
		StatusCode: 429,
		RetryAfter: 5 * time.Minute,
	})

	a := &Actor{}
	resp, err := a.handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}

	// Sorted by key: anthropic::claude-3, openai::gpt-4o
	if resp.Items[0].ID != "anthropic::claude-3" {
		t.Errorf("first item ID = %q, want anthropic::claude-3", resp.Items[0].ID)
	}
	if resp.Items[0].HealthState != llmclient.HealthStateCoolingDown {
		t.Errorf("first item state = %q, want cooling_down", resp.Items[0].HealthState)
	}
	if resp.Items[1].ID != "openai::gpt-4o" {
		t.Errorf("second item ID = %q, want openai::gpt-4o", resp.Items[1].ID)
	}
	if resp.Items[1].HealthState != llmclient.HealthStateDisabled {
		t.Errorf("second item state = %q, want disabled", resp.Items[1].HealthState)
	}
}

// TestHandleUnitHealthList_IdentityFields verifies that the identity fields
// (Model, ProviderName) are correctly populated from the snapshot.
func TestHandleUnitHealthList_IdentityFields(t *testing.T) {
	resetHealth()
	defer resetHealth()

	llmclient.RecordFailure("deepseek", "deepseek-chat", &llmclient.UpstreamError{
		StatusCode: 403,
		Code:       "insufficient_user_quota",
	})

	a := &Actor{}
	resp, err := a.handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}

	item := resp.Items[0]
	if item.ProviderName != "deepseek" {
		t.Errorf("ProviderName = %q, want deepseek", item.ProviderName)
	}
	if item.Model != "deepseek-chat" {
		t.Errorf("Model = %q, want deepseek-chat", item.Model)
	}
	if item.ID != "deepseek::deepseek-chat" {
		t.Errorf("ID = %q, want deepseek::deepseek-chat", item.ID)
	}
	if item.HealthState != llmclient.HealthStateCoolingDown {
		t.Errorf("HealthState = %q, want cooling_down", item.HealthState)
	}
	if item.HealthReason != llmclient.HealthReasonQuotaExhausted {
		t.Errorf("HealthReason = %q, want %q", item.HealthReason, llmclient.HealthReasonQuotaExhausted)
	}
	if item.CooldownUntil <= 0 {
		t.Error("quota cooldown should have non-zero CooldownUntil")
	}
	if item.RecoveryMode != recoveryCooldown {
		t.Errorf("RecoveryMode = %q, want %q", item.RecoveryMode, recoveryCooldown)
	}
}

// TestHandleUnitHealthList_AggregatorFieldsEmpty verifies that aggregator-local
// fields (Endpoint, Protocol, AggregatorID, DispatchActivity, OnDemand) are
// left empty for global health entries.
func TestHandleUnitHealthList_AggregatorFieldsEmpty(t *testing.T) {
	resetHealth()
	defer resetHealth()

	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{
		StatusCode: 429,
		RetryAfter: 5 * time.Minute,
	})

	a := &Actor{}
	resp, err := a.handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}

	item := resp.Items[0]
	if item.Endpoint != "" {
		t.Errorf("Endpoint should be empty, got %q", item.Endpoint)
	}
	if item.Protocol != "" {
		t.Errorf("Protocol should be empty, got %q", item.Protocol)
	}
	if item.AggregatorID != "" {
		t.Errorf("AggregatorID should be empty, got %q", item.AggregatorID)
	}
	if item.DispatchActivity != nil {
		t.Error("DispatchActivity should be nil")
	}
	if item.OnDemand {
		t.Error("OnDemand should be false (not aggregator-specific)")
	}
}

// TestHandleUnitHealthList_ConsecutiveFailures verifies that the consecutive
// failure count is projected from the snapshot.
func TestHandleUnitHealthList_ConsecutiveFailures(t *testing.T) {
	resetHealth()
	defer resetHealth()

	// Record multiple availability failures to build up consecutive count
	// (below the threshold of 5, so unit stays healthy but has failures).
	for i := 0; i < 3; i++ {
		llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{
			StatusCode: 500,
		})
	}

	// 3 failures < threshold (5), so the unit is still healthy → not surfaced.
	a := &Actor{}
	resp, err := a.handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("unit below threshold should be healthy and not surfaced, got %d items", len(resp.Items))
	}

	// Now hit the threshold to trigger a cooldown.
	for i := 0; i < 3; i++ {
		llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{
			StatusCode: 500,
		})
	}

	resp, err = a.handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item after threshold, got %d", len(resp.Items))
	}
	item := resp.Items[0]
	if item.HealthState != llmclient.HealthStateCoolingDown {
		t.Errorf("HealthState = %q, want cooling_down", item.HealthState)
	}
	if item.ConsecutiveFailures < 5 {
		t.Errorf("ConsecutiveFailures = %d, want >= 5", item.ConsecutiveFailures)
	}
}

// windowProvider builds an actor with one provider whose disable window is
// currently active (start 2 minutes ago, end 30 minutes from now) and the given
// model names.
func windowProvider(models ...string) *Actor {
	now := time.Now()
	return &Actor{Providers: []domain.Provider{{
		Name:   "openai",
		Kind:   "openai",
		Models: modelList(models),
		DisableWindows: []domain.ProviderDisableWindow{{
			Start: now.Add(-2 * time.Minute).Format("15:04"),
			End:   now.Add(30 * time.Minute).Format("15:04"),
		}},
	}}}
}

func modelList(names []string) []domain.ProviderModel {
	ms := make([]domain.ProviderModel, len(names))
	for i, n := range names {
		ms[i] = domain.ProviderModel{Name: n}
	}
	return ms
}

// TestHandleUnitHealthList_DisableWindowSynthesizesHealthyUnits verifies that
// units under an active disable window are surfaced even without any failure
// record — otherwise the composer renders them as healthy rows.
func TestHandleUnitHealthList_DisableWindowSynthesizesHealthyUnits(t *testing.T) {
	resetHealth()
	defer resetHealth()

	resp, err := windowProvider("gpt-4o", "gpt-4o-mini").handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 synthesized items, got %d", len(resp.Items))
	}
	for _, item := range resp.Items {
		if item.HealthState != llmclient.HealthStateCoolingDown {
			t.Errorf("%s: HealthState = %q, want cooling_down", item.ID, item.HealthState)
		}
		if item.HealthReason != disableWindowReason {
			t.Errorf("%s: HealthReason = %q, want %q", item.ID, item.HealthReason, disableWindowReason)
		}
		if item.RecoveryMode != recoveryCooldown {
			t.Errorf("%s: RecoveryMode = %q, want %q", item.ID, item.RecoveryMode, recoveryCooldown)
		}
		if item.DisableUntil <= time.Now().Unix() {
			t.Errorf("%s: DisableUntil = %d, want future deadline", item.ID, item.DisableUntil)
		}
		if item.CooldownUntil != item.DisableUntil {
			t.Errorf("%s: CooldownUntil = %d, want window deadline %d", item.ID, item.CooldownUntil, item.DisableUntil)
		}
	}
	if resp.Items[0].ID != "openai::gpt-4o" || resp.Items[1].ID != "openai::gpt-4o-mini" {
		t.Errorf("items not sorted by key: %q, %q", resp.Items[0].ID, resp.Items[1].ID)
	}
}

// TestHandleUnitHealthList_DisableWindowDominatesFailureCooldown verifies that
// when both a failure cooldown and an active disable window apply, the surfaced
// deadline is the later of the two so the composer countdown cannot expire
// while the window still blocks the unit.
func TestHandleUnitHealthList_DisableWindowDominatesFailureCooldown(t *testing.T) {
	resetHealth()
	defer resetHealth()

	// Failure cooldown ends in ~5 minutes; the window ends in ~30.
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{
		StatusCode: 429,
		RetryAfter: 5 * time.Minute,
	})

	resp, err := windowProvider("gpt-4o", "gpt-4o-mini").handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}
	for _, item := range resp.Items {
		if item.HealthReason != disableWindowReason {
			t.Errorf("%s: HealthReason = %q, want %q", item.ID, item.HealthReason, disableWindowReason)
		}
		minDeadline := time.Now().Add(25 * time.Minute).Unix()
		if item.CooldownUntil < minDeadline {
			t.Errorf("%s: CooldownUntil = %d, want >= window deadline ~%d", item.ID, item.CooldownUntil, minDeadline)
		}
	}
}

// TestHandleUnitHealthList_ExpiredCooldownUnderWindowStillSurfaced verifies
// that an expired failure cooldown is not omitted while the disable window is
// still active.
func TestHandleUnitHealthList_ExpiredCooldownUnderWindowStillSurfaced(t *testing.T) {
	resetHealth()
	defer resetHealth()

	llmclient.DefaultProviderHealth.SetCooldownPolicy(llmclient.CooldownPolicy{
		AvailabilityThreshold: 5,
		BaseCooldown:          30 * time.Second,
		BackoffFactor:         1.5,
		MaxCooldown:           15 * time.Minute,
		RateLimitFloor:        100 * time.Millisecond,
		RateLimitCeiling:      15 * time.Minute,
		QuotaCooldown:         10 * time.Minute,
	})
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{
		StatusCode: 429,
		RetryAfter: 100 * time.Millisecond,
	})
	time.Sleep(200 * time.Millisecond)

	resp, err := windowProvider("gpt-4o").handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expired cooldown under active window must stay surfaced, got %d items", len(resp.Items))
	}
	item := resp.Items[0]
	if item.HealthReason != disableWindowReason {
		t.Errorf("HealthReason = %q, want %q", item.HealthReason, disableWindowReason)
	}
	if item.CooldownUntil <= time.Now().Unix() {
		t.Errorf("CooldownUntil = %d, want future window deadline", item.CooldownUntil)
	}
}

// TestHandleUnitHealthList_DisabledWinsOverDisableWindow verifies that an
// llmclient disabled state (auth failure, manual recovery) is not masked by
// the time-based disable-window projection.
func TestHandleUnitHealthList_DisabledWinsOverDisableWindow(t *testing.T) {
	resetHealth()
	defer resetHealth()

	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{
		StatusCode: 401,
		Code:       "invalid_api_key",
	})

	resp, err := windowProvider("gpt-4o").handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	item := resp.Items[0]
	if item.HealthState != llmclient.HealthStateDisabled {
		t.Errorf("HealthState = %q, want disabled", item.HealthState)
	}
	if item.HealthReason != llmclient.HealthReasonAuthenticationFailed {
		t.Errorf("HealthReason = %q, want %q", item.HealthReason, llmclient.HealthReasonAuthenticationFailed)
	}
	if item.RecoveryMode != recoveryManualOrBalance {
		t.Errorf("RecoveryMode = %q, want %q", item.RecoveryMode, recoveryManualOrBalance)
	}
}

// TestHandleUnitHealthList_InactiveWindowNotSynthesized verifies that a
// provider whose windows do not cover the current time contributes no entries.
func TestHandleUnitHealthList_InactiveWindowNotSynthesized(t *testing.T) {
	resetHealth()
	defer resetHealth()

	now := time.Now()
	a := &Actor{Providers: []domain.Provider{{
		Name:   "openai",
		Kind:   "openai",
		Models: modelList([]string{"gpt-4o"}),
		DisableWindows: []domain.ProviderDisableWindow{{
			Start: now.Add(time.Hour).Format("15:04"),
			End:   now.Add(2 * time.Hour).Format("15:04"),
		}},
	}}}

	resp, err := a.handleUnitHealthList(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("inactive window must not surface entries, got %d", len(resp.Items))
	}
}
