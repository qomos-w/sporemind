package cloudaccount

import (
	"context"
	"strings"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ---------------------------------------------------------------------------
// handleRedeem tests
// ---------------------------------------------------------------------------

type fakeRedeemer struct {
	calledWithCode string
	calledWithTok  string
	result         cdkeyRedeemResult
	err            error
}

func (f *fakeRedeemer) Redeem(_ context.Context, accessToken, code string) (cdkeyRedeemResult, error) {
	f.calledWithTok = accessToken
	f.calledWithCode = code
	return f.result, f.err
}

type fakeEntitlementsFetcher struct {
	snapshot EntitlementsSnapshot
	err      error
	calls    int
}

func (f *fakeEntitlementsFetcher) Fetch(_ context.Context, _ string) (EntitlementsSnapshot, error) {
	f.calls++
	if f.err != nil {
		return EntitlementsSnapshot{}, f.err
	}
	return f.snapshot, nil
}

func TestHandleRedeem_RequiresCode(t *testing.T) {
	a := &Actor{redeemer: &fakeRedeemer{}}
	_, err := a.handleRedeem(stubPureContext{}, gen.CdkeyRedeemReq{Code: "   "})
	if err == nil || !strings.Contains(err.Error(), "code is required") {
		t.Fatalf("expected code-required error, got: %v", err)
	}
}

func TestHandleRedeem_RequiresLinked(t *testing.T) {
	a := &Actor{redeemer: &fakeRedeemer{}}
	_, err := a.handleRedeem(stubPureContext{}, gen.CdkeyRedeemReq{Code: "SPX-AAAAA-BBBBB-CCCCC"})
	if err == nil || !strings.Contains(err.Error(), "no cloud account linked") {
		t.Fatalf("expected not-linked error, got: %v", err)
	}
}

func TestHandleRedeem_Success_ReturnsResult(t *testing.T) {
	redeemer := &fakeRedeemer{
		result: cdkeyRedeemResult{
			ProductSlug: "insider-30d",
			ProductName: "Insider 30 天",
			PeriodDays:  30,
			PassStarts:  time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			PassEnds:    time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	a := &Actor{
		state:    CloudAccountState{AccountID: "acct-1", AccessToken: "token"},
		redeemer: redeemer,
		// syncOnce runs after a successful redeem; a fetcher is wired so the
		// refresh path completes instead of panicking on a nil interface.
		fetcher: &fakeEntitlementsFetcher{},
	}

	resp, err := a.handleRedeem(stubPureContext{}, gen.CdkeyRedeemReq{Code: " SPX-AAAAA-BBBBB-CCCCC "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ProductName != "Insider 30 天" {
		t.Fatalf("ProductName = %q", resp.ProductName)
	}
	if resp.PeriodDays != 30 {
		t.Fatalf("PeriodDays = %d, want 30", resp.PeriodDays)
	}
	if resp.StartsAt != "2026-09-01T00:00:00Z" {
		t.Fatalf("StartsAt = %q", resp.StartsAt)
	}
	if resp.EndsAt != "2026-10-01T00:00:00Z" {
		t.Fatalf("EndsAt = %q", resp.EndsAt)
	}
	if redeemer.calledWithCode != "SPX-AAAAA-BBBBB-CCCCC" {
		t.Fatalf("code not trimmed before call: %q", redeemer.calledWithCode)
	}
	if redeemer.calledWithTok != "token" {
		t.Fatalf("access token not passed: %q", redeemer.calledWithTok)
	}
}

func TestHandleRedeem_CloudError_Bubbles(t *testing.T) {
	redeemer := &fakeRedeemer{err: context.DeadlineExceeded}
	a := &Actor{
		state:    CloudAccountState{AccountID: "acct-1", AccessToken: "token"},
		redeemer: redeemer,
	}
	_, err := a.handleRedeem(stubPureContext{}, gen.CdkeyRedeemReq{Code: "SPX-AAAAA-BBBBB-CCCCC"})
	if err == nil || !strings.Contains(err.Error(), "cloudaccount.redeem") {
		t.Fatalf("expected bubbled cloud error, got: %v", err)
	}
}

func TestHandleRedeem_Success_RefreshesEntitlements(t *testing.T) {
	redeemer := &fakeRedeemer{
		result: cdkeyRedeemResult{ProductName: "Insider", PeriodDays: 30, PassEnds: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
	}
	fetcher := &fakeEntitlementsFetcher{snapshot: EntitlementsSnapshot{Tier: "pro"}}
	a := &Actor{
		state:    CloudAccountState{AccountID: "acct-1", AccessToken: "token"},
		redeemer: redeemer,
		fetcher:  fetcher,
	}

	if _, err := a.handleRedeem(stubPureContext{}, gen.CdkeyRedeemReq{Code: "SPX-AAAAA-BBBBB-CCCCC"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("expected 1 entitlements refresh after redeem, got %d", fetcher.calls)
	}
}