package aiaggregator

import (
	"context"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
)

func TestSelectUnit_DisabledProviderHardSkipped(t *testing.T) {
	a := &Actor{
		id:       systemAggregatorID,
		strategy: NewFallbackStrategy(),
		units: []CallableUnit{
			{ID: "openai::gpt-4o", Model: "gpt-4o", ProviderName: "openai", Disabled: true},
			{ID: "anthropic::claude-opus-4-7", Model: "claude-opus-4-7", ProviderName: "anthropic"},
		},
	}

	got, err := a.selectUnit(SelectRequest{Unit: domain.ModelUnit{}})
	if err != nil {
		t.Fatalf("selectUnit: %v", err)
	}
	if got.ID != "anthropic::claude-opus-4-7" {
		t.Fatalf("selected %q, want anthropic::claude-opus-4-7", got.ID)
	}
}

func TestSelectUnit_DisabledModelLeavesSiblingModelsAvailable(t *testing.T) {
	a := &Actor{
		id:       systemAggregatorID,
		strategy: NewFallbackStrategy(),
		units: []CallableUnit{
			{ID: "openai::gpt-4o", Model: "gpt-4o", ProviderName: "openai", Disabled: true},
			{ID: "openai::gpt-4o-mini", Model: "gpt-4o-mini", ProviderName: "openai"},
		},
	}

	got, err := a.selectUnit(SelectRequest{Unit: domain.ModelUnit{}})
	if err != nil {
		t.Fatalf("selectUnit: %v", err)
	}
	if got.ID != "openai::gpt-4o-mini" {
		t.Fatalf("selected %q, want openai::gpt-4o-mini", got.ID)
	}
}

func TestSelectUnit_DisabledAggregatorRefHardSkipped(t *testing.T) {
	a := &Actor{
		id:       "parent",
		strategy: NewFallbackStrategy(),
		units: []CallableUnit{
			{ID: "agg:child", AggregatorID: "child", Disabled: true},
			{ID: "openai::gpt-4o", Model: "gpt-4o", ProviderName: "openai"},
		},
	}

	got, err := a.selectUnit(SelectRequest{Unit: domain.ModelUnit{}})
	if err != nil {
		t.Fatalf("selectUnit: %v", err)
	}
	if got.ID != "openai::gpt-4o" {
		t.Fatalf("selected %q, want openai::gpt-4o", got.ID)
	}
}

func TestSelectUnit_AllDisabledReturnsNoUnit(t *testing.T) {
	a := &Actor{
		id:       systemAggregatorID,
		strategy: NewFallbackStrategy(),
		units: []CallableUnit{
			{ID: "openai::gpt-4o", Model: "gpt-4o", ProviderName: "openai", Disabled: true},
		},
	}

	_, err := a.selectUnit(SelectRequest{Unit: domain.ModelUnit{}})
	if err == nil {
		t.Fatal("expected error when all units are disabled")
	}
	if !strings.Contains(err.Error(), "no callable unit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSelectOnDemandUnit_DisabledRejected(t *testing.T) {
	a := &Actor{
		id:           systemAggregatorID,
		lifecycleCtx: context.Background(),
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_model": domain.AIManagerProviderResolveModelResp{
				Endpoint: "https://api.example.com",
				Protocol: "openai",
				Disabled: true,
			},
		}},
	}

	_, err := a.selectOnDemandUnit(domain.ModelUnit{Provider: "openai", Model: "gpt-4o"}, false)
	if err == nil {
		t.Fatal("expected disabled on-demand unit to be rejected")
	}
	if !strings.Contains(err.Error(), "no callable unit") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleImageResolve_SkipsDisabledUnit(t *testing.T) {
	a := &Actor{
		configLoaded: true,
		lifecycleCtx: context.Background(),
		units: []CallableUnit{
			{ID: "openai::dall-e", Model: "dall-e", ProviderName: "openai", Modality: "image", Disabled: true},
			{ID: "stability::sd3", Model: "sd3", ProviderName: "stability", Modality: "image"},
		},
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_token": domain.AIManagerProviderResolveTokenResp{AuthToken: "token"},
		}},
	}

	resp, err := a.handleImageResolve(nil, domain.AIAggregatorImageResolveReq{})
	if err != nil {
		t.Fatalf("handleImageResolve: %v", err)
	}
	if resp.ProviderName != "stability" {
		t.Fatalf("selected provider %q, want stability", resp.ProviderName)
	}
}

func TestHandleDispatch_FailsFastWhenAggregatorDisabled(t *testing.T) {
	a := &Actor{
		id:           "named",
		name:         "named-agg",
		configLoaded: true,
		disabled:     true,
		units: []CallableUnit{
			{ID: "openai::gpt-4o", Model: "gpt-4o", ProviderName: "openai"},
		},
		lifecycleCtx: context.Background(),
	}

	err := a.handleDispatch(nil, domain.SendSessionMessageReq{}, nil)
	if err == nil {
		t.Fatal("expected dispatch to fail fast when aggregator is disabled")
	}
	if !strings.Contains(err.Error(), "is disabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyResolvedConfig_CarriesDisabled(t *testing.T) {
	a := &Actor{
		id:       systemAggregatorID,
		strategy: NewRoundRobinStrategy(),
	}
	a.applyResolvedConfig(domain.AIManagerAggregatorResolveResp{
		ID:       systemAggregatorID,
		Name:     "test",
		Disabled: true,
		Units: []domain.ManualCallableUnit{
			{Model: "gpt-4o", ProviderName: "openai", Endpoint: "https://openai", Protocol: "openai", Disabled: true},
			{Model: "claude-opus-4-7", ProviderName: "anthropic", Endpoint: "https://anthropic", Protocol: "anthropic"},
		},
	})

	a.mu.RLock()
	defer a.mu.RUnlock()
	if !a.disabled {
		t.Fatal("expected aggregator disabled flag to be set")
	}
	if len(a.units) != 2 {
		t.Fatalf("expected 2 units, got %d", len(a.units))
	}
	if !a.units[0].Disabled {
		t.Fatal("expected first unit to be disabled")
	}
	if a.units[1].Disabled {
		t.Fatal("expected second unit to not be disabled")
	}
}

func TestHandleStatus_ExposesDisabled(t *testing.T) {
	a := &Actor{
		id:           systemAggregatorID,
		name:         "status-agg",
		configLoaded: true,
		disabled:     true,
		units: []CallableUnit{
			{ID: "openai::gpt-4o", Model: "gpt-4o", ProviderName: "openai", Disabled: true},
			{ID: "anthropic::claude-opus-4-7", Model: "claude-opus-4-7", ProviderName: "anthropic"},
		},
	}

	resp, err := a.handleStatus(nil)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if !resp.Disabled {
		t.Fatal("expected status resp Disabled to be true")
	}
	if len(resp.Units) != 2 {
		t.Fatalf("expected 2 unit views, got %d", len(resp.Units))
	}
	if !resp.Units[0].Disabled {
		t.Fatal("expected first unit view Disabled to be true")
	}
	if resp.Units[1].Disabled {
		t.Fatal("expected second unit view Disabled to be false")
	}
}

// An aggregator-ref entry whose child pool is registered unavailable must
// project a concrete HealthReason ("availability") — an empty reason makes the
// frontend dropdown render the raw i18n key ("health.reason.").
func TestHandleStatus_AggRefUnavailableProjectsReason(t *testing.T) {
	t.Cleanup(func() { clearAggHealth("ref-child") })
	llmclient.RegisterAggregatorHealth("ref-child", llmclient.AggregatorHealthUnavailable)

	a := &Actor{
		id:           "parent-status",
		configLoaded: true,
		units: []CallableUnit{
			{ID: "agg:ref-child", AggregatorID: "ref-child"},
		},
		strategy: NewFallbackStrategy(),
	}

	resp, err := a.handleStatus(nil)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if len(resp.Units) != 1 {
		t.Fatalf("expected 1 unit view, got %d", len(resp.Units))
	}
	u := resp.Units[0]
	if u.HealthState != llmclient.HealthStateDisabled {
		t.Fatalf("HealthState = %q, want disabled", u.HealthState)
	}
	if u.HealthReason != llmclient.HealthReasonAvailability {
		t.Fatalf("HealthReason = %q, want %q", u.HealthReason, llmclient.HealthReasonAvailability)
	}
	if u.RecoveryMode != "cooldown" {
		t.Fatalf("RecoveryMode = %q, want cooldown", u.RecoveryMode)
	}
}
