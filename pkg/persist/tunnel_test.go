package persist

import (
	"errors"
	"testing"
)

func TestResolveTunnelEmptyRefIsDirectDial(t *testing.T) {
	SetTunnelResolver(nil)
	addr, err := ResolveTunnel("", "db.example.com:5432")
	if err != nil {
		t.Fatalf("empty ref: unexpected error: %v", err)
	}
	if addr != "" {
		t.Fatalf("empty ref: expected empty addr, got %q", addr)
	}
}

func TestResolveTunnelWithoutResolverIsExplicitError(t *testing.T) {
	SetTunnelResolver(nil)
	if _, err := ResolveTunnel("host-abc", "db.example.com:5432"); err == nil {
		t.Fatal("expected explicit error when TunnelRef set but no resolver installed")
	}
}

func TestResolveTunnelDelegatesToResolver(t *testing.T) {
	t.Cleanup(func() { SetTunnelResolver(nil) })
	SetTunnelResolver(func(ref, target string) (string, error) {
		if ref != "host-abc" || target != "db.example.com:5432" {
			t.Fatalf("resolver got ref=%q target=%q", ref, target)
		}
		return "127.0.0.1:54321", nil
	})
	addr, err := ResolveTunnel("host-abc", "db.example.com:5432")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if addr != "127.0.0.1:54321" {
		t.Fatalf("expected local addr, got %q", addr)
	}
}

func TestResolveTunnelPropagatesResolverError(t *testing.T) {
	t.Cleanup(func() { SetTunnelResolver(nil) })
	SetTunnelResolver(func(ref, target string) (string, error) {
		return "", errors.New("ssh unreachable")
	})
	if _, err := ResolveTunnel("host-abc", "db.example.com:5432"); err == nil {
		t.Fatal("expected error propagation")
	}
}

func TestResolveTunnelRejectsEmptyLocalAddr(t *testing.T) {
	t.Cleanup(func() { SetTunnelResolver(nil) })
	SetTunnelResolver(func(ref, target string) (string, error) {
		return "", nil
	})
	if _, err := ResolveTunnel("host-abc", "db.example.com:5432"); err == nil {
		t.Fatal("expected error for empty local address")
	}
}

func TestResolveTunnelReplaceAndReset(t *testing.T) {
	t.Cleanup(func() { SetTunnelResolver(nil) })
	SetTunnelResolver(func(ref, target string) (string, error) { return "127.0.0.1:1", nil })
	SetTunnelResolver(func(ref, target string) (string, error) { return "127.0.0.1:2", nil })
	addr, err := ResolveTunnel("host-abc", "db.example.com:5432")
	if err != nil || addr != "127.0.0.1:2" {
		t.Fatalf("replacement resolver not in effect: addr=%q err=%v", addr, err)
	}
}
