package persist

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func newRedisForTest(t *testing.T, addr, ns string) *RedisPersist {
	t.Helper()
	p, err := New(PersistConfig{Backend: BackendRedis, Endpoint: addr, Prefix: ns})
	if err != nil {
		t.Fatalf("New(redis): %v", err)
	}
	rp := p.(*RedisPersist)
	t.Cleanup(func() { _ = rp.Close() })
	return rp
}

// TestRedisContract runs the full T1 backend contract suite against a real
// redis container. The Appender readback subtests inside the suite skip
// because this backend has no BasePather; their semantics are covered by
// TestRedisAppenderReadback below.
func TestRedisContract(t *testing.T) {
	addr := redisTestAddr(t)
	RunContractTests(t, func(t *testing.T) Persist {
		return newRedisForTest(t, addr, freshNamespace(t))
	})
}

// TestRedisListPrefixIsolation verifies the acceptance criterion that SCAN/
// Lister namespaces are invisible to each other: same server, different
// Prefix or different Database, no cross-contamination in either direction.
func TestRedisListPrefixIsolation(t *testing.T) {
	addr := redisTestAddr(t)
	a := newRedisForTest(t, addr, "iso-a-"+freshNamespace(t))
	b := newRedisForTest(t, addr, "iso-b-"+freshNamespace(t))

	for _, n := range []string{"ws", "ws/abc-1", "ws/abc-2", "other"} {
		if err := a.Save(n, "a"); err != nil {
			t.Fatal(err)
		}
		if err := b.Save(n, "b"); err != nil {
			t.Fatal(err)
		}
	}
	// b must not see a's names.
	got, err := b.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("b.List(\"\") = %v, want exactly its own 4 names", got)
	}
	// Hierarchical semantics: List("ws") excludes the "ws" document itself.
	sub, err := a.List("ws/")
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 2 {
		t.Fatalf("a.List(ws/) = %v, want [ws/abc-1 ws/abc-2]", sub)
	}
	// And the sibling namespace is invisible through the hierarchical scan too.
	bsub, err := b.List("ws")
	if err != nil {
		t.Fatal(err)
	}
	if len(bsub) != 2 {
		t.Fatalf("b.List(ws) = %v, want its own 2 children", bsub)
	}

	// Database-as-namespace: two stores, one Database value, different Prefix
	// keys still isolate because the namespace is Database here.
	p1, err := New(PersistConfig{Backend: BackendRedis, Endpoint: addr, Database: "db-" + freshNamespace(t)})
	if err != nil {
		t.Fatal(err)
	}
	dp1 := p1.(*RedisPersist)
	t.Cleanup(func() { _ = dp1.Close() })
	if err := dp1.Save("shared-name", 1); err != nil {
		t.Fatal(err)
	}
	// A second connection to the same Database sees the document (store-global
	// namespace is shared by design); a different Database does not.
	p2, err := New(PersistConfig{Backend: BackendRedis, Endpoint: addr, Database: "db-" + freshNamespace(t)})
	if err != nil {
		t.Fatal(err)
	}
	dp2 := p2.(*RedisPersist)
	t.Cleanup(func() { _ = dp2.Close() })
	var v int
	if err := dp2.Load("shared-name", &v); !errors.Is(err, ErrNotExist) {
		t.Fatalf("different Database must be invisible, got %v", err)
	}
}

// TestRedisAppenderReadback is the backend-specific companion to the suite's
// Appender tests (which need a BasePather): concurrent Appends must yield
// every line exactly once and whole.
func TestRedisAppenderReadback(t *testing.T) {
	addr := redisTestAddr(t)
	p := newRedisForTest(t, addr, freshNamespace(t))
	const writers = 8
	const linesPerWriter = 50
	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < linesPerWriter; j++ {
				if err := p.Append("log/jsonl", []byte(fmt.Sprintf("writer-%02d-line-%03d\n", w, j))); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	data, err := p.appendPayload("log/jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != writers*linesPerWriter {
		t.Fatalf("line count: got %d, want %d", len(lines), writers*linesPerWriter)
	}
	seen := map[string]bool{}
	for _, line := range lines {
		if !appendLinePattern.MatchString(line) {
			t.Fatalf("torn or malformed line: %q", line)
		}
		if seen[line] {
			t.Fatalf("duplicate line: %q", line)
		}
		seen[line] = true
	}
	// Delete cascades into the append payload.
	if err := p.Delete("log"); err != nil {
		t.Fatal(err)
	}
	if data, err := p.appendPayload("log/jsonl"); err != nil || len(data) != 0 {
		t.Fatalf("append payload must be cascaded away: %v %q", err, data)
	}
}

// TestRedisRefcountShareAndClose verifies the B2-principle client lifecycle:
// same dial parameters share one client, the last Close tears it down, and a
// fresh constructor re-dials cleanly.
func TestRedisRefcountShareAndClose(t *testing.T) {
	addr := redisTestAddr(t)
	cfg := PersistConfig{Backend: BackendRedis, Endpoint: addr, Prefix: "rc-" + freshNamespace(t)}
	p1, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	r1, r2 := p1.(*RedisPersist), p2.(*RedisPersist)
	if r1.h != r2.h {
		t.Fatal("same-dial instances must share one client handle")
	}
	// A different namespace reuses the same client too (dial params equal).
	cfgOtherNS := cfg
	cfgOtherNS.Prefix = "rc-other-" + freshNamespace(t)
	p3, err := New(cfgOtherNS)
	if err != nil {
		t.Fatal(err)
	}
	if p3.(*RedisPersist).h != r1.h {
		t.Fatal("namespace must not change dial params; client should be shared")
	}
	// First Close keeps the client alive for siblings.
	if err := r1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r2.Save("alive", "yes"); err != nil {
		t.Fatalf("save after sibling Close must work: %v", err)
	}
	if err := p3.(*RedisPersist).Close(); err != nil {
		t.Fatal(err)
	}
	if err := r2.Close(); err != nil {
		t.Fatal(err)
	}
	// After the last Close a fresh dial must succeed again.
	p4, err := New(cfg)
	if err != nil {
		t.Fatalf("re-dial after last Close: %v", err)
	}
	t.Cleanup(func() { _ = p4.(*RedisPersist).Close() })
	var got string
	if err := p4.Load("alive", &got); err != nil || got != "yes" {
		t.Fatalf("data lost across client re-dial: %v %q", err, got)
	}
}

// TestRedisRegisteredThroughRegistry proves construction goes through the B1
// registry and that an unknown backend still errors with the registered list
// including redis — never a silent fallback.
func TestRedisRegisteredThroughRegistry(t *testing.T) {
	addr := redisTestAddr(t)
	p, err := New(PersistConfig{Backend: "redis", Endpoint: addr, Prefix: "reg-" + freshNamespace(t)})
	if err != nil {
		t.Fatalf("registered redis backend must construct: %v", err)
	}
	t.Cleanup(func() { _ = p.(*RedisPersist).Close() })
	if _, ok := p.(Lister); !ok {
		t.Error("redis backend must implement Lister")
	}
	if _, ok := p.(Appender); !ok {
		t.Error("redis backend must implement Appender")
	}
	_, err = New(PersistConfig{Backend: "redsi", Endpoint: addr})
	if err == nil || !strings.Contains(err.Error(), "redis") {
		t.Fatalf("typo must list registered backends incl. redis: %v", err)
	}
}

// TestRedisDialFailureExplicit verifies the 约束面 rule: an unreachable
// endpoint is an explicit startup error, never a silent fs fallback.
func TestRedisDialFailureExplicit(t *testing.T) {
	p, err := New(PersistConfig{Backend: BackendRedis, Endpoint: "127.0.0.1:1", Prefix: "x"})
	if err == nil {
		if c, ok := p.(interface{ Close() error }); ok {
			_ = c.Close()
		}
		t.Fatal("unreachable redis must fail construction explicitly")
	}
	if p != nil {
		t.Fatal("failed construction must return a nil Persist")
	}
	if !strings.Contains(err.Error(), "redis") {
		t.Fatalf("error must name the backend: %v", err)
	}
	if _, ok := p.(interface {
		BasePath() string
	}); ok {
		t.Fatal("must never fall back to a filesystem-shaped backend")
	}
}

// TestRedisDialOptionsTLSAndCredentials pins the decision-point-4 TLS matrix
// and the B1 credential plumbing without needing live TLS servers.
func TestRedisDialOptionsTLSAndCredentials(t *testing.T) {
	// Tunnel cases resolve the dial address through the installed resolver
	// (B7 wiring); the returned local forward address replaces the endpoint.
	t.Cleanup(func() { SetTunnelResolver(nil) })
	SetTunnelResolver(func(ref, target string) (string, error) {
		if ref != "host-1" {
			return "", fmt.Errorf("unexpected tunnel ref %q", ref)
		}
		if target != "db.internal:6379" {
			return "", fmt.Errorf("unexpected tunnel target %q", target)
		}
		return "127.0.0.1:16379", nil
	})

	// Direct: TLS on. Tunnel: TLS off. Explicit scheme overrides the default.
	opts, err := redisDialOptions(PersistConfig{Endpoint: "db.internal:6379"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.TLSConfig == nil {
		t.Error("direct dial must default to TLS on (decision point 4)")
	}
	opts, err = redisDialOptions(PersistConfig{Endpoint: "db.internal:6379", TunnelRef: "host-1"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.TLSConfig != nil {
		t.Error("tunnel dial must default to TLS off (decision point 4)")
	}
	if opts.Addr != "127.0.0.1:16379" {
		t.Errorf("tunnel dial must route through the resolved local address, got %q", opts.Addr)
	}
	opts, err = redisDialOptions(PersistConfig{Endpoint: "rediss://db.internal:6379", TunnelRef: "host-1"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.TLSConfig == nil {
		t.Error("explicit rediss:// scheme must force TLS on even through a tunnel")
	}
	opts, err = redisDialOptions(PersistConfig{Endpoint: "redis://db.internal:6379"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.TLSConfig != nil {
		t.Error("explicit redis:// scheme must force TLS off even for a direct dial")
	}
	// Loopback exemption: local dev/test endpoints dial plaintext.
	opts, err = redisDialOptions(PersistConfig{Endpoint: "127.0.0.1:6379"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.TLSConfig != nil {
		t.Error("loopback direct dial must be plaintext")
	}
	opts, err = redisDialOptions(PersistConfig{Endpoint: "localhost:6379"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.TLSConfig != nil {
		t.Error("localhost direct dial must be plaintext")
	}
	opts, err = redisDialOptions(PersistConfig{Endpoint: "rediss://127.0.0.1:6379"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.TLSConfig == nil {
		t.Error("explicit rediss:// scheme must force TLS even on loopback")
	}

	// CredentialRef resolves at dial time; secrets never live in the endpoint.
	installTestCredentialResolver(t, func(ref string) (Credential, error) {
		if ref != "profile-1" {
			return Credential{}, fmt.Errorf("unexpected ref %q", ref)
		}
		return Credential{Username: "alice", Password: "s3cret"}, nil
	})
	opts, err = redisDialOptions(PersistConfig{Endpoint: "db.internal:6379", CredentialRef: "profile-1"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Username != "alice" || opts.Password != "s3cret" {
		t.Errorf("credential not plumbed: %q/%q", opts.Username, opts.Password)
	}
	// A non-empty ref with no resolver installed is an explicit error.
	credMu.Lock()
	saved := credResolver
	credResolver = nil
	credMu.Unlock()
	_, err = redisDialOptions(PersistConfig{Endpoint: "db.internal:6379", CredentialRef: "profile-1"})
	credMu.Lock()
	credResolver = saved
	credMu.Unlock()
	if err == nil {
		t.Error("CredentialRef without a resolver must error, not dial anonymously")
	}
	// Missing endpoint is a config error, not a dial-time surprise.
	if _, err := redisDialOptions(PersistConfig{}); err == nil {
		t.Error("empty endpoint must error")
	}
	// A TunnelRef with no resolver installed is an explicit error (约束面:
	// never a silent direct dial around the declared tunnel).
	SetTunnelResolver(nil)
	if _, err := redisDialOptions(PersistConfig{Endpoint: "db.internal:6379", TunnelRef: "host-1"}); err == nil {
		t.Error("TunnelRef without a resolver must error, not dial directly")
	}
}

// TestRedisTunnelE2E exercises the B3-tunnel path end to end: the backend
// resolves its TunnelRef to a local 127.0.0.1 forwarder (exactly what
// sshmanager's tunnel_open returns), dials it with TLS off, and performs a
// full document roundtrip through it — the redis-flavored "PING" acceptance
// check. The endpoint stays the real target; resolution must replace it.
func TestRedisTunnelE2E(t *testing.T) {
	target := redisTestAddr(t)
	fwd := startTCPForwarder(t, target)
	t.Cleanup(func() { SetTunnelResolver(nil) })
	SetTunnelResolver(func(ref, targetAddr string) (string, error) {
		if ref != "host-1" || targetAddr != target {
			return "", fmt.Errorf("unexpected resolve(%q, %q)", ref, targetAddr)
		}
		return fwd, nil
	})
	p, err := New(PersistConfig{Backend: BackendRedis, Endpoint: target, Prefix: "tun-" + freshNamespace(t), TunnelRef: "host-1"})
	if err != nil {
		t.Fatalf("dial through tunnel forwarder: %v", err)
	}
	rp := p.(*RedisPersist)
	t.Cleanup(func() { _ = rp.Close() })
	type payload struct {
		V int `json:"v"`
	}
	if err := rp.Save("doc/ping", payload{V: 42}); err != nil {
		t.Fatalf("Save through tunnel: %v", err)
	}
	var got payload
	if err := rp.Load("doc/ping", &got); err != nil {
		t.Fatalf("Load through tunnel: %v", err)
	}
	if got.V != 42 {
		t.Fatalf("roundtrip through tunnel: got %+v", got)
	}
	if err := rp.Delete("doc/ping"); err != nil {
		t.Fatalf("Delete through tunnel: %v", err)
	}
	if err := rp.Load("doc/ping", &got); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Load after Delete through tunnel: got %v, want ErrNotExist", err)
	}
}
