package cloudaccount

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// --- fakes implementing the unexported fetcher/refresher interfaces ---

type fakeRefresher struct {
	calledWith string
	result     RefreshedTokens
	err        error
}

func (f *fakeRefresher) Refresh(_ context.Context, refreshToken string) (RefreshedTokens, error) {
	f.calledWith = refreshToken
	return f.result, f.err
}

type fakeFetcher struct {
	calledWith string
	result     EntitlementsSnapshot
	err        error
}

func (f *fakeFetcher) Fetch(_ context.Context, accessToken string) (EntitlementsSnapshot, error) {
	f.calledWith = accessToken
	return f.result, f.err
}

func TestSyncOnce_NotLinked(t *testing.T) {
	a := &Actor{state: CloudAccountState{}}
	ref := &fakeRefresher{}
	fch := &fakeFetcher{}
	a.refresher = ref
	a.fetcher = fch

	a.syncOnce(context.Background())

	if ref.calledWith != "" || fch.calledWith != "" {
		t.Fatal("syncOnce must not call cloud when not linked")
	}
}

func TestSyncOnce_RefreshThenFetch(t *testing.T) {
	now := time.Now()
	a := &Actor{
		state: CloudAccountState{
			AccountID:      "acct-1",
			AccessToken:    "old-access",
			RefreshToken:   "old-refresh",
			TokenExpiresAt: now.Add(30 * time.Second), // within 2m skew → triggers refresh
		},
	}
	ref := &fakeRefresher{result: RefreshedTokens{
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
		ExpiresAt:    now.Add(15 * time.Minute),
	}}
	wantSnap := EntitlementsSnapshot{
		RateLimitTier:            "experimental",
		IsExperimentalSubscriber: true,
		Credits:                  42,
		Features:                 map[string]bool{"experimental_features": true},
	}
	fch := &fakeFetcher{result: wantSnap}
	a.refresher = ref
	a.fetcher = fch

	a.syncOnce(context.Background())

	if ref.calledWith != "old-refresh" {
		t.Fatalf("refresher called with %q, want old-refresh", ref.calledWith)
	}
	if fch.calledWith != "new-access" {
		t.Fatalf("fetcher called with %q, want new-access (post-refresh token)", fch.calledWith)
	}

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.state.AccessToken != "new-access" || a.state.RefreshToken != "new-refresh" {
		t.Fatalf("tokens not rotated: %+v", a.state)
	}
	if a.state.Entitlements == nil || a.state.Entitlements.Credits != 42 {
		t.Fatalf("entitlements not cached: %+v", a.state.Entitlements)
	}
	if a.state.EntitlementsFetchedAt.IsZero() {
		t.Fatal("EntitlementsFetchedAt must be set after a successful fetch")
	}
}

func TestSyncOnce_NoRefreshNeeded_FetchesWithCurrentToken(t *testing.T) {
	now := time.Now()
	a := &Actor{
		state: CloudAccountState{
			AccountID:      "acct-1",
			AccessToken:    "fresh-access",
			RefreshToken:   "fresh-refresh",
			TokenExpiresAt: now.Add(1 * time.Hour), // far from expiry → no refresh
		},
	}
	ref := &fakeRefresher{}
	fch := &fakeFetcher{result: EntitlementsSnapshot{RateLimitTier: "free"}}
	a.refresher = ref
	a.fetcher = fch

	a.syncOnce(context.Background())

	if ref.calledWith != "" {
		t.Fatal("refresh must not happen when token is far from expiry")
	}
	if fch.calledWith != "fresh-access" {
		t.Fatalf("fetcher called with %q, want fresh-access", fch.calledWith)
	}
}

func TestSyncOnce_RefreshFailureStillFetchesWithOldToken(t *testing.T) {
	now := time.Now()
	a := &Actor{
		state: CloudAccountState{
			AccountID:      "acct-1",
			AccessToken:    "old-access",
			RefreshToken:   "old-refresh",
			TokenExpiresAt: now.Add(30 * time.Second), // within skew → tries refresh
		},
	}
	ref := &fakeRefresher{err: errors.New("cloud unavailable")}
	fch := &fakeFetcher{result: EntitlementsSnapshot{RateLimitTier: "free"}}
	a.refresher = ref
	a.fetcher = fch

	a.syncOnce(context.Background())

	if ref.calledWith == "" {
		t.Fatal("refresh should have been attempted")
	}
	// Despite refresh failure, fetch proceeds with the old token.
	if fch.calledWith != "old-access" {
		t.Fatalf("fetcher called with %q, want old-access", fch.calledWith)
	}
	a.mu.RLock()
	tok := a.state.AccessToken
	a.mu.RUnlock()
	if tok != "old-access" {
		t.Fatalf("old token must remain after refresh failure, got %q", tok)
	}
}

func TestSyncOnce_FetchFailureLeavesCacheIntact(t *testing.T) {
	now := time.Now()
	existing := &EntitlementsSnapshot{RateLimitTier: "experimental", Credits: 9}
	a := &Actor{
		state: CloudAccountState{
			AccountID:             "acct-1",
			AccessToken:           "fresh-access",
			RefreshToken:          "fresh-refresh",
			TokenExpiresAt:        now.Add(1 * time.Hour),
			Entitlements:          existing,
			EntitlementsFetchedAt: now.Add(-30 * time.Second),
		},
	}
	ref := &fakeRefresher{}
	fch := &fakeFetcher{err: errors.New("network down")}
	a.refresher = ref
	a.fetcher = fch

	a.syncOnce(context.Background())

	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.state.Entitlements != existing {
		t.Fatal("cached entitlements must be preserved when fetch fails")
	}
	if a.state.Entitlements.Credits != 9 {
		t.Fatalf("cached credits altered: %d", a.state.Entitlements.Credits)
	}
}

func TestStatusSnapshot_DegradedReturnsFreeTier(t *testing.T) {
	now := time.Now()
	a := &Actor{
		state: CloudAccountState{
			AccountID:      "acct-1",
			AccessToken:    "token",
			DisplayName:    "Test User",
			AvatarURL:      "https://example.com/avatar.png",
			TokenExpiresAt: now.Add(1 * time.Hour),
			// No entitlements → degraded → free-tier fallback.
		},
	}
	st := a.statusSnapshot()
	if !st.Linked {
		t.Fatal("expected linked")
	}
	if st.EntitlementsDegraded != true {
		t.Fatal("expected degraded when no entitlements cached")
	}
	if st.Tier != "free" {
		t.Fatalf("Tier = %q, want %q (free-tier fallback when degraded)", st.Tier, "free")
	}
	if st.AccountID != "acct-1" || st.DisplayName != "Test User" || st.AvatarURL != "https://example.com/avatar.png" {
		t.Fatal("status fields not preserved")
	}
}

func TestStatusSnapshot_StaleEntitlementsDegradedReturnsFreeTier(t *testing.T) {
	now := time.Now()
	a := &Actor{
		state: CloudAccountState{
			AccountID:      "acct-1",
			AccessToken:    "token",
			TokenExpiresAt: now.Add(1 * time.Hour),
			Entitlements: &EntitlementsSnapshot{
				RateLimitTier: "experimental",
				Tier:          "ultimate",
				Pass:          gen.PassView{Type: "annual"},
				Credits:       100,
			},
			EntitlementsFetchedAt: now.Add(-2 * EntitlementsTTL), // stale
		},
	}
	st := a.statusSnapshot()
	if st.EntitlementsDegraded != true {
		t.Fatal("expected degraded when cache is stale")
	}
	if st.Tier != "free" {
		t.Fatalf("Tier = %q, want %q (free-tier fallback when degraded)", st.Tier, "free")
	}
	// RateLimitTier still reads from snapshot (existing behavior unchanged).
	if st.RateLimitTier != "experimental" {
		t.Fatalf("RateLimitTier = %q, want %q", st.RateLimitTier, "experimental")
	}
}

func TestStatusSnapshot_FreshEntitlementsPassesTier(t *testing.T) {
	now := time.Now()
	a := &Actor{
		state: CloudAccountState{
			AccountID:      "acct-1",
			AccessToken:    "token",
			TokenExpiresAt: now.Add(1 * time.Hour),
			Entitlements: &EntitlementsSnapshot{
				RateLimitTier: "pro",
				Tier:          "pro",
				Pass:          gen.PassView{Type: "monthly"},
				Credits:       50,
			},
			EntitlementsFetchedAt: now.Add(-time.Minute), // fresh
		},
	}
	st := a.statusSnapshot()
	if st.EntitlementsDegraded != false {
		t.Fatal("expected not degraded for fresh cache")
	}
	if st.Tier != "pro" {
		t.Fatalf("Tier = %q, want %q", st.Tier, "pro")
	}
}

func TestHandleGetEntitlements_DegradedReturnsFreeTier(t *testing.T) {
	now := time.Now()
	a := &Actor{
		state: CloudAccountState{
			AccountID:      "acct-1",
			AccessToken:    "token",
			TokenExpiresAt: now.Add(1 * time.Hour),
			// No entitlements → degraded.
		},
	}
	resp, err := a.handleGetEntitlements(nil)
	if err != nil {
		t.Fatalf("handleGetEntitlements: %v", err)
	}
	if !resp.Degraded {
		t.Fatal("expected degraded when no entitlements cached")
	}
	if resp.Entitlements.Tier != "free" {
		t.Fatalf("Entitlements.Tier = %q, want %q", resp.Entitlements.Tier, "free")
	}
	if resp.Entitlements.Pass.Type != "" {
		t.Fatalf("Entitlements.Pass.Type = %q, want empty zero value", resp.Entitlements.Pass.Type)
	}
}

func TestHandleSync_RequiresLinked(t *testing.T) {
	a := &Actor{fetcher: &fakeEntitlementsFetcher{}, refresher: &fakeRefresher{}}
	_, err := a.handleSync(stubPureContext{})
	if err == nil || !strings.Contains(err.Error(), "no cloud account linked") {
		t.Fatalf("expected not-linked error, got: %v", err)
	}
}

func TestHandleSync_RefreshesEntitlementsAndClearsDegraded(t *testing.T) {
	now := time.Now()
	fetcher := &fakeEntitlementsFetcher{snapshot: EntitlementsSnapshot{
		Tier:          "pro",
		RateLimitTier: "pro",
		Pass:          gen.PassView{Type: "monthly"},
		Credits:       50,
	}}
	a := &Actor{
		state: CloudAccountState{
			AccountID:      "acct-1",
			AccessToken:    "token",
			RefreshToken:   "refresh",
			TokenExpiresAt: now.Add(1 * time.Hour), // far from expiry → no token refresh
			// No entitlements → degraded before sync.
		},
		refresher: &fakeRefresher{},
		fetcher:   fetcher,
	}

	if st := a.statusSnapshot(); !st.EntitlementsDegraded {
		t.Fatal("precondition: expected degraded before sync")
	}

	st, err := a.handleSync(stubPureContext{})
	if err != nil {
		t.Fatalf("handleSync: %v", err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("expected 1 entitlements fetch, got %d", fetcher.calls)
	}
	if st.EntitlementsDegraded {
		t.Fatal("expected sync to clear degraded state")
	}
	if st.Tier != "pro" {
		t.Fatalf("Tier = %q, want pro", st.Tier)
	}
}

func TestHandleSync_FetchFailureKeepsDegraded(t *testing.T) {
	now := time.Now()
	fetcher := &fakeEntitlementsFetcher{err: errors.New("network down")}
	a := &Actor{
		state: CloudAccountState{
			AccountID:      "acct-1",
			AccessToken:    "token",
			RefreshToken:   "refresh",
			TokenExpiresAt: now.Add(1 * time.Hour),
		},
		refresher: &fakeRefresher{},
		fetcher:   fetcher,
	}

	st, err := a.handleSync(stubPureContext{})
	if err != nil {
		t.Fatalf("handleSync must not fail on a cloud error: %v", err)
	}
	if !st.EntitlementsDegraded {
		t.Fatal("expected degraded to persist when fetch fails")
	}
}
