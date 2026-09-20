package cloudaccount

import (
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// CloudAccountState is the persisted state of the cloud-account actor. It
// stores the sporemind cloud OAuth tokens and a cached entitlements snapshot.
// Serialized via persist.Persist (state.json); the JSON tags follow the spec
// (账户体系实施计划 §2.3) field names so the on-disk format is stable.
//
// This is internal actor state, NOT a protocol type: the time.Time fields are
// not wire-compatible and never cross the callable boundary.
type CloudAccountState struct {
	AccountID             string                `json:"account_id"`
	AccessToken           string                `json:"access_token"`
	RefreshToken          string                `json:"refresh_token"`
	TokenExpiresAt        time.Time             `json:"token_expires_at"`
	DisplayName           string                `json:"display_name"`
	AvatarURL             string                `json:"avatar_url"`
	Entitlements          *EntitlementsSnapshot `json:"entitlements"`
	EntitlementsFetchedAt time.Time             `json:"entitlements_fetched_at"`
	LinkedAt              time.Time             `json:"linked_at"`
}

// Linked reports whether a cloud account is currently linked (has a non-empty
// account ID and access token).
func (s CloudAccountState) Linked() bool {
	return s.AccountID != "" && s.AccessToken != ""
}

// EntitlementsSnapshot mirrors the cloud /entitlements payload. It is the
// cached local copy; on TTL-expiry the actor degrades to a free-tier
// fallback (see FreeTierEntitlements) whose core features remain enabled.
type EntitlementsSnapshot struct {
	Features                 map[string]bool `json:"features"`
	RateLimitTier            string          `json:"rate_limit_tier"`
	Tier                     string          `json:"tier"`
	Pass                     gen.PassView   `json:"pass"`
	IsExperimentalSubscriber bool            `json:"is_experimental_subscriber"`
	Credits                  int64           `json:"credits"`
}
