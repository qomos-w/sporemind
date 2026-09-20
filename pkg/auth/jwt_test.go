package auth

import (
	"testing"
	"time"
)

func TestIssueRefreshToken(t *testing.T) {
	mgr := NewManager(JWTConfig{
		Secret:        []byte("test-secret"),
		Issuer:        "test",
		ExpiryTime:    time.Hour,
		RefreshExpiry: 30 * 24 * time.Hour,
	})

	rt := mgr.IssueRefreshToken()
	if len(rt) != 64 {
		t.Fatalf("refresh token length = %d, want 64 (32 bytes hex)", len(rt))
	}

	rt2 := mgr.IssueRefreshToken()
	if rt == rt2 {
		t.Fatal("two refresh tokens must differ")
	}
}

func TestRefreshExpiryTime(t *testing.T) {
	mgr := NewManager(JWTConfig{
		Secret:        []byte("test-secret"),
		Issuer:        "test",
		ExpiryTime:    2 * time.Hour,
		RefreshExpiry: 30 * 24 * time.Hour,
	})
	if got := mgr.RefreshExpiryTime(); got != 30*24*time.Hour {
		t.Fatalf("RefreshExpiryTime = %v, want %v", got, 30*24*time.Hour)
	}
}

func TestDefaultJWTConfigTTL(t *testing.T) {
	cfg := DefaultJWTConfig()
	if cfg.ExpiryTime != 2*time.Hour {
		t.Fatalf("ExpiryTime = %v, want 2h", cfg.ExpiryTime)
	}
	if cfg.RefreshExpiry != 30*24*time.Hour {
		t.Fatalf("RefreshExpiry = %v, want 30d", cfg.RefreshExpiry)
	}
}
