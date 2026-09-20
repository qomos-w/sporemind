package persist

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/studio-b12/gowebdav"
)

// BackendWebDAV is the document-over-HTTP backend (workflow card B8). It
// covers 坚果云 (app 专用密码), Nextcloud/ownCloud, 群晖 NAS and
// Apache/nginx mod_dav: the storage shape is "fs over HTTP" — one remote
// document per name, PROPFIND enumeration, collection DELETE for the
// <actorID>/ cascade — so it lines up naturally with the T2 document-path
// semantics. It is the one network backend whose conformance suite needs no
// container: golang.org/x/net/webdav serves in-process.
const BackendWebDAV BackendType = "webdav"

// webdavPersist implements Persist + Lister + Appender over a WebDAV
// collection. The layout mirrors FSPersist: document <name> lives at
// <root>/<name>.json and its child documents at <root>/<name>/, so
// Delete(name) reclaims the whole subtree with one collection DELETE.
type webdavPersist struct {
	client *gowebdav.Client
	// root is the remote collection prefix, e.g. "/documents/workspace"
	// (webdavRoot always yields at least "/", the server namespace root).
	// Documents map to root/<name>.json.
	root string
	// weakAtomic marks the degraded atomicity path: the server rejected
	// MOVE-with-overwrite, so Save falls back to a bare PUT and no longer
	// guarantees a crash cannot leave a truncated document (workflow
	// decision point 7; servers without MOVE make that guarantee
	// unattainable).
	weakAtomic atomic.Bool
	// appendMu serializes read-max-seq → write-record so concurrent Appends
	// on the same name can never reuse a sequence number.
	appendMu sync.Mutex
}

// webdavPathLock returns a per-remote-path RWMutex, mirroring FSPersist's
// fileLock. Save takes the write lock so concurrent Saves to the same name
// never collide on the shared <name>.json.tmp staging resource and the
// tmp→MOVE commit is observed atomically; Load takes the read lock so a
// reader can never observe a half-written document (x/net/webdav's MemFS
// truncates-then-writes within a PUT, so without this lock a concurrent GET
// can read a partial body).
var (
	webdavLocksMu sync.Mutex
	webdavLocks   = map[string]*sync.RWMutex{}
)

func webdavPathLock(path string) *sync.RWMutex {
	webdavLocksMu.Lock()
	defer webdavLocksMu.Unlock()
	if mu, ok := webdavLocks[path]; ok {
		return mu
	}
	mu := &sync.RWMutex{}
	webdavLocks[path] = mu
	return mu
}

// newWebDAVPersist constructs the webdav backend from cfg. Endpoint must be
// an http(s) URL of the WebDAV endpoint; Database and Prefix select the
// remote collection root (Database is the store-global namespace, Prefix the
// actor-type segment, mirroring fs' DataDir/Prefix join); CredentialRef
// resolves at dial time to Basic/Digest credentials or a Bearer token;
// TunnelRef routes the dial through an sshmanager local forward while TLS
// verification still targets the real endpoint hostname.
func newWebDAVPersist(cfg PersistConfig) (Persist, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("persist: webdav requires an Endpoint (the DAV collection URL)")
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("persist: webdav parse endpoint %q: %w", cfg.Endpoint, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("persist: webdav endpoint scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("persist: webdav endpoint must include a host")
	}

	cred, err := ResolveCredential(cfg.CredentialRef)
	if err != nil {
		return nil, err
	}

	var client *gowebdav.Client
	switch {
	case cred.Token != "":
		client = gowebdav.NewAuthClient(u.String(), gowebdav.NewPreemptiveAuth(bearerAuth{token: cred.Token}))
	case cred.Username != "" || cred.Password != "":
		// NewAutoAuth negotiates Basic vs Digest from the server's
		// WWW-Authenticate challenge (坚果云/Nextcloud app passwords are
		// Basic; Apache htDigest is Digest).
		client = gowebdav.NewClient(u.String(), cred.Username, cred.Password)
	default:
		client = gowebdav.NewClient(u.String(), "", "")
	}

	if cfg.TunnelRef != "" {
		localAddr, terr := ResolveTunnel(cfg.TunnelRef, u.Host)
		if terr != nil {
			return nil, terr
		}
		tlsConf := &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12}
		if webdavTestRootCAs != nil {
			tlsConf.RootCAs = webdavTestRootCAs
		}
		client.SetTransport(&http.Transport{
			// Every dial physically goes to the tunnel's local forward
			// port; the requested addr (real host:443) is dropped on
			// purpose — the tunnel is already bound to the target.
			// ServerName above keeps certificate verification on the
			// real domain.
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
				return d.DialContext(ctx, "tcp", localAddr)
			},
			TLSClientConfig:   tlsConf,
			ForceAttemptHTTP2: true,
		})
	}

	return &webdavPersist{client: client, root: webdavRoot(cfg)}, nil
}

// webdavRoot renders the remote collection prefix from Database and Prefix.
// Both are trimmed of slashes and joined under a single leading slash; an
// empty result means the namespace root.
func webdavRoot(cfg PersistConfig) string {
	var parts []string
	for _, p := range []string{cfg.Database, cfg.Prefix} {
		if p = strings.Trim(p, "/"); p != "" {
			parts = append(parts, p)
		}
	}
	return "/" + strings.Join(parts, "/")
}

// abs maps a validated slash-relative name onto the remote root.
func (p *webdavPersist) abs(name string) string {
	if p.root == "/" {
		return "/" + name
	}
	return p.root + "/" + name
}

// cleanName validates name against the document-path contract and renders
// its slash-cleaned relative form. Backslashes are rejected outright: on
// Windows validateName already rejects traversal spelled with them, and
// gowebdav would otherwise rewrite a surviving "\" to "/" server-side,
// turning a platform-dependent filename into a real traversal.
func (p *webdavPersist) cleanName(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	if strings.Contains(name, `\`) {
		return "", fmt.Errorf("persist: name must use forward slashes: %q", name)
	}
	return strings.TrimPrefix(path.Clean("/"+name), "/"), nil
}

// docPath returns the remote document path for name: <root>/<name>.json.
func (p *webdavPersist) docPath(name string) (string, error) {
	rel, err := p.cleanName(name)
	if err != nil {
		return "", err
	}
	return p.abs(rel) + ".json", nil
}

// mkdirAll creates the remote collection chain for dir one segment at a
// time, treating "already exists" (405) as success so repeated and partially
// existing chains are idempotent. gowebdav's own MkdirAll aborts its walk on
// the first existing segment, which breaks exactly the partially-existing
// case (e.g. "/root" present, "/root/a" missing).
func (p *webdavPersist) mkdirAll(dir string) error {
	cur := ""
	for _, seg := range strings.Split(strings.Trim(strings.TrimPrefix(dir, "/"), "/"), "/") {
		if seg == "" {
			continue
		}
		cur += "/" + seg
		if err := p.client.Mkdir(cur, 0755); err != nil && !isWebDAVStatus(err, http.StatusMethodNotAllowed) {
			return fmt.Errorf("persist: webdav mkdir %s: %w", cur, err)
		}
	}
	return nil
}

// Save persists v as the remote document <root>/<name>.json. The write goes
// to <name>.json.tmp first and is committed with MOVE Overwrite:T, so a
// reader never observes a truncated document (PUT alone is not atomic on
// every server — decision point 7). When the server rejects MOVE the backend
// degrades to a bare PUT, records weakAtomic, and that weaker guarantee is
// documented on the backend; the contract suite still passes because the
// fallback is only ever a complete PUT.
func (p *webdavPersist) Save(name string, v any) error {
	doc, err := p.docPath(name)
	if err != nil {
		return err
	}
	if err := p.mkdirAll(path.Dir(doc)); err != nil {
		return err
	}
	data, err := encode(v)
	if err != nil {
		return fmt.Errorf("persist: encode: %w", err)
	}
	mu := webdavPathLock(doc)
	mu.Lock()
	defer mu.Unlock()
	if p.weakAtomic.Load() {
		return p.write(doc, data)
	}
	tmp := doc + ".tmp"
	if err := p.write(tmp, data); err != nil {
		return err
	}
	if err := p.client.Rename(tmp, doc, true); err != nil {
		_ = p.client.Remove(tmp)
		if isMoveUnsupported(err) {
			p.weakAtomic.Store(true)
			return p.write(doc, data)
		}
		return fmt.Errorf("persist: webdav rename %s: %w", doc, err)
	}
	return nil
}

func (p *webdavPersist) write(path string, data []byte) error {
	if err := p.client.Write(path, data, 0644); err != nil {
		return fmt.Errorf("persist: webdav write %s: %w", path, err)
	}
	return nil
}

// Load restores v for name. A 404 maps to ErrNotExist (first start or after
// Delete); every other status is a genuine I/O error. A document that fails
// to decode is moved aside to <name>.json.corrupt-<timestamp>, mirroring
// FSPersist's corrupt backup.
func (p *webdavPersist) Load(name string, v any) error {
	doc, err := p.docPath(name)
	if err != nil {
		return err
	}
	mu := webdavPathLock(doc)
	mu.RLock()
	defer mu.RUnlock()
	data, err := p.client.Read(doc)
	if err != nil {
		if isWebDAVStatus(err, http.StatusNotFound) {
			return ErrNotExist
		}
		return fmt.Errorf("persist: webdav read %s: %w", doc, err)
	}
	if err := decode(data, v); err != nil {
		backup := doc + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
		if rerr := p.client.Rename(doc, backup, true); rerr == nil {
			return fmt.Errorf("persist: decode: %w (corrupt document backed up to %s)", err, backup)
		}
		return fmt.Errorf("persist: decode: %w", err)
	}
	return nil
}

// Delete removes the document <root>/<name>.json AND cascades into the
// same-named collection <root>/<name>/ (the <actorID>/ sub-document
// convention), so Delete(actorID) reclaims the actor's whole subtree in one
// call. A server that refuses a non-empty collection DELETE gets a
// PROPFIND-walk fallback that deletes children first.
func (p *webdavPersist) Delete(name string) error {
	rel, err := p.cleanName(name)
	if err != nil {
		return err
	}
	for _, target := range []string{p.abs(rel) + ".json", p.abs(rel)} {
		if err := p.client.RemoveAll(target); err != nil {
			if cerr := p.cascadeDelete(target); cerr != nil {
				return fmt.Errorf("persist: webdav delete %s: %w", target, err)
			}
		}
	}
	return nil
}

// cascadeDelete deletes a collection whose recursive DELETE the server
// rejected: enumerate the subtree, delete leaf documents first, then the
// collections deepest-first, then the target itself.
func (p *webdavPersist) cascadeDelete(target string) error {
	files, dirs, err := p.walk(target)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := p.client.RemoveAll(f); err != nil {
			return err
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := p.client.RemoveAll(dirs[i]); err != nil {
			return err
		}
	}
	return p.client.RemoveAll(target)
}

// walk enumerates the subtree under dir via repeated Depth:1 PROPFIND
// (ReadDir), returning all file paths and all directory paths. A missing dir
// yields empty results, matching List's "prefix that does not exist" rule.
func (p *webdavPersist) walk(dir string) (files []string, dirs []string, err error) {
	entries, err := p.client.ReadDir(dir)
	if err != nil {
		if isWebDAVStatus(err, http.StatusNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	for _, e := range entries {
		child := strings.TrimSuffix(dir, "/") + "/" + e.Name()
		if e.IsDir() {
			dirs = append(dirs, child)
			subFiles, subDirs, werr := p.walk(child)
			if werr != nil {
				return nil, nil, werr
			}
			files = append(files, subFiles...)
			dirs = append(dirs, subDirs...)
		} else {
			files = append(files, child)
		}
	}
	return files, dirs, nil
}

// List implements Lister with the same semantics as FSPersist: it walks the
// subtree under prefix and returns the persisted names relative to the
// backend root (forward slashes, ".json" stripped), matching the names
// accepted by Save/Delete. Only ".json" documents are enumerated, so
// transient ".tmp" writes and ".corrupt-" backups never leak, and neither do
// Appender records (their sequence objects use the ".part" suffix precisely
// so this enumeration — like fs' *.json rule and leveldb's doc-prefix scan —
// only ever surfaces Save documents). List("ws") and List("ws/") both
// enumerate the ws/ subtree, never the "ws" document itself; a missing
// prefix yields an empty list.
func (p *webdavPersist) List(prefix string) ([]string, error) {
	rel := strings.Trim(prefix, "/")
	base := p.abs(rel)
	// relOf renders a remote path relative to the backend root; at the
	// namespace root ("") the leading slash is trimmed directly.
	relOf := func(remote string) string {
		if p.root == "/" {
			return strings.TrimPrefix(remote, "/")
		}
		return strings.TrimPrefix(remote, p.root+"/")
	}
	var names []string
	files, _, err := p.walk(base)
	if err != nil {
		return nil, fmt.Errorf("persist: webdav list %q: %w", prefix, err)
	}
	for _, f := range files {
		name := relOf(f)
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		if strings.HasSuffix(name, ".tmp") || strings.Contains(name, ".corrupt-") {
			continue
		}
		names = append(names, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(names)
	return names, nil
}

// Append implements Appender. WebDAV has no native append, so each call
// writes one complete record as a sequential object <root>/<name>/<seq>.part
// (8-digit, zero-padded; lexicographic order equals write order). The
// read-max-seq → write-record sequence is serialized by appendMu, so
// concurrent Appends never reuse a sequence number and no record is ever
// torn or overwritten. Sequence objects keep the ".part" suffix — not
// ".json" — so List (a ".json" enumeration, mirroring fs) never exposes
// them, matching LevelDBPersist's "append records are never listed".
func (p *webdavPersist) Append(name string, data []byte) error {
	rel, err := p.cleanName(name)
	if err != nil {
		return err
	}
	p.appendMu.Lock()
	defer p.appendMu.Unlock()
	dir := p.abs(rel)
	if err := p.mkdirAll(dir); err != nil {
		return err
	}
	var seq uint64
	entries, err := p.client.ReadDir(dir)
	if err != nil && !isWebDAVStatus(err, http.StatusNotFound) {
		return fmt.Errorf("persist: webdav append scan %q: %w", name, err)
	}
	for _, e := range entries {
		if n, ok := parseSeq(e.Name()); ok && n > seq {
			seq = n
		}
	}
	seq++
	rec := fmt.Sprintf("%s/%08d.part", dir, seq)
	if err := p.client.Write(rec, data, 0644); err != nil {
		return fmt.Errorf("persist: webdav append %q seq %d: %w", name, seq, err)
	}
	return nil
}

// webdavRecords reads back the append records for name in write order; the
// companion to the contract suite's Appender tests (which need a BasePather
// this remote backend does not have).
func (p *webdavPersist) webdavRecords(name string) ([][]byte, error) {
	rel, err := p.cleanName(name)
	if err != nil {
		return nil, err
	}
	dir := p.abs(rel)
	entries, err := p.client.ReadDir(dir)
	if err != nil {
		if isWebDAVStatus(err, http.StatusNotFound) {
			return nil, nil
		}
		return nil, err
	}
	type rec struct {
		seq  uint64
		data []byte
	}
	var recs []rec
	for _, e := range entries {
		n, ok := parseSeq(e.Name())
		if !ok {
			continue
		}
		data, err := p.client.Read(strings.TrimSuffix(dir, "/") + "/" + e.Name())
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec{seq: n, data: data})
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].seq < recs[j].seq })
	out := make([][]byte, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.data)
	}
	return out, nil
}

// parseSeq parses a record object name of the form "%08d.part".
func parseSeq(name string) (uint64, bool) {
	if !strings.HasSuffix(name, ".part") {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSuffix(name, ".part"), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// webdavStatus extracts the HTTP status from a gowebdav error. gowebdav
// reports failures as *os.PathError wrapping StatusError; errors.As keeps
// this working if the library adds intermediate wrapping.
func webdavStatus(err error) (int, bool) {
	var pe *os.PathError
	if errors.As(err, &pe) {
		var se gowebdav.StatusError
		if errors.As(pe.Err, &se) {
			return se.Status, true
		}
	}
	return 0, false
}

func isWebDAVStatus(err error, code int) bool {
	s, ok := webdavStatus(err)
	return ok && s == code
}

// isMoveUnsupported reports whether a MOVE failure means "this server cannot
// do MOVE-with-overwrite" rather than a transient error: 405/501 (method not
// implemented), 412 (overwrite refused), 403 (policy-forbidden).
func isMoveUnsupported(err error) bool {
	for _, code := range []int{http.StatusMethodNotAllowed, http.StatusNotImplemented, http.StatusPreconditionFailed, http.StatusForbidden} {
		if isWebDAVStatus(err, code) {
			return true
		}
	}
	return false
}

// bearerAuth implements gowebdav.Authenticator for "Authorization: Bearer"
// (Nextcloud app tokens and other token-auth DAV endpoints). gowebdav ships
// Basic/Digest negotiation only, so the Bearer path is provided here.
type bearerAuth struct {
	token string
}

func (b bearerAuth) Authorize(_ *http.Client, rq *http.Request, _ string) error {
	rq.Header.Set("Authorization", "Bearer "+b.token)
	return nil
}

func (b bearerAuth) Verify(_ *http.Client, rs *http.Response, _ string) (bool, error) {
	if rs.StatusCode == http.StatusUnauthorized {
		return false, fmt.Errorf("webdav: bearer token rejected (401)")
	}
	return false, nil
}

func (b bearerAuth) Clone() gowebdav.Authenticator { return b }

func (b bearerAuth) Close() error { return nil }

// webdavTestRootCAs, when non-nil, replaces the root CA pool for tunneled
// webdav TLS dials. Production code never sets it (system roots apply); the
// tunnel e2e test points it at a private CA so certificate verification
// against the real ServerName is exercised end to end.
var webdavTestRootCAs *x509.CertPool
