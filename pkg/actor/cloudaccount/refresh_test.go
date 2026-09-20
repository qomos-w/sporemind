package cloudaccount

import (
	"testing"
	"time"
)

func TestShouldRefreshToken(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	skew := RefreshSkew

	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"zero expiry (never linked)", time.Time{}, false},
		{"far from expiry", now.Add(1 * time.Hour), false},
		{"exactly at skew boundary", now.Add(skew), false}, // now+skew.After(expiresAt) is false at equality
		{"just inside skew window", now.Add(skew - 1*time.Second), true},
		{"already expired", now.Add(-1 * time.Minute), true},
		{"expired long ago", now.Add(-24 * time.Hour), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldRefreshToken(tt.expiresAt, now, skew); got != tt.want {
				t.Fatalf("ShouldRefreshToken(%v, %v, %v) = %v, want %v", tt.expiresAt, now, skew, got, tt.want)
			}
		})
	}
}

func TestShouldRefreshToken_CustomSkew(t *testing.T) {
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	// A 10-minute skew refreshes anything expiring within 10 minutes.
	if !ShouldRefreshToken(now.Add(5*time.Minute), now, 10*time.Minute) {
		t.Fatal("expected refresh within 10m skew")
	}
	if ShouldRefreshToken(now.Add(20*time.Minute), now, 10*time.Minute) {
		t.Fatal("expected no refresh for token 20m out with 10m skew")
	}
}
