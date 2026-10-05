package glassinteract_test

// Claim-response token renewal over the real gateway, gated by
// SPOREMIND_GLASS_CLAIM_RENEW=1 (default OFF — the wire change is pending
// bilateral confirmation with MentraOS). The gate flips the callable's
// registered handler to the GlassSessionClaimRespV2 variant, so the
// default-off path stays byte-identical to the deployed behavior (every
// other e2e in this package runs gate-off).

import (
	"context"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/actor/glassinteract/glasssim"
)

func TestGlassClaimRenewalOverRealGateway(t *testing.T) {
	t.Setenv("SPOREMIND_GLASS_CLAIM_RENEW", "1")
	addr, _, _ := startGlassRuntime(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dev, err := glasssim.Connect(ctx, addr, glassConnKey, "dev-renew-e2e")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer dev.Close()
	bootToken := dev.Token()

	// Space the bootstrap and renewal mints into different seconds:
	// IssueGlass uses second-resolution iat/exp with no jti, so two mints in
	// the same second produce byte-identical JWTs and "adopted the renewal"
	// would be indistinguishable from "kept the bootstrap token".
	time.Sleep(1100 * time.Millisecond)

	claim, err := dev.Claim()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.Generation != 1 || claim.Reconnected {
		t.Fatalf("first claim = %+v", claim)
	}

	// The claim carried a renewal token and the simulator adopted it for the
	// next reconnect (client priority: current token -> renewed -> bootstrap).
	if dev.LastRenewal == "" {
		t.Fatal("claim response carried no RenewedToken")
	}
	if held := dev.Token(); held == "" || held == bootToken {
		t.Fatalf("held token %q did not adopt the renewal (bootstrap %q)", held, bootToken)
	}

	// Drop and come back on the RENEWED token; the session must resume.
	dev.Drop()
	if err := dev.Reconnect(ctx); err != nil {
		t.Fatalf("reconnect with renewed token: %v", err)
	}
	dev.LastRenewal = "" // prove claim 2 renews again
	rc, err := dev.Claim()
	if err != nil {
		t.Fatalf("claim after reconnect: %v", err)
	}
	if !rc.Reconnected || rc.Generation != 1 {
		t.Fatalf("renewed-token claim = %+v, want reconnected gen 1", rc)
	}

	// Each claim mints a fresh renewal (a renewal response arrives again).
	if dev.LastRenewal == "" {
		t.Fatalf("second claim did not mint a renewal")
	}
}

// TestGlassClaimRenewalDefaultOff pins the default gate state: without
// SPOREMIND_GLASS_CLAIM_RENEW the claim response must not carry RenewedToken.
// The other claim e2e tests decode into the legacy struct, which would
// silently drop an accidentally-emitted renewal field — this is the guard
// that catches a default-on regression.
func TestGlassClaimRenewalDefaultOff(t *testing.T) {
	addr, _, _ := startGlassRuntime(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	dev, err := glasssim.Connect(ctx, addr, glassConnKey, "dev-renew-off")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer dev.Close()

	if _, err := dev.Claim(); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if dev.LastRenewal != "" {
		t.Fatalf("default-off claim carried RenewedToken (%d bytes)", len(dev.LastRenewal))
	}
}
