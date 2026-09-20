//go:build devrelease

package config

import "testing"

// Dev-release builds remap missing and stock-default addresses onto their own
// port so a shared sporemind.yaml (auto-generated with the stock default)
// cannot pin the dev-release binary onto the dev lane's port. Explicit
// non-default addresses are honored as-is.
func TestResolveGatewayDefaultDevRelease(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", devReleaseGatewayAddr},
		{DefaultGatewayAddr, devReleaseGatewayAddr},
		{":18080", ":18080"},
		{"127.0.0.1:19000", "127.0.0.1:19000"},
	}
	if devReleaseGatewayAddr == DefaultGatewayAddr {
		t.Fatal("devrelease build must define its own gateway port")
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
