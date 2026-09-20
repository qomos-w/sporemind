package aiaggregator

import (
	"context"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestHandleImageResolve_Diagnostics locks the resolve-error contract: a
// pinned (provider, model) reports WHY nothing matched — unknown pair, wrong
// modality, or a disabled/cooling unit — instead of one generic message.
func TestHandleImageResolve_Diagnostics(t *testing.T) {
	base := func() *Actor {
		return &Actor{
			configLoaded: true,
			lifecycleCtx: context.Background(),
			units: []CallableUnit{
				{ID: "chatp::gpt-5", Model: "gpt-5", ProviderName: "chatp", Modality: "chat"},
				{ID: "relayp::nano", Model: "gemini-2.5-flash-image", ProviderName: "relayp", Modality: "image", Disabled: true},
				{ID: "imgp::gpt-image-2", Model: "gpt-image-2", ProviderName: "imgp", Modality: "image"},
			},
		}
	}
	cases := []struct {
		name    string
		req     domain.AIAggregatorImageResolveReq
		wantSub string
	}{
		{
			"pinned pair matches nothing",
			domain.AIAggregatorImageResolveReq{Provider: "gone", Model: "whatever"},
			`no unit matches provider "gone" model "whatever"`,
		},
		{
			"pinned model has wrong modality",
			domain.AIAggregatorImageResolveReq{Provider: "chatp", Model: "gpt-5"},
			`model "gpt-5" on provider "chatp" is a "chat" model, not image-generation`,
		},
		{
			"pinned unit is disabled",
			domain.AIAggregatorImageResolveReq{Provider: "relayp", Model: "gemini-2.5-flash-image"},
			"image-generation unit relayp/gemini-2.5-flash-image is disabled or cooling down",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := base().handleImageResolve(nil, tc.req)
			if err == nil {
				t.Fatal("expected resolve error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestHandleVideoResolve_Diagnostics mirrors the image diagnostics for the
// video resolve path.
func TestHandleVideoResolve_Diagnostics(t *testing.T) {
	a := &Actor{
		configLoaded: true,
		lifecycleCtx: context.Background(),
		units: []CallableUnit{
			{ID: "imgp::model", Model: "gpt-image-2", ProviderName: "imgp", Modality: "image"},
		},
	}
	_, err := a.handleVideoResolve(nil, domain.AIAggregatorVideoResolveReq{Provider: "imgp", Model: "gpt-image-2"})
	if err == nil || !strings.Contains(err.Error(), `is a "image" model, not video-generation`) {
		t.Fatalf("unexpected error: %v", err)
	}
}
