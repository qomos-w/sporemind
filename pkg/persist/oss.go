package persist

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// BackendOSS is the S3-compatible object-storage backend (minio/aliyun OSS /
// Tencent COS / Huawei OBS / Cloudflare R2). One implementation covers every
// S3-compatible provider; local development uses a single minio container.
const BackendOSS BackendType = "oss"

const (
	ossDocSuffix = ".json"
	ossSeqFormat = "%020d" // zero-padded so lexical order == numeric order
)

// ossTestRootCAs, when non-nil, replaces the system roots for tunneled TLS
// verification. Test-only seam for the SNI e2e (self-signed certificates);
// production always verifies against system roots.
var ossTestRootCAs *x509.CertPool

// OSSPersist stores name→JSON documents as S3 objects. Document <name> lives
// at object key <prefix>/<name>.json; append-only records live as sequential
// objects under <prefix>/<name>/<seq> (no extension, matching FSPersist's
// "caller controls extension" convention so List does not surface append
// shards). Delete(<name>) removes the document object and cascades the whole
// <prefix>/<name>/ subtree, mirroring FSPersist's <actorID>/ namespace
// contract.
type OSSPersist struct {
	client *minio.Client
	bucket string
	prefix string // key namespace, no leading or trailing slash; may be ""

	appMu  sync.Mutex
	appSeq map[string]int64 // last assigned seq per append name
}

// newOSSPersist is the BackendFactory for BackendOSS. PersistConfig fields:
//   - Database: bucket name (non-sensitive, from yaml)
//   - Endpoint: service URL (http(s)://host[:port]); bare host[:port] → https
//   - Prefix:   key namespace (actor-type segment)
//   - CredentialRef: dbmanager profile id → AccessKey/Secret; empty = anonymous
//   - TunnelRef: sshmanager hostID; when set the client dials the tunnel's
//     local listener while TLS verification targets the real endpoint domain
//     (decision point 4: tunnel+https verifies real domain SNI via a custom
//     http.Transport whose DialContext dials the local addr and whose
//     TLSClientConfig.ServerName stays the real host).
func newOSSPersist(cfg PersistConfig) (Persist, error) {
	if cfg.Database == "" {
		return nil, errors.New("persist: oss backend requires bucket (config.Database)")
	}
	epURL, err := ParseOSSEndpoint(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	cred, err := ResolveCredential(cfg.CredentialRef)
	if err != nil {
		return nil, err
	}
	// Empty CredentialRef is the anonymous path (public buckets); an empty
	// access key signs as anonymous rather than dialing as a malformed user.
	static := credentials.NewStaticV4(cred.AccessKey, cred.Secret, "")

	// Resolve the dial target. Direct: the endpoint host:port. Tunnel: the
	// local listener address returned by the resolver; the real host still
	// drives TLS SNI and signing via a custom transport below.
	dialTarget := epURL.DialTarget()
	var transport http.RoundTripper // nil → minio builds its default transport
	if cfg.TunnelRef != "" {
		localAddr, terr := ResolveTunnel(cfg.TunnelRef, dialTarget)
		if terr != nil {
			return nil, terr
		}
		dialTarget = localAddr
		if epURL.Secure {
			transport = TunnelTransport(epURL.Host)
		}
		// tunnel + plain: SSH channel is already encrypted (decision point 4);
		// dial localAddr over plain HTTP, no custom transport needed.
	}

	opts := &minio.Options{
		Creds:        static,
		Secure:       epURL.Secure,
		Transport:    transport,
		Region:       "",                     // minio resolves via GetBucketLocation
		BucketLookup: minio.BucketLookupPath, // path-style: works for custom endpoints + tunnels
	}
	client, err := minio.New(dialTarget, opts)
	if err != nil {
		return nil, fmt.Errorf("persist: oss client: %w", err)
	}
	client.SetAppInfo("sporemind", "persist-oss")
	return &OSSPersist{
		client: client,
		bucket: cfg.Database,
		prefix: strings.Trim(cfg.Prefix, "/"),
	}, nil
}

// OSSEndpoint is the parsed S3-compatible service URL. Exported so external
// dialers (dbclient) reuse the same normalization — host/port plus the
// inferred secure flag drive both the dial target and the tunnel-vs-direct
// TLS decision.
type OSSEndpoint struct {
	Host   string
	Port   string
	Secure bool
}

// DialTarget returns the host:port string the minio client dials.
func (e OSSEndpoint) DialTarget() string {
	if e.Port == "" {
		if e.Secure {
			return net.JoinHostPort(e.Host, "443")
		}
		return net.JoinHostPort(e.Host, "80")
	}
	return net.JoinHostPort(e.Host, e.Port)
}

// ParseOSSEndpoint accepts a full URL (http(s)://host[:port]) or a bare
// host[:port] (https assumed). An empty endpoint is an explicit error: a
// network backend must know where to dial. Exported for external dialers
// (dbclient) so the OSS dial rules stay defined in exactly one place.
func ParseOSSEndpoint(endpoint string) (OSSEndpoint, error) {
	if endpoint == "" {
		return OSSEndpoint{}, errors.New("persist: oss backend requires endpoint (config.Endpoint)")
	}
	var raw string
	var secure bool
	switch {
	case strings.HasPrefix(endpoint, "https://"):
		raw, secure = strings.TrimPrefix(endpoint, "https://"), true
	case strings.HasPrefix(endpoint, "http://"):
		raw, secure = strings.TrimPrefix(endpoint, "http://"), false
	default:
		raw, secure = endpoint, true // bare host[:port] → https
	}
	// Parse as a synthetic URL so host/port/ip-literal parsing is uniform;
	// strip any trailing path (only the host:port is dial-relevant).
	u, err := url.Parse("scheme://" + raw)
	if err != nil || u.Hostname() == "" {
		return OSSEndpoint{}, fmt.Errorf("persist: oss endpoint %q: %w", endpoint, err)
	}
	return OSSEndpoint{Host: u.Hostname(), Port: u.Port(), Secure: secure}, nil
}

// TunnelTransport builds an *http.Transport that dials the tunnel's local
// listener while presenting the real domain in TLS (ServerName) and
// verifying the certificate against it. This preserves certificate validation
// when traffic is routed through an SSH tunnel whose local listener has no
// DNS entry for the real host — the same DialContext/ServerName split used by
// the llmclient proxy transport (pkg/llmclient/proxy.go). Exported for
// external dialers (dbclient) so the SNI-preserving transport is defined in
// exactly one place.
func TunnelTransport(tlsServerName string) *http.Transport {
	return &http.Transport{
		// Override the dial address: every TLS connection to the "real" host
		// is physically routed to the tunnel's local listener.
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}
			tlsCfg := &tls.Config{
				ServerName:         tlsServerName,
				MinVersion:         tls.VersionTLS12,
				RootCAs:            ossTestRootCAs, // nil → system roots
				InsecureSkipVerify: false,
			}
			tconn := tls.Client(conn, tlsCfg)
			if err := tconn.HandshakeContext(ctx); err != nil {
				conn.Close()
				return nil, err
			}
			return tconn, nil
		},
		TLSHandshakeTimeout:   10 * time.Second,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
}

// key joins the key namespace prefix with relative segments, omitting empty
// components so a bare name still maps cleanly when prefix is "".
func (o *OSSPersist) key(segments ...string) string {
	parts := make([]string, 0, len(segments)+1)
	if o.prefix != "" {
		parts = append(parts, o.prefix)
	}
	for _, s := range segments {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "/")
}

func (o *OSSPersist) docKey(name string) string {
	return o.key(name + ossDocSuffix)
}

func (o *OSSPersist) appendPrefix(name string) string {
	return o.key(name) + "/"
}

func (o *OSSPersist) ctx() context.Context {
	return context.Background()
}

// Save writes the JSON document as a single S3 object, clobbering any prior
// value. S3 PUT is atomic per object: a concurrent Save race resolves to the
// last-writer-wins value, never a torn document.
func (o *OSSPersist) Save(name string, v any) error {
	if err := validateName(name); err != nil {
		return err
	}
	data, err := encode(v)
	if err != nil {
		return fmt.Errorf("persist: oss save %q: %w", name, err)
	}
	ctx := o.ctx()
	if _, err := o.client.PutObject(ctx, o.bucket, o.docKey(name), bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{ContentType: "application/json"}); err != nil {
		return fmt.Errorf("persist: oss save %q: %w", name, err)
	}
	return nil
}

// Load reads the document object into v. A missing object yields
// ErrNotExist so callers distinguish first-start from a real I/O failure; a
// decode failure copies the corrupt bytes to <name>.json.corrupt-<ts> and
// removes the original, mirroring FSPersist's backup-then-clobber behaviour
// so the corrupt payload survives for manual recovery.
func (o *OSSPersist) Load(name string, v any) error {
	if err := validateName(name); err != nil {
		return err
	}
	ctx := o.ctx()
	obj, err := o.client.GetObject(ctx, o.bucket, o.docKey(name), minio.GetObjectOptions{})
	if err != nil {
		if isOSSNotFound(err) {
			return ErrNotExist
		}
		return fmt.Errorf("persist: oss load %q: %w", name, err)
	}
	defer obj.Close()
	data, rerr := io.ReadAll(obj)
	if rerr != nil {
		if isOSSNotFound(rerr) {
			return ErrNotExist
		}
		return fmt.Errorf("persist: oss load %q: %w", name, rerr)
	}
	if err := decode(data, v); err != nil {
		// Preserve the corrupt payload for recovery, then drop the original so
		// the next Save starts clean — same shape as the fs corrupt backup.
		backup := o.key(name + ossDocSuffix + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z"))
		if _, berr := o.client.PutObject(ctx, o.bucket, backup, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{}); berr == nil {
			_ = o.client.RemoveObject(ctx, o.bucket, o.docKey(name), minio.RemoveObjectOptions{})
			return fmt.Errorf("persist: oss decode %q: %w (corrupt object backed up to %s)", name, err, backup)
		}
		return fmt.Errorf("persist: oss decode %q: %w", name, err)
	}
	return nil
}

// Delete removes the document object and cascades the whole <prefix>/<name>/
// append subtree. Idempotent: a missing object or empty subtree is not an
// error, matching FSPersist's os.Remove semantics.
func (o *OSSPersist) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	ctx := o.ctx()
	// Remove the document; NoSuchKey is tolerated.
	if err := o.client.RemoveObject(ctx, o.bucket, o.docKey(name), minio.RemoveObjectOptions{}); err != nil && !isOSSNotFound(err) {
		return fmt.Errorf("persist: oss delete %q: %w", name, err)
	}
	// Cascade the append subtree: list every object under the prefix and
	// batch-delete via RemoveObjects (S3 has no recursive delete). Collecting
	// first keeps the list error path explicit; actor append subtrees are
	// bounded so memory is not a concern here.
	ap := o.appendPrefix(name)
	var toDelete []minio.ObjectInfo
	for obj := range o.client.ListObjects(ctx, o.bucket, minio.ListObjectsOptions{Prefix: ap, Recursive: true}) {
		if obj.Err != nil {
			return fmt.Errorf("persist: oss cascade list %q: %w", name, obj.Err)
		}
		toDelete = append(toDelete, obj)
	}
	if len(toDelete) == 0 {
		return nil
	}
	rmCh := make(chan minio.ObjectInfo, len(toDelete))
	for _, oi := range toDelete {
		rmCh <- oi
	}
	close(rmCh)
	for rerr := range o.client.RemoveObjects(ctx, o.bucket, rmCh, minio.RemoveObjectsOptions{}) {
		if rerr.Err != nil && !isOSSNotFound(rerr.Err) {
			return fmt.Errorf("persist: oss cascade delete %q: %w", name, rerr.Err)
		}
	}
	return nil
}

// List implements Lister. It recursively enumerates objects under the
// namespace joined with prefix and returns every <rel>.json key, stripping the
// scanned prefix and ".json" suffix — the same shape FSPersist returns from
// filepath.WalkDir. Append-seq objects carry no ".json" suffix, so they are
// excluded here and never pollute document enumeration.
func (o *OSSPersist) List(prefix string) ([]string, error) {
	scan := strings.Trim(prefix, "/")
	keyPrefix := o.key(scan)
	if scan != "" {
		keyPrefix += "/"
	}
	var names []string
	for obj := range o.client.ListObjects(o.ctx(), o.bucket, minio.ListObjectsOptions{Prefix: keyPrefix, Recursive: true}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("persist: oss list %q: %w", prefix, obj.Err)
		}
		key := obj.Key
		if !strings.HasPrefix(key, keyPrefix) {
			continue
		}
		rel := strings.TrimPrefix(key, keyPrefix)
		if !strings.HasSuffix(rel, ossDocSuffix) {
			continue
		}
		names = append(names, strings.TrimSuffix(rel, ossDocSuffix))
	}
	return names, nil
}

// Append implements Appender. S3 has no native append, so each call writes a
// new sequential object under <prefix>/<name>/<seq> (decision point 6). A
// single PUT is atomic per object; callers ensure each record is
// self-delimiting. The seq counter is process-local and seeded from the
// existing object listing on first use, so a restarted actor continues after
// the highest prior seq.
func (o *OSSPersist) Append(name string, data []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	seq, err := o.nextSeq(name)
	if err != nil {
		return err
	}
	key := o.key(name, fmt.Sprintf(ossSeqFormat, seq))
	ctx := o.ctx()
	if _, err := o.client.PutObject(ctx, o.bucket, key, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{}); err != nil {
		return fmt.Errorf("persist: oss append %q: %w", name, err)
	}
	return nil
}

// nextSeq returns the next sequence number for name, seeding the in-memory
// counter from the live object listing when the name is seen for the first
// time. Guarded by appMu so concurrent Appends within one process never
// collide (cross-process concurrency for a single actor name is out of scope:
// an actor ID is unique).
func (o *OSSPersist) nextSeq(name string) (int64, error) {
	o.appMu.Lock()
	defer o.appMu.Unlock()
	if o.appSeq == nil {
		o.appSeq = make(map[string]int64)
	}
	if _, ok := o.appSeq[name]; !ok {
		max, err := o.maxSeq(name)
		if err != nil {
			return 0, err
		}
		o.appSeq[name] = max
	}
	o.appSeq[name]++
	return o.appSeq[name], nil
}

// maxSeq scans the append subtree and returns the highest existing seq, or 0
// when the subtree is empty/absent.
func (o *OSSPersist) maxSeq(name string) (int64, error) {
	ap := o.appendPrefix(name)
	var max int64
	for obj := range o.client.ListObjects(o.ctx(), o.bucket, minio.ListObjectsOptions{Prefix: ap, Recursive: true}) {
		if obj.Err != nil {
			return 0, fmt.Errorf("persist: oss append scan %q: %w", name, obj.Err)
		}
		rel := strings.TrimPrefix(obj.Key, ap)
		if n, err := strconv.ParseInt(rel, 10, 64); err == nil && n > max {
			max = n
		}
	}
	return max, nil
}

// isOSSNotFound reports whether err is an S3 NoSuchKey response. minio
// surfaces missing-object cases (GetObject body read, RemoveObject) as
// ErrorResponse with code NoSuchKey; matching on the code (not the English
// message) keeps the mapping locale-proof.
func isOSSNotFound(err error) bool {
	return minio.ToErrorResponse(err).Code == "NoSuchKey"
}
