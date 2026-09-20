package aiaggregator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestHandleVideoResolve_SkipsNonVideoAndDisabledUnits locks the video resolve
// selection contract: only Modality "video" units are candidates, disabled and
// disable-windowed units are skipped, and the first healthy video unit wins.
func TestHandleVideoResolve_SkipsNonVideoAndDisabledUnits(t *testing.T) {
	a := &Actor{
		configLoaded: true,
		lifecycleCtx: context.Background(),
		units: []CallableUnit{
			{ID: "chat::model", Model: "gpt-5", ProviderName: "chatp", Modality: "chat"},
			{ID: "img::model", Model: "gpt-image-2", ProviderName: "imgp", Modality: "image"},
			{ID: "disabled::video", Model: "veo-3", ProviderName: "dis", Modality: "video", Disabled: true},
			{ID: "scheduled::video", Model: "seedance-2", ProviderName: "sched", Modality: "video", DisableUntil: time.Now().Add(time.Minute).Unix()},
			{ID: "healthy::video", Model: "veo-3.1", ProviderName: "google", Modality: "video"},
		},
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_token": domain.AIManagerProviderResolveTokenResp{AuthToken: "video-token"},
		}},
	}

	resp, err := a.handleVideoResolve(nil, domain.AIAggregatorVideoResolveReq{})
	if err != nil {
		t.Fatalf("handleVideoResolve: %v", err)
	}
	if resp.ProviderName != "google" || resp.Model != "veo-3.1" {
		t.Fatalf("selected %+v, want google/veo-3.1", resp)
	}
	if resp.AuthToken != "video-token" {
		t.Fatalf("token = %q, want video-token", resp.AuthToken)
	}
}

// TestHandleVideoResolve_ExplicitProviderModelFilter locks that Provider/Model
// request filters narrow the candidate set the same way as image resolve.
func TestHandleVideoResolve_ExplicitProviderModelFilter(t *testing.T) {
	a := &Actor{
		configLoaded: true,
		lifecycleCtx: context.Background(),
		units: []CallableUnit{
			{ID: "ark::seedance", Model: "dreamina-seedance-2-0-260128", ProviderName: "volc", Modality: "video", Protocol: "ark"},
			{ID: "gemini::veo", Model: "veo-3.1", ProviderName: "google", Modality: "video", Protocol: "gemini"},
		},
		aimanagerRef: &fakeRef{results: map[string]any{
			"aimanager.provider_resolve_token": domain.AIManagerProviderResolveTokenResp{AuthToken: "ark-token"},
		}},
	}

	resp, err := a.handleVideoResolve(nil, domain.AIAggregatorVideoResolveReq{Provider: "volc", Model: "dreamina-seedance-2-0-260128"})
	if err != nil {
		t.Fatalf("handleVideoResolve: %v", err)
	}
	if resp.ProviderName != "volc" || resp.Model != "dreamina-seedance-2-0-260128" || resp.Protocol != "ark" {
		t.Fatalf("selected %+v, want volc/dreamina-seedance/ark", resp)
	}
}

// TestHandleVideoResolve_NoVideoUnits locks the error when no Modality "video"
// unit exists at all.
func TestHandleVideoResolve_NoVideoUnits(t *testing.T) {
	a := &Actor{
		configLoaded: true,
		lifecycleCtx: context.Background(),
		units: []CallableUnit{
			{ID: "chat::model", Model: "gpt-5", ProviderName: "chatp", Modality: "chat"},
		},
	}

	_, err := a.handleVideoResolve(nil, domain.AIAggregatorVideoResolveReq{})
	if err == nil || !strings.Contains(err.Error(), "no video-generation units available") {
		t.Fatalf("unexpected error: %v", err)
	}
}