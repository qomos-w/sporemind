package cloudaccount

import (
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestFreeTierEntitlements(t *testing.T) {
	snap := FreeTierEntitlements()
	if snap.RateLimitTier != "free" {
		t.Fatalf("RateLimitTier = %q, want %q", snap.RateLimitTier, "free")
	}
	if snap.Tier != "free" {
		t.Fatalf("Tier = %q, want %q", snap.Tier, "free")
	}
	if snap.Pass.Type != "" {
		t.Fatalf("Pass.Type = %q, want empty zero value", snap.Pass.Type)
	}
	if snap.IsExperimentalSubscriber {
		t.Fatal("IsExperimentalSubscriber should be false for free tier")
	}
	if snap.Credits != 0 {
		t.Fatalf("Credits = %d, want 0", snap.Credits)
	}
	// Core features must remain enabled in the degraded fallback.
	if !snap.Features["content_publish"] || !snap.Features["content_download"] {
		t.Fatalf("core features must be enabled in free tier, got %v", snap.Features)
	}
	// Experimental/premium features disabled.
	if snap.Features["experimental_features"] || snap.Features["extended_rate_limits"] {
		t.Fatalf("experimental features must be disabled in free tier, got %v", snap.Features)
	}
}

func TestResolveEntitlements(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	ttl := EntitlementsTTL

	cached := &EntitlementsSnapshot{
		Features:                 map[string]bool{"experimental_features": true},
		RateLimitTier:            "experimental",
		Tier:                     "ultimate",
		Pass:                     gen.PassView{Type: "lifetime"},
		IsExperimentalSubscriber: true,
		Credits:                  100,
	}

	tests := []struct {
		name          string
		cached        *EntitlementsSnapshot
		fetchedAt     time.Time
		wantRateTier  string
		wantTier      string
		wantPassType  string
		wantDeg       bool
	}{
		{"fresh cache served", cached, now.Add(-ttl / 2), "experimental", "ultimate", "lifetime", false},
		{"cache at exact TTL boundary still fresh", cached, now.Add(-ttl), "experimental", "ultimate", "lifetime", false},
		{"stale cache degrades to free", cached, now.Add(-ttl - time.Second), "free", "free", "", true},
		{"nil cache degrades to free", nil, time.Time{}, "free", "free", "", true},
		{"non-nil cache but zero fetchedAt degrades", cached, time.Time{}, "free", "free", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, degraded := ResolveEntitlements(tt.cached, tt.fetchedAt, now, ttl)
			if got.RateLimitTier != tt.wantRateTier {
				t.Fatalf("RateLimitTier = %q, want %q", got.RateLimitTier, tt.wantRateTier)
			}
			if got.Tier != tt.wantTier {
				t.Fatalf("Tier = %q, want %q", got.Tier, tt.wantTier)
			}
			if got.Pass.Type != tt.wantPassType {
				t.Fatalf("Pass.Type = %q, want %q", got.Pass.Type, tt.wantPassType)
			}
			if degraded != tt.wantDeg {
				t.Fatalf("degraded = %v, want %v", degraded, tt.wantDeg)
			}
		})
	}
}

func TestResolveEntitlements_FreshCacheCopied(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	cached := &EntitlementsSnapshot{
		Features:      map[string]bool{"x": true},
		RateLimitTier: "experimental",
		Credits:       7,
	}
	got, degraded := ResolveEntitlements(cached, now.Add(-time.Second), now, EntitlementsTTL)
	if degraded {
		t.Fatal("fresh cache should not be degraded")
	}
	if got.RateLimitTier != "experimental" || got.Credits != 7 || !got.Features["x"] {
		t.Fatalf("fresh snapshot not preserved: %+v", got)
	}
}

func TestToEntitlementsView(t *testing.T) {
	src := EntitlementsSnapshot{
		Features:                 map[string]bool{"a": true, "b": false},
		RateLimitTier:            "experimental",
		Tier:                     "pro",
		Pass:                     gen.PassView{Type: "monthly", StartsAt: "2026-01-01T00:00:00Z", EndsAt: "2026-02-01T00:00:00Z"},
		IsExperimentalSubscriber: true,
		Credits:                  5,
	}
	view := toEntitlementsView(src)
	if view.RateLimitTier != "experimental" || view.Credits != 5 || !view.IsExperimentalSubscriber {
		t.Fatalf("projection mismatch: %+v", view)
	}
	if view.Tier != "pro" {
		t.Fatalf("Tier = %q, want %q", view.Tier, "pro")
	}
	if view.Pass.Type != "monthly" || view.Pass.StartsAt != "2026-01-01T00:00:00Z" || view.Pass.EndsAt != "2026-02-01T00:00:00Z" {
		t.Fatalf("Pass not copied: %+v", view.Pass)
	}
	if !view.Features["a"] || view.Features["b"] {
		t.Fatalf("features not copied: %+v", view.Features)
	}
	// Mutating the view must not affect the source (defensive copy).
	view.Features["a"] = false
	if !src.Features["a"] {
		t.Fatal("toEntitlementsView shared the underlying map")
	}
}

func TestCloudAccountState_Linked(t *testing.T) {
	if (CloudAccountState{}).Linked() {
		t.Fatal("empty state must not be linked")
	}
	if !(CloudAccountState{AccountID: "a", AccessToken: "t"}).Linked() {
		t.Fatal("state with id+token must be linked")
	}
	if (CloudAccountState{AccountID: "a"}).Linked() {
		t.Fatal("state without access token must not be linked")
	}
}
