package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testGlassManager() *Manager {
	return NewManager(JWTConfig{Secret: []byte("glass-test-secret"), Issuer: "test", ExpiryTime: time.Hour})
}

// TestIssueGlassBindsSessionDevice verifies the token carries session id,
// device id, kind/scope "glass" and a future expiry, and that subject is the
// session id (the identity the gateway binds the /ws connection to).
func TestIssueGlassBindsSessionDevice(t *testing.T) {
	m := testGlassManager()
	pair, err := m.IssueGlass("gs_123", "dev-42", 0)
	if err != nil {
		t.Fatal(err)
	}
	if pair.Token == "" || pair.ExpiresAt.IsZero() {
		t.Fatal("empty token or expiry")
	}
	if !pair.ExpiresAt.After(time.Now()) {
		t.Fatalf("expiry %v not in future", pair.ExpiresAt)
	}

	claims, err := m.VerifyGlass(pair.Token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.SessionID != "gs_123" || claims.DeviceID != "dev-42" {
		t.Fatalf("claims = %+v, want session gs_123 device dev-42", claims)
	}
	if claims.Scope != GlassScope || claims.Kind != GlassScope {
		t.Fatalf("scope/kind = %q/%q, want glass/glass", claims.Scope, claims.Kind)
	}
	if claims.Subject != "gs_123" {
		t.Fatalf("subject = %q, want gs_123", claims.Subject)
	}
	if claims.Issuer != "test" {
		t.Fatalf("issuer = %q, want test", claims.Issuer)
	}
}

func TestIssueGlassRequiresSessionID(t *testing.T) {
	m := testGlassManager()
	if _, err := m.IssueGlass("", "dev-42", time.Minute); err == nil {
		t.Fatal("IssueGlass with empty session id succeeded")
	}
}

// TestVerifyGlassRejectsExpiredToken verifies an expired glass token is
// reported as ErrTokenExpired.
func TestVerifyGlassRejectsExpiredToken(t *testing.T) {
	m := testGlassManager()
	now := time.Now()
	claims := GlassClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test",
			Subject:   "gs_1",
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-time.Hour)),
		},
		Scope:     GlassScope,
		Kind:      GlassScope,
		SessionID: "gs_1",
		DeviceID:  "dev-1",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte("glass-test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.VerifyGlass(signed)
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("err = %v, want ErrTokenExpired", err)
	}
}

// TestVerifyGlassRejectsWrongScope ensures a non-glass token or a token
// missing the glass scope/kind is rejected.
func TestVerifyGlassRejectsWrongScope(t *testing.T) {
	m := testGlassManager()

	bad := GlassClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   "test",
			Subject:  "gs_1",
			IssuedAt: jwt.NewNumericDate(time.Now()),
		},
		Scope:     "other",
		Kind:      GlassScope,
		SessionID: "gs_1",
		DeviceID:  "dev-1",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, bad)
	signed, err := tok.SignedString([]byte("glass-test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.VerifyGlass(signed); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}

	// A normal user token must not verify as glass.
	userPair, err := m.Issue("user-1", "alice", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.VerifyGlass(userPair.AccessToken); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("user token err = %v, want ErrInvalidToken", err)
	}
}

// TestVerifyGlassRejectsForgedSecret ensures a token signed with a different
// secret fails validation.
func TestVerifyGlassRejectsForgedSecret(t *testing.T) {
	m := testGlassManager()
	claims := GlassClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test",
			Subject:   "gs_1",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
		Scope:     GlassScope,
		Kind:      GlassScope,
		SessionID: "gs_1",
		DeviceID:  "dev-1",
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte("attacker-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.VerifyGlass(signed); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}
