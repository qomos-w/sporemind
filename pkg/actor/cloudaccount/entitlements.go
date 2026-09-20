package cloudaccount

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// EntitlementsTTL is how long a cached entitlements snapshot is considered
// fresh. The background loop re-fetches every minute while the cloud is
// reachable, so this constant is the *offline grace period*: only when the
// cloud stays unreachable past it does the actor serve a free-tier fallback
// (core features still enabled). Short TTLs degrade on transient blips, so
// the grace period spans a week.
const EntitlementsTTL = 7 * 24 * time.Hour

// FreeTierEntitlements returns the degraded free-tier fallback used when the
// cache is stale or missing. Core features (content publish/download) remain
// fully enabled; only experimental/premium capabilities are turned off.
//
// This matches the cloud entitlements aggregator's featuresFor("free", false).
func FreeTierEntitlements() EntitlementsSnapshot {
	return EntitlementsSnapshot{
		Features: map[string]bool{
			"experimental_features": false,
			"extended_rate_limits":  false,
			"priority_support":      false,
			"content_publish":       true,
			"content_download":      true,
		},
		RateLimitTier:            "free",
		Tier:                     "free",
		Pass:                     gen.PassView{},
		IsExperimentalSubscriber: false,
		Credits:                  0,
	}
}

// ResolveEntitlements decides which entitlements to serve given a cache and the
// current time. It is a pure function (now is a parameter) so it can be unit
// tested without HTTP servers or actor assembly.
//
//   - Fresh cache (fetchedAt within ttl of now): return the cached snapshot,
//     degraded=false.
//   - Stale cache (past ttl) or missing cache: return the free-tier fallback,
//     degraded=true. Core features remain enabled so the runtime stays usable
//     offline.
func ResolveEntitlements(cached *EntitlementsSnapshot, fetchedAt time.Time, now time.Time, ttl time.Duration) (EntitlementsSnapshot, bool) {
	if cached != nil && !fetchedAt.IsZero() && now.Sub(fetchedAt) <= ttl {
		return *cached, false
	}
	return FreeTierEntitlements(), true
}

// entitlementsFetcher abstracts the cloud /entitlements GET so the actor can
// swap in a fake in tests. The real implementation is httpClient.fetchEntitlements.
type entitlementsFetcher interface {
	Fetch(ctx context.Context, accessToken string) (EntitlementsSnapshot, error)
}

// cloudHTTPClient wraps the cloud base URL and a reusable *http.Client.
type cloudHTTPClient struct {
	baseURL string
	client  *http.Client
}

// Fetch calls GET {baseURL}/entitlements with the bearer access token and
// decodes the cloud Snapshot into the local EntitlementsSnapshot. The shapes
// are identical (features/rate_limit_tier/is_experimental_subscriber/credits).
func (c *cloudHTTPClient) Fetch(ctx context.Context, accessToken string) (EntitlementsSnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/entitlements", nil)
	if err != nil {
		return EntitlementsSnapshot{}, fmt.Errorf("cloudaccount: build entitlements request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return EntitlementsSnapshot{}, fmt.Errorf("cloudaccount: entitlements request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return EntitlementsSnapshot{}, fmt.Errorf("cloudaccount: entitlements: status %d: %s", resp.StatusCode, string(body))
	}

	var snap EntitlementsSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return EntitlementsSnapshot{}, fmt.Errorf("cloudaccount: decode entitlements: %w", err)
	}
	return snap, nil
}
