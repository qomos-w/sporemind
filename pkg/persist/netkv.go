package persist

import (
	"crypto/tls"
	"net"
	"strings"
	"time"
)

// Shared dial-key construction for the network kv backends (B4 redis/etcd,
// and later B5/B6/B8). Client handles are refcounted per process (same
// principle as B2 goleveldb): actor code constructs persist instances per
// actor and several of them may dial the same endpoint, so the transport
// client must be shared. The map holds no actor business state — only the
// dial parameters, which are exactly what identifies a shareable client.
//
// The key deliberately includes every dial-affecting parameter so two
// instances that dial differently never share a handle.

// netOpTimeout bounds a single network operation against a remote backend.
// Owner-lane handlers must stay in the µs~ms budget; persist calls run inside
// them, so a hung remote must surface as an error, not an unbounded stall.
const netOpTimeout = 10 * time.Second

// backendNamespace returns the store-global key namespace for a kv backend:
// Database when set (B1: "Maps to a store-global namespace for kv backends"),
// else the actor-type Prefix. Document names already embed the actor-type
// segment (e.g. "agent/abc-123", "workspace/<id>/rmounts"), so a shared
// Database namespace never mixes actor types; the explicit Database escape
// hatch isolates whole stores sharing one server.
func backendNamespace(cfg PersistConfig) string {
	if cfg.Database != "" {
		return cfg.Database
	}
	return cfg.Prefix
}

// backendTLS reports whether a backend dials with TLS per workflow decision
// point 4: tunnel-routed connections (the B3 ssh -L channel is already
// encrypted) default to TLS off; direct connections default to TLS on.
//
// The one explicit exception is loopback: sporemind is a desktop runtime and
// a bare "127.0.0.1:6379" endpoint is the dev/test topology — loopback is a
// trusted network, so plaintext is the policy there, and the T1 contract
// suites run against local plaintext containers. Production direct dials to
// real hosts are always TLS. An explicit redis:// / rediss:// (or http:// /
// https://) scheme overrides this default in either direction.
func backendTLS(cfg PersistConfig) bool {
	return BackendTLSFor(cfg.Endpoint, cfg.TunnelRef)
}

// BackendTLSFor exposes the decision-point-4 TLS default for external dialers
// (dbclient). Empty tunnelRef mirrors a direct dial; non-empty mirrors a
// B3 ssh -L forward, which is already encrypted. The per-backend scheme
// parsers (redis.ParseURL, oss parseOSSEndpoint, etc.) override this default
// when an explicit scheme is present; BackendTLSFor is the fallback the
// caller applies when no scheme was given.
func BackendTLSFor(endpoint, tunnelRef string) bool {
	if tunnelRef != "" {
		return false
	}
	return !isLoopbackEndpoint(endpoint)
}

// isLoopbackEndpoint extracts the host from a bare host:port or scheme://
// endpoint and reports whether it is the local machine.
func isLoopbackEndpoint(endpoint string) bool {
	host := endpoint
	if i := strings.LastIndex(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	} else if i := strings.Index(host, ":"); i >= 0 {
		host = host[:i] // bare IPv6 without brackets
	}
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// tlsConfigFor returns the TLS config for a dial, or nil for a plaintext
// (tunnel) dial.
func tlsConfigFor(cfg PersistConfig) *tls.Config {
	if !backendTLS(cfg) {
		return nil
	}
	return &tls.Config{MinVersion: tls.VersionTLS12}
}

// netHandleKey builds the process-wide share key for a refcounted client.
// credTag must already be free of the separator (dial params never contain
// '|' in practice; endpoints are host:port or URLs without userinfo because
// credentials come from ResolveCredential, never from the endpoint string).
// TunnelRef is part of the key: two instances dialing the same endpoint
// through DIFFERENT tunnels must not share a client (they dial different
// local forward addresses).
func netHandleKey(credTag string, cfg PersistConfig, tlsOn bool) string {
	ep := strings.TrimSpace(cfg.Endpoint)
	return ep + "|tls=" + boolString(tlsOn) + "|cred=" + credTag + "|tun=" + cfg.TunnelRef
}

func boolString(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
