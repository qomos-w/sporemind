package gatewayauth

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/qomos-w/sporemind/pkg/auth"
)

func testManager() *auth.Manager {
	return auth.NewManager(auth.JWTConfig{Secret: []byte("gw-test-secret"), Issuer: "test", ExpiryTime: time.Hour})
}

// TestGlassTokenMapsToGlassRole verifies a valid glass token authenticates a
// /ws connection as role=glass with the session id as subject.
func TestGlassTokenMapsToGlassRole(t *testing.T) {
	m := testManager()
	ua := New(m)

	pair, err := m.IssueGlass("gs_77", "dev-9", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{}
	q.Set("token", pair.Token)
	role, subject, err := ua(context.Background(), q, nil)
	if err != nil {
		t.Fatal(err)
	}
	if role != "glass" {
		t.Fatalf("role = %q, want glass", role)
	}
	if subject != "gs_77" {
		t.Fatalf("subject = %q, want gs_77", subject)
	}
}

// TestUserTokenStillResolves verifies normal user tokens keep the existing
// role/subject mapping.
func TestUserTokenStillResolves(t *testing.T) {
	m := testManager()
	ua := New(m)

	pair, err := m.Issue("user-1", "alice", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{}
	q.Set("token", pair.AccessToken)
	role, subject, err := ua(context.Background(), q, nil)
	if err != nil {
		t.Fatal(err)
	}
	if role != "admin" || subject != "user-1" {
		t.Fatalf("role/subject = %q/%q, want admin/user-1", role, subject)
	}
}

// TestNoTokenIsAnonymous verifies the anonymous fallback.
func TestNoTokenIsAnonymous(t *testing.T) {
	ua := New(testManager())
	role, subject, err := ua(context.Background(), url.Values{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if role != "anonymous" || subject != "" {
		t.Fatalf("role/subject = %q/%q, want anonymous/empty", role, subject)
	}
}

// TestExpiredGlassTokenRejected verifies expired glass tokens are refused at
// the gateway (they cannot affect an existing session).
func TestExpiredGlassTokenRejected(t *testing.T) {
	m := testManager()
	ua := New(m)

	now := time.Now()
	expired := auth.GlassClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "test",
			Subject:   "gs_1",
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			ExpiresAt: jwt.NewNumericDate(now.Add(-time.Hour)),
		},
		Scope:     auth.GlassScope,
		Kind:      auth.GlassScope,
		SessionID: "gs_1",
		DeviceID:  "dev-1",
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, expired).SignedString([]byte("gw-test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{}
	q.Set("token", signed)
	if _, _, err := ua(context.Background(), q, nil); err == nil {
		t.Fatal("expired glass token accepted")
	}
}

// TestInvalidTokenRejected verifies garbage tokens are refused.
func TestInvalidTokenRejected(t *testing.T) {
	ua := New(testManager())
	q := url.Values{}
	q.Set("token", "not-a-jwt")
	if _, _, err := ua(context.Background(), q, nil); err == nil {
		t.Fatal("invalid token accepted")
	}
}
