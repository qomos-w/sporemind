package persist

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func newEtcdForTest(t *testing.T, ep, ns string) *EtcdPersist {
	t.Helper()
	p, err := New(PersistConfig{Backend: BackendETCD, Endpoint: ep, Prefix: ns})
	if err != nil {
		t.Fatalf("New(etcd): %v", err)
	}
	epd := p.(*EtcdPersist)
	t.Cleanup(func() { _ = epd.Close() })
	return epd
}

// TestEtcdContract runs the full T1 backend contract suite against a real
// etcd container. The Appender readback subtests inside the suite skip
// because this backend has no BasePather; their semantics are covered by
// TestEtcdAppenderReadback below.
func TestEtcdContract(t *testing.T) {
	ep := etcdTestEndpoint(t)
	RunContractTests(t, func(t *testing.T) Persist {
		return newEtcdForTest(t, ep, freshNamespace(t))
	})
}

// TestEtcdListPrefixIsolation verifies the acceptance criterion that
// WithPrefix Lister namespaces are invisible to each other.
func TestEtcdListPrefixIsolation(t *testing.T) {
	ep := etcdTestEndpoint(t)
	a := newEtcdForTest(t, ep, "iso-a-"+freshNamespace(t))
	b := newEtcdForTest(t, ep, "iso-b-"+freshNamespace(t))

	for _, n := range []string{"ws", "ws/abc-1", "ws/abc-2", "other"} {
		if err := a.Save(n, "a"); err != nil {
			t.Fatal(err)
		}
		if err := b.Save(n, "b"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := b.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("b.List(\"\") = %v, want exactly its own 4 names", got)
	}
	sub, err := a.List("ws/")
	if err != nil {
		t.Fatal(err)
	}
	if len(sub) != 2 {
		t.Fatalf("a.List(ws/) = %v, want [ws/abc-1 ws/abc-2]", sub)
	}
	bsub, err := b.List("ws")
	if err != nil {
		t.Fatal(err)
	}
	if len(bsub) != 2 {
		t.Fatalf("b.List(ws) = %v, want its own 2 children", bsub)
	}

	// Database-as-namespace: same Database value is store-global (shared by
	// design); a different Database is invisible.
	db1, db2 := "db-"+freshNamespace(t), "db-"+freshNamespace(t)
	p1, err := New(PersistConfig{Backend: BackendETCD, Endpoint: ep, Database: db1})
	if err != nil {
		t.Fatal(err)
	}
	dp1 := p1.(*EtcdPersist)
	t.Cleanup(func() { _ = dp1.Close() })
	p2, err := New(PersistConfig{Backend: BackendETCD, Endpoint: ep, Database: db2})
	if err != nil {
		t.Fatal(err)
	}
	dp2 := p2.(*EtcdPersist)
	t.Cleanup(func() { _ = dp2.Close() })
	if err := dp1.Save("shared-name", 1); err != nil {
		t.Fatal(err)
	}
	var v int
	if err := dp2.Load("shared-name", &v); !errors.Is(err, ErrNotExist) {
		t.Fatalf("different Database must be invisible, got %v", err)
	}
}

// TestEtcdAppenderReadback is the backend-specific companion to the suite's
// Appender tests: concurrent Appends must yield every record exactly once,
// whole, in write order (decision point 6: sequential keys <name>/<seq>).
func TestEtcdAppenderReadback(t *testing.T) {
	ep := etcdTestEndpoint(t)
	p := newEtcdForTest(t, ep, freshNamespace(t))
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
	recs, err := p.appendRecords("log/jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != writers*linesPerWriter {
		t.Fatalf("record count: got %d, want %d", len(recs), writers*linesPerWriter)
	}
	seen := map[string]bool{}
	for _, r := range recs {
		line := strings.TrimRight(string(r), "\n")
		if !appendLinePattern.MatchString(line) {
			t.Fatalf("torn or malformed record: %q", line)
		}
		if seen[line] {
			t.Fatalf("duplicate record: %q", line)
		}
		seen[line] = true
	}
	// appendRecords returns keys in lexicographic ( = write) order; verify
	// writer 0's lines ascend.
	prev := -1
	for _, r := range recs {
		line := strings.TrimRight(string(r), "\n")
		if !strings.HasPrefix(line, "writer-00-") {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(line, "writer-00-line-%03d", &n); err != nil {
			t.Fatal(err)
		}
		if n <= prev {
			t.Fatalf("writer-00 records out of order: %d after %d", n, prev)
		}
		prev = n
	}
	// Delete cascades into records and the sequence counter.
	if err := p.Delete("log"); err != nil {
		t.Fatal(err)
	}
	if recs, err := p.appendRecords("log/jsonl"); err != nil || len(recs) != 0 {
		t.Fatalf("append records must be cascaded away: %v %d", err, len(recs))
	}
	if err := p.Append("log/jsonl", []byte("restarted-1\n")); err != nil {
		t.Fatal(err)
	}
	recs, err = p.appendRecords("log/jsonl")
	if err != nil || len(recs) != 1 || string(recs[0]) != "restarted-1\n" {
		t.Fatalf("sequence must restart from 1 after cascade delete: %v %q", err, recs)
	}
}

// TestEtcdRefcountShareAndClose verifies the B2-principle client lifecycle.
func TestEtcdRefcountShareAndClose(t *testing.T) {
	ep := etcdTestEndpoint(t)
	cfg := PersistConfig{Backend: BackendETCD, Endpoint: ep, Prefix: "rc-" + freshNamespace(t)}
	p1, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e1, e2 := p1.(*EtcdPersist), p2.(*EtcdPersist)
	if e1.h != e2.h {
		t.Fatal("same-dial instances must share one client handle")
	}
	if err := e1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := e2.Save("alive", "yes"); err != nil {
		t.Fatalf("save after sibling Close must work: %v", err)
	}
	if err := e2.Close(); err != nil {
		t.Fatal(err)
	}
	p4, err := New(cfg)
	if err != nil {
		t.Fatalf("re-dial after last Close: %v", err)
	}
	t.Cleanup(func() { _ = p4.(*EtcdPersist).Close() })
	var got string
	if err := p4.Load("alive", &got); err != nil || got != "yes" {
		t.Fatalf("data lost across client re-dial: %v %q", err, got)
	}
}

// TestEtcdRegisteredThroughRegistry proves construction goes through the B1
// registry and that an unknown backend still errors with the registered list
// including etcd.
func TestEtcdRegisteredThroughRegistry(t *testing.T) {
	ep := etcdTestEndpoint(t)
	p, err := New(PersistConfig{Backend: "etcd", Endpoint: ep, Prefix: "reg-" + freshNamespace(t)})
	if err != nil {
		t.Fatalf("registered etcd backend must construct: %v", err)
	}
	t.Cleanup(func() { _ = p.(*EtcdPersist).Close() })
	if _, ok := p.(Lister); !ok {
		t.Error("etcd backend must implement Lister")
	}
	if _, ok := p.(Appender); !ok {
		t.Error("etcd backend must implement Appender")
	}
	_, err = New(PersistConfig{Backend: "etcdt", Endpoint: ep})
	if err == nil || !strings.Contains(err.Error(), "etcd") {
		t.Fatalf("typo must list registered backends incl. etcd: %v", err)
	}
}

// TestEtcdDialFailureExplicit verifies the 约束面 rule: an unreachable
// endpoint is an explicit startup error, never a silent fs fallback.
func TestEtcdDialFailureExplicit(t *testing.T) {
	p, err := New(PersistConfig{Backend: BackendETCD, Endpoint: "127.0.0.1:1", Prefix: "x"})
	if err == nil {
		if c, ok := p.(interface{ Close() error }); ok {
			_ = c.Close()
		}
		t.Fatal("unreachable etcd must fail construction explicitly")
	}
	if p != nil {
		t.Fatal("failed construction must return a nil Persist")
	}
	if !strings.Contains(err.Error(), "etcd") {
		t.Fatalf("error must name the backend: %v", err)
	}
}

// TestEtcdClientConfigTLSAndCredentials pins the decision-point-4 TLS matrix
// and the B1 credential plumbing.
func TestEtcdClientConfigTLSAndCredentials(t *testing.T) {
	// Tunnel cases resolve the dial endpoint through the installed resolver
	// (B7 wiring); the returned local forward address replaces the endpoint.
	t.Cleanup(func() { SetTunnelResolver(nil) })
	SetTunnelResolver(func(ref, target string) (string, error) {
		if ref != "host-1" || target != "db.internal:2379" {
			return "", fmt.Errorf("unexpected resolve(%q, %q)", ref, target)
		}
		return "127.0.0.1:12379", nil
	})

	conf, err := etcdClientConfig(PersistConfig{Endpoint: "db.internal:2379"})
	if err != nil {
		t.Fatal(err)
	}
	if conf.TLS == nil {
		t.Error("direct dial must default to TLS on (decision point 4)")
	}
	conf, err = etcdClientConfig(PersistConfig{Endpoint: "db.internal:2379", TunnelRef: "host-1"})
	if err != nil {
		t.Fatal(err)
	}
	if conf.TLS != nil {
		t.Error("tunnel dial must default to TLS off (decision point 4)")
	}
	if len(conf.Endpoints) != 1 || conf.Endpoints[0] != "127.0.0.1:12379" {
		t.Errorf("tunnel dial must route through the resolved local address, got %v", conf.Endpoints)
	}
	conf, err = etcdClientConfig(PersistConfig{Endpoint: "https://db.internal:2379", TunnelRef: "host-1"})
	if err != nil {
		t.Fatal(err)
	}
	if conf.TLS == nil {
		t.Error("explicit https:// scheme must force TLS on even through a tunnel")
	}
	conf, err = etcdClientConfig(PersistConfig{Endpoint: "http://db.internal:2379"})
	if err != nil {
		t.Fatal(err)
	}
	if conf.TLS != nil {
		t.Error("explicit http:// scheme must force TLS off even for a direct dial")
	}
	// Loopback exemption: local dev/test endpoints dial plaintext.
	conf, err = etcdClientConfig(PersistConfig{Endpoint: "127.0.0.1:2379"})
	if err != nil {
		t.Fatal(err)
	}
	if conf.TLS != nil {
		t.Error("loopback direct dial must be plaintext")
	}
	conf, err = etcdClientConfig(PersistConfig{Endpoint: "https://127.0.0.1:2379"})
	if err != nil {
		t.Fatal(err)
	}
	if conf.TLS == nil {
		t.Error("explicit https:// scheme must force TLS even on loopback")
	}

	installTestCredentialResolver(t, func(ref string) (Credential, error) {
		return Credential{Username: "root", Password: "toor"}, nil
	})
	conf, err = etcdClientConfig(PersistConfig{Endpoint: "db.internal:2379", CredentialRef: "profile-1"})
	if err != nil {
		t.Fatal(err)
	}
	if conf.Username != "root" || conf.Password != "toor" {
		t.Errorf("credential not plumbed: %q/%q", conf.Username, conf.Password)
	}
	credMu.Lock()
	saved := credResolver
	credResolver = nil
	credMu.Unlock()
	_, err = etcdClientConfig(PersistConfig{Endpoint: "db.internal:2379", CredentialRef: "profile-1"})
	credMu.Lock()
	credResolver = saved
	credMu.Unlock()
	if err == nil {
		t.Error("CredentialRef without a resolver must error, not dial anonymously")
	}
	if _, err := etcdClientConfig(PersistConfig{}); err == nil {
		t.Error("empty endpoint must error")
	}
	// A TunnelRef with no resolver installed is an explicit error (约束面:
	// never a silent direct dial around the declared tunnel).
	SetTunnelResolver(nil)
	if _, err := etcdClientConfig(PersistConfig{Endpoint: "db.internal:2379", TunnelRef: "host-1"}); err == nil {
		t.Error("TunnelRef without a resolver must error, not dial directly")
	}
}

// TestEtcdTunnelE2E exercises the B3-tunnel path end to end: etcd is gRPC/TCP,
// so the backend resolves its TunnelRef to the tunnel's local forward port
// (ssh -L semantics, no special handling) and performs a full Put-Get
// roundtrip through it. The endpoint stays the real target; resolution must
// replace it.
func TestEtcdTunnelE2E(t *testing.T) {
	target := etcdTestEndpoint(t)
	fwd := startTCPForwarder(t, target)
	t.Cleanup(func() { SetTunnelResolver(nil) })
	SetTunnelResolver(func(ref, targetAddr string) (string, error) {
		if ref != "host-1" || targetAddr != target {
			return "", fmt.Errorf("unexpected resolve(%q, %q)", ref, targetAddr)
		}
		return fwd, nil
	})
	p, err := New(PersistConfig{Backend: BackendETCD, Endpoint: target, Prefix: "tun-" + freshNamespace(t), TunnelRef: "host-1"})
	if err != nil {
		t.Fatalf("dial through tunnel forwarder: %v", err)
	}
	epd := p.(*EtcdPersist)
	t.Cleanup(func() { _ = epd.Close() })
	type payload struct {
		V int `json:"v"`
	}
	if err := epd.Save("doc/ping", payload{V: 42}); err != nil {
		t.Fatalf("Put through tunnel: %v", err)
	}
	var got payload
	if err := epd.Load("doc/ping", &got); err != nil {
		t.Fatalf("Get through tunnel: %v", err)
	}
	if got.V != 42 {
		t.Fatalf("roundtrip through tunnel: got %+v", got)
	}
	if err := epd.Delete("doc/ping"); err != nil {
		t.Fatalf("Delete through tunnel: %v", err)
	}
	if err := epd.Load("doc/ping", &got); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Get after Delete through tunnel: got %v, want ErrNotExist", err)
	}
}
