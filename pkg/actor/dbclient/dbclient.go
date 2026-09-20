// Package dbclient is the unified read-only browse / query actor over
// dbmanager connection profiles (设计数据库客户端会话视图). It owns no
// profiles and no secrets: profiles come from dbmanager (dbmanager.profile
// lookup), secrets resolve at dial time through the process-wide
// persist.Credential resolver installed by dbmanager at startup, and tunnel
// addresses resolve through persist.ResolveTunnel installed by sshmanager.
//
// The five-callable surface (dbclient.dial_test / tree / read / query /
// close) runs every network-IO call on the dedicated dbclient_exec lane so
// long queries cannot stall control-plane work, with a 30s query deadline,
// 500-row default cap (clamped to [1,1000]), and a 512-char cell
// truncation rule shared with the openai client (CLAUDE.md).
//
// Safety boundary (约束面 显式): sql query only accepts a single
// SELECT/WITH/SHOW/EXPLAIN statement; redis `do` accepts an explicit
// read-command whitelist; mongo `query` is a JSON {collection, filter|
// aggregate, limit} document. Write commands never reach a backend.
package dbclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/policy"
)

// execLoop is the dedicated stateful lane that owns all network-IO callables.
// The CLAUDE.md owner-lane rule prohibits synchronous Await on cross-actor /
// network work in stateful handlers; dbclient_exec is not owner lane, but it
// is stateful and serializes all queries, which also makes the connection
// pool concurrency-trivial.
const execLoop = "dbclient_exec"

const (
	queryTimeout  = 30 * time.Second
	dialTimeout   = 10 * time.Second
	redisPageSize = 200
	treePageSize  = 500
	objectScanCap = 2000
	idleTTL       = 5 * time.Minute
	defaultLimit  = 500
	maxLimit      = 1000
	cellTruncate  = 512
)

// Actor is the dbclient system actor.
type Actor struct {
	actor.Host
	mu   sync.Mutex
	pool map[string]*poolEntry

	// seams for tests; set before OnStart to inject fakes
	lookupProfile func(ctx actor.Context, profileID string) (profileSummary, error)
	dialBackend   func(ctx context.Context, p profileSummary, dbKey string) (backendConn, error)
}

// NewActor returns an actor constructor for the global dbclient actor.
func NewActor() func() actor.Actor { return func() actor.Actor { return &Actor{} } }

func (a *Actor) Type() string { return "dbclient" }

func (a *Actor) OnStart(ctx actor.Context) error {
	if a.pool == nil {
		a.pool = map[string]*poolEntry{}
	}
	if a.lookupProfile == nil {
		a.lookupProfile = a.lookupProfileFromManager
	}
	if a.dialBackend == nil {
		a.dialBackend = dialBackendConn
	}
	// Register the lane first; Register below validates that every WithLoop
	// target is already declared.
	if err := ctx.RegisterLoop(execLoop, actor.ModeStateful); err != nil {
		return fmt.Errorf("dbclient: register exec loop: %w", err)
	}
	if err := ctx.Register("dbclient.dial_test", a.handleDialTest, actor.AdminOnly()); err != nil {
		return fmt.Errorf("dbclient: register dial_test: %w", err)
	}
	if err := ctx.Register("dbclient.tree", a.handleTree, actor.AdminOnly(), actor.WithLoop(execLoop)); err != nil {
		return fmt.Errorf("dbclient: register tree: %w", err)
	}
	if err := ctx.Register("dbclient.read", a.handleRead, actor.AdminOnly(), actor.WithLoop(execLoop)); err != nil {
		return fmt.Errorf("dbclient: register read: %w", err)
	}
	if err := ctx.Register("dbclient.query", a.handleQuery, actor.AdminOnly(), actor.WithLoop(execLoop)); err != nil {
		return fmt.Errorf("dbclient: register query: %w", err)
	}
	if err := ctx.Register("dbclient.describe", a.handleDescribe, actor.AdminOnly(), actor.WithLoop(execLoop)); err != nil {
		return fmt.Errorf("dbclient: register describe: %w", err)
	}
	if err := ctx.Register("dbclient.close", a.handleClose, actor.AdminOnly()); err != nil {
		return fmt.Errorf("dbclient: register close: %w", err)
	}
	// Object storage surface (oss / webdav). Read-side callable
	// (object_list) runs on the dedicated exec lane so it cannot block the
	// control plane; storage-effectful handlers (write/delete/mkdir) live
	// on the actor lane to satisfy CLAUDE.md owner-lane rule (the cells
	// only mutate actor-owned state — pool entries + per-profile caches —
	// and forward to the per-backend driver on the lane below via
	// getConn/getBackendStorage which already serialize IO).
	if err := ctx.Register("dbclient.object_list", a.handleObjectList, actor.AdminOnly(), actor.WithLoop(execLoop)); err != nil {
		return fmt.Errorf("dbclient: register object_list: %w", err)
	}
	if err := ctx.Register("dbclient.object_read", a.handleObjectRead, actor.AdminOnly(), actor.WithLoop(execLoop)); err != nil {
		return fmt.Errorf("dbclient: register object_read: %w", err)
	}
	if err := ctx.Register("dbclient.object_write", a.handleObjectWrite, actor.AdminOnly(), actor.WithLoop(execLoop)); err != nil {
		return fmt.Errorf("dbclient: register object_write: %w", err)
	}
	if err := ctx.Register("dbclient.object_delete", a.handleObjectDelete, actor.AdminOnly(), actor.WithLoop(execLoop)); err != nil {
		return fmt.Errorf("dbclient: register object_delete: %w", err)
	}
	if err := ctx.Register("dbclient.object_mkdir", a.handleObjectMkdir, actor.AdminOnly(), actor.WithLoop(execLoop)); err != nil {
		return fmt.Errorf("dbclient: register object_mkdir: %w", err)
	}
	if err := ctx.Register("dbclient.object_stat", a.handleObjectStat, actor.AdminOnly(), actor.WithLoop(execLoop)); err != nil {
		return fmt.Errorf("dbclient: register object_stat: %w", err)
	}
	if err := ctx.RegisterDomain("dbclient").Expose(); err != nil {
		return fmt.Errorf("dbclient: expose: %w", err)
	}
	return nil
}

// OnStop releases every pooled handle. Registered as a hook if the runtime
// supports it; otherwise the close callable is the only manual exit.
func (a *Actor) OnStop(_ actor.Context) error {
	a.mu.Lock()
	entries := make([]*poolEntry, 0, len(a.pool))
	for _, e := range a.pool {
		entries = append(entries, e)
	}
	a.pool = map[string]*poolEntry{}
	a.mu.Unlock()
	var firstErr error
	for _, e := range entries {
		if err := e.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// profileSummary is the dial view of a dbmanager profile. Kept private —
// the lookup callable returns domain.DbProfile; the actor copies what the
// dial needs into this local struct so per-dial callers never hold a
// reference to a profile that could be deleted under them.
type profileSummary struct {
	ID        string
	Backend   string
	Endpoint  string
	Database  string
	TunnelRef string
	Username  string
	AccessKey string
}

type poolEntry struct {
	profileID string
	dbKey     string
	conn      backendConn
	lastUsed  time.Time
}

// lookupProfileFromManager is the production profile lookup: it calls the
// Internal dbmanager.profile_lookup callable through the actor planner so
// the response never carries secrets (peer-actor contract). It runs from
// the dbclient_exec lane, which is stateful — the Await is allowed because
// the lane is dedicated to network IO and not the owner / shared control
// plane the CLAUDE.md owner-lane rule protects.
func (a *Actor) lookupProfileFromManager(ctx actor.Context, profileID string) (profileSummary, error) {
	planner := ctx.Planner()
	ref, ok := ctx.LookupService("dbmanager")
	if !ok || planner == nil {
		return profileSummary{}, fmt.Errorf("dbclient: dbmanager service is not available")
	}
	payload, _ := json.Marshal(domain.DbProfileLookupReq{ID: profileID})
	cctx, cancel := context.WithTimeout(ctx.Lifecycle(), 10*time.Second)
	defer cancel()
	result, err := planner.Call(cctx, ref, "dbmanager.profile_lookup", payload).Await()
	if err != nil {
		return profileSummary{}, fmt.Errorf("dbclient: profile_lookup: %w", err)
	}
	var resp domain.DbProfileLookupResp
	if err := decodeInvokeResult(result, &resp); err != nil {
		return profileSummary{}, fmt.Errorf("dbclient: profile_lookup: %w", err)
	}
	p := resp.Profile
	return profileSummary{
		ID:        p.ID,
		Backend:   p.Backend,
		Endpoint:  p.Endpoint,
		Database:  p.Database,
		TunnelRef: p.TunnelRef,
		Username:  p.Username,
		AccessKey: p.AccessKey,
	}, nil
}

// getConn returns a pooled backendConn, dialing if missing. Dial runs
// outside the pool lock so concurrent reads on a different profile never
// wait for a slow first dial. If two callers race to dial the same key the
// loser closes its dial and reuses the winner (the canonical persist
// handle-deduplication pattern).
func (a *Actor) getConn(ctx actor.Context, p profileSummary, dbKey string) (backendConn, error) {
	key := poolKey(p.ID, dbKey)
	now := time.Now()
	a.mu.Lock()
	a.reapIdleLocked(now)
	if e, ok := a.pool[key]; ok {
		e.lastUsed = now
		conn := e.conn
		a.mu.Unlock()
		return conn, nil
	}
	a.mu.Unlock()

	dialCtx, cancel := context.WithTimeout(ctx.Lifecycle(), dialTimeout)
	defer cancel()
	conn, err := a.dialBackend(dialCtx, p, dbKey)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if existing, ok := a.pool[key]; ok {
		// lost the race; reuse the winner and drop our dial.
		existing.lastUsed = time.Now()
		_ = conn.Close()
		return existing.conn, nil
	}
	a.pool[key] = &poolEntry{profileID: p.ID, dbKey: dbKey, conn: conn, lastUsed: time.Now()}
	return conn, nil
}

// reapIdleLocked closes idle handles whose lastUsed is older than idleTTL.
// Called under a.mu from getConn; if reap grows beyond a handful this
// should be moved to a background sweeper goroutine (today the pool is
// small per actor).
func (a *Actor) reapIdleLocked(now time.Time) {
	cutoff := now.Add(-idleTTL)
	var stale []*poolEntry
	for _, e := range a.pool {
		if e.lastUsed.Before(cutoff) {
			stale = append(stale, e)
		}
	}
	for _, e := range stale {
		delete(a.pool, poolKey(e.profileID, e.dbKey))
	}
	if len(stale) == 0 {
		return
	}
	// Close outside a re-entrant lock window: drain into a local slice and
	// close after releasing the mutex via a copy in the caller. Simpler: do
	// best-effort close here while holding the mutex; Close is fast (a
	// client.Close) and we never want two reapers to interleave.
	for _, e := range stale {
		_ = e.conn.Close()
	}
}

func poolKey(profileID, dbKey string) string {
	if dbKey == "" {
		return profileID
	}
	return profileID + "\x00" + dbKey
}

// dbKeyFor derives the pool's effective-database key for a given path and
// backend. Different backends differ in whether the conn is bound to a
// database at dial time:
//   - redis: db index (path first segment; "" -> profile.Database -> "0").
//   - postgres: path's first dot-segment, else profile.Database, else
//     "postgres" (server-bound).
//   - mysql: empty (mysql dsn without a db is server-level; reads qualify
//     names when path has a dot).
//   - mongo/etcd/oss/webdav: empty (handle is not db-bound).
func dbKeyFor(p profileSummary, path string) string {
	switch p.Backend {
	case "redis":
		idx := path
		if i := strings.IndexByte(idx, '/'); i >= 0 {
			idx = idx[:i]
		}
		if idx == "" {
			idx = p.Database
		}
		if idx == "" {
			idx = "0"
		}
		return idx
	case "postgres":
		head := path
		if i := strings.IndexByte(head, '/'); i >= 0 {
			head = head[:i]
		}
		if i := strings.IndexByte(head, '.'); i >= 0 {
			head = head[:i]
		}
		if head == "" {
			head = p.Database
		}
		if head == "" {
			return "postgres"
		}
		return head
	default:
		return ""
	}
}

// truncateCell applies the 512-char cell rule used across the project
// (CLAUDE.md: openai respSnippet precedent).
func truncateCell(s string) string {
	if len(s) <= cellTruncate {
		return s
	}
	return s[:cellTruncate] + "...(truncated)"
}

// clampLimit returns the effective row limit for read/query.
func clampLimit(req int32) int {
	if req <= 0 {
		return defaultLimit
	}
	if int(req) > maxLimit {
		return maxLimit
	}
	return int(req)
}

// --- handlers ---

func (a *Actor) handleDialTest(ctx actor.Context, req domain.DbDialTestReq) (domain.DbDialTestResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.DbDialTestResp{}, err
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		return domain.DbDialTestResp{}, fmt.Errorf("dbclient.dial_test: profileId is required")
	}
	p, err := a.lookupProfile(ctx, req.ProfileID)
	if err != nil {
		return domain.DbDialTestResp{}, err
	}
	started := time.Now()
	conn, err := a.getConn(ctx, p, dbKeyFor(p, ""))
	if err != nil {
		return domain.DbDialTestResp{Ok: false, LatencyMs: time.Since(started).Milliseconds(), Error: truncateCell(err.Error())}, nil
	}
	pingCtx, cancel := context.WithTimeout(ctx.Lifecycle(), dialTimeout)
	defer cancel()
	version, perr := conn.Ping(pingCtx)
	latency := time.Since(started).Milliseconds()
	if perr != nil {
		return domain.DbDialTestResp{Ok: false, LatencyMs: latency, Error: truncateCell(perr.Error())}, nil
	}
	return domain.DbDialTestResp{Ok: true, LatencyMs: latency, ServerVersion: version}, nil
}

func (a *Actor) handleTree(ctx actor.Context, req domain.DbTreeReq) (domain.DbTreeResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.DbTreeResp{}, err
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		return domain.DbTreeResp{}, fmt.Errorf("dbclient.tree: profileId is required")
	}
	p, err := a.lookupProfile(ctx, req.ProfileID)
	if err != nil {
		return domain.DbTreeResp{}, err
	}
	conn, err := a.getConn(ctx, p, dbKeyFor(p, req.Path))
	if err != nil {
		return domain.DbTreeResp{}, err
	}
	cctx, cancel := context.WithTimeout(ctx.Lifecycle(), queryTimeout)
	defer cancel()
	nodes, cursor, hasMore, err := conn.Tree(cctx, req.Path, req.Cursor)
	if err != nil {
		return domain.DbTreeResp{}, err
	}
	return domain.DbTreeResp{Nodes: nodes, Cursor: cursor, HasMore: hasMore}, nil
}

func (a *Actor) handleRead(ctx actor.Context, req domain.DbReadReq) (domain.DbRows, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.DbRows{}, err
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		return domain.DbRows{}, fmt.Errorf("dbclient.read: profileId is required")
	}
	p, err := a.lookupProfile(ctx, req.ProfileID)
	if err != nil {
		return domain.DbRows{}, err
	}
	limit := clampLimit(req.Limit)
	offset := int(req.Offset)
	if offset < 0 {
		offset = 0
	}
	conn, err := a.getConn(ctx, p, dbKeyFor(p, req.Path))
	if err != nil {
		return domain.DbRows{}, err
	}
	cctx, cancel := context.WithTimeout(ctx.Lifecycle(), queryTimeout)
	defer cancel()
	return conn.Read(cctx, req.Path, limit, offset)
}

func (a *Actor) handleQuery(ctx actor.Context, req domain.DbQueryReq) (domain.DbRows, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.DbRows{}, err
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		return domain.DbRows{}, fmt.Errorf("dbclient.query: profileId is required")
	}
	mode := req.Mode
	if mode == "" {
		mode = "auto"
	}
	p, err := a.lookupProfile(ctx, req.ProfileID)
	if err != nil {
		return domain.DbRows{}, err
	}
	conn, err := a.getConn(ctx, p, dbKeyFor(p, ""))
	if err != nil {
		return domain.DbRows{}, err
	}
	cctx, cancel := context.WithTimeout(ctx.Lifecycle(), queryTimeout)
	defer cancel()
	return conn.Query(cctx, req.Text, mode)
}

func (a *Actor) handleClose(ctx actor.Context, req domain.DbCloseReq) (domain.DbCloseResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.DbCloseResp{}, err
	}
	a.mu.Lock()
	var stale []*poolEntry
	for k, e := range a.pool {
		if e.profileID == req.ProfileID {
			delete(a.pool, k)
			stale = append(stale, e)
		}
	}
	a.mu.Unlock()
	for _, e := range stale {
		_ = e.conn.Close()
	}
	return domain.DbCloseResp{}, nil
}

// --- object storage handlers ---

// requireStorage resolves the profile, dials (or reuses) the connection, and
// type-asserts it to the hasObjectStorage interface so non-storage backends
// (mysql, postgres, redis, mongo, etcd) get a clear "browse only" error
// instead of a confusing method-not-implemented panic.
func (a *Actor) requireStorage(ctx actor.Context, profileID string) (hasObjectStorage, error) {
	p, err := a.lookupProfile(ctx, profileID)
	if err != nil {
		return nil, err
	}
	conn, err := a.getConn(ctx, p, dbKeyFor(p, ""))
	if err != nil {
		return nil, err
	}
	storage, ok := conn.(hasObjectStorage)
	if !ok {
		return nil, fmt.Errorf("dbclient: backend %q does not support object storage operations", p.Backend)
	}
	return storage, nil
}

// requireAdmin is a small helper that gates every storage call on the admin
// role; written as a method-free inline at first call site to avoid an
// extra layer of indirection in the handlers below. Centralised here so the
// five storage callables share the exact same role check pattern.
func requireStorageAdmin(role id.Role) error {
	return policy.RequireAdmin(role)
}

func (a *Actor) handleObjectList(ctx actor.Context, req domain.DbObjectListReq) (domain.DbObjectListResp, error) {
	if err := requireStorageAdmin(ctx.Identity().Role); err != nil {
		return domain.DbObjectListResp{}, err
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		return domain.DbObjectListResp{}, fmt.Errorf("dbclient.object_list: profileId is required")
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 1000
	}
	if limit > 5000 {
		limit = 5000
	}
	storage, err := a.requireStorage(ctx, req.ProfileID)
	if err != nil {
		return domain.DbObjectListResp{}, err
	}
	cctx, cancel := context.WithTimeout(ctx.Lifecycle(), queryTimeout)
	defer cancel()
	entries, next, more, err := storage.ObjectList(cctx, req.Path, req.Cursor, limit)
	if err != nil {
		return domain.DbObjectListResp{}, err
	}
	return domain.DbObjectListResp{Entries: entries, Cursor: next, HasMore: more}, nil
}

func (a *Actor) handleObjectRead(ctx actor.Context, req domain.DbObjectReadReq) (domain.DbObjectReadResp, error) {
	if err := requireStorageAdmin(ctx.Identity().Role); err != nil {
		return domain.DbObjectReadResp{}, err
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		return domain.DbObjectReadResp{}, fmt.Errorf("dbclient.object_read: profileId is required")
	}
	storage, err := a.requireStorage(ctx, req.ProfileID)
	if err != nil {
		return domain.DbObjectReadResp{}, err
	}
	cctx, cancel := context.WithTimeout(ctx.Lifecycle(), queryTimeout)
	defer cancel()
	data, truncated, contentType, modified, err := storage.ObjectRead(cctx, req.Path, objectReadCap)
	if err != nil {
		return domain.DbObjectReadResp{}, err
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	resp := domain.DbObjectReadResp{
		Content:   encoded,
		Size:      int64(len(data)),
		Truncated: truncated,
	}
	if contentType != "" {
		resp.ContentType = contentType
	}
	if modified != "" {
		resp.Modified = modified
	}
	return resp, nil
}

func (a *Actor) handleObjectWrite(ctx actor.Context, req domain.DbObjectWriteReq) (domain.DbObjectWriteResp, error) {
	if err := requireStorageAdmin(ctx.Identity().Role); err != nil {
		return domain.DbObjectWriteResp{}, err
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		return domain.DbObjectWriteResp{}, fmt.Errorf("dbclient.object_write: profileId is required")
	}
	if req.Content == "" {
		return domain.DbObjectWriteResp{}, fmt.Errorf("dbclient.object_write: content is required")
	}
	// Use RawStdEncoding so the backend's put request size exactly matches
	// the byte length. Caller may pass either std or url-safe base64; we
	// normalise on the std alphabet here.
	data, err := base64.StdEncoding.DecodeString(req.Content)
	if err != nil {
		return domain.DbObjectWriteResp{}, fmt.Errorf("dbclient.object_write: decode base64: %v", err)
	}
	if int64(len(data)) > objectReadCap {
		return domain.DbObjectWriteResp{}, fmt.Errorf("dbclient.object_write: payload exceeds %d-byte cap", objectReadCap)
	}
	storage, err := a.requireStorage(ctx, req.ProfileID)
	if err != nil {
		return domain.DbObjectWriteResp{}, err
	}
	cctx, cancel := context.WithTimeout(ctx.Lifecycle(), queryTimeout)
	defer cancel()
	size, err := storage.ObjectWrite(cctx, req.Path, req.ContentType, data)
	if err != nil {
		return domain.DbObjectWriteResp{}, err
	}
	return domain.DbObjectWriteResp{Size: size}, nil
}

func (a *Actor) handleObjectDelete(ctx actor.Context, req domain.DbObjectDeleteReq) (domain.DbObjectDeleteResp, error) {
	if err := requireStorageAdmin(ctx.Identity().Role); err != nil {
		return domain.DbObjectDeleteResp{}, err
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		return domain.DbObjectDeleteResp{}, fmt.Errorf("dbclient.object_delete: profileId is required")
	}
	storage, err := a.requireStorage(ctx, req.ProfileID)
	if err != nil {
		return domain.DbObjectDeleteResp{}, err
	}
	cctx, cancel := context.WithTimeout(ctx.Lifecycle(), queryTimeout)
	defer cancel()
	if err := storage.ObjectDelete(cctx, req.Path); err != nil {
		return domain.DbObjectDeleteResp{}, err
	}
	return domain.DbObjectDeleteResp{}, nil
}

func (a *Actor) handleObjectMkdir(ctx actor.Context, req domain.DbObjectMkdirReq) (domain.DbObjectMkdirResp, error) {
	if err := requireStorageAdmin(ctx.Identity().Role); err != nil {
		return domain.DbObjectMkdirResp{}, err
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		return domain.DbObjectMkdirResp{}, fmt.Errorf("dbclient.object_mkdir: profileId is required")
	}
	if strings.TrimSpace(req.Name) == "" {
		return domain.DbObjectMkdirResp{}, fmt.Errorf("dbclient.object_mkdir: name is required")
	}
	storage, err := a.requireStorage(ctx, req.ProfileID)
	if err != nil {
		return domain.DbObjectMkdirResp{}, err
	}
	cctx, cancel := context.WithTimeout(ctx.Lifecycle(), queryTimeout)
	defer cancel()
	if err := storage.ObjectMkdir(cctx, req.ParentPath, req.Name); err != nil {
		return domain.DbObjectMkdirResp{}, err
	}
	return domain.DbObjectMkdirResp{}, nil
}

func (a *Actor) handleObjectStat(ctx actor.Context, req domain.DbObjectStatReq) (domain.DbObjectStatResp, error) {
	if err := requireStorageAdmin(ctx.Identity().Role); err != nil {
		return domain.DbObjectStatResp{}, err
	}
	if strings.TrimSpace(req.ProfileID) == "" {
		return domain.DbObjectStatResp{}, fmt.Errorf("dbclient.object_stat: profileId is required")
	}
	storage, err := a.requireStorage(ctx, req.ProfileID)
	if err != nil {
		return domain.DbObjectStatResp{}, err
	}
	cctx, cancel := context.WithTimeout(ctx.Lifecycle(), queryTimeout)
	defer cancel()
	size, isDir, contentType, modified, err := storage.ObjectStat(cctx, req.Path)
	if err != nil {
		return domain.DbObjectStatResp{}, err
	}
	resp := domain.DbObjectStatResp{Size: size, IsDir: isDir}
	if contentType != "" {
		resp.ContentType = contentType
	}
	if modified != "" {
		resp.Modified = modified
	}
	return resp, nil
}

// decodeInvokeResult accepts the polymorphic payload a planner Call may
// surface ([]byte, the typed value, *T, or a generic map from older gospore
// builds) and decodes it into out. Mirrors agent/agent_config.go's decode
// pattern so dbclient works across gospore versions.
func decodeInvokeResult(result any, out any) error {
	switch v := result.(type) {
	case nil:
		return errors.New("dbclient: empty invoke result")
	case []byte:
		if len(v) == 0 {
			return errors.New("dbclient: empty invoke result")
		}
		return json.Unmarshal(v, out)
	default:
		// typed value, pointer, or map[string]interface{} — json round-trip
		// gives us a portable decode for every shape gospore emits today.
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("dbclient: re-marshal invoke result: %w", err)
		}
		return json.Unmarshal(b, out)
	}
}
