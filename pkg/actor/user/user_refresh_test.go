package user

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/auth"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// memPersist is a minimal in-memory Persist for testing.
type memPersist struct {
	data map[string][]byte
}

func newMemPersist() *memPersist {
	return &memPersist{data: make(map[string][]byte)}
}

func (m *memPersist) Load(name string, v any) error {
	raw, ok := m.data[name]
	if !ok {
		return fmt.Errorf("not found")
	}
	return json.Unmarshal(raw, v)
}

func (m *memPersist) Save(name string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.data[name] = raw
	return nil
}

func (m *memPersist) Delete(name string) error {
	delete(m.data, name)
	return nil
}

func newTestActor(t *testing.T) *Actor {
	t.Helper()
	mgr := auth.NewManager(auth.JWTConfig{
		Secret:        []byte("test-secret"),
		Issuer:        "test",
		ExpiryTime:    2 * time.Hour,
		RefreshExpiry: 30 * 24 * time.Hour,
	})
	return &Actor{
		jwt:   mgr,
		store: newMemPersist(),
	}
}

func TestDisabledAdminKeepsDesktopAccessButWebLoginRejected(t *testing.T) {
	a := newTestActor(t)
	a.Accounts = []gen.Account{
		{ID: "admin", Username: "admin", DisplayName: "Administrator", PasswordHash: "x", Roles: []string{"admin"}, Status: "disabled"},
	}

	token, err := a.IssueDesktopToken()
	if err != nil {
		t.Fatalf("desktop token must not depend on admin status: %v", err)
	}
	if token == "" {
		t.Fatal("desktop token must be non-empty")
	}

	if _, err := a.handleLogin(testutil.AnonCtx(testutil.GenActorID()), gen.AuthLoginReq{Username: "admin", Password: "admin"}); err == nil {
		t.Fatal("web login for disabled admin must be rejected")
	}
}

func TestHandleRefreshValidRotation(t *testing.T) {
	a := newTestActor(t)
	a.Accounts = []gen.Account{{
		ID:       "u1",
		Username: "alice",
		Roles:    []string{"admin"},
		Status:   "active",
	}}
	rt := a.jwt.IssueRefreshToken()
	a.RefreshTokens = []gen.RefreshTokenEntry{{
		Token:     rt,
		UserID:    "u1",
		ExpiresAt: time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	}}

	ctx := testutil.AnonCtx(testutil.GenActorID())
	resp, err := a.handleRefresh(ctx, gen.AuthRefreshReq{RefreshToken: rt})
	if err != nil {
		t.Fatalf("handleRefresh failed: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("refresh returned empty access token")
	}
	if resp.RefreshToken == "" || resp.RefreshToken == rt {
		t.Fatal("refresh must return a new refresh token different from the old one")
	}

	// Old refresh token must be invalidated.
	for _, entry := range a.RefreshTokens {
		if entry.Token == rt {
			t.Fatal("old refresh token must be removed after rotation")
		}
	}

	// New refresh token must be stored.
	found := false
	for _, entry := range a.RefreshTokens {
		if entry.Token == resp.RefreshToken {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("new refresh token must be stored")
	}
}

func TestHandleRefreshOldTokenInvalidated(t *testing.T) {
	a := newTestActor(t)
	a.Accounts = []gen.Account{{
		ID:       "u1",
		Username: "alice",
		Roles:    []string{"admin"},
		Status:   "active",
	}}
	rt := a.jwt.IssueRefreshToken()
	a.RefreshTokens = []gen.RefreshTokenEntry{{
		Token:     rt,
		UserID:    "u1",
		ExpiresAt: time.Now().Add(24 * time.Hour).Format(time.RFC3339),
	}}

	ctx := testutil.AnonCtx(testutil.GenActorID())
	_, err := a.handleRefresh(ctx, gen.AuthRefreshReq{RefreshToken: rt})
	if err != nil {
		t.Fatalf("first refresh failed: %v", err)
	}

	// Reusing the old token must fail.
	_, err = a.handleRefresh(ctx, gen.AuthRefreshReq{RefreshToken: rt})
	if err == nil {
		t.Fatal("old refresh token must be rejected after rotation")
	}
}

func TestHandleRefreshExpiredToken(t *testing.T) {
	a := newTestActor(t)
	a.Accounts = []gen.Account{{
		ID:       "u1",
		Username: "alice",
		Roles:    []string{"admin"},
		Status:   "active",
	}}
	rt := a.jwt.IssueRefreshToken()
	a.RefreshTokens = []gen.RefreshTokenEntry{{
		Token:     rt,
		UserID:    "u1",
		ExpiresAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	}}

	ctx := testutil.AnonCtx(testutil.GenActorID())
	_, err := a.handleRefresh(ctx, gen.AuthRefreshReq{RefreshToken: rt})
	if err == nil {
		t.Fatal("expired refresh token must be rejected")
	}
}

func TestHandleRefreshInvalidToken(t *testing.T) {
	a := newTestActor(t)
	a.Accounts = []gen.Account{{
		ID:       "u1",
		Username: "alice",
		Roles:    []string{"admin"},
		Status:   "active",
	}}

	ctx := testutil.AnonCtx(testutil.GenActorID())
	_, err := a.handleRefresh(ctx, gen.AuthRefreshReq{RefreshToken: "nonexistent-token"})
	if err == nil {
		t.Fatal("invalid refresh token must be rejected")
	}
}
