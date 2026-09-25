//go:build devrelease

package config

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Dev-release builds remap missing and stock-default addresses onto an
// ephemeral port (":0" — the actual address is adopted at runtime after the
// listener binds) so a shared sporemind.yaml (auto-generated with the stock
// default) cannot pin the dev-release binary onto the dev lane's port.
// Explicit non-default addresses are honored as-is.
func TestResolveGatewayDefaultDevRelease(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", devReleaseGatewayAddr},
		{DefaultGatewayAddr, devReleaseGatewayAddr},
		{":18080", ":18080"},
		{"127.0.0.1:19000", "127.0.0.1:19000"},
	}
	if devReleaseGatewayAddr == DefaultGatewayAddr {
		t.Fatal("devrelease build must define its own gateway address")
	}
	if !isEphemeralAddr(devReleaseGatewayAddr) {
		t.Fatalf("devrelease gateway address must be ephemeral (port 0), got %q", devReleaseGatewayAddr)
	}
	for _, c := range cases {
		if got := resolveGatewayDefault(c.in); got != c.want {
			t.Errorf("resolveGatewayDefault(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if devReleaseDataDirName == ".sporemind" {
		t.Fatal("devrelease build must define its own data dir name")
	}
}

// The auto-written sporemind.yaml must carry the stock default, never the
// flavor (or ephemeral) address — a sibling dev binary sharing the config
// file would otherwise honor the devrelease lane literally.
func TestDefaultConfigWritesStockGatewayAddr(t *testing.T) {
	if got := defaultConfig().GatewayAddr; got != DefaultGatewayAddr {
		t.Fatalf("defaultConfig().GatewayAddr = %q, want stock %q", got, DefaultGatewayAddr)
	}
}

func TestAdoptAndTakeoverGatewayAddrDevRelease(t *testing.T) {
	dir := t.TempDir()
	origDataDir := cfg.DataDir
	cfg.DataDir = dir
	t.Cleanup(func() { cfg.DataDir = origDataDir })

	// Ephemeral config: adopting the bound address updates the effective
	// address and records it in the data dir for takeover discovery.
	cfg.GatewayAddr = devReleaseGatewayAddr
	AdoptBoundGatewayAddr("127.0.0.1:53111")
	if cfg.GatewayAddr != "127.0.0.1:53111" {
		t.Fatalf("after adopt, cfg.GatewayAddr = %q, want 127.0.0.1:53111", cfg.GatewayAddr)
	}
	recorded, err := os.ReadFile(filepath.Join(dir, gatewayPortFileName))
	if err != nil {
		t.Fatalf("adopt must record the bound address: %v", err)
	}
	if string(recorded) != "127.0.0.1:53111" {
		t.Fatalf("gateway.port record = %q, want 127.0.0.1:53111", string(recorded))
	}
	if got := TakeoverGatewayAddr(); got != "127.0.0.1:53111" {
		t.Fatalf("TakeoverGatewayAddr() = %q, want recorded 127.0.0.1:53111", got)
	}

	// Explicit fixed config: the configured address wins even though a
	// stale ephemeral record exists — it must never be shadowed.
	cfg.GatewayAddr = "127.0.0.1:19000"
	if got := TakeoverGatewayAddr(); got != "127.0.0.1:19000" {
		t.Fatalf("TakeoverGatewayAddr() with explicit addr = %q, want 127.0.0.1:19000", got)
	}

	// Ephemeral config without a record: takeover falls back to the
	// (undialable) configured form rather than failing.
	cfg.GatewayAddr = devReleaseGatewayAddr
	if err := os.Remove(filepath.Join(dir, gatewayPortFileName)); err != nil {
		t.Fatal(err)
	}
	if got := TakeoverGatewayAddr(); got != devReleaseGatewayAddr {
		t.Fatalf("TakeoverGatewayAddr() without record = %q, want %q", got, devReleaseGatewayAddr)
	}
}

func isEphemeralAddr(addr string) bool {
	_, port, err := net.SplitHostPort(addr)
	return err == nil && port == "0"
}
