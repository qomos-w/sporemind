package aimanager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestPropagateProviderUpdate_RefreshesMatchingUnits(t *testing.T) {
	a := &Actor{
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"my-agg": {
				ID:   "my-agg",
				Name: "My Aggregator",
				Units: []domain.ManualCallableUnit{
					{ProviderName: "openai", Model: "gpt-4o", Endpoint: "https://old.example", Protocol: "openai", MaxConcurrency: 1},
					{ProviderName: "anthropic", Model: "claude-opus-4-7", Endpoint: "https://anthropic", Protocol: "anthropic"},
				},
			},
		},
	}

	updated := domain.Provider{
		Name:     "openai",
		Kind:     "openai",
		Endpoint: "https://new.example",
		Models: []domain.ProviderModel{
			{Name: "gpt-4o"},
			{Name: "gpt-4o-mini"},
		},
		MaxConcurrency: 10,
	}

	toNotify, toStop := a.propagateProviderUpdate("openai", updated, false)
	if len(toNotify) != 1 || toNotify[0] != "my-agg" {
		t.Fatalf("expected my-agg notified, got %v", toNotify)
	}
	if len(toStop) != 0 {
		t.Fatalf("expected no stops, got %d", len(toStop))
	}

	cfg := a.aggregators["my-agg"]
	openaiUnit := cfg.Units[0]
	if openaiUnit.Endpoint != "https://new.example" {
		t.Errorf("endpoint not refreshed: %q", openaiUnit.Endpoint)
	}
	if openaiUnit.MaxConcurrency != 10 {
		t.Errorf("max concurrency not refreshed: %d", openaiUnit.MaxConcurrency)
	}
	if openaiUnit.Protocol != "openai" {
		t.Errorf("protocol not refreshed: %q", openaiUnit.Protocol)
	}
	anthropicUnit := cfg.Units[1]
	if anthropicUnit.Endpoint != "https://anthropic" {
		t.Errorf("unrelated provider endpoint should be untouched: %q", anthropicUnit.Endpoint)
	}
}

func TestPropagateProviderUpdate_DropsStaleModelUnits(t *testing.T) {
	a := &Actor{
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"my-agg": {
				ID:   "my-agg",
				Name: "My Aggregator",
				Units: []domain.ManualCallableUnit{
					{ProviderName: "openai", Model: "gpt-4o"},
					{ProviderName: "openai", Model: "legacy-model"},
				},
			},
		},
	}

	updated := domain.Provider{
		Name:   "openai",
		Kind:   "openai",
		Models: []domain.ProviderModel{{Name: "gpt-4o"}},
	}

	toNotify, _ := a.propagateProviderUpdate("openai", updated, false)
	if len(toNotify) != 1 {
		t.Fatalf("expected my-agg notified, got %v", toNotify)
	}

	cfg := a.aggregators["my-agg"]
	if len(cfg.Units) != 1 {
		t.Fatalf("expected 1 unit after drop, got %d", len(cfg.Units))
	}
	if cfg.Units[0].Model != "gpt-4o" {
		t.Errorf("expected gpt-4o to survive, got %q", cfg.Units[0].Model)
	}
}

func TestPropagateProviderUpdate_SkipsAutoAggregator(t *testing.T) {
	a := &Actor{
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			autoAggregatorID: {
				ID: autoAggregatorID,
				Units: []domain.ManualCallableUnit{
					{ProviderName: "openai", Model: "gpt-4o", Endpoint: "stale"},
				},
			},
		},
	}

	updated := domain.Provider{
		Name:     "openai",
		Kind:     "openai",
		Models:   []domain.ProviderModel{{Name: "gpt-4o"}},
		Endpoint: "new-endpoint",
	}

	toNotify, _ := a.propagateProviderUpdate("openai", updated, false)
	if len(toNotify) != 0 {
		t.Fatalf("auto aggregator must not be notified via this path, got %v", toNotify)
	}
	if a.aggregators[autoAggregatorID].Units[0].Endpoint != "stale" {
		t.Errorf("auto aggregator must be left untouched, got endpoint %q", a.aggregators[autoAggregatorID].Units[0].Endpoint)
	}
}

func TestPropagateProviderUpdate_StopsEmptyNamelessAggregator(t *testing.T) {
	a := &Actor{
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"unnamed": {
				ID:    "unnamed",
				Name:  "",
				Units: []domain.ManualCallableUnit{{ProviderName: "openai", Model: "gpt-4o"}},
			},
		},
	}

	updated := domain.Provider{
		Name:   "openai",
		Kind:   "openai",
		Models: []domain.ProviderModel{},
	}

	toNotify, toStop := a.propagateProviderUpdate("openai", updated, false)
	if len(toNotify) != 0 {
		t.Fatalf("unnamed empty aggregator should not be notified, got %v", toNotify)
	}
	if len(toStop) != 0 {
		t.Fatalf("no refs registered, expected no stops, got %d", len(toStop))
	}
	if _, exists := a.aggregators["unnamed"]; exists {
		t.Error("empty nameless aggregator should be removed")
	}
}

func TestPropagateProviderUpdate_NoOpWhenNothingChanged(t *testing.T) {
	a := &Actor{
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"my-agg": {
				ID:   "my-agg",
				Name: "My Aggregator",
				Units: []domain.ManualCallableUnit{
					{ProviderName: "openai", Model: "gpt-4o", Endpoint: "https://e", Protocol: "openai", MaxConcurrency: 5},
				},
			},
		},
	}

	updated := domain.Provider{
		Name:           "openai",
		Kind:           "openai",
		Endpoint:       "https://e",
		Models:         []domain.ProviderModel{{Name: "gpt-4o"}},
		MaxConcurrency: 5,
	}

	toNotify, toStop := a.propagateProviderUpdate("openai", updated, false)
	if len(toNotify) != 0 || len(toStop) != 0 {
		t.Fatalf("expected no-op, got notify=%v stop=%d", toNotify, len(toStop))
	}
}

// Token Plan field propagation (Phase 3).

func TestPropagateProviderUpdate_SyncsTokenPlanFields(t *testing.T) {
	a := &Actor{
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"my-agg": {
				ID:   "my-agg",
				Name: "My Aggregator",
				Units: []domain.ManualCallableUnit{
					{ProviderName: "openai", Model: "gpt-4o", Endpoint: "https://e", Protocol: "openai"},
				},
			},
		},
	}
	updated := domain.Provider{
		Name:                  "openai",
		Kind:                  "openai",
		Endpoint:              "https://e",
		Models:                []domain.ProviderModel{{Name: "gpt-4o"}},
		IsTokenPlan:           true,
		TokenPlanExpiresAt:    "2026-12-31T23:59:59Z",
		TokenPlanRemainingPct: 42,
		TokenPlanWindowMs:     3600000,
	}
	toNotify, _ := a.propagateProviderUpdate("openai", updated, false)
	if len(toNotify) != 1 {
		t.Fatalf("expected my-agg notified for token-plan change, got %v", toNotify)
	}
	u := a.aggregators["my-agg"].Units[0]
	if !u.IsTokenPlan || u.TokenPlanExpiresAt != "2026-12-31T23:59:59Z" || u.TokenPlanRemainingPct != 42 || u.TokenPlanWindowMs != 3600000 {
		t.Errorf("token-plan mirror not synced: %+v", u)
	}
}

func TestBuildAutoUnits_PropagatesTokenPlanFromProvider(t *testing.T) {
	a := &Actor{
		Providers: []domain.Provider{
			{
				Name: "kimi", Kind: "openai", Endpoint: "https://kimi",
				Models:      []domain.ProviderModel{{Name: "moonshot"}},
				IsTokenPlan: true, TokenPlanExpiresAt: "2026-12-31T23:59:59Z",
				TokenPlanRemainingPct: 77, TokenPlanWindowMs: 7200000,
			},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	units := a.buildAutoUnits()
	if len(units) != 1 {
		t.Fatalf("expected 1 unit, got %d", len(units))
	}
	u := units[0]
	if !u.IsTokenPlan || u.TokenPlanRemainingPct != 77 || u.TokenPlanWindowMs != 7200000 || u.TokenPlanExpiresAt != "2026-12-31T23:59:59Z" {
		t.Errorf("auto unit token-plan mirror wrong: %+v", u)
	}
}

func TestHandleProviderSetTokenPlan_UpdatesAndPropagates(t *testing.T) {
	a := &Actor{
		actorID: "test-aimanager",
		Providers: []domain.Provider{
			{Name: "kimi", Kind: "openai", Endpoint: "https://kimi", Models: []domain.ProviderModel{{Name: "moonshot"}}},
		},
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"my-agg": {
				ID:   "my-agg",
				Name: "My Agg",
				Units: []domain.ManualCallableUnit{
					{ProviderName: "kimi", Model: "moonshot", Endpoint: "https://kimi", Protocol: "openai"},
				},
			},
		},
	}
	// Use an isolated fs store so Save() does not touch the real data dir.
	a.store = persist.MustNew(persist.PersistConfig{DataDir: t.TempDir(), Prefix: "aimanager"})

	ctx := testutil.AdminCtx(testutil.GenActorID())

	resp, err := a.handleProviderSetTokenPlan(ctx, domain.AIManagerProviderSetTokenPlanReq{
		ProviderName: "kimi",
		ExpiresAt:    "2026-12-31T23:59:59Z",
		RemainingPct: 30,
		WindowMs:     3600000,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("expected Ok, got %+v", resp)
	}
	p := a.Providers[0]
	if !p.IsTokenPlan || p.TokenPlanRemainingPct != 30 || p.TokenPlanWindowMs != 3600000 || p.TokenPlanExpiresAt != "2026-12-31T23:59:59Z" {
		t.Errorf("provider token-plan not updated: %+v", p)
	}
	u := a.aggregators["my-agg"].Units[0]
	if !u.IsTokenPlan || u.TokenPlanRemainingPct != 30 {
		t.Errorf("aggregator unit token-plan mirror not synced: %+v", u)
	}
}

func TestHandleProviderSetTokenPlan_UnknownProviderFails(t *testing.T) {
	a := &Actor{
		actorID: "test-aimanager",
		Providers: []domain.Provider{
			{Name: "kimi"},
		},
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	resp, err := a.handleProviderSetTokenPlan(ctx, domain.AIManagerProviderSetTokenPlanReq{
		ProviderName: "ghost", RemainingPct: 50,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Ok || resp.Error == "" {
		t.Fatalf("expected failure for unknown provider, got %+v", resp)
	}
}

func TestHandleProviderSetTokenPlan_BadExpiryRejected(t *testing.T) {
	a := &Actor{
		actorID: "test-aimanager",
		Providers: []domain.Provider{
			{Name: "kimi"},
		},
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	resp, _ := a.handleProviderSetTokenPlan(ctx, domain.AIManagerProviderSetTokenPlanReq{
		ProviderName: "kimi", ExpiresAt: "not-a-date",
	})
	if resp.Ok {
		t.Fatalf("expected rejection of malformed expiry, got %+v", resp)
	}
}

func TestConfigImport_DoesNotTouchAggregators(t *testing.T) {
	local1ID := testutil.GenActorID()
	local2ID := testutil.GenActorID()
	local1Ref := testutil.NewFakeRef(local1ID, func(callID string, payload any) any { return nil })
	local2Ref := testutil.NewFakeRef(local2ID, func(callID string, payload any) any { return nil })

	autoID := testutil.GenActorID()
	autoRef := testutil.NewFakeRef(autoID, func(callID string, payload any) any { return nil })

	a := &Actor{
		actorID:   "test-aimanager",
		Providers: []domain.Provider{{Name: "openai", Kind: "openai", Endpoint: "https://openai.example.com", Models: []domain.ProviderModel{{Name: "gpt-4o"}}}},
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"local-1": {ID: "local-1", Name: "MyAgg", Units: []domain.ManualCallableUnit{{Model: "old-model", ProviderName: "openai"}}},
			"local-2": {ID: "local-2", Name: "Orphan", Units: []domain.ManualCallableUnit{{Model: "orphan-model", ProviderName: "openai"}}},
			autoAggregatorID: {ID: autoAggregatorID, Name: "Auto (All Providers)", Units: []domain.ManualCallableUnit{{Model: "gpt-4o", ProviderName: "openai"}}},
		},
		aggRefs: map[string]ref.Ref{
			"local-1":        local1Ref,
			"local-2":        local2Ref,
			autoAggregatorID: autoRef,
		},
		aggActorIDs: map[string]string{
			"local-1":        local1ID.String(),
			"local-2":        local2ID.String(),
			autoAggregatorID: autoID.String(),
		},
		AggregatorNames: make(map[string]string),
		configVersion:   1,
	}

	var spawned []string
	stoppedCh := make(chan string, 2)
	var notified []string
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = func(_ actor.Props, name string) (ref.Ref, error) {
		spawned = append(spawned, name)
		return testutil.NewFakeRef(testutil.GenActorID(), func(callID string, payload any) any { return nil }), nil
	}
	ctx.StopFn = func(target ref.Ref) error {
		stoppedCh <- target.ID().String()
		return nil
	}

	payload := domain.AIManagerConfigImportReq{
		Data: `{"providers":[{"Name":"anthropic","Kind":"anthropic","Endpoint":"https://anthropic.example.com","Models":[{"Name":"claude-opus"}]}],"aggregators":{"imported-1":{"ID":"imported-1","Name":"ImportedAgg","Units":[{"Model":"gpt-4o","ProviderName":"openai"}]}}}`,
	}

	_, err := a.handleConfigImport(ctx, payload)
	if err != nil {
		t.Fatalf("handleConfigImport returned error: %v", err)
	}

	// User-defined aggregators must be left untouched.
	if len(a.aggregators) != 3 {
		t.Fatalf("expected 3 aggregators after import, got %d", len(a.aggregators))
	}
	if cfg, ok := a.aggregators["local-1"]; !ok || cfg.Units[0].Model != "old-model" {
		t.Errorf("expected local-1 aggregator to be preserved, got %+v", cfg)
	}
	if cfg, ok := a.aggregators["local-2"]; !ok || cfg.Units[0].Model != "orphan-model" {
		t.Errorf("expected local-2 aggregator to be preserved, got %+v", cfg)
	}
	if a.aggRefs["local-1"].ID().String() != local1ID.String() {
		t.Error("expected local-1 actor ref to be preserved")
	}
	if a.aggRefs["local-2"].ID().String() != local2ID.String() {
		t.Error("expected local-2 actor ref to be preserved")
	}

	// Providers should be replaced.
	if len(a.Providers) != 1 || a.Providers[0].Name != "anthropic" {
		t.Errorf("expected providers to be replaced, got %+v", a.Providers)
	}

	// Auto aggregator should be rebuilt from new providers.
	auto := a.aggregators[autoAggregatorID]
	if len(auto.Units) != 1 || auto.Units[0].Model != "claude-opus" {
		t.Errorf("expected auto aggregator to be rebuilt from new providers, got %+v", auto.Units)
	}

	// No user aggregators should be spawned or stopped.
	if len(spawned) != 0 {
		t.Errorf("expected no new aggregators spawned, got %v", spawned)
	}
	select {
	case id := <-stoppedCh:
		t.Errorf("expected no aggregator to be stopped, got %s", id)
	default:
	}

	if a.configVersion <= 1 {
		t.Error("expected configVersion to be incremented")
	}

	t.Logf("notified aggregator IDs: %v", notified)
}

func TestHandleProviderResolveToken_ReturnsCurrentToken(t *testing.T) {
	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", AuthToken: "sk-real-token"},
			{Name: "anthropic", AuthToken: "sk-ant-token"},
		},
	}

	resp, err := a.handleProviderResolveToken(nil, domain.AIManagerProviderResolveTokenReq{Name: "openai"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.AuthToken != "sk-real-token" {
		t.Errorf("expected sk-real-token, got %q", resp.AuthToken)
	}
}

func TestHandleProviderResolveToken_UnknownProviderReturnsEmpty(t *testing.T) {
	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", AuthToken: "sk-real-token"},
		},
	}

	resp, err := a.handleProviderResolveToken(nil, domain.AIManagerProviderResolveTokenReq{Name: "unknown"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.AuthToken != "" {
		t.Errorf("expected empty token for unknown provider, got %q", resp.AuthToken)
	}
}

func TestMigrateLegacyProviderModels_ConvertsStringSlice(t *testing.T) {
	raw := map[string]any{
		"providers": []any{
			map[string]any{
				"Name":   "glm",
				"Kind":   "openai",
				"Models": []any{"glm-4.5", "glm-4.6"},
			},
			map[string]any{
				"Name":   "deepseek",
				"Kind":   "openai",
				"Models": []any{"deepseek-v4-pro"},
			},
		},
	}
	migrateLegacyProviderModels(raw)

	var state persistState
	rawBytes, _ := json.Marshal(raw)
	if err := json.Unmarshal(rawBytes, &state); err != nil {
		t.Fatalf("decode after migration failed: %v", err)
	}
	if len(state.Providers) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(state.Providers))
	}
	if len(state.Providers[0].Models) != 2 {
		t.Fatalf("provider 0: expected 2 models, got %d", len(state.Providers[0].Models))
	}
	if state.Providers[0].Models[0].Name != "glm-4.5" {
		t.Errorf("provider 0 model 0: expected glm-4.5, got %q", state.Providers[0].Models[0].Name)
	}
	if state.Providers[1].Models[0].Name != "deepseek-v4-pro" {
		t.Errorf("provider 1 model 0: expected deepseek-v4-pro, got %q", state.Providers[1].Models[0].Name)
	}
}

func TestMigrateLegacyProviderModels_Idempotent(t *testing.T) {
	raw := map[string]any{
		"providers": []any{
			map[string]any{
				"Name":   "glm",
				"Models": []any{map[string]any{"Name": "glm-4.5"}},
			},
		},
	}
	migrateLegacyProviderModels(raw)

	var state persistState
	rawBytes, _ := json.Marshal(raw)
	if err := json.Unmarshal(rawBytes, &state); err != nil {
		t.Fatalf("decode after no-op migration failed: %v", err)
	}
	if len(state.Providers) != 1 || len(state.Providers[0].Models) != 1 {
		t.Fatalf("expected 1 provider with 1 model, got %d/%d", len(state.Providers), len(state.Providers[0].Models))
	}
	if state.Providers[0].Models[0].Name != "glm-4.5" {
		t.Errorf("expected glm-4.5, got %q", state.Providers[0].Models[0].Name)
	}
}

func TestRecordProviderFailure_EntersCooldownAfterThreshold(t *testing.T) {
	resetHealth()
	defer resetHealth()

	policy := llmclient.DefaultProviderHealth.Policy()
	for i := 0; i < policy.AvailabilityThreshold-1; i++ {
		llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 503})
	}
	if s := llmclient.HealthSnapshot()["openai::gpt-4o"]; s.State != llmclient.HealthStateHealthy {
		t.Fatalf("failure before threshold should remain healthy, got %q", s.State)
	}
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 503})
	s := llmclient.HealthSnapshot()["openai::gpt-4o"]
	if s.State != llmclient.HealthStateCoolingDown {
		t.Fatalf("expected cooldown at threshold, got %q", s.State)
	}
	if s.CooldownUntil.IsZero() {
		t.Fatal("cooldown deadline must be set")
	}
}

func TestRecordProviderFailure_CooldownBackoffUsesInjectedPolicy(t *testing.T) {
	resetHealth()
	defer resetHealth()

	policy := llmclient.DefaultCooldownPolicy()
	llmclient.SetCooldownPolicy(policy)
	for i := 0; i < policy.AvailabilityThreshold; i++ {
		llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 503})
	}
	first := llmclient.HealthSnapshot()["openai::gpt-4o"].CooldownUntil
	if first.IsZero() {
		t.Fatal("expected initial cooldown")
	}
	// The llmclient state intentionally keeps the first cooldown while active;
	// verify the policy cap directly instead of relying on deleted aimanager
	// backoff helpers.
	if first.Sub(time.Now()) > policy.MaxCooldown {
		t.Fatalf("cooldown exceeds policy cap: %v", first.Sub(time.Now()))
	}
}

func TestBuildAutoUnits_IncludesCooledDownProvidersWithAnnotation(t *testing.T) {
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
	if len(units) != 2 {
		t.Fatalf("expected 2 units, got %d", len(units))
	}
	var openaiUnit, anthropicUnit *domain.ManualCallableUnit
	for i := range units {
		switch units[i].ProviderName {
		case "openai":
			openaiUnit = &units[i]
		case "anthropic":
			anthropicUnit = &units[i]
		}
	}
	if openaiUnit == nil || openaiUnit.CooldownUntil == 0 {
		t.Errorf("expected openai unit cooldown annotation, got %+v", openaiUnit)
	}
	if anthropicUnit == nil || anthropicUnit.CooldownUntil != 0 {
		t.Errorf("expected anthropic unit without cooldown, got %+v", anthropicUnit)
	}
}

func TestBuildAutoUnits_AllProvidersAlwaysIncluded(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
			{Name: "anthropic", Kind: "anthropic", Endpoint: "https://anthropic", Models: []domain.ProviderModel{{Name: "claude"}}},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 401})
	units := a.buildAutoUnits()
	if len(units) != 2 {
		t.Fatalf("expected both providers (disabled included), got %+v", units)
	}
}

func TestBuildAutoUnits_IncludesProviderAfterCooldownExpires(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers:       []domain.Provider{{Name: "openai", Kind: "openai", Endpoint: "https://openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}}},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	// Supply an already-expired snapshot entry to the pure projection path.
	snap := map[string]llmclient.UnitHealthSnapshot{
		"openai::gpt-4o": {Provider: "openai", Model: "gpt-4o", State: llmclient.HealthStateCoolingDown, CooldownUntil: time.Now().Add(-time.Second)},
	}
	if state, _, _, cd := unitHealthProjection(snap, "openai", "gpt-4o", time.Now()); state != "" || cd != 0 {
		t.Fatalf("expired cooldown should project healthy, got state=%q cd=%d", state, cd)
	}
	units := a.buildAutoUnits()
	if len(units) != 1 || units[0].CooldownUntil != 0 {
		t.Fatalf("provider should remain selectable after expiry, got %+v", units)
	}
}

func TestHandleProviderResetHealth_ClearsLlmclientState(t *testing.T) {
	resetHealth()
	defer resetHealth()

	a := &Actor{
		Providers:       []domain.Provider{{Name: "openai", Models: []domain.ProviderModel{{Name: "gpt-4o"}}}},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 429, RetryAfter: time.Minute})
	if _, ok := llmclient.HealthSnapshot()["openai::gpt-4o"]; !ok {
		t.Fatal("setup failed: unit health missing")
	}

	a.clearProviderHealthLocked("openai")
	if _, ok := llmclient.HealthSnapshot()["openai::gpt-4o"]; ok {
		t.Fatal("provider health should be cleared")
	}
}

func TestRecordProviderFailure_SuccessThenFailureUsesFreshCounter(t *testing.T) {
	resetHealth()
	defer resetHealth()

	policy := llmclient.DefaultProviderHealth.Policy()
	for i := 0; i < policy.AvailabilityThreshold-1; i++ {
		llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 503})
	}
	llmclient.RecordSuccess("openai", "gpt-4o")
	llmclient.RecordFailure("openai", "gpt-4o", &llmclient.UpstreamError{StatusCode: 503})
	s := llmclient.HealthSnapshot()["openai::gpt-4o"]
	if s.State != llmclient.HealthStateHealthy || s.ConsecutiveFailures != 1 {
		t.Fatalf("success should reset the availability counter, got state=%q failures=%d", s.State, s.ConsecutiveFailures)
	}
}

// memStore is an in-memory persist.Persist for unit tests.
type memStore struct {
	data map[string][]byte
}

func (m *memStore) Load(name string, v any) error {
	b, ok := m.data[name]
	if !ok {
		return persist.ErrNotExist
	}
	return json.Unmarshal(b, v)
}

func (m *memStore) Save(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.data[name] = b
	return nil
}

func (m *memStore) Delete(name string) error {
	delete(m.data, name)
	return nil
}

func TestSave_PersistsAutoAggregatorActorID(t *testing.T) {
	ms := &memStore{data: make(map[string][]byte)}

	autoID := testutil.GenActorID()
	manualID := testutil.GenActorID()
	a := &Actor{
		actorID:     "test-aimanager",
		store:       ms,
		aggregators: make(map[string]domain.AIManagerAggregatorGetResp),
		aggRefs: map[string]ref.Ref{
			autoAggregatorID: testutil.NewFakeRef(autoID, nil),
			"manual":         testutil.NewFakeRef(manualID, nil),
		},
	}
	a.aggregators[autoAggregatorID] = domain.AIManagerAggregatorGetResp{ID: autoAggregatorID, Name: "Auto (All Providers)"}
	a.aggregators["manual"] = domain.AIManagerAggregatorGetResp{ID: "manual", Name: "Manual"}

	if err := a.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	raw, ok := ms.data["test-aimanager"]
	if !ok {
		t.Fatal("state not saved")
	}
	var state persistState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	if state.AggActorIDs[autoAggregatorID] != autoID.String() {
		t.Fatalf("auto aggregator actor ID not persisted, got %q want %q", state.AggActorIDs[autoAggregatorID], autoID.String())
	}
	if state.AggActorIDs["manual"] != manualID.String() {
		t.Fatalf("manual aggregator actor ID not persisted, got %q want %q", state.AggActorIDs["manual"], manualID.String())
	}
	if _, ok := state.Aggregators[autoAggregatorID]; ok {
		t.Fatal("auto aggregator config should not be persisted")
	}
}

func TestLoad_RestoresAutoAggregatorActorID(t *testing.T) {
	ms := &memStore{data: make(map[string][]byte)}

	autoID := testutil.GenActorID()
	seed := persistState{
		Providers:   []domain.Provider{},
		Aggregators: map[string]domain.AIManagerAggregatorGetResp{},
		AggActorIDs: map[string]string{autoAggregatorID: autoID.String()},
	}
	raw, _ := json.Marshal(seed)
	ms.data["test-aimanager"] = raw

	a := &Actor{actorID: "test-aimanager", store: ms}
	if err := a.Load(); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if a.aggActorIDs[autoAggregatorID] != autoID.String() {
		t.Fatalf("loaded auto aggregator actor ID mismatch, got %q want %q", a.aggActorIDs[autoAggregatorID], autoID.String())
	}
}

func TestLoad_NormalizesLegacyPriorityStrategy(t *testing.T) {
	ms := &memStore{data: make(map[string][]byte)}
	seed := persistState{
		Aggregators: map[string]domain.AIManagerAggregatorGetResp{
			"legacy": {ID: "legacy", Name: "Legacy", Strategy: "priority", Units: []domain.ManualCallableUnit{}},
		},
	}
	raw, _ := json.Marshal(seed)
	ms.data["test-aimanager"] = raw

	a := &Actor{actorID: "test-aimanager", store: ms}
	if err := a.Load(); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if got := a.aggregators["legacy"].Strategy; got != "fallback" {
		t.Fatalf("loaded strategy = %q, want fallback", got)
	}
}

func TestSpawnAggregator_ReusesPersistedAutoAggregatorActorID(t *testing.T) {
	autoID := testutil.GenActorID()
	a := &Actor{
		actorID:     "test-aimanager",
		aggregators: make(map[string]domain.AIManagerAggregatorGetResp),
		aggRefs:     make(map[string]ref.Ref),
		aggActorIDs: map[string]string{autoAggregatorID: autoID.String()},
	}

	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.SpawnFn = func(props actor.Props, name string) (ref.Ref, error) {
		if name != autoAggregatorID {
			t.Fatalf("unexpected spawn name %q", name)
		}
		if props.ID().String() != autoID.String() {
			t.Fatalf("expected spawn with actor ID %q, got %q", autoID.String(), props.ID().String())
		}
		return testutil.NewFakeRef(props.ID(), nil), nil
	}
	ctx.LookupIDFn = func(id.ActorID) (ref.Ref, bool) { return nil, false }

	ref := a.spawnAggregator(ctx, autoAggregatorID, a.aggActorIDs[autoAggregatorID])
	if ref == nil {
		t.Fatal("expected non-nil ref")
	}
	if ref.ID().String() != autoID.String() {
		t.Fatalf("expected auto aggregator ref ID %q, got %q", autoID.String(), ref.ID().String())
	}
}

// TestInferProtocol_ExplicitWins verifies a non-empty explicit protocol
// (ProviderModel.Protocol) takes precedence over Provider.Kind. This replaces
// the previous model-name heuristic: claude-* / gpt-* names no longer imply a
// protocol.
func TestInferProtocol_ExplicitWins(t *testing.T) {
	if got := inferProtocol("claude-opus-4", "endpoint", "anthropic"); got != "endpoint" {
		t.Errorf("explicit must win: got %q, want endpoint", got)
	}
	if got := inferProtocol("gpt-4o", "anthropic", "openai"); got != "anthropic" {
		t.Errorf("explicit must win: got %q, want anthropic", got)
	}
}

// TestInferProtocol_FallsBackToProviderKind verifies that when a model has no
// explicit protocol, the provider Kind is used.
func TestInferProtocol_FallsBackToProviderKind(t *testing.T) {
	if got := inferProtocol("claude-opus-4", "", "anthropic"); got != "anthropic" {
		t.Errorf("kind fallback failed: got %q, want anthropic", got)
	}
	if got := inferProtocol("gpt-4o", "", "openai"); got != "openai" {
		t.Errorf("kind fallback failed: got %q, want openai", got)
	}
	if got := inferProtocol("gpt-5", "", "responses"); got != "responses" {
		t.Errorf("kind fallback failed: got %q, want responses", got)
	}
}

// TestInferProtocol_NoModelNameHeuristic verifies the model name is no longer
// inspected: a claude- model on an openai-kind provider resolves to openai,
// and a gpt- model on an anthropic-kind provider resolves to anthropic.
// Previously these names would override the provider kind.
func TestInferProtocol_NoModelNameHeuristic(t *testing.T) {
	if got := inferProtocol("claude-opus-4", "", "openai"); got != "openai" {
		t.Errorf("claude- name must not override kind: got %q, want openai", got)
	}
	if got := inferProtocol("gpt-4o", "", "anthropic"); got != "anthropic" {
		t.Errorf("gpt- name must not override kind: got %q, want anthropic", got)
	}
}

// TestInferProtocol_NoGeminiSynthesis verifies a gemini- model name no longer
// synthesises a "gemini" protocol; it falls back to the provider kind. This
// closes the gap where the old heuristic returned "gemini" but no chat client
// existed, causing dispatch-time failures.
func TestInferProtocol_NoGeminiSynthesis(t *testing.T) {
	// gemini- name on an openai-compatible provider -> openai, not gemini.
	if got := inferProtocol("gemini-2.5-flash", "", "openai"); got != "openai" {
		t.Errorf("gemini- name must not synthesise gemini protocol: got %q, want openai", got)
	}
	// No kind, no explicit -> empty (unknown), not the old "openai" default.
	if got := inferProtocol("gemini-pro", "", ""); got != "" {
		t.Errorf("empty kind must yield empty protocol, got %q", got)
	}
}

// TestSupportedProtocols locks the set of protocols with a client impl. It must
// stay in sync with aiaggregator.newBuiltinRegistry; "gemini" is intentionally
// absent (no chat client).
func TestSupportedProtocols(t *testing.T) {
	for _, p := range []string{"anthropic", "openai", "endpoint", "responses"} {
		if !isSupportedProtocol(p) {
			t.Errorf("%q should be supported", p)
		}
	}
	if isSupportedProtocol("gemini") {
		t.Error("gemini chat must not be supported")
	}
	if isSupportedProtocol("") {
		t.Error("empty protocol must not be supported")
	}
}

// TestBuildAutoUnits_SkipsUnsupportedProtocol verifies that a model resolving
// to an unsupported protocol (e.g. gemini chat) is dropped from auto units
// rather than carried into dispatch where it would fail.
func TestBuildAutoUnits_SkipsUnsupportedProtocol(t *testing.T) {
	a := &Actor{
		Providers: []domain.Provider{
			{
				Name:     "google",
				Kind:     "gemini", // no gemini chat client -> unsupported
				Endpoint: "https://generativelanguage.googleapis.com",
				Models: []domain.ProviderModel{
					{Name: "gemini-2.5-flash"}, // resolves to "gemini" -> skipped
				},
			},
			{
				Name: "openai",
				Kind: "openai",
				Models: []domain.ProviderModel{
					{Name: "gpt-4o", Protocol: "openai"},
				},
			},
		},
	}

	units := a.buildAutoUnits()
	// google provider: gemini-2.5-flash resolves to "gemini" protocol -> skipped.
	// openai provider: gpt-4o resolves to "openai" -> kept.
	if len(units) != 1 {
		t.Fatalf("expected 1 unit (unsupported gemini dropped), got %d: %+v", len(units), units)
	}
	if units[0].Model != "gpt-4o" {
		t.Errorf("expected gpt-4o to survive, got %q", units[0].Model)
	}
	if units[0].Protocol != "openai" {
		t.Errorf("expected openai protocol, got %q", units[0].Protocol)
	}
}

// TestBuildAutoUnits_ResponsesKind verifies a provider with Kind="responses"
// produces chat units resolving to the "responses" protocol (default from the
// provider kind). The responses client is registered in pkg/llmclient, so the
// unsupported-protocol gate must let these units through.
func TestBuildAutoUnits_ResponsesKind(t *testing.T) {
	a := &Actor{
		Providers: []domain.Provider{
			{
				Name:     "openai-responses",
				Kind:     "responses",
				Endpoint: "https://api.openai.com/v1",
				Models: []domain.ProviderModel{
					{Name: "gpt-5"},
					{Name: "gpt-5.1", Protocol: "openai"}, // explicit overrides kind
				},
			},
		},
	}
	units := a.buildAutoUnits()
	if len(units) != 2 {
		t.Fatalf("expected 2 units, got %d: %+v", len(units), units)
	}
	byModel := map[string]string{}
	for _, u := range units {
		byModel[u.Model] = u.Protocol
	}
	if byModel["gpt-5"] != "responses" {
		t.Errorf("kind fallback should yield responses protocol, got %q", byModel["gpt-5"])
	}
	if byModel["gpt-5.1"] != "openai" {
		t.Errorf("explicit protocol must override responses kind, got %q", byModel["gpt-5.1"])
	}
}

// TestBuildAutoUnits_ExplicitModelProtocolOverridesKind verifies a model with
// an explicit Protocol field overrides its provider Kind for unit resolution.
func TestBuildAutoUnits_ExplicitModelProtocolOverridesKind(t *testing.T) {
	a := &Actor{
		Providers: []domain.Provider{
			{
				Name: "third-party",
				Kind: "openai",
				Models: []domain.ProviderModel{
					{Name: "claude-via-gateway", Protocol: "anthropic"},
					{Name: "plain-gpt"},
				},
			},
		},
	}
	units := a.buildAutoUnits()
	if len(units) != 2 {
		t.Fatalf("expected 2 units, got %d", len(units))
	}
	byModel := map[string]string{}
	for _, u := range units {
		byModel[u.Model] = u.Protocol
	}
	if byModel["claude-via-gateway"] != "anthropic" {
		t.Errorf("explicit protocol anthropic not applied: %q", byModel["claude-via-gateway"])
	}
	if byModel["plain-gpt"] != "openai" {
		t.Errorf("provider kind fallback failed: %q", byModel["plain-gpt"])
	}
}

// TestBuildAutoUnits_KeepsGeminiImageModels verifies the unsupported-protocol
// gate does NOT drop gemini image models. Image units route through
// pkg/llmclient/imagegen (which supports "gemini"), not the chat client path,
// so they must survive buildAutoUnits to feed handleImageResolve. This is the
// regression guard for the gemini-image breakage that an earlier, modality-
// blind version of the gate introduced.
func TestBuildAutoUnits_KeepsGeminiImageModels(t *testing.T) {
	a := &Actor{
		Providers: []domain.Provider{
			{
				Name:     "google",
				Kind:     "gemini", // unsupported for chat, but valid for image
				Endpoint: "https://generativelanguage.googleapis.com",
				Models: []domain.ProviderModel{
					{Name: "gemini-2.5-flash"},                      // chat -> dropped (no gemini chat client)
					{Name: "gemini-3-pro-image-preview"},            // image via name heuristic -> kept
					{Name: "gemini-3-pro-image", Modality: "image"}, // image via explicit -> kept
				},
			},
		},
	}
	units := a.buildAutoUnits()
	byModel := map[string]domain.ManualCallableUnit{}
	for _, u := range units {
		byModel[u.Model] = u
	}
	if _, ok := byModel["gemini-2.5-flash"]; ok {
		t.Error("gemini chat model must be dropped (no chat client)")
	}
	img1, ok := byModel["gemini-3-pro-image-preview"]
	if !ok {
		t.Fatal("gemini image model (name-inferred) must be kept")
	}
	if img1.Modality != "image" {
		t.Errorf("expected image modality, got %q", img1.Modality)
	}
	if img1.Protocol != "gemini" {
		t.Errorf("expected gemini protocol preserved, got %q", img1.Protocol)
	}
	img2, ok := byModel["gemini-3-pro-image"]
	if !ok {
		t.Fatal("gemini image model (explicit modality) must be kept")
	}
	if img2.Modality != "image" {
		t.Errorf("expected image modality, got %q", img2.Modality)
	}
}

func TestNotifyAutoAggregator_DefaultsToSmartStrategy(t *testing.T) {
	autoID := testutil.GenActorID()
	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://api.openai.com",
				Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			autoAggregatorID: {ID: autoAggregatorID, Name: "Auto (All Providers)"},
		},
		aggRefs: map[string]ref.Ref{
			autoAggregatorID: testutil.NewFakeRef(autoID, func(string, any) any { return nil }),
		},
		persistedHealth: make(map[string]persistedHealthEntry),
		lifecycleCtx:    context.Background(),
	}

	a.notifyAutoAggregator()

	cfg := a.aggregators[autoAggregatorID]
	if cfg.Strategy != "smart" {
		t.Errorf("auto aggregator strategy: got %q, want %q", cfg.Strategy, "smart")
	}
}

func TestNotifyAutoAggregator_PreservesPersistedStrategy(t *testing.T) {
	autoID := testutil.GenActorID()
	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://api.openai.com",
				Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			autoAggregatorID: {ID: autoAggregatorID, Name: "Auto (All Providers)", Strategy: "fallback"},
		},
		aggRefs: map[string]ref.Ref{
			autoAggregatorID: testutil.NewFakeRef(autoID, func(string, any) any { return nil }),
		},
		persistedHealth: make(map[string]persistedHealthEntry),
		lifecycleCtx:    context.Background(),
	}

	a.notifyAutoAggregator()

	cfg := a.aggregators[autoAggregatorID]
	if cfg.Strategy != "fallback" {
		t.Errorf("auto aggregator strategy: got %q, want %q (should preserve persisted, not default to smart)", cfg.Strategy, "fallback")
	}
}

// TestNotifyAutoAggregator_NormalizesAliasStrategy verifies that a persisted
// legacy alias ("round-robin", hyphenated) is canonicalized to the wire name
// "round_robin" on refresh, instead of being echoed back verbatim.
func TestNotifyAutoAggregator_NormalizesAliasStrategy(t *testing.T) {
	autoID := testutil.GenActorID()
	a := &Actor{
		Providers: []domain.Provider{
			{Name: "openai", Kind: "openai", Endpoint: "https://api.openai.com",
				Models: []domain.ProviderModel{{Name: "gpt-4o"}}},
		},
		aggregators: map[string]domain.AIManagerAggregatorGetResp{
			autoAggregatorID: {ID: autoAggregatorID, Name: "Auto (All Providers)", Strategy: "round-robin"},
		},
		aggRefs: map[string]ref.Ref{
			autoAggregatorID: testutil.NewFakeRef(autoID, func(string, any) any { return nil }),
		},
		persistedHealth: make(map[string]persistedHealthEntry),
		lifecycleCtx:    context.Background(),
	}

	a.notifyAutoAggregator()

	cfg := a.aggregators[autoAggregatorID]
	if cfg.Strategy != "round_robin" {
		t.Errorf("auto aggregator strategy: got %q, want %q (legacy alias should normalize)", cfg.Strategy, "round_robin")
	}
}

// TestFetchModels_ResponsesKindUsesOpenAICompatPath verifies that fetching
// models for a "responses" provider hits the same OpenAI-compatible
// GET {endpoint}/models listing (with Bearer auth) and that the returned
// models default to Protocol="responses" — matching the openai path, not the
// anthropic/gemini branches.
func TestFetchModels_ResponsesKindUsesOpenAICompatPath(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"},{"id":"gpt-5.1"}]}`))
	}))
	defer srv.Close()

	a := &Actor{}
	resp, err := a.handleProviderFetchModels(nil, domain.AIManagerProviderFetchModelsReq{
		Name:      "openai-responses",
		Kind:      "responses",
		Endpoint:  srv.URL,
		AuthToken: "test-token",
	})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if gotPath != "/models" {
		t.Errorf("expected GET /models, got %q", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("expected Bearer auth, got %q", gotAuth)
	}
	if len(resp.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(resp.Models))
	}
	for _, m := range resp.Models {
		if m.Protocol != "responses" {
			t.Errorf("model %q: expected protocol responses, got %q", m.Name, m.Protocol)
		}
	}
}

// openaiCompatServer returns a test server that serves the OpenAI-compatible
// GET {endpoint}/models listing and answers POST {endpoint}/responses with the
// given probe status. When probeHits is non-nil it is incremented on each
// /responses request so tests can assert the probe was (or was not) sent.
func openaiCompatServer(t *testing.T, probeStatus int, probeHits *int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/responses":
			if probeHits != nil {
				*probeHits++
			}
			w.WriteHeader(probeStatus)
		case "/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-5"},{"id":"gpt-5.1"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestFetchModels_OpenaiKindProbesResponses_OnSuccess verifies that a plain
// "openai" fetch probes /responses and, on success (200), infers the responses
// protocol for every listed model.
func TestFetchModels_OpenaiKindProbesResponses_OnSuccess(t *testing.T) {
	var probeHits int
	srv := openaiCompatServer(t, http.StatusOK, &probeHits)

	a := &Actor{}
	resp, err := a.handleProviderFetchModels(nil, domain.AIManagerProviderFetchModelsReq{
		Name:      "openai",
		Kind:      "openai",
		Endpoint:  srv.URL,
		AuthToken: "test-token",
	})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if probeHits != 1 {
		t.Errorf("expected probe /responses to fire once, got %d hits", probeHits)
	}
	if len(resp.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(resp.Models))
	}
	for _, m := range resp.Models {
		if m.Protocol != "responses" {
			t.Errorf("model %q: expected protocol responses (probe success), got %q", m.Name, m.Protocol)
		}
	}
}

// TestFetchModels_OpenaiKindFallsBackOn404 verifies that when the probe
// returns 404, the openai protocol is kept (no responses inference).
func TestFetchModels_OpenaiKindFallsBackOn404(t *testing.T) {
	var probeHits int
	srv := openaiCompatServer(t, http.StatusNotFound, &probeHits)

	a := &Actor{}
	resp, err := a.handleProviderFetchModels(nil, domain.AIManagerProviderFetchModelsReq{
		Name:      "openai",
		Kind:      "openai",
		Endpoint:  srv.URL,
		AuthToken: "test-token",
	})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if probeHits != 1 {
		t.Errorf("expected probe /responses to fire once, got %d hits", probeHits)
	}
	for _, m := range resp.Models {
		if m.Protocol != "openai" {
			t.Errorf("model %q: expected protocol openai (404 fallback), got %q", m.Name, m.Protocol)
		}
	}
}

// TestFetchModels_ResponsesKindDoesNotProbe verifies that an explicit
// "responses" kind is never probed — the user's choice always wins.
func TestFetchModels_ResponsesKindDoesNotProbe(t *testing.T) {
	var probeHits int
	srv := openaiCompatServer(t, http.StatusOK, &probeHits)

	a := &Actor{}
	resp, err := a.handleProviderFetchModels(nil, domain.AIManagerProviderFetchModelsReq{
		Name:      "openai-responses",
		Kind:      "responses",
		Endpoint:  srv.URL,
		AuthToken: "test-token",
	})
	if err != nil {
		t.Fatalf("fetch failed: %v", err)
	}
	if probeHits != 0 {
		t.Errorf("expected no probe for explicit responses kind, got %d hits", probeHits)
	}
	for _, m := range resp.Models {
		if m.Protocol != "responses" {
			t.Errorf("model %q: expected protocol responses, got %q", m.Name, m.Protocol)
		}
	}
}
