package persist

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// --- helpers --------------------------------------------------------------

// minioEnv describes a running minio container (or an externally supplied
// endpoint) for the OSS contract suite.
type minioEnv struct {
	endpoint string
	access   string
	secret   string
	bucket   string
}

// startMinioContainer launches a throwaway minio server in a container on a
// free loopback port and returns its connection details. It pulls the image
// on first use. On any failure it calls t.Skip with the reason so CI without
// docker stays green (card: "minio 容器，CI 可选跳过").
func startMinioContainer(t *testing.T) minioEnv {
	t.Helper()
	if !dockerAvailable() {
		t.Skip("docker daemon unavailable; skipping minio-backed OSS suite")
	}
	port := freePort(t)
	const access, secret = "minioadmin", "minioadmin123456"
	container := fmt.Sprintf("persist-oss-test-%d", port)
	cmd := exec.Command("docker", "run", "-d", "--rm",
		"--name", container,
		"-p", fmt.Sprintf("127.0.0.1:%d:9000", port),
		"-e", "MINIO_ROOT_USER="+access,
		"-e", "MINIO_ROOT_PASSWORD="+secret,
		"minio/minio", "server", "/data",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("docker run minio failed (skipping): %v: %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", container).Run()
	})

	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	if !waitForMinio(endpoint, 30*time.Second) {
		t.Skipf("minio did not become ready on %s within timeout", endpoint)
	}
	env := minioEnv{
		endpoint: endpoint,
		access:   access,
		secret:   secret,
		bucket:   fmt.Sprintf("sporemind-oss-test-%d", port),
	}
	if err := makeBucket(env, env.bucket); err != nil {
		t.Skipf("make bucket failed (skipping): %v", err)
	}
	return env
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitForMinio(endpoint string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(endpoint + "/minio/health/ready")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// rawClient builds an unconfigured minio client for harness operations
// (bucket creation, object listing) outside the persist interface.
func rawClient(env minioEnv) (*minio.Client, error) {
	u, _ := url.Parse(env.endpoint)
	c, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(env.access, env.secret, ""),
		Secure: u.Scheme == "https",
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func makeBucket(env minioEnv, bucket string) error {
	c, err := rawClient(env)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
}

// ossFactory returns a RunContractTests factory backed by a fresh OSSPersist
// with a unique key prefix per call, so parallel subtests never collide.
func ossFactory(t *testing.T, env minioEnv) Persist {
	t.Helper()
	prefix := fmt.Sprintf("ns-%d", time.Now().UnixNano())
	p, err := newOSSPersist(PersistConfig{
		Backend:       BackendOSS,
		Endpoint:      env.endpoint,
		Database:      env.bucket,
		Prefix:        prefix,
		CredentialRef: "minio-test",
	})
	if err != nil {
		t.Fatalf("newOSSPersist: %v", err)
	}
	return p
}

// installMinioCreds installs a CredentialResolver that returns the minio root
// credentials for any ref; production wiring resolves real profiles. Tests
// must reset it.
func installMinioCreds(t *testing.T, env minioEnv) {
	t.Helper()
	t.Cleanup(func() { SetCredentialResolver(nil) })
	SetCredentialResolver(func(ref string) (Credential, error) {
		return Credential{AccessKey: env.access, Secret: env.secret}, nil
	})
}

// --- contract suite ------------------------------------------------------

func TestOSS_RunContractSuite(t *testing.T) {
	env := startMinioContainer(t)
	installMinioCreds(t, env)
	RunContractTests(t, func(t *testing.T) Persist { return ossFactory(t, env) })
}

// --- prefix isolation ----------------------------------------------------

func TestOSS_PrefixIsolation(t *testing.T) {
	env := startMinioContainer(t)
	installMinioCreds(t, env)

	mk := func(prefix string) Persist {
		p, err := newOSSPersist(PersistConfig{
			Backend: BackendOSS, Endpoint: env.endpoint, Database: env.bucket,
			Prefix: prefix, CredentialRef: "minio-test",
		})
		if err != nil {
			t.Fatalf("newOSSPersist(%s): %v", prefix, err)
		}
		return p
	}
	a := mk("ns-iso-a")
	b := mk("ns-iso-b")
	if err := a.Save("doc", map[string]string{"v": "a"}); err != nil {
		t.Fatalf("save a: %v", err)
	}
	var got map[string]string
	if err := a.Load("doc", &got); err != nil || got["v"] != "a" {
		t.Fatalf("a.Load: got=%v err=%v", got, err)
	}
	if err := b.Load("doc", &got); !errors.Is(err, ErrNotExist) {
		t.Fatalf("b.Load must be ErrNotExist, got %v", err)
	}
	if err := a.Save("shared", map[string]string{"v": "from-a"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Save("shared", map[string]string{"v": "from-b"}); err != nil {
		t.Fatal(err)
	}
	var av, bv map[string]string
	if err := a.Load("shared", &av); err != nil || av["v"] != "from-a" {
		t.Fatalf("a shared: %v %v", av, err)
	}
	if err := b.Load("shared", &bv); err != nil || bv["v"] != "from-b" {
		t.Fatalf("b shared: %v %v", bv, err)
	}
}

// --- cascade delete ------------------------------------------------------

func TestOSS_DeleteCascadesAppendSubtree(t *testing.T) {
	env := startMinioContainer(t)
	installMinioCreds(t, env)
	p := ossFactory(t, env)
	ap, ok := p.(*OSSPersist)
	if !ok {
		t.Fatalf("expected *OSSPersist, got %T", p)
	}

	if err := p.Save("actor1", map[string]string{"k": "v"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := ap.Append("actor1", []byte(fmt.Sprintf("rec-%d\n", i))); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Pre-delete: the append subtree has exactly three objects.
	c, _ := rawClient(env)
	if n := countObjects(t, c, env.bucket, ap.appendPrefix("actor1")); n != 3 {
		t.Fatalf("append subtree count before delete = %d, want 3", n)
	}

	if err := p.Delete("actor1"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if err := p.Load("actor1", &map[string]string{}); !errors.Is(err, ErrNotExist) {
		t.Fatalf("Load after delete must be ErrNotExist, got %v", err)
	}
	if n := countObjects(t, c, env.bucket, ap.appendPrefix("actor1")); n != 0 {
		t.Fatalf("append subtree count after delete = %d, want 0 (cascade failed)", n)
	}
}

func countObjects(t *testing.T, c *minio.Client, bucket, prefix string) int {
	t.Helper()
	ctx := context.Background()
	n := 0
	for obj := range c.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			t.Fatalf("list %s: %v", prefix, obj.Err)
		}
		n++
	}
	return n
}

// --- appender seq --------------------------------------------------------

func TestOSS_AppendSeqObjects(t *testing.T) {
	env := startMinioContainer(t)
	installMinioCreds(t, env)
	p := ossFactory(t, env)
	ap := p.(*OSSPersist)

	records := []string{"alpha\n", "beta\n", "gamma\n"}
	for i, r := range records {
		if err := ap.Append("stream", []byte(r)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Read back via the raw client: objects named %020d in order, contents
	// matching the records exactly.
	c, _ := rawClient(env)
	ctx := context.Background()
	var objs []minio.ObjectInfo
	for obj := range c.ListObjects(ctx, env.bucket, minio.ListObjectsOptions{Prefix: ap.appendPrefix("stream"), Recursive: true}) {
		if obj.Err != nil {
			t.Fatalf("list: %v", obj.Err)
		}
		objs = append(objs, obj)
	}
	if len(objs) != len(records) {
		t.Fatalf("seq object count = %d, want %d", len(objs), len(records))
	}
	for i, oi := range objs {
		wantKey := ap.key("stream", fmt.Sprintf(ossSeqFormat, int64(i+1)))
		if oi.Key != wantKey {
			t.Errorf("obj %d key = %q, want %q", i, oi.Key, wantKey)
		}
		body, err := c.GetObject(ctx, env.bucket, oi.Key, minio.GetObjectOptions{})
		if err != nil {
			t.Fatalf("get %s: %v", oi.Key, err)
		}
		data, _ := io.ReadAll(body)
		body.Close()
		if string(data) != records[i] {
			t.Errorf("obj %d content = %q, want %q", i, data, records[i])
		}
	}

	// A restart-simulated second instance continues after the max seq: a
	// fresh OSSPersist over the same prefix must append seq=4, not overwrite 3.
	p2, err := newOSSPersist(PersistConfig{
		Backend: BackendOSS, Endpoint: env.endpoint, Database: env.bucket,
		Prefix: ap.prefix, CredentialRef: "minio-test",
	})
	if err != nil {
		t.Fatalf("second persist: %v", err)
	}
	if err := p2.(*OSSPersist).Append("stream", []byte("delta\n")); err != nil {
		t.Fatalf("append after restart: %v", err)
	}
	if n := countObjects(t, c, env.bucket, ap.appendPrefix("stream")); n != 4 {
		t.Fatalf("post-restart count = %d, want 4", n)
	}
}

// --- tunnel SNI e2e ------------------------------------------------------

// TestOSS_TunnelSNI asserts that a tunneled HTTPS path dials the tunnel's
// local listener while presenting the real endpoint domain in the TLS
// ClientHello (SNI) and verifying the certificate against that domain — the
// decision-point-4 invariant. An in-process TLS reverse proxy fronts the
// minio container; a plain TCP forwarder stands in for the sshmanager
// tunnel.
func TestOSS_TunnelSNI(t *testing.T) {
	env := startMinioContainer(t)
	installMinioCreds(t, env)

	const realDomain = "oss-b6-sni.test"

	// Self-signed cert for the real domain; the client must trust it via the
	// test CA seam, and the server records the SNI it received.
	cert, pool := selfSignedCert(t, realDomain)

	var observedSNI string
	var sniMu sync.Mutex
	tlsCfg := &tls.Config{
		GetCertificate: func(chi *tls.ClientHelloInfo) (*tls.Certificate, error) {
			sniMu.Lock()
			observedSNI = chi.ServerName
			sniMu.Unlock()
			return &cert, nil
		},
	}

	// TLS reverse proxy → minio container. Host header is preserved (the
	// proxy director does not rewrite req.Host) so SigV4 signatures, which
	// the client computed against the tunnel's local address, still validate
	// at the server.
	minioURL, _ := url.Parse(env.endpoint)
	proxy := httputil.NewSingleHostReverseProxy(minioURL)
	tlsLn, err := tls.Listen("tcp", "127.0.0.1:0", tlsCfg)
	if err != nil {
		t.Fatalf("tls listen: %v", err)
	}
	srv := &http.Server{Handler: proxy}
	go srv.Serve(tlsLn)
	t.Cleanup(func() { srv.Close() })

	// Plain TCP forwarder = the "tunnel" local listener. Bytes copied both
	// directions to the TLS server.
	fwdLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("forwarder listen: %v", err)
	}
	tlsAddr := tlsLn.Addr().String()
	go func() {
		for {
			c, err := fwdLn.Accept()
			if err != nil {
				return
			}
			go forwardConn(c, tlsAddr)
		}
	}()
	t.Cleanup(func() { fwdLn.Close() })

	// Inject the test CA so the client verifies the self-signed cert against
	// the real domain. Test-only seam; nil in production.
	t.Cleanup(func() { ossTestRootCAs = nil })
	ossTestRootCAs = pool

	// Tunnel resolver returns the forwarder's local address; the backend then
	// dials it while TLS SNI/verification target the real domain.
	t.Cleanup(func() { SetTunnelResolver(nil) })
	SetTunnelResolver(func(ref, target string) (string, error) {
		if ref != "tunnel-b6" {
			t.Errorf("tunnel resolver got ref %q, want tunnel-b6", ref)
		}
		return fwdLn.Addr().String(), nil
	})

	// Use a unique suffix for the endpoint host:port so the resolver target is
	// the real domain on 443 (the dial is overridden by the transport anyway).
	p, err := newOSSPersist(PersistConfig{
		Backend:       BackendOSS,
		Endpoint:      "https://" + realDomain,
		Database:      env.bucket,
		Prefix:        fmt.Sprintf("tun-%d", time.Now().UnixNano()),
		CredentialRef: "minio-test",
		TunnelRef:     "tunnel-b6",
	})
	if err != nil {
		t.Fatalf("newOSSPersist(tunnel): %v", err)
	}

	want := map[string]string{"hello": "through-tunnel"}
	if err := p.Save("doc", want); err != nil {
		t.Fatalf("tunnel Save: %v", err)
	}
	var got map[string]string
	if err := p.Load("doc", &got); err != nil {
		t.Fatalf("tunnel Load: %v", err)
	}
	if !mapsEqual(got, want) {
		t.Fatalf("tunnel roundtrip mismatch: got %v, want %v", got, want)
	}

	sniMu.Lock()
	gotSNI := observedSNI
	sniMu.Unlock()
	if gotSNI != realDomain {
		t.Fatalf("server observed SNI = %q, want %q (cert verification did not target the real domain)", gotSNI, realDomain)
	}
}

func forwardConn(c net.Conn, target string) {
	defer c.Close()
	up, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer up.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(up, c) }()
	go func() { defer wg.Done(); _, _ = io.Copy(c, up) }()
	wg.Wait()
}

func selfSignedCert(t *testing.T, domain string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{domain},
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	return cert, pool
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// --- concurrent appender (contract skips without BasePather; verify via
// raw-object readback instead) -------------------------------------------

func TestOSS_AppendConcurrentNoInterleave(t *testing.T) {
	env := startMinioContainer(t)
	installMinioCreds(t, env)
	p := ossFactory(t, env)
	ap := p.(*OSSPersist)

	const writers = 8
	const linesPerWriter = 50
	var wg sync.WaitGroup
	errCh := make(chan error, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := 0; j < linesPerWriter; j++ {
				if err := ap.Append("conc-stream", []byte(fmt.Sprintf("writer-%02d-line-%03d\n", w, j))); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	// Read back every seq object; all lines must be present, complete, and
	// untorn — the same guarantee the contract suite checks via BasePath.
	c, _ := rawClient(env)
	ctx := context.Background()
	type rec struct {
		seq  int64
		data string
	}
	var recs []rec
	for obj := range c.ListObjects(ctx, env.bucket, minio.ListObjectsOptions{Prefix: ap.appendPrefix("conc-stream"), Recursive: true}) {
		if obj.Err != nil {
			t.Fatalf("list: %v", obj.Err)
		}
		body, err := c.GetObject(ctx, env.bucket, obj.Key, minio.GetObjectOptions{})
		if err != nil {
			t.Fatalf("get %s: %v", obj.Key, err)
		}
		data, _ := io.ReadAll(body)
		body.Close()
		var seq int64
		if _, err := fmt.Sscanf(obj.Key[len(ap.appendPrefix("conc-stream")):], "%d", &seq); err != nil {
			t.Fatalf("seq parse %q: %v", obj.Key, err)
		}
		recs = append(recs, rec{seq, string(data)})
	}
	if len(recs) != writers*linesPerWriter {
		t.Fatalf("seq object count = %d, want %d (lost or duplicate appends)", len(recs), writers*linesPerWriter)
	}
	// Lexical (== numeric, zero-padded) order must match append order 1..N.
	for i, r := range recs {
		if r.seq != int64(i+1) {
			t.Fatalf("rec %d seq = %d, want %d (gaps or reordering)", i, r.seq, i+1)
		}
	}
	seen := make(map[string]bool, writers*linesPerWriter)
	for _, r := range recs {
		if !strings.HasPrefix(r.data, "writer-") || !strings.HasSuffix(r.data, "\n") || strings.Count(r.data, "\n") != 1 {
			t.Errorf("torn record: %q", r.data)
			continue
		}
		seen[strings.TrimSuffix(r.data, "\n")] = true
	}
	for w := 0; w < writers; w++ {
		for j := 0; j < linesPerWriter; j++ {
			key := fmt.Sprintf("writer-%02d-line-%03d", w, j)
			if !seen[key] {
				t.Errorf("missing record: %s", key)
			}
		}
	}
}

// --- corrupt decode backup -------------------------------------------------

func TestOSS_LoadCorruptDecodeBacksUp(t *testing.T) {
	env := startMinioContainer(t)
	installMinioCreds(t, env)
	p := ossFactory(t, env)
	ap := p.(*OSSPersist)

	// Plant invalid JSON directly at the document key (raw client), then Load
	// must surface a decode error and preserve the corrupt bytes under a
	// .corrupt-<ts> object while removing the original.
	c, _ := rawClient(env)
	ctx := context.Background()
	docKey := ap.docKey("corrupt-doc")
	if _, err := c.PutObject(ctx, env.bucket, docKey, bytes.NewReader([]byte("{not-json")), int64(len("{not-json")), minio.PutObjectOptions{}); err != nil {
		t.Fatalf("plant corrupt doc: %v", err)
	}
	var v map[string]string
	err := p.Load("corrupt-doc", &v)
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("Load of corrupt doc: want decode error, got %v", err)
	}
	if !strings.Contains(err.Error(), ".corrupt-") {
		t.Fatalf("decode error should name the backup object: %v", err)
	}
	// Original is gone; exactly one corrupt backup holds the raw bytes.
	if err := p.Load("corrupt-doc", &v); !errors.Is(err, ErrNotExist) {
		t.Fatalf("original must be removed after backup, got %v", err)
	}
	backupPrefix := ap.key("corrupt-doc.json.corrupt-")
	var backups []minio.ObjectInfo
	for obj := range c.ListObjects(ctx, env.bucket, minio.ListObjectsOptions{Prefix: backupPrefix, Recursive: true}) {
		if obj.Err != nil {
			t.Fatalf("list backups: %v", obj.Err)
		}
		backups = append(backups, obj)
	}
	if len(backups) != 1 {
		t.Fatalf("backup count = %d, want 1", len(backups))
	}
	body, err := c.GetObject(ctx, env.bucket, backups[0].Key, minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("get backup: %v", err)
	}
	data, _ := io.ReadAll(body)
	body.Close()
	if string(data) != "{not-json" {
		t.Fatalf("backup content = %q, want raw corrupt bytes", data)
	}
}

// --- unit tests (no docker) ----------------------------------------------

func TestOSS_ParseEndpoint(t *testing.T) {
	cases := []struct {
		in         string
		host, port string
		secure     bool
		wantErr    bool
	}{
		{"", "", "", false, true},
		{"http://127.0.0.1:9000", "127.0.0.1", "9000", false, false},
		{"https://s3.example.com", "s3.example.com", "", true, false},
		{"https://s3.example.com:443", "s3.example.com", "443", true, false},
		{"oss-cn-hangzhou.aliyuncs.com", "oss-cn-hangzhou.aliyuncs.com", "", true, false},
		{"127.0.0.1:19000", "127.0.0.1", "19000", true, false},
	}
	for _, c := range cases {
		ep, err := ParseOSSEndpoint(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseOSSEndpoint(%q): want error, got %+v", c.in, ep)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseOSSEndpoint(%q): %v", c.in, err)
			continue
		}
		if ep.Host != c.host || ep.Port != c.port || ep.Secure != c.secure {
			t.Errorf("ParseOSSEndpoint(%q) = %+v, want host=%q port=%q secure=%v", c.in, ep, c.host, c.port, c.secure)
		}
	}
}

func TestOSS_NewMissingBucket(t *testing.T) {
	if _, err := newOSSPersist(PersistConfig{Backend: BackendOSS, Endpoint: "http://127.0.0.1:1", Prefix: "x"}); err == nil {
		t.Fatal("expected error when bucket (Database) missing")
	}
}

func TestOSS_NewMissingEndpoint(t *testing.T) {
	if _, err := newOSSPersist(PersistConfig{Backend: BackendOSS, Database: "b", Prefix: "x"}); err == nil {
		t.Fatal("expected error when endpoint missing")
	}
}

func TestOSS_NewTunnelRefWithoutResolverIsExplicitError(t *testing.T) {
	SetTunnelResolver(nil)
	_, err := newOSSPersist(PersistConfig{
		Backend: BackendOSS, Endpoint: "https://db.example.com", Database: "b",
		Prefix: "x", TunnelRef: "host-abc",
	})
	if err == nil || !strings.Contains(err.Error(), "no tunnel resolver") {
		t.Fatalf("expected explicit tunnel-resolver error, got %v", err)
	}
}

func TestOSS_NewCredentialRefWithoutResolverIsExplicitError(t *testing.T) {
	SetCredentialResolver(nil)
	SetTunnelResolver(nil)
	_, err := newOSSPersist(PersistConfig{
		Backend: BackendOSS, Endpoint: "http://127.0.0.1:1", Database: "b",
		Prefix: "x", CredentialRef: "prof-1",
	})
	if err == nil || !strings.Contains(err.Error(), "no credential resolver") {
		t.Fatalf("expected explicit credential-resolver error, got %v", err)
	}
}
