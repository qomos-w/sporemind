package cloudaccount

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RefreshSkew is how far before expiry the actor proactively refreshes the
// access token, so callers never see an expired token. A token within this
// window of expiry (or already past it) triggers an unattended refresh.
const RefreshSkew = 2 * time.Minute

// RefreshedTokens is the result of a successful refresh: the new token pair
// and the absolute expiry of the new access token.
type RefreshedTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    time.Time
}

// ShouldRefreshToken reports whether the access token should be refreshed now.
// It is a pure function (now is a parameter) so refresh timing can be unit
// tested without actor assembly.
//
// Returns true when the token is within RefreshSkew of its expiry (or already
// expired). A zero expiry (never linked) returns false — refresh is only
// meaningful for a linked account with a known expiry.
func ShouldRefreshToken(expiresAt time.Time, now time.Time, skew time.Duration) bool {
	if expiresAt.IsZero() {
		return false
	}
	return now.Add(skew).After(expiresAt)
}

// tokenRefresher abstracts the cloud /auth/token/refresh POST so the actor can
// swap in a fake in tests. The real implementation is
// httpClient.RefreshToken.
type tokenRefresher interface {
	Refresh(ctx context.Context, refreshToken string) (RefreshedTokens, error)
}

// refreshResponse mirrors the cloud TokenPair JSON returned by
// POST /auth/token/refresh.
type refreshResponse struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresIn    int64     `json:"expires_in"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// RefreshToken calls POST {baseURL}/auth/token/refresh with the current refresh
// token and returns the rotated pair. The cloud invalidates the old refresh
// token (rotation), so the caller must persist the new RefreshToken.
func (c *cloudHTTPClient) Refresh(ctx context.Context, refreshToken string) (RefreshedTokens, error) {
	body, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return RefreshedTokens{}, fmt.Errorf("cloudaccount: encode refresh request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/auth/token/refresh", bytes.NewReader(body))
	if err != nil {
		return RefreshedTokens{}, fmt.Errorf("cloudaccount: build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return RefreshedTokens{}, fmt.Errorf("cloudaccount: refresh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return RefreshedTokens{}, fmt.Errorf("cloudaccount: refresh: status %d: %s", resp.StatusCode, string(raw))
	}

	var pair refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&pair); err != nil {
		return RefreshedTokens{}, fmt.Errorf("cloudaccount: decode refresh response: %w", err)
	}
	return RefreshedTokens{
		AccessToken:  pair.AccessToken,
		RefreshToken: pair.RefreshToken,
		ExpiresAt:    pair.ExpiresAt,
	}, nil
}
