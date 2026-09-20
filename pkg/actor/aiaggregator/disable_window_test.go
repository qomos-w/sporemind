package aiaggregator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func TestSelectUnit_AutoSkipsActiveDisableWindow(t *testing.T) {
	a := &Actor{
		id:       systemAggregatorID,
		strategy: NewFallbackStrategy(),
		units: []CallableUnit{
			{ID: "scheduled::m", Model: "m", ProviderName: "scheduled", DisableUntil: time.Now().Add(time.Minute).Unix()},
			{ID: "healthy::m", Model: "m", ProviderName: "healthy"},
		},
	}

	got, err := a.selectUnit(SelectRequest{Unit: domain.ModelUnit{}})
	if err != nil {
		t.Fatalf("selectUnit: %v", err)
	}
	if got.ID != "healthy::m" {
		t.Fatalf("selected %q, want healthy::m", got.ID)
	}
}

func TestSelectOnDemandUnit_RejectsActiveDisableWindow(t *testing.T) {
	a := &Actor{
		id:           systemAggregatorID,
		lifecycleCtx: context.Background(),
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_model": domain.AIManagerProviderResolveModelResp{
				Endpoint:     "https://api.example.com",
				Protocol:     "openai",
				DisableUntil: time.Now().Add(time.Minute).Unix(),
			},
		}},
	}

	_, err := a.selectOnDemandUnit(domain.ModelUnit{Provider: "scheduled", Model: "m"}, false)
	if err == nil {
		t.Fatal("expected active disable window to reject on-demand selection")
	}
	if !strings.Contains(err.Error(), "daily disable window") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleImageResolve_SkipsActiveDisableWindow(t *testing.T) {
	a := &Actor{
		configLoaded: true,
		lifecycleCtx: context.Background(),
		units: []CallableUnit{
			{ID: "scheduled::image", Model: "image-1", ProviderName: "scheduled", Modality: "image", DisableUntil: time.Now().Add(time.Minute).Unix()},
			{ID: "healthy::image", Model: "image-2", ProviderName: "healthy", Modality: "image"},
		},
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_token": domain.AIManagerProviderResolveTokenResp{AuthToken: "token"},
		}},
	}

	resp, err := a.handleImageResolve(nil, domain.AIAggregatorImageResolveReq{})
	if err != nil {
		t.Fatalf("handleImageResolve: %v", err)
	}
	if resp.ProviderName != "healthy" {
		t.Fatalf("selected provider %q, want healthy", resp.ProviderName)
	}
	if resp.AuthToken != "token" {
		t.Fatalf("selected token %q, want token", resp.AuthToken)
	}
}

func TestHandleImageResolve_RejectsWhenAllUnitsAreDisabled(t *testing.T) {
	a := &Actor{
		configLoaded: true,
		units: []CallableUnit{{
			ID: "scheduled::image", Model: "image-1", ProviderName: "scheduled", Modality: "image", DisableUntil: time.Now().Add(time.Minute).Unix(),
		}},
	}

	_, err := a.handleImageResolve(nil, domain.AIAggregatorImageResolveReq{})
	if err == nil {
		t.Fatal("expected no image unit when every image provider is disabled")
	}
}
