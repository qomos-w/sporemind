//go:build !devrelease

package config

import "testing"

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
