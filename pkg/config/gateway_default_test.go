//go:build !devrelease

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// Regular builds: resolveGatewayDefault is the identity on every input —
// missing and stock-default addresses stay on the stock default, explicit
// addresses are untouched.
func TestResolveGatewayDefaultStock(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", DefaultGatewayAddr},
		{DefaultGatewayAddr, DefaultGatewayAddr},
		{":18080", ":18080"},
		{"127.0.0.1:19000", "127.0.0.1:19000"},
	}
	for _, c := range cases {
		if got := resolveGatewayDefault(c.in); got != c.want {
			t.Errorf("resolveGatewayDefault(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The auto-written sporemind.yaml always carries the stock default gateway
// address — the flavor remap happens at load time, never in the file.
func TestDefaultConfigWritesStockGatewayAddr(t *testing.T) {
	if got := defaultConfig().GatewayAddr; got != DefaultGatewayAddr {
		t.Fatalf("defaultConfig().GatewayAddr = %q, want stock %q", got, DefaultGatewayAddr)
	}
}

// Stock builds honor an explicitly configured ephemeral port (":0 means
// OS-assigned"): adoption updates the effective address, but no discovery
// record is written and takeover simply reports the configured address.
func TestAdoptAndTakeoverGatewayAddrStock(t *testing.T) {
	dir := t.TempDir()
	origDataDir := cfg.DataDir
	cfg.DataDir = dir
	t.Cleanup(func() { cfg.DataDir = origDataDir })

	cfg.GatewayAddr = "127.0.0.1:0"
	AdoptBoundGatewayAddr("127.0.0.1:53111")
	if cfg.GatewayAddr != "127.0.0.1:53111" {
		t.Fatalf("after adopt, cfg.GatewayAddr = %q, want 127.0.0.1:53111", cfg.GatewayAddr)
	}
	if _, err := os.Stat(filepath.Join(dir, gatewayPortFileName)); !os.IsNotExist(err) {
		t.Fatalf("stock builds must not write a gateway.port record (err=%v)", err)
	}
	if got := TakeoverGatewayAddr(); got != "127.0.0.1:53111" {
		t.Fatalf("TakeoverGatewayAddr() = %q, want effective 127.0.0.1:53111", got)
	}
}
