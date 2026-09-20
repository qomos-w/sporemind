package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// GlassScope is the scope/kind marker carried by short-lived Glass session
// tokens. The /ws handshake maps a glass token to role "glass" with the
// server-generated session_id as subject.
const GlassScope = "glass"

// GlassTokenTTL is the default lifetime of a Glass session token. Glass uses
// short-lived tokens: sporemind_key is only used at bootstrap, and each token
// is bound to the session_id, device_id, scope and an expiry.
const GlassTokenTTL = 5 * time.Minute

// GlassClaims carries the JWT custom claims of a Glass session token.
// Subject is the server-generated session_id so the gateway can bind the
// /ws connection to the logical session without a separate lookup.
type GlassClaims struct {
	jwt.RegisteredClaims
	Scope     string `json:"scope"`
	Kind      string `json:"kind"`
	SessionID string `json:"sid"`
	DeviceID  string `json:"did"`
}

// GlassTokenPair holds a signed Glass session token and its expiry.
type GlassTokenPair struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// IssueGlass signs a short-lived HS256 Glass session token bound to the
// server-generated sessionID and deviceID with scope/kind "glass".
func (m *Manager) IssueGlass(sessionID, deviceID string, ttl time.Duration) (GlassTokenPair, error) {
	if sessionID == "" {
		return GlassTokenPair{}, fmt.Errorf("auth: glass session id is required")
	}
	if ttl <= 0 {
		ttl = GlassTokenTTL
	}
	now := time.Now()
	claims := GlassClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.cfg.Issuer,
			Subject:   sessionID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Scope:     GlassScope,
		Kind:      GlassScope,
		SessionID: sessionID,
		DeviceID:  deviceID,
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.cfg.Secret)
	if err != nil {
		return GlassTokenPair{}, fmt.Errorf("auth: sign glass token: %w", err)
	}
	return GlassTokenPair{Token: signed, ExpiresAt: now.Add(ttl)}, nil
}

// VerifyGlass parses and validates a Glass session token, returning the
// embedded claims. Kind and scope must both be "glass" and the session id
// must be non-empty; anything else is rejected as a non-glass token.
func (m *Manager) VerifyGlass(tokenString string) (GlassClaims, error) {
	var claims GlassClaims
	token, err := jwt.ParseWithClaims(tokenString, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("auth: unexpected signing method: %v", t.Header["alg"])
		}
		return m.cfg.Secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return GlassClaims{}, ErrTokenExpired
		}
		return GlassClaims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !token.Valid || claims.Scope != GlassScope || claims.Kind != GlassScope || claims.SessionID == "" {
		return GlassClaims{}, ErrInvalidToken
	}
	return claims, nil
}
