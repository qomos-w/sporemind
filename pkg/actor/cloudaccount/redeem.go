package cloudaccount

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// cdkeyRedeemResult mirrors the cloud POST /cdkeys/redeem success payload
// (cdkey.Result in sporecloud): product_slug, product_name, period_days,
// pass_starts_at, pass_ends_at.
type cdkeyRedeemResult struct {
	ProductSlug string    `json:"product_slug"`
	ProductName string    `json:"product_name"`
	PeriodDays  int32     `json:"period_days"`
	PassStarts  time.Time `json:"pass_starts_at"`
	PassEnds    time.Time `json:"pass_ends_at"`
}

// cdkeyRedeemer abstracts the cloud POST /cdkeys/redeem so the actor can swap
// in a fake in tests. The real implementation is httpClient.redeemCdkey.
type cdkeyRedeemer interface {
	Redeem(ctx context.Context, accessToken, code string) (cdkeyRedeemResult, error)
}

// Redeem posts the code to the cloud with the bearer access token and decodes
// the result. Cloud error codes (cdkey_not_found / cdkey_disabled /
// cdkey_expired / already_redeemed / no_gain) arrive as 4xx with a JSON body
// like {"error":{"code":"...","message":"..."}} or plain text; both shapes are
// preserved in the returned error so the UI can surface them.
func (c *cloudHTTPClient) Redeem(ctx context.Context, accessToken, code string) (cdkeyRedeemResult, error) {
	body, err := json.Marshal(map[string]string{"code": code})
	if err != nil {
		return cdkeyRedeemResult{}, fmt.Errorf("cloudaccount: encode redeem request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/cdkeys/redeem", strings.NewReader(string(body)))
	if err != nil {
		return cdkeyRedeemResult{}, fmt.Errorf("cloudaccount: build redeem request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return cdkeyRedeemResult{}, fmt.Errorf("cloudaccount: redeem request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode != http.StatusOK {
		return cdkeyRedeemResult{}, fmt.Errorf("cloudaccount: redeem: status %d: %s", resp.StatusCode, snippet(string(raw)))
	}

	var result cdkeyRedeemResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return cdkeyRedeemResult{}, fmt.Errorf("cloudaccount: decode redeem result: %w", err)
	}
	return result, nil
}

// snippet truncates a potentially long payload for error strings, following
// the respSnippet logging-truncation convention.
func snippet(s string) string {
	if len(s) > 512 {
		return s[:512] + "...(truncated)"
	}
	return s
}

// handleRedeem redeems a CDKey for the linked cloud account. The cloud grants
// the account a tier pass window; the actor then re-syncs entitlements so the
// new tier is immediately visible in the UI (no TTL wait).
func (a *Actor) handleRedeem(ctx actor.PureContext, req gen.CdkeyRedeemReq) (gen.CdkeyRedeemResp, error) {
	req.Code = strings.TrimSpace(req.Code)
	if req.Code == "" {
		return gen.CdkeyRedeemResp{}, fmt.Errorf("cloudaccount.redeem: code is required")
	}

	a.mu.RLock()
	accessToken := a.state.AccessToken
	linked := a.state.Linked()
	a.mu.RUnlock()
	if !linked {
		return gen.CdkeyRedeemResp{}, fmt.Errorf("cloudaccount.redeem: no cloud account linked")
	}

	result, err := a.redeemer.Redeem(ctx.Lifecycle(), accessToken, req.Code)
	if err != nil {
		return gen.CdkeyRedeemResp{}, fmt.Errorf("cloudaccount.redeem: %w", err)
	}
	ctx.Logger().Info("cloudaccount: cdkey redeemed", "product", result.ProductName, "period_days", result.PeriodDays)

	// Best-effort immediate entitlements re-sync: the redeem granted a pass
	// window, so the cached snapshot is stale by definition. A failure here
	// does not fail the redeem itself — the background loop will retry.
	a.syncOnce(context.Background())

	return gen.CdkeyRedeemResp{
		ProductName: result.ProductName,
		PeriodDays:  int64(result.PeriodDays),
		StartsAt:    result.PassStarts.UTC().Format(time.RFC3339),
		EndsAt:      result.PassEnds.UTC().Format(time.RFC3339),
	}, nil
}