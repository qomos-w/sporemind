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

	claim, err := dev.Claim()
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claim.Generation != 1 || claim.Reconnected {
		t.Fatalf("first claim = %+v", claim)
	}

	// The claim carried a renewal token and the simulator adopted it for the
	// next reconnect (client priority: current token -> renewed -> bootstrap).
	// Two mints within the same second produce byte-identical JWTs, so the
	// assertion is "a renewal arrived", not "the bytes differ".
	if dev.LastRenewal == "" {
		t.Fatal("claim response carried no RenewedToken")
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
