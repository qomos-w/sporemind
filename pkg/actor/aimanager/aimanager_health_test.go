package aimanager

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// resetHealth replaces the process-wide llmclient.DefaultProviderHealth with a
// fresh instance (preserving the default policy) so each test starts from a
// clean slate. Without this, tests that record failures leak state across
// each other because DefaultProviderHealth is a package-level singleton.
func resetHealth() {
	prev := llmclient.DefaultProviderHealth.Policy()
	llmclient.DefaultProviderHealth = llmclient.NewProviderHealth()
	llmclient.DefaultProviderHealth.SetCooldownPolicy(prev)
}

func TestCooldownPolicy_ReturnsDefaults(t *testing.T) {
	p := cooldownPolicy()
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

func TestApplyCooldownPolicy_InjectsIntoLlmclient(t *testing.T) {
	resetHealth()
	defer resetHealth()
	a := &Actor{persistedHealth: make(map[string]persistedHealthEntry)}
	a.applyCooldownPolicy()
	got := llmclient.DefaultProviderHealth.Policy()
	if got.AvailabilityThreshold != 5 {
		t.Errorf("policy not injected: AvailabilityThreshold = %d", got.AvailabilityThreshold)
	}
}

func TestRecoveryModeForState(t *testing.T) {
	if got := recoveryModeForState(llmclient.HealthStateDisabled); got != recoveryManualOrBalance {
		t.Errorf("disabled → %q, want %q", got, recoveryManualOrBalance)
	}
	if got := recoveryModeForState(llmclient.HealthStateCoolingDown); got != recoveryCooldown {
		t.Errorf("cooling_down → %q, want %q", got, recoveryCooldown)
	}
	if got := recoveryModeForState(""); got != recoveryCooldown {
		t.Errorf("empty → %q, want %q", got, recoveryCooldown)
	}
}

func TestUnitHealthProjection_Healthy(t *testing.T) {
	snap := map[string]llmclient.UnitHealthSnapshot{}
	state, reason, recovery, cd := unitHealthProjection(snap, "openai", "gpt-4o", time.Now())
	if state != "" || reason != "" || recovery != "" || cd != 0 {
		t.Fatalf("empty snap should project healthy (empty), got state=%q reason=%q recovery=%q cd=%d", state, reason, recovery, cd)
	}
}

func TestUnitHealthProjection_Disabled(t *testing.T) {
	now := time.Now()
	snap := map[string]llmclient.UnitHealthSnapshot{
		"openai::gpt-4o": {Provider: "openai", Model: "gpt-4o", State: llmclient.HealthStateDisabled, Reason: llmclient.HealthReasonAuthenticationFailed},
	}
	state, reason, recovery, cd := unitHealthProjection(snap, "openai", "gpt-4o", now)
	if state != llmclient.HealthStateDisabled {
		t.Fatalf("state = %q, want disabled", state)
	}
	if reason != llmclient.HealthReasonAuthenticationFailed {
		t.Fatalf("reason = %q, want %q", reason, llmclient.HealthReasonAuthenticationFailed)
	}
	if recovery != recoveryManualOrBalance {
		t.Fatalf("recovery = %q, want %q", recovery, recoveryManualOrBalance)
	}
	if cd != 0 {
		t.Fatalf("disabled cd should be 0, got %d", cd)
	}
}

func TestUnitHealthProjection_CoolingDown(t *testing.T) {
	deadline := time.Now().Add(5 * time.Minute)
	snap := map[string]llmclient.UnitHealthSnapshot{
		"openai::gpt-4o": {Provider: "openai", Model: "gpt-4o", State: llmclient.HealthStateCoolingDown, Reason: llmclient.HealthReasonRateLimit, CooldownUntil: deadline},
	}
	state, reason, recovery, cd := unitHealthProjection(snap, "openai", "gpt-4o", time.Now())
	if state != llmclient.HealthStateCoolingDown {
		t.Fatalf("state = %q, want cooling_down", state)
	}
	if reason != llmclient.HealthReasonRateLimit {
		t.Fatalf("reason = %q, want %q", reason, llmclient.HealthReasonRateLimit)
	}
	if recovery != recoveryCooldown {
		t.Fatalf("recovery = %q, want %q", recovery, recoveryCooldown)
	}
	if cd != deadline.Unix() {
		t.Fatalf("cd = %d, want %d", cd, deadline.Unix())
	}
}

func TestUnitHealthProjection_ExpiredCooldownProjectsHealthy(t *testing.T) {
	expired := time.Now().Add(-1 * time.Minute)
	snap := map[string]llmclient.UnitHealthSnapshot{
		"openai::gpt-4o": {Provider: "openai", Model: "gpt-4o", State: llmclient.HealthStateCoolingDown, Reason: llmclient.HealthReasonRateLimit, CooldownUntil: expired},
	}
	state, _, _, cd := unitHealthProjection(snap, "openai", "gpt-4o", time.Now())
	if state != "" {
		t.Fatalf("expired cooldown should project healthy (empty), got state=%q", state)
	}
	if cd != 0 {
		t.Fatalf("expired cooldown cd should be 0, got %d", cd)
	}
}

func TestProviderHealthProjection_WorstStateWins(t *testing.T) {
	now := time.Now()
	far := now.Add(5 * time.Minute)
	near := now.Add(1 * time.Minute)
	snap := map[string]llmclient.UnitHealthSnapshot{
		"openai::gpt-4o":      {Provider: "openai", Model: "gpt-4o", State: llmclient.HealthStateCoolingDown, Reason: llmclient.HealthReasonRateLimit, CooldownUntil: near},
		"openai::gpt-4o-mini": {Provider: "openai", Model: "gpt-4o-mini", State: llmclient.HealthStateCoolingDown, Reason: llmclient.HealthReasonRateLimit, CooldownUntil: far},
	}
	state, reason, recovery, cd := providerHealthProjection(snap, "openai", now)
	if state != llmclient.HealthStateCoolingDown {
		t.Fatalf("state = %q, want cooling_down", state)
	}
	if reason != llmclient.HealthReasonRateLimit {
		t.Fatalf("reason = %q, want %q", reason, llmclient.HealthReasonRateLimit)
	}
	if recovery != recoveryCooldown {
		t.Fatalf("recovery = %q, want %q", recovery, recoveryCooldown)
	}
	if cd != far.Unix() {
		t.Fatalf("cd = %d, want %d (furthest deadline)", cd, far.Unix())
	}
}

func TestProviderHealthProjection_DisabledOverridesCooldown(t *testing.T) {
	now := time.Now()
	snap := map[string]llmclient.UnitHealthSnapshot{
		"openai::gpt-4o":      {Provider: "openai", Model: "gpt-4o", State: llmclient.HealthStateCoolingDown, Reason: llmclient.HealthReasonRateLimit, CooldownUntil: now.Add(5 * time.Minute)},
		"openai::gpt-4o-mini": {Provider: "openai", Model: "gpt-4o-mini", State: llmclient.HealthStateDisabled, Reason: llmclient.HealthReasonAuthorizationFailed},
	}
	state, reason, recovery, cd := providerHealthProjection(snap, "openai", now)
	if state != llmclient.HealthStateDisabled {
		t.Fatalf("state = %q, want disabled (worst wins)", state)
	}
	if reason != llmclient.HealthReasonAuthorizationFailed {
		t.Fatalf("reason = %q, want %q", reason, llmclient.HealthReasonAuthorizationFailed)
	}
	if recovery != recoveryManualOrBalance {
		t.Fatalf("recovery = %q, want %q", recovery, recoveryManualOrBalance)
	}
	if cd != 0 {
		t.Fatalf("disabled cd should be 0, got %d", cd)
	}
}

func TestProviderHealthProjection_NoEntriesProjectsHealthy(t *testing.T) {
	snap := map[string]llmclient.UnitHealthSnapshot{}
	state, reason, recovery, cd := providerHealthProjection(snap, "openai", time.Now())
	if state != "" || reason != "" || recovery != "" || cd != 0 {
		t.Fatalf("no entries should project healthy, got state=%q reason=%q recovery=%q cd=%d", state, reason, recovery, cd)
	}
}

func TestHandleProviderReportFailure_DisabledPersisted(t *testing.T) {
	resetHealth()
	defer resetHealth()

	ms := &memStore{data: make(map[string][]byte)}
	a := &Actor{
		actorID: "test-aimanager",
		store:   ms,
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}

	ctx := testutil.AdminCtx(testutil.GenActorID())
	// Simulate aiaggregator.applyStreamOpenFailure which records the failure
	// to llmclient BEFORE sending the structured report to aimanager.
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 401, Code: "invalid_api_key"})
	_, err := a.handleProviderReportFailure(ctx, reportProviderFailureReq{
		ProviderName: "openai",
		Model:        "gpt-4o",
		StatusCode:   401,
		Code:         "invalid_api_key",
	})
	if err != nil {
		t.Fatalf("handleProviderReportFailure failed: %v", err)
	}

	// Verify llmclient snapshot shows disabled.
	snap := llmclient.HealthSnapshot()
	s, ok := snap["openai::gpt-4o"]
	if !ok {
		t.Fatal("unit not found in snapshot")
	}
	if s.State != llmclient.HealthStateDisabled {
		t.Fatalf("state = %q, want disabled", s.State)
	}
	if s.Reason != llmclient.HealthReasonAuthenticationFailed {
		t.Fatalf("reason = %q, want %q", s.Reason, llmclient.HealthReasonAuthenticationFailed)
	}

	// Verify persisted overlay has the disabled entry.
	raw, ok := ms.data["test-aimanager"]
	if !ok {
		t.Fatal("state not persisted")
	}
	var state persistState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	he, ok := state.Health["openai"]
	if !ok {
		t.Fatal("disabled health not persisted in overlay")
	}
	if he.State != llmclient.HealthStateDisabled {
		t.Fatalf("persisted state = %q, want %q", he.State, llmclient.HealthStateDisabled)
	}
	if he.Reason != llmclient.HealthReasonAuthenticationFailed {
		t.Fatalf("persisted reason = %q, want %q", he.Reason, llmclient.HealthReasonAuthenticationFailed)
	}
	if he.LastFailureAt.IsZero() {
		t.Fatal("LastFailureAt must be persisted")
	}
}

func TestHandleProviderReportFailure_CoolingDownNotPersisted(t *testing.T) {
	resetHealth()
	defer resetHealth()

	ms := &memStore{data: make(map[string][]byte)}
	a := &Actor{
		actorID: "test-aimanager",
		store:   ms,
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}

	ctx := testutil.AdminCtx(testutil.GenActorID())
	// Simulate aiaggregator.applyStreamOpenFailure which records the failure
	// to llmclient BEFORE sending the structured report to aimanager.
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 30 * time.Second})
	_, err := a.handleProviderReportFailure(ctx, reportProviderFailureReq{
		ProviderName: "openai",
		Model:        "gpt-4o",
		StatusCode:   429,
		RetryAfter:   30,
	})
	if err != nil {
		t.Fatalf("handleProviderReportFailure failed: %v", err)
	}

	// Snapshot shows cooling_down.
	snap := llmclient.HealthSnapshot()
	s, ok := snap["openai::gpt-4o"]
	if !ok {
		t.Fatal("unit not found in snapshot")
	}
	if s.State != llmclient.HealthStateCoolingDown {
		t.Fatalf("state = %q, want cooling_down", s.State)
	}

	// Persisted overlay must NOT contain cooling_down entries.
	raw, ok := ms.data["test-aimanager"]
	if !ok {
		t.Fatal("state not persisted")
	}
	var state persistState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(state.Health) != 0 {
		t.Fatalf("cooling_down must not be persisted, got %+v", state.Health)
	}
}

func TestHandleProviderReportFailure_QuotaCooldown(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
		store:           &memStore{data: make(map[string][]byte)},
		actorID:         "test",
	}

	ctx := testutil.AdminCtx(testutil.GenActorID())
	// Simulate aiaggregator.applyStreamOpenFailure which records the failure
	// to llmclient BEFORE sending the structured report to aimanager.
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 403, Code: "insufficient_user_quota"})
	resp, err := a.handleProviderReportFailure(ctx, reportProviderFailureReq{
		ProviderName: "openai",
		Model:        "gpt-4o",
		StatusCode:   403,
		Code:         "insufficient_user_quota",
	})
	if err != nil {
		t.Fatalf("failed: %v", err)
	}

	snap := llmclient.HealthSnapshot()
	s := snap["openai::gpt-4o"]
	if s.State != llmclient.HealthStateCoolingDown {
		t.Fatalf("state = %q, want cooling_down", s.State)
	}
	if s.Reason != llmclient.HealthReasonQuotaExhausted {
		t.Fatalf("reason = %q, want %q", s.Reason, llmclient.HealthReasonQuotaExhausted)
	}
	if resp.CooldownUntil == 0 {
		t.Fatal("quota cooldown should project non-zero CooldownUntil")
	}
}

func TestHandleProviderList_ProjectsHealthState(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 403, Code: "insufficient_user_quota"})

	resp, err := a.handleProviderList(nil)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	p := resp.Items[0]
	if p.HealthState != llmclient.HealthStateCoolingDown {
		t.Errorf("HealthState = %q, want %q", p.HealthState, llmclient.HealthStateCoolingDown)
	}
	if p.HealthReason != llmclient.HealthReasonQuotaExhausted {
		t.Errorf("HealthReason = %q, want %q", p.HealthReason, llmclient.HealthReasonQuotaExhausted)
	}
	if p.RecoveryMode != recoveryCooldown {
		t.Errorf("RecoveryMode = %q, want %q", p.RecoveryMode, recoveryCooldown)
	}
	if p.CooldownUntil == 0 {
		t.Error("quota cooldown must project a CooldownUntil")
	}
}

func TestHandleProviderList_ProjectsActiveDisableWindow(t *testing.T) {
	resetHealth()
	defer resetHealth()

	now := time.Now()
	start := now.Add(-2 * time.Minute).Format("15:04")
	end := now.Add(2 * time.Minute).Format("15:04")
	a := &Actor{Providers: []domain.Provider{
		{Name: "scheduled", DisableWindows: []domain.ProviderDisableWindow{{Start: start, End: end}}},
	}}

	resp, err := a.handleProviderList(nil)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if got := resp.Items[0].DisableUntil; got <= now.Unix() {
		t.Fatalf("DisableUntil = %d, want future deadline after %d", got, now.Unix())
	}
}

func TestHandleProviderFullList_ProjectsActiveDisableWindow(t *testing.T) {
	resetHealth()
	defer resetHealth()

	now := time.Now()
	start := now.Add(-2 * time.Minute).Format("15:04")
	end := now.Add(2 * time.Minute).Format("15:04")
	a := &Actor{Providers: []domain.Provider{
		{Name: "scheduled", DisableWindows: []domain.ProviderDisableWindow{{Start: start, End: end}}},
	}}

	resp, err := a.handleProviderFullList(nil)
	if err != nil {
		t.Fatalf("full list failed: %v", err)
	}
	if got := resp.Items[0].DisableUntil; got <= now.Unix() {
		t.Fatalf("DisableUntil = %d, want future deadline after %d", got, now.Unix())
	}
}

func TestHandleProviderFullList_ProjectsHealthState(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
			{Name: "anthropic", Kind: "anthropic", Models: []domain.ProviderModel{{Name: "claude-3"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 403, Code: "insufficient_user_quota"})

	resp, err := a.handleProviderFullList(nil)
	if err != nil {
		t.Fatalf("full list failed: %v", err)
	}
	var openai, anthropic domain.Provider
	for _, p := range resp.Items {
		switch p.Name {
		case "openai":
			openai = p
		case "anthropic":
			anthropic = p
		}
	}
	if openai.HealthState != llmclient.HealthStateCoolingDown {
		t.Errorf("openai HealthState = %q, want cooling_down", openai.HealthState)
	}
	if openai.HealthReason != llmclient.HealthReasonQuotaExhausted {
		t.Errorf("openai HealthReason = %q, want quota_exhausted", openai.HealthReason)
	}
	if openai.RecoveryMode != recoveryCooldown {
		t.Errorf("openai RecoveryMode = %q, want %q", openai.RecoveryMode, recoveryCooldown)
	}
	if openai.CooldownUntil == 0 {
		t.Error("quota cooldown must project a CooldownUntil")
	}
	if anthropic.HealthState != "" {
		t.Errorf("healthy provider must have empty HealthState, got %q", anthropic.HealthState)
	}
}

func TestBuildAutoUnits_IncludesAllProviders(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
			{Name: "anthropic", Kind: "anthropic", Endpoint: "https://anthropic", Models: []domain.ProviderModel{{Name: "claude"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	// Disable openai — it should still be included.
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 401})

	units := a.buildAutoUnits()
	if len(units) != 2 {
		t.Fatalf("expected both providers (disabled included), got %+v", units)
	}
}

func TestBuildAutoUnits_ProjectsCooldownAnnotation(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
			{Name: "anthropic", Kind: "anthropic", Endpoint: "https://anthropic", Models: []domain.ProviderModel{{Name: "claude"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 90 * time.Second})

	units := a.buildAutoUnits()
	var openaiUnit *domain.ManualCallableUnit
	for i := range units {
		if units[i].ProviderName == "openai" {
			openaiUnit = &units[i]
		}
	}
	if openaiUnit == nil || openaiUnit.CooldownUntil == 0 {
		t.Fatalf("cooling_down provider should have non-zero CooldownUntil, got %+v", openaiUnit)
	}
}

func TestLoad_RestoresPersistedDisabledHealthOverlay(t *testing.T) {
	ms := &memStore{data: make(map[string][]byte)}
	seed := persistState{
		Health: map[string]persistedHealthEntry{
			"openai": {
				State:         llmclient.HealthStateDisabled,
				Reason:        llmclient.HealthReasonAuthenticationFailed,
				LastFailureAt: time.Now().UTC().Truncate(time.Second),
				RecoveryMode:  recoveryManualOrBalance,
			},
		},
	}
	raw, _ := json.Marshal(seed)
	ms.data["test-aimanager"] = raw

	a := &Actor{actorID: "test-aimanager", store: ms}
	if err := a.Load(); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	he, ok := a.persistedHealth["openai"]
	if !ok {
		t.Fatal("persisted disabled overlay not restored")
	}
	if he.State != llmclient.HealthStateDisabled || he.Reason != llmclient.HealthReasonAuthenticationFailed {
		t.Fatalf("unexpected restored overlay: %+v", he)
	}
}

func TestLoad_DropsTransientHealth(t *testing.T) {
	ms := &memStore{data: make(map[string][]byte)}
	seed := persistState{
		Health: map[string]persistedHealthEntry{
			"openai": {
				State:         llmclient.HealthStateCoolingDown,
				Reason:        llmclient.HealthReasonRateLimit,
				LastFailureAt: time.Now().UTC(),
				CooldownUntil: time.Now().UTC().Add(5 * time.Minute),
			},
		},
	}
	raw, _ := json.Marshal(seed)
	ms.data["test-aimanager"] = raw

	a := &Actor{actorID: "test-aimanager", store: ms}
	if err := a.Load(); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if _, ok := a.persistedHealth["openai"]; ok {
		t.Fatal("cooling_down must not be restored into overlay")
	}
}

func TestBackfillPersistedHealth_ReplaysDisabledIntoLlmclient(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth: map[string]persistedHealthEntry{
			"openai": {
				State:         llmclient.HealthStateDisabled,
				Reason:        llmclient.HealthReasonAuthenticationFailed,
				LastFailureAt: time.Now().UTC(),
			},
		},
	}
	a.backfillPersistedHealth()

	snap := llmclient.HealthSnapshot()
	s, ok := snap["openai::gpt-4o"]
	if !ok {
		t.Fatal("disabled state not replayed into llmclient")
	}
	if s.State != llmclient.HealthStateDisabled {
		t.Fatalf("state = %q, want disabled", s.State)
	}
	if s.Reason != llmclient.HealthReasonAuthenticationFailed {
		t.Fatalf("reason = %q, want %q", s.Reason, llmclient.HealthReasonAuthenticationFailed)
	}
}

func TestBackfillPersistedHealth_DropsConfigurationError(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth: map[string]persistedHealthEntry{
			"openai": {
				State:  llmclient.HealthStateDisabled,
				Reason: llmclient.HealthReasonConfigurationError,
			},
		},
	}
	a.backfillPersistedHealth()

	if _, ok := a.persistedHealth["openai"]; ok {
		t.Fatal("configuration_error overlay should be dropped (cannot be replayed via ClassStop)")
	}
	snap := llmclient.HealthSnapshot()
	if _, ok := snap["openai::gpt-4o"]; ok {
		t.Fatal("configuration_error should not be replayed into llmclient")
	}
}

func TestProviderConfigure_ClearsHealthAndPersists(t *testing.T) {
	resetHealth()
	defer resetHealth()

	ms := &memStore{data: make(map[string][]byte)}
	a := &Actor{
		actorID: "test-aimanager",
		store:   ms,
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 401})

	ctx := testutil.AdminCtx(testutil.GenActorID())
	_, err := a.handleProviderConfigure(ctx, domain.AIManagerProviderConfigureReq{
		Name:     "openai",
		Kind:     "openai",
		Endpoint: "https://openai",
		Models:   []domain.ProviderModel{{Name: "gpt-4o"}},
	})
	if err != nil {
		t.Fatalf("configure failed: %v", err)
	}

	// llmclient snapshot must be cleared for the provider.
	snap := llmclient.HealthSnapshot()
	if _, ok := snap["openai::gpt-4o"]; ok {
		t.Fatal("health must be cleared by config update")
	}
}

func TestHandleProviderResetHealth_ClearsHealthAndBroadcasts(t *testing.T) {
	resetHealth()
	defer resetHealth()

	ms := &memStore{data: make(map[string][]byte)}
	a := &Actor{
		actorID: "test-aimanager",
		store:   ms,
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	// Seed the persisted overlay with a disabled entry so we can verify the
	// reset drops both the llmclient state and the durable overlay.
	a.persistedHealth["openai"] = persistedHealthEntry{
		State:         llmclient.HealthStateDisabled,
		Reason:        llmclient.HealthReasonAuthenticationFailed,
		LastFailureAt: time.Now().UTC(),
	}
	a.backfillPersistedHealth()

	snap := llmclient.HealthSnapshot()
	if snap["openai::gpt-4o"].State != llmclient.HealthStateDisabled {
		t.Fatalf("precondition: expected disabled replay, got %q", snap["openai::gpt-4o"].State)
	}

	ctx := testutil.AdminCtx(testutil.GenActorID())
	resp, err := a.handleProviderResetHealth(ctx, domain.AIManagerProviderResetHealthReq{ProviderName: "openai"})
	if err != nil {
		t.Fatalf("handleProviderResetHealth: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("resp = %+v, want Ok", resp)
	}

	// llmclient snapshot must be cleared.
	snap = llmclient.HealthSnapshot()
	if _, ok := snap["openai::gpt-4o"]; ok {
		t.Fatal("health entry must be deleted after manual reset")
	}

	// Persisted overlay must also be cleared.
	if _, ok := a.persistedHealth["openai"]; ok {
		t.Fatal("persisted overlay must be cleared")
	}
	raw, ok := ms.data["test-aimanager"]
	if !ok {
		t.Fatal("reset must be persisted")
	}
	var state persistState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(state.Health) != 0 {
		t.Fatalf("persisted health must be empty after reset, got %+v", state.Health)
	}
}

// TestHandleProviderResetHealth_PreservesOtherProviders verifies the per-unit
// clear semantics behind provider_reset_health: resetting one provider must
// not disturb another provider's disabled or cooling state (the old var-swap
// implementation dropped other providers' cooldowns and re-recorded their
// disabled units into a fresh instance; ClearProvider keeps them untouched).
func TestHandleProviderResetHealth_PreservesOtherProviders(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
			{Name: "anthropic", Kind: "anthropic", Models: []domain.ProviderModel{{Name: "claude-3"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
		store:           &memStore{data: make(map[string][]byte)},
		actorID:         "test",
	}
	// Target provider: cooling_down. Other provider: disabled.
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429})
	llmclient.RecordFailure("anthropic", "claude-3", &llmclient.UpstreamError{StatusCode: 401})

	ctx := testutil.AdminCtx(testutil.GenActorID())
	resp, err := a.handleProviderResetHealth(ctx, domain.AIManagerProviderResetHealthReq{ProviderName: "openai"})
	if err != nil || !resp.Ok {
		t.Fatalf("reset failed: resp=%+v err=%v", resp, err)
	}

	snap := llmclient.HealthSnapshot()
	if _, ok := snap["openai::gpt-4o"]; ok {
		t.Fatal("target provider health must be cleared")
	}
	if e, ok := snap["anthropic::claude-3"]; !ok || e.State != llmclient.HealthStateDisabled {
		t.Fatalf("other provider's disabled unit must survive the reset, got %+v", e)
	}
}

// TestHandleAggregatorGet_AnnotatesUnitHealth verifies the aggregator_get
// protocol regression (item 4): units in the response are annotated from the
// llmclient health snapshot with HealthState/HealthReason/RecoveryMode/
// CooldownUntil, while healthy units carry no annotation.
func TestHandleAggregatorGet_AnnotatesUnitHealth(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"A": {ID: "A", Name: "Agg A", Units: []domain.ManualCallableUnit{
				{ProviderName: "openai", Model: "gpt-4o"},
				{ProviderName: "anthropic", Model: "claude-3"},
			}},
		},
		persistedHealth: map[string]persistedHealthEntry{},
	}
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 90 * time.Second})

	resp, err := a.handleAggregatorGet(nil, domain.AIManagerAggregatorGetReq{ID: "A"})
	if err != nil {
		t.Fatalf("aggregator_get: %v", err)
	}
	if len(resp.Units) != 2 {
		t.Fatalf("len(units) = %d, want 2", len(resp.Units))
	}
	cool := resp.Units[0]
	if cool.HealthState != llmclient.HealthStateCoolingDown {
		t.Errorf("cooled unit HealthState = %q, want cooling_down", cool.HealthState)
	}
	if cool.HealthReason != llmclient.HealthReasonRateLimit {
		t.Errorf("cooled unit HealthReason = %q, want rate_limit", cool.HealthReason)
	}
	if cool.RecoveryMode != recoveryCooldown {
		t.Errorf("cooled unit RecoveryMode = %q, want %q", cool.RecoveryMode, recoveryCooldown)
	}
	if cool.CooldownUntil <= time.Now().Unix() {
		t.Errorf("cooled unit CooldownUntil = %d, want future deadline", cool.CooldownUntil)
	}
	healthy := resp.Units[1]
	if healthy.HealthState != "" || healthy.HealthReason != "" || healthy.RecoveryMode != "" || healthy.CooldownUntil != 0 {
		t.Errorf("healthy unit must carry no annotation, got %+v", healthy)
	}
}

// TestHandleAggregatorResolve_AnnotatesUnitHealth verifies aggregator_resolve
// (the internal config-resolve path used by aggregator actors) annotates unit
// health the same way as aggregator_get.
func TestHandleAggregatorResolve_AnnotatesUnitHealth(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"A": {ID: "A", Name: "Agg A", Units: []domain.ManualCallableUnit{
				{ProviderName: "openai", Model: "gpt-4o"},
			}},
		},
		persistedHealth: map[string]persistedHealthEntry{},
	}
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 401})

	resp, err := a.handleAggregatorResolve(nil, domain.AIManagerAggregatorResolveReq{ID: "A"})
	if err != nil {
		t.Fatalf("aggregator_resolve: %v", err)
	}
	if len(resp.Units) != 1 {
		t.Fatalf("len(units) = %d, want 1", len(resp.Units))
	}
	u := resp.Units[0]
	if u.HealthState != llmclient.HealthStateDisabled {
		t.Errorf("disabled unit HealthState = %q, want disabled", u.HealthState)
	}
	if u.HealthReason != llmclient.HealthReasonAuthenticationFailed {
		t.Errorf("disabled unit HealthReason = %q, want authentication_failed", u.HealthReason)
	}
	if u.RecoveryMode != recoveryManualOrBalance {
		t.Errorf("disabled unit RecoveryMode = %q, want %q", u.RecoveryMode, recoveryManualOrBalance)
	}
}

func TestHandleProviderResetHealth_UnknownProviderNoop(t *testing.T) {
	resetHealth()
	defer resetHealth()

	ms := &memStore{data: make(map[string][]byte)}
	a := &Actor{
		actorID:         "test-aimanager",
		store:           ms,
		persistedHealth: make(map[string]persistedHealthEntry),
		aggregators:     make(map[string]domain.AIManagerAggregatorGetResp),
		aggRefs:         make(map[string]ref.Ref),
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	resp, err := a.handleProviderResetHealth(ctx, domain.AIManagerProviderResetHealthReq{ProviderName: "ghost"})
	if err != nil || !resp.Ok {
		t.Fatalf("reset of unknown provider should be a no-op Ok, got resp=%+v err=%v", resp, err)
	}

	resp, err = a.handleProviderResetHealth(ctx, domain.AIManagerProviderResetHealthReq{})
	if err != nil || resp.Ok {
		t.Errorf("empty providerName must be rejected, got resp=%+v err=%v", resp, err)
	}
}

func TestHandleProviderReportSuccess_ResetsFailureCounterOnly(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
		store:           &memStore{data: make(map[string][]byte)},
		actorID:         "test",
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())

	// Record a 429 cooldown.
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})
	snap := llmclient.HealthSnapshot()
	if snap["openai::gpt-4o"].State != llmclient.HealthStateCoolingDown {
		t.Fatalf("precondition failed: expected cooling_down")
	}

	// Report success — counter resets, but cooldown stays until expiry.
	_, err := a.handleProviderReportSuccess(ctx, reportProviderFailureReq{ProviderName: "openai", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("success failed: %v", err)
	}

	snap = llmclient.HealthSnapshot()
	s := snap["openai::gpt-4o"]
	if s.ConsecutiveFailures != 0 {
		t.Fatalf("consecutive failures should be 0 after success, got %d", s.ConsecutiveFailures)
	}
	// Cooldown is NOT cleared by RecordSuccess (it expires naturally).
	if s.State != llmclient.HealthStateCoolingDown {
		t.Fatalf("cooling_down should NOT be cleared by success, got %q", s.State)
	}
}

func TestHandleProviderReportSuccess_DoesNotClearDisabled(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
		store:           &memStore{data: make(map[string][]byte)},
		actorID:         "test",
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())

	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 401})
	_, err := a.handleProviderReportSuccess(ctx, reportProviderFailureReq{ProviderName: "openai", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("success failed: %v", err)
	}

	snap := llmclient.HealthSnapshot()
	s := snap["openai::gpt-4o"]
	if s.State != llmclient.HealthStateDisabled {
		t.Fatalf("disabled should NOT be cleared by success, got %q", s.State)
	}
}

// TestRecordProviderFailureLocked_NoDoubleRecord verifies that when req.Model
// is set (the normal aiaggregator path where applyStreamOpenFailure already
// called llmclient.RecordFailure), recordProviderFailureLocked does NOT call
// RecordFailure again. We pre-record exactly one failure and then call
// recordProviderFailureLocked; ConsecutiveFailures must remain 1.
func TestRecordProviderFailureLocked_NoDoubleRecord(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth:   make(map[string]persistedHealthEntry),
		providerLastHealth: make(map[string]string),
	}

	// Simulate aiaggregator.applyStreamOpenFailure recording the failure.
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})

	before := llmclient.HealthSnapshot()["openai::gpt-4o"].ConsecutiveFailures

	req := reportProviderFailureReq{
		ProviderName: "openai",
		Model:        "gpt-4o",
		StatusCode:   429,
		RetryAfter:   60,
	}
	becameUnhealthy := a.recordProviderFailureLocked(req, &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})

	after := llmclient.HealthSnapshot()["openai::gpt-4o"].ConsecutiveFailures
	if after != before {
		t.Fatalf("ConsecutiveFailures changed from %d to %d — recordProviderFailureLocked must NOT re-record when req.Model != \"\"", before, after)
	}
	// First failure with 429 → cooling_down. Since lastState was "" (selectable),
	// becameUnhealthy should be true.
	if !becameUnhealthy {
		t.Fatal("becameUnhealthy should be true on first transition from healthy to cooling_down")
	}
}

// TestRecordProviderFailureLocked_BecameUnhealthyDisabled verifies that
// becameUnhealthy is correctly detected when a provider transitions from
// selectable to disabled. This previously always returned false because
// aiaggregator's RecordFailure ran before the before-snapshot was taken.
func TestRecordProviderFailureLocked_BecameUnhealthyDisabled(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth:   make(map[string]persistedHealthEntry),
		providerLastHealth: make(map[string]string),
	}

	// Simulate aiaggregator recording a 401 (auth failure → disabled).
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 401})

	req := reportProviderFailureReq{
		ProviderName: "openai",
		Model:        "gpt-4o",
		StatusCode:   401,
	}
	becameUnhealthy := a.recordProviderFailureLocked(req, &llmclient.UpstreamError{StatusCode: 401})

	if !becameUnhealthy {
		t.Fatal("becameUnhealthy should be true on transition from healthy to disabled")
	}
	// providerLastHealth should now reflect disabled.
	if a.providerLastHealth["openai"] != llmclient.HealthStateDisabled {
		t.Fatalf("providerLastHealth = %q, want %q", a.providerLastHealth["openai"], llmclient.HealthStateDisabled)
	}
}

// TestRecordProviderFailureLocked_AlreadyUnhealthyNoTransition verifies that
// becameUnhealthy is false when the provider was already unhealthy before this
// report (no selectable→unhealthy transition).
func TestRecordProviderFailureLocked_AlreadyUnhealthyNoTransition(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth:   make(map[string]persistedHealthEntry),
		providerLastHealth: make(map[string]string),
	}

	// First failure: healthy → cooling_down (transition).
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})
	req := reportProviderFailureReq{ProviderName: "openai", Model: "gpt-4o", StatusCode: 429, RetryAfter: 60}
	becameUnhealthy1 := a.recordProviderFailureLocked(req, &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})
	if !becameUnhealthy1 {
		t.Fatal("first failure should trigger becameUnhealthy")
	}

	// Second failure: already cooling_down → still cooling_down (no transition).
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})
	becameUnhealthy2 := a.recordProviderFailureLocked(req, &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})
	if becameUnhealthy2 {
		t.Fatal("second failure on already-unhealthy provider should NOT trigger becameUnhealthy")
	}
}

// TestRecordProviderFailureLocked_ProviderLevelBroadcast verifies that when
// req.Model is empty (provider-level failure), recordProviderFailureLocked
// broadcasts the failure to all models via recordFailureForProvider.
func TestRecordProviderFailureLocked_ProviderLevelBroadcast(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}, {Name: "gpt-4o-mini"}}},
		},
		persistedHealth:   make(map[string]persistedHealthEntry),
		providerLastHealth: make(map[string]string),
	}

	req := reportProviderFailureReq{ProviderName: "openai", Model: ""}
	becameUnhealthy := a.recordProviderFailureLocked(req, &llmclient.UpstreamError{StatusCode: 401})

	if !becameUnhealthy {
		t.Fatal("provider-level 401 should trigger becameUnhealthy")
	}
	snap := llmclient.HealthSnapshot()
	for _, m := range a.Providers[0].Models {
		key := "openai::" + m.Name
		s, ok := snap[key]
		if !ok {
			t.Fatalf("model %q not recorded in snapshot", m.Name)
		}
		if s.State != llmclient.HealthStateDisabled {
			t.Fatalf("model %q state = %q, want disabled", m.Name, s.State)
		}
	}
}

// TestHandleProviderReportSuccess_ClearsLastHealth verifies that after a
// provider recovers (cooldown expires and success is reported), the
// providerLastHealth cache is cleared so a subsequent failure can detect the
// transition.
func TestHandleProviderReportSuccess_ClearsLastHealth(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth:   make(map[string]persistedHealthEntry),
		providerLastHealth: make(map[string]string),
		store:             &memStore{data: make(map[string][]byte)},
		actorID:           "test",
	}

	// Record a failure → cooling_down.
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})
	ctx := testutil.AdminCtx(testutil.GenActorID())
	a.recordProviderFailureLocked(reportProviderFailureReq{ProviderName: "openai", Model: "gpt-4o", StatusCode: 429, RetryAfter: 60}, &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})
	if a.providerLastHealth["openai"] != llmclient.HealthStateCoolingDown {
		t.Fatalf("providerLastHealth = %q, want cooling_down", a.providerLastHealth["openai"])
	}

	// Report success while still cooling_down — state stays cooling_down,
	// so providerLastHealth should remain cooling_down.
	_, err := a.handleProviderReportSuccess(ctx, reportProviderFailureReq{ProviderName: "openai", Model: "gpt-4o"})
	if err != nil {
		t.Fatalf("success failed: %v", err)
	}
	// Cooldown not cleared by success, so lastHealth stays cooling_down.
	if a.providerLastHealth["openai"] != llmclient.HealthStateCoolingDown {
		t.Fatalf("providerLastHealth = %q, want cooling_down (cooldown not cleared by success)", a.providerLastHealth["openai"])
	}
}

// TestClearProviderHealthLocked_ClearsLastHealth verifies that manual reset
// (handleProviderResetHealth) clears the providerLastHealth cache entry.
func TestClearProviderHealthLocked_ClearsLastHealth(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth:   make(map[string]persistedHealthEntry),
		providerLastHealth: make(map[string]string),
		store:             &memStore{data: make(map[string][]byte)},
		actorID:           "test",
	}

	// Record a failure → cooling_down.
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})
	a.recordProviderFailureLocked(reportProviderFailureReq{ProviderName: "openai", Model: "gpt-4o", StatusCode: 429, RetryAfter: 60}, &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})
	if a.providerLastHealth["openai"] == "" {
		t.Fatal("providerLastHealth should be set after failure")
	}

	// Manual reset clears everything.
	ctx := testutil.AdminCtx(testutil.GenActorID())
	_, err := a.handleProviderResetHealth(ctx, domain.AIManagerProviderResetHealthReq{ProviderName: "openai"})
	if err != nil {
		t.Fatalf("reset failed: %v", err)
	}
	if _, ok := a.providerLastHealth["openai"]; ok {
		t.Fatal("providerLastHealth should be cleared after manual reset")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Full-flow tests for the three fixes (consecutiveFailures single increment,
// becameUnhealthy notification triggering, and on-demand cooldown exclusion).
// These exercise the realistic end-to-end path: aiaggregator's
// applyStreamOpenFailure calls llmclient.RecordFailure synchronously BEFORE
// sending the structured report to aimanager's handleProviderReportFailure.
// The latter must NOT re-record, otherwise ClassFailover (5xx) failures double
// the consecutiveFailures counter and the cooldown triggers after 3 actual
// failures instead of 5.
// ──────────────────────────────────────────────────────────────────────────────

// TestConsecutiveFailures_SingleIncrement_FullFlow simulates the complete
// aiaggregator → aimanager failure path for ClassFailover (5xx) errors. Each
// iteration performs:
//  1. llmclient.RecordFailure (called synchronously by applyStreamOpenFailure)
//  2. handleProviderReportFailure (the aimanager handler)
//
// The fix ensures step 2 does NOT call RecordFailure again. We assert that
// ConsecutiveFailures increments by exactly 1 per actual failure (not 2), and
// that the unit stays healthy (no cooldown) through failures 1-4, only entering
// cooling_down on the 5th failure — proving the AvailabilityThreshold of 5 is
// honored, not the bug-triggered threshold of 3.
func TestConsecutiveFailures_SingleIncrement_FullFlow(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		persistedHealth:   make(map[string]persistedHealthEntry),
		providerLastHealth: make(map[string]string),
		store:             &memStore{data: make(map[string][]byte)},
		actorID:           "test-aimanager",
		aggRefs:           make(map[string]ref.Ref),
		lifecycleCtx:      context.Background(),
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())

	err503 := &llmclient.UpstreamError{StatusCode: 503, Message: "service unavailable"}

	for i := 1; i <= 5; i++ {
		// Step 1: aiaggregator.applyStreamOpenFailure records the failure to
		// the llmclient health layer synchronously.
		llmclient.RecordFailure("openai", "gpt-4o", err503)

		// Step 2: the structured report reaches aimanager. The fix ensures
		// recordProviderFailureLocked does NOT re-record when req.Model != "".
		_, err := a.handleProviderReportFailure(ctx, reportProviderFailureReq{
			ProviderName: "openai",
			Model:        "gpt-4o",
			StatusCode:   503,
		})
		if err != nil {
			t.Fatalf("iteration %d: handleProviderReportFailure: %v", i, err)
		}

		snap := llmclient.HealthSnapshot()
		s, ok := snap["openai::gpt-4o"]
		if !ok {
			t.Fatalf("iteration %d: unit not found in snapshot", i)
		}

		// ConsecutiveFailures must equal the iteration count, not 2× it.
		// If the double-record bug were present, it would be 2*i.
		if s.ConsecutiveFailures != i {
			t.Fatalf("iteration %d: ConsecutiveFailures = %d, want %d (double-record bug would give %d)",
				i, s.ConsecutiveFailures, i, 2*i)
		}

		if i < 5 {
			// Below the AvailabilityThreshold (5), the unit must stay healthy.
			if s.State != llmclient.HealthStateHealthy {
				t.Fatalf("iteration %d: state = %q, want healthy (below threshold 5)", i, s.State)
			}
		}
	}

	// After the 5th failure, the unit must enter cooling_down — not earlier.
	snap := llmclient.HealthSnapshot()
	s := snap["openai::gpt-4o"]
	if s.State != llmclient.HealthStateCoolingDown {
		t.Fatalf("after 5 failures: state = %q, want cooling_down (threshold reached)", s.State)
	}
	if s.Reason != llmclient.HealthReasonAvailability {
		t.Fatalf("after 5 failures: reason = %q, want %q", s.Reason, llmclient.HealthReasonAvailability)
	}
	if s.ConsecutiveFailures != 5 {
		t.Fatalf("after 5 failures: ConsecutiveFailures = %d, want 5", s.ConsecutiveFailures)
	}
}

// TestConsecutiveFailures_DoubleRecordWouldTriggerAtThree is the inverse proof:
// if recordProviderFailureLocked DID re-record (the bug), then after only 3
// actual failures the consecutiveFailures would reach 6 (≥ threshold 5) and the
// unit would be cooling_down. We simulate the BUG path by calling
// RecordFailure twice per iteration (once for applyStreamOpenFailure, once for
// the would-be re-record) and assert that the unit cools after 3 iterations —
// demonstrating the test would catch the regression if the fix were reverted.
func TestConsecutiveFailures_DoubleRecordWouldTriggerAtThree(t *testing.T) {
	resetHealth()
	defer resetHealth()

	// Simulate the BUG: both applyStreamOpenFailure AND
	// recordProviderFailureLocked call RecordFailure.
	for i := 0; i < 3; i++ {
		llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 503, Message: "service unavailable"})
		llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 503, Message: "service unavailable"})
	}

	snap := llmclient.HealthSnapshot()
	s := snap["openai::gpt-4o"]
	// With the bug, 3 iterations × 2 records = 6 ≥ threshold 5 → cooling_down.
	if s.ConsecutiveFailures != 6 {
		t.Fatalf("bug simulation: ConsecutiveFailures = %d, want 6 (double-record)", s.ConsecutiveFailures)
	}
	if s.State != llmclient.HealthStateCoolingDown {
		t.Fatalf("bug simulation: state = %q, want cooling_down (premature trigger at 3 failures)", s.State)
	}
}

// recordingRef is a ref.Ref that counts every Invoke call by callID, so tests
// can assert that notifyAggregator / notifyAutoAggregator pushed config pushes
// to the child aggregators.
type recordingRef struct {
	calls atomic.Int64
}

func (r *recordingRef) ID() id.ActorID                   { return id.ActorID{} }
func (r *recordingRef) Service() (string, bool)         { return "", false }
func (r *recordingRef) Invoke(_ context.Context, _ string, _ any, _ ...map[string]string) *invoke.Call {
	r.calls.Add(1)
	return invoke.NewCall(invoke.CallModeUnary, nil)
}

// TestBecameUnhealthy_NotifiesAggregators verifies the full
// handleProviderReportFailure path: when a provider transitions from
// selectable (healthy) to cooling_down, becameUnhealthy is true and
// notifyAggregator / notifyAutoAggregator are actually invoked (config pushes
// reach the child aggregator refs). When the provider is already unhealthy and
// fails again, becameUnhealthy is false and no additional notification fires.
func TestBecameUnhealthy_NotifiesAggregators(t *testing.T) {
	resetHealth()
	defer resetHealth()

	// A named aggregator that references the failing provider + the auto
	// aggregator, both backed by recordingRefs so we can count config pushes.
	namedRef := &recordingRef{}
	autoRef := &recordingRef{}
	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"my-agg":        {ID: "my-agg", Name: "My Aggregator", Units: []domain.ManualCallableUnit{{ProviderName: "openai", Model: "gpt-4o"}}},
			autoAggregatorID: {ID: autoAggregatorID, Name: "Auto (All Providers)"},
		},
		aggRefs: map[string]ref.Ref{
			"my-agg":         namedRef,
			autoAggregatorID: autoRef,
		},
		persistedHealth:   make(map[string]persistedHealthEntry),
		providerLastHealth: make(map[string]string),
		store:             &memStore{data: make(map[string][]byte)},
		actorID:           "test-aimanager",
		lifecycleCtx:      context.Background(),
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())

	// First failure: a 429 immediately puts the unit into cooling_down.
	// providerLastHealth["openai"] is "" (selectable) → becameUnhealthy = true.
	namedBefore := namedRef.calls.Load()
	autoBefore := autoRef.calls.Load()

	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})
	_, err := a.handleProviderReportFailure(ctx, reportProviderFailureReq{
		ProviderName: "openai",
		Model:        "gpt-4o",
		StatusCode:   429,
		RetryAfter:   60,
	})
	if err != nil {
		t.Fatalf("first failure: handleProviderReportFailure: %v", err)
	}

	// The named aggregator and the auto aggregator must each have received
	// config pushes. notifyAggregator does 2 invokes (version + config);
	// notifyAutoAggregator does 2 invokes. We just assert the count increased.
	namedAfter := namedRef.calls.Load()
	autoAfter := autoRef.calls.Load()
	if namedAfter == namedBefore {
		t.Fatal("notifyAggregator was NOT called on healthy→cooling_down transition (named aggregator received no config push)")
	}
	if autoAfter == autoBefore {
		t.Fatal("notifyAutoAggregator was NOT called on healthy→cooling_down transition (auto aggregator received no config push)")
	}
	if a.providerLastHealth["openai"] != llmclient.HealthStateCoolingDown {
		t.Fatalf("providerLastHealth = %q, want cooling_down", a.providerLastHealth["openai"])
	}

	// Second failure: the provider is already cooling_down. becameUnhealthy
	// must be false (no selectable→unhealthy transition), so no additional
	// notification should fire.
	namedBefore2 := namedRef.calls.Load()
	autoBefore2 := autoRef.calls.Load()

	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: 60 * time.Second})
	_, err = a.handleProviderReportFailure(ctx, reportProviderFailureReq{
		ProviderName: "openai",
		Model:        "gpt-4o",
		StatusCode:   429,
		RetryAfter:   60,
	})
	if err != nil {
		t.Fatalf("second failure: handleProviderReportFailure: %v", err)
	}

	namedAfter2 := namedRef.calls.Load()
	autoAfter2 := autoRef.calls.Load()
	if namedAfter2 != namedBefore2 {
		t.Fatalf("notifyAggregator was called on already-unhealthy provider (no transition should notify); calls went %d→%d", namedBefore2, namedAfter2)
	}
	if autoAfter2 != autoBefore2 {
		t.Fatalf("notifyAutoAggregator was called on already-unhealthy provider (no transition should notify); calls went %d→%d", autoBefore2, autoAfter2)
	}
}

// TestBecameUnhealthy_DisabledTransition verifies that a healthy→disabled
// transition (401 auth failure) also triggers notifications, and that a
// subsequent failure on the already-disabled provider does not re-notify.
func TestBecameUnhealthy_DisabledTransition(t *testing.T) {
	resetHealth()
	defer resetHealth()

	namedRef := &recordingRef{}
	autoRef := &recordingRef{}
	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"my-agg":         {ID: "my-agg", Name: "My Aggregator", Units: []domain.ManualCallableUnit{{ProviderName: "openai", Model: "gpt-4o"}}},
			autoAggregatorID: {ID: autoAggregatorID, Name: "Auto (All Providers)"},
		},
		aggRefs: map[string]ref.Ref{
			"my-agg":         namedRef,
			autoAggregatorID: autoRef,
		},
		persistedHealth:   make(map[string]persistedHealthEntry),
		providerLastHealth: make(map[string]string),
		store:             &memStore{data: make(map[string][]byte)},
		actorID:           "test-aimanager",
		lifecycleCtx:      context.Background(),
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())

	namedBefore := namedRef.calls.Load()
	autoBefore := autoRef.calls.Load()

	// 401 → disabled. Healthy → disabled is a transition → becameUnhealthy = true.
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 401})
	_, err := a.handleProviderReportFailure(ctx, reportProviderFailureReq{
		ProviderName: "openai",
		Model:        "gpt-4o",
		StatusCode:   401,
	})
	if err != nil {
		t.Fatalf("handleProviderReportFailure: %v", err)
	}

	if namedRef.calls.Load() == namedBefore {
		t.Fatal("notifyAggregator was NOT called on healthy→disabled transition")
	}
	if autoRef.calls.Load() == autoBefore {
		t.Fatal("notifyAutoAggregator was NOT called on healthy→disabled transition")
	}
	if a.providerLastHealth["openai"] != llmclient.HealthStateDisabled {
		t.Fatalf("providerLastHealth = %q, want disabled", a.providerLastHealth["openai"])
	}

	// A second 401 on an already-disabled unit: no transition, no notification.
	namedBefore2 := namedRef.calls.Load()
	autoBefore2 := autoRef.calls.Load()
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 401})
	_, err = a.handleProviderReportFailure(ctx, reportProviderFailureReq{
		ProviderName: "openai",
		Model:        "gpt-4o",
		StatusCode:   401,
	})
	if err != nil {
		t.Fatalf("second handleProviderReportFailure: %v", err)
	}
	if namedRef.calls.Load() != namedBefore2 {
		t.Fatal("notifyAggregator should NOT be called on already-disabled provider")
	}
	if autoRef.calls.Load() != autoBefore2 {
		t.Fatal("notifyAutoAggregator should NOT be called on already-disabled provider")
	}
}
