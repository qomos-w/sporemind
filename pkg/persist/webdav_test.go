package persist

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/webdav"
)

// newWebDAVTestServer starts an in-process WebDAV server backed by an
// in-memory filesystem — the one network backend whose conformance suite
// needs no container (golang.org/x/net/webdav is already a dependency).
func newWebDAVTestServer(t *testing.T) string {
	t.Helper()
	handler := &webdav.Handler{
		FileSystem: webdav.NewMemFS(),
		LockSystem: webdav.NewMemLS(),
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

// newWebDAVForTest builds a fresh, empty webdav backend against url, using a
// unique Database so each store is an isolated subtree of the shared
// in-process server.
func newWebDAVForTest(t *testing.T, url string, n *uint64) *webdavPersist {
	t.Helper()
	db := fmt.Sprintf("t%d", atomic.AddUint64(n, 1))
	p, err := newWebDAVPersist(PersistConfig{
		Backend:  BackendWebDAV,
		Endpoint: url,
		Database: db,
	})
	if err != nil {
		t.Fatalf("newWebDAVPersist: %v", err)
	}
	return p.(*webdavPersist)
}

// TestWebDAVContract runs the full T1 backend contract suite against the
// webdav backend. The Appender subtests inside the suite skip because this
// backend has no BasePather; their semantics are covered by
// TestWebDAVAppenderRecords below.
func TestWebDAVContract(t *testing.T) {
	url := newWebDAVTestServer(t)
	var n uint64
	RunContractTests(t, func(t *testing.T) Persist {
		return newWebDAVForTest(t, url, &n)
	})
}

// TestWebDAVAppenderRecords is the backend-specific companion to the suite's
// Appender tests (which need a BasePather): concurrent Appends must yield
// every record exactly once, whole and uninterleaved, in per-writer order.
func TestWebDAVAppenderRecords(t *testing.T) {
	url := newWebDAVTestServer(t)
	p := newWebDAVForTest(t, url, new(uint64))
	const writers = 8
	const linesPerWriter = 50
	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < linesPerWriter; j++ {
				if err := p.Append("log/jsonl", []byte(fmt.Sprintf("writer-%02d-line-%03d\n", w, j))); err != nil {
					errCh <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	recs, err := p.webdavRecords("log/jsonl")
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
		seen[line] = true
	}
	for w := 0; w < writers; w++ {
		for j := 0; j < linesPerWriter; j++ {
			key := fmt.Sprintf("writer-%02d-line-%03d", w, j)
			if !seen[key] {
				t.Fatalf("missing record: %s", key)
			}
		}
	}
	// Per-writer order: writer 0's records must ascend in write order.
	prev := -1
	for _, r := range recs {
		line := strings.TrimRight(string(r), "\n")
		if !strings.HasPrefix(line, "writer-00-") {
			continue
		}
		var v int
		if _, err := fmt.Sscanf(line, "writer-00-line-%03d", &v); err != nil {
			t.Fatal(err)
		}
		if v <= prev {
			t.Fatalf("writer-00 records out of order: %d after %d", v, prev)
		}
		prev = v
	}
}

// TestWebDAVListAndCascade verifies Lister enumeration and that Delete
// cascades over the <name>/ sub-document namespace, matching FSPersist and
// LevelDBPersist. It also confirms append records are never enumerated by
// List and are reclaimed by the cascade.
func TestWebDAVListAndCascade(t *testing.T) {
	url := newWebDAVTestServer(t)
	p := newWebDAVForTest(t, url, new(uint64))
	for _, n := range []string{"ws", "ws/abc-1", "ws/abc-1/cards/x", "ws/abc-2", "other"} {
		if err := p.Save(n, "v"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := p.List("ws/")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"ws/abc-1": false, "ws/abc-1/cards/x": false, "ws/abc-2": false}
	for _, n := range got {
		if _, ok := want[n]; !ok {
			t.Fatalf("unexpected name %q", n)
		}
		want[n] = true
	}
	for n, seen := range want {
		if !seen {
			t.Fatalf("missing name in List: %q", n)
		}
	}
	all, err := p.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("List(\"\") = %v, want 5 names", all)
	}
	// Append records are stored under ws/abc-2/requests/ and must not be
	// listed (their ".part" suffix keeps them out of the ".json"
	// enumeration, like leveldb's rec/seq keys).
	if err := p.Append("ws/abc-2/requests", []byte("r1\n")); err != nil {
		t.Fatal(err)
	}
	if all2, err := p.List(""); err != nil || len(all2) != 5 {
		t.Fatalf("append records must not appear in List: %v %d", err, len(all2))
	}
	// Cascade: Delete("ws/abc-1") reclaims the whole abc-1 subtree.
	if err := p.Delete("ws/abc-1"); err != nil {
		t.Fatal(err)
	}
	var v string
	for _, n := range []string{"ws/abc-1", "ws/abc-1/cards/x"} {
		if err := p.Load(n, &v); err != ErrNotExist {
			t.Fatalf("Load(%q) after cascade Delete: got %v, want ErrNotExist", n, err)
		}
	}
	for _, n := range []string{"ws", "ws/abc-2", "other"} {
		if err := p.Load(n, &v); err != nil {
			t.Fatalf("Load(%q) must survive sibling cascade: %v", n, err)
		}
	}
	// Append records live in the same cascade namespace.
	if err := p.Delete("ws/abc-2"); err != nil {
		t.Fatal(err)
	}
	if recs, err := p.webdavRecords("ws/abc-2/requests"); err != nil || len(recs) != 0 {
		t.Fatalf("append records must be cascaded away: %v %d", err, len(recs))
	}
}

// TestWebDAVNoTmpArtifacts confirms a successful Save leaves no .tmp
// resource behind (the atomicity contract).
func TestWebDAVNoTmpArtifacts(t *testing.T) {
	url := newWebDAVTestServer(t)
	p := newWebDAVForTest(t, url, new(uint64))
	if err := p.Save("a/b/c", "v"); err != nil {
		t.Fatal(err)
	}
	if err := p.Save("a/b/c", "v2"); err != nil {
		t.Fatal(err)
	}
	files, _, err := p.walk(p.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, ".tmp") {
			t.Fatalf("stray .tmp artifact left behind: %s", f)
		}
	}
}

// TestWebDAVCorruptBackup verifies a document that fails to decode is moved
// aside to a .corrupt-* name and Load returns the decode error, mirroring
// FSPersist's corrupt-state backup.
func TestWebDAVCorruptBackup(t *testing.T) {
	url := newWebDAVTestServer(t)
	p := newWebDAVForTest(t, url, new(uint64))
	doc := p.abs("k") + ".json"
	if err := p.client.Write(doc, []byte("not-json"), 0644); err != nil {
		t.Fatal(err)
	}
	var v string
	err := p.Load("k", &v)
	if err == nil {
		t.Fatal("Load of corrupt document must error")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Fatalf("error should mention decode: %v", err)
	}
	files, _, _ := p.walk(p.root)
	var found bool
	for _, f := range files {
		if strings.Contains(f, ".corrupt-") {
			found = true
		}
	}
	if !found {
		t.Fatal("corrupt document must be backed up to a .corrupt-* name")
	}
}

// TestWebDAVMoveDegradation verifies the atomicity fallback: when a server
// rejects MOVE-with-overwrite, Save degrades to a bare PUT, sets the
// weakAtomic flag, and still returns the latest value — no .tmp left behind.
func TestWebDAVMoveDegradation(t *testing.T) {
	inner := &webdav.Handler{
		FileSystem: webdav.NewMemFS(),
		LockSystem: webdav.NewMemLS(),
	}
	// Block MOVE entirely: any MOVE returns 412 Precondition Failed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "MOVE" {
			http.Error(w, "move unsupported", http.StatusPreconditionFailed)
			return
		}
		inner.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	p, err := newWebDAVPersist(PersistConfig{
		Backend:  BackendWebDAV,
		Endpoint: srv.URL,
		Database: "d",
	})
	if err != nil {
		t.Fatal(err)
	}
	wp := p.(*webdavPersist)

	// First Save writes tmp, MOVE fails with 412, degrades to direct PUT.
	if err := p.Save("k", "v1"); err != nil {
		t.Fatalf("first save (degrade path): %v", err)
	}
	if !wp.weakAtomic.Load() {
		t.Fatal("weakAtomic must be set after MOVE rejection")
	}
	// Second Save takes the direct path (weak already set).
	if err := p.Save("k", "v2"); err != nil {
		t.Fatalf("second save (weak path): %v", err)
	}
	var got string
	if err := p.Load("k", &got); err != nil {
		t.Fatal(err)
	}
	if got != "v2" {
		t.Fatalf("want v2, got %q", got)
	}
	// No .tmp artifacts survive the degraded writes.
	files, _, err := wp.walk(wp.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, ".tmp") {
			t.Fatalf("stray .tmp artifact after degraded save: %s", f)
		}
	}
}

// TestWebDAVAuthBasic verifies Basic credentials resolve through the B1
// credential resolver and authenticate against a challenge-issuing server.
func TestWebDAVAuthBasic(t *testing.T) {
	const user, pass = "alice", "secret"
	inner := &webdav.Handler{FileSystem: webdav.NewMemFS(), LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(basicAuthGuard(inner, user, pass))
	t.Cleanup(srv.Close)

	SetCredentialResolver(func(ref string) (Credential, error) {
		if ref != "prof" {
			return Credential{}, fmt.Errorf("unknown profile %q", ref)
		}
		return Credential{Username: user, Password: pass}, nil
	})
	t.Cleanup(func() { SetCredentialResolver(nil) })

	p, err := newWebDAVPersist(PersistConfig{
		Backend:       BackendWebDAV,
		Endpoint:      srv.URL,
		Database:      "d",
		CredentialRef: "prof",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Save("k", "v"); err != nil {
		t.Fatalf("save with correct creds: %v", err)
	}
	var got string
	if err := p.Load("k", &got); err != nil {
		t.Fatal(err)
	}
	if got != "v" {
		t.Fatalf("want v, got %q", got)
	}

	// Wrong password must fail, never silently succeed as anonymous.
	SetCredentialResolver(func(string) (Credential, error) {
		return Credential{Username: user, Password: "wrong"}, nil
	})
	bad, err := newWebDAVPersist(PersistConfig{
		Backend:       BackendWebDAV,
		Endpoint:      srv.URL,
		Database:      "d2",
		CredentialRef: "prof",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Save("k", "v"); err == nil {
		t.Fatal("save with wrong creds must fail")
	}
}

// TestWebDAVAuthBearer verifies a Bearer token resolves through the B1
// credential resolver and authenticates against a token-guarded server.
func TestWebDAVAuthBearer(t *testing.T) {
	const token = "abc123"
	inner := &webdav.Handler{FileSystem: webdav.NewMemFS(), LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(bearerAuthGuard(inner, token))
	t.Cleanup(srv.Close)

	SetCredentialResolver(func(ref string) (Credential, error) {
		if ref != "tok" {
			return Credential{}, fmt.Errorf("unknown")
		}
		return Credential{Token: token}, nil
	})
	t.Cleanup(func() { SetCredentialResolver(nil) })

	p, err := newWebDAVPersist(PersistConfig{
		Backend:       BackendWebDAV,
		Endpoint:      srv.URL,
		Database:      "d",
		CredentialRef: "tok",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Save("k", "v"); err != nil {
		t.Fatalf("save with bearer token: %v", err)
	}
	var got string
	if err := p.Load("k", &got); err != nil {
		t.Fatal(err)
	}
	if got != "v" {
		t.Fatalf("want v, got %q", got)
	}
}

// TestWebDAVRegisteredThroughRegistry proves construction goes through the
// registry and that an unknown backend still errors listing the registered
// set including webdav.
func TestWebDAVRegisteredThroughRegistry(t *testing.T) {
	url := newWebDAVTestServer(t)
	p, err := New(PersistConfig{
		Backend:  BackendWebDAV,
		Endpoint: url,
		Database: "reg",
	})
	if err != nil {
		t.Fatalf("registered webdav backend must construct: %v", err)
	}
	if err := p.Save("k", "v"); err != nil {
		t.Fatal(err)
	}
	_, err = New(PersistConfig{Backend: "webdv", Endpoint: url, Database: "reg"})
	if err == nil || !strings.Contains(err.Error(), "webdav") {
		t.Fatalf("typo must list registered backends incl. webdav: %v", err)
	}
}

// TestWebDAVTunnelE2E is the tunnel end-to-end: the dial is routed through a
// fake tunnel resolver to a local TLS port, while TLS verification targets
// the real endpoint hostname (ServerName=dav.test) using a private CA. A
// mismatched ServerName would fail the handshake, so a passing PUT/GET proves
// the SNI/certificate path is wired through the tunnel.
func TestWebDAVTunnelE2E(t *testing.T) {
	cert, caCert := webdavSelfSignedCert(t, "dav.test")
	inner := &webdav.Handler{FileSystem: webdav.NewMemFS(), LockSystem: webdav.NewMemLS()}
	srv := httptest.NewUnstartedServer(inner)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	tunnelAddr := srv.Listener.Addr().String()

	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	prev := webdavTestRootCAs
	webdavTestRootCAs = pool
	t.Cleanup(func() { webdavTestRootCAs = prev })

	SetTunnelResolver(func(ref, target string) (string, error) {
		if ref != "host-1" {
			return "", fmt.Errorf("unknown host %q", ref)
		}
		if target == "" {
			return "", fmt.Errorf("empty target for tunnel %q", ref)
		}
		return tunnelAddr, nil
	})
	t.Cleanup(func() { SetTunnelResolver(nil) })

	p, err := newWebDAVPersist(PersistConfig{
		Backend:   BackendWebDAV,
		Endpoint:  "https://dav.test",
		Database:  "tn",
		TunnelRef: "host-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Save("k", "tunnel-value"); err != nil {
		t.Fatalf("tunnel save: %v", err)
	}
	var got string
	if err := p.Load("k", &got); err != nil {
		t.Fatalf("tunnel load: %v", err)
	}
	if got != "tunnel-value" {
		t.Fatalf("want tunnel-value, got %q", got)
	}
}

// noInfinityDeleteGuard simulates a server that forbids Depth:infinity
// collection DELETE (RFC 4918 allows rejecting it with 403): a DELETE on a
// non-empty collection is refused, forcing the backend's PROPFIND-walk
// fallback; empty collections and plain files delete normally.
func noInfinityDeleteGuard(h http.Handler, fsys webdav.FileSystem) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			if fi, err := fsys.Stat(r.Context(), r.URL.Path); err == nil && fi.IsDir() {
				if f, err := fsys.OpenFile(r.Context(), r.URL.Path, os.O_RDONLY, 0); err == nil {
					entries, _ := f.Readdir(1)
					_ = f.Close()
					if len(entries) > 0 {
						http.Error(w, "depth infinity forbidden", http.StatusForbidden)
						return
					}
				}
			}
		}
		h.ServeHTTP(w, r)
	})
}

// TestWebDAVCascadeDeleteFallback exercises the recursive-delete fallback:
// when the server refuses a non-empty collection DELETE, Delete must still
// reclaim the whole subtree by walking and deleting members individually.
func TestWebDAVCascadeDeleteFallback(t *testing.T) {
	fsys := webdav.NewMemFS()
	inner := &webdav.Handler{FileSystem: fsys, LockSystem: webdav.NewMemLS()}
	srv := httptest.NewServer(noInfinityDeleteGuard(inner, fsys))
	t.Cleanup(srv.Close)

	pi, err := newWebDAVPersist(PersistConfig{Backend: BackendWebDAV, Endpoint: srv.URL, Database: "d"})
	if err != nil {
		t.Fatal(err)
	}
	p := pi.(*webdavPersist)
	for _, n := range []string{"ws/abc-1", "ws/abc-1/cards/x", "ws"} {
		if err := p.Save(n, "v"); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Append("ws/abc-1/requests", []byte("r1\n")); err != nil {
		t.Fatal(err)
	}
	// The guard must actually be blocking recursive DELETE.
	if err := p.client.RemoveAll(p.abs("ws/abc-1")); err == nil {
		t.Fatal("guard must reject non-empty collection DELETE")
	}
	if err := p.Delete("ws/abc-1"); err != nil {
		t.Fatalf("cascade fallback delete: %v", err)
	}
	var v string
	for _, n := range []string{"ws/abc-1", "ws/abc-1/cards/x"} {
		if err := p.Load(n, &v); err != ErrNotExist {
			t.Fatalf("Load(%q) after fallback cascade: got %v, want ErrNotExist", n, err)
		}
	}
	if recs, err := p.webdavRecords("ws/abc-1/requests"); err != nil || len(recs) != 0 {
		t.Fatalf("append records must be reclaimed by fallback cascade: %v %d", err, len(recs))
	}
	if err := p.Load("ws", &v); err != nil {
		t.Fatalf("sibling must survive fallback cascade: %v", err)
	}
}

// basicAuthGuard wraps h with HTTP Basic auth, issuing a 401 challenge for
// gowebdav's AutoAuth to negotiate.
func basicAuthGuard(h http.Handler, user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="dav"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// bearerAuthGuard wraps h requiring an "Authorization: Bearer <token>" header.
func bearerAuthGuard(h http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.Header().Set("WWW-Authenticate", `Bearer realm="dav"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// webdavSelfSignedCert generates a self-signed certificate for host and returns it
// with its parsed form (used as the trust root for the tunnel e2e).
func webdavSelfSignedCert(t *testing.T, host string) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, caCert
}
