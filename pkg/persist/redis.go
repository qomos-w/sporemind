package persist

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/redis/go-redis/v9"
)

// BackendRedis is the network kv backend backed by a Redis server (B4).
// Every document is a JSON string value; documents, append payloads, and
// cascade subtrees live in disjoint key ranges under one namespace:
//
//	<ns>:d:<name>          document (Save/Load/Delete)
//	<ns>:d:<name>/*        sub-document cascade namespace (Delete reclaims)
//	<ns>:a:<name>          append payload (Appender, single-key APPEND)
//	<ns>:a:<name>/*        append cascade namespace (Delete reclaims)
//
// <ns> is backendNamespace(cfg): Database when set, else Prefix. The
// namespace plus the kind segment keeps stores sharing one Redis server
// invisible to each other (SCAN/Lister prefix isolation).
const BackendRedis BackendType = "redis"

// Key kind segments. ':' cannot be produced by validateName, and the
// namespace itself is caller-chosen, so kind ranges never collide with
// document names.
const (
	redisDocKind = "d"
	redisAppKind = "a"
)

// redisMu/redisHandles refcounts open clients by dial parameters (same
// principle as B2's ldbHandles): actor code constructs persist instances per
// actor and many of them dial the same endpoint, so they share one
// *redis.Client. Process-wide by construction; holds no actor business state.
var (
	redisMu      sync.Mutex
	redisHandles = map[string]*redisHandle{}
)

type redisHandle struct {
	c    *redis.Client
	refs int
}

// acquireRedis returns a shared client for opts keyed by key, dialing and
// health-checking (PING) a fresh client when this is the first reference.
// A failed health check is an explicit startup error — never a silent
// fallback to another backend (约束面).
func acquireRedis(key string, opts *redis.Options) (*redisHandle, error) {
	redisMu.Lock()
	defer redisMu.Unlock()
	if h, ok := redisHandles[key]; ok {
		h.refs++
		return h, nil
	}
	c := redis.NewClient(opts)
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("persist: redis ping %s: %w", opts.Addr, err)
	}
	h := &redisHandle{c: c, refs: 1}
	redisHandles[key] = h
	return h, nil
}

// release drops one reference; the client is closed with the last one.
func (h *redisHandle) release(key string) error {
	redisMu.Lock()
	if cur, ok := redisHandles[key]; ok && cur == h {
		if h.refs > 1 {
			h.refs--
			redisMu.Unlock()
			return nil
		}
		delete(redisHandles, key)
	}
	if h.refs <= 1 {
		h.refs = 0
		redisMu.Unlock()
		return h.c.Close()
	}
	h.refs--
	redisMu.Unlock()
	return nil
}

// RedisPersist implements Persist on a remote Redis server.
type RedisPersist struct {
	h   *redisHandle
	key string // share key in redisHandles
	ns  string // key namespace
}

// redisDialOptions builds the client dial options from cfg: endpoint parsing
// (bare host:port or a redis:// / rediss:// URL), tunnel resolution (B7: a
// TunnelRef routes the dial through the resolver's local forward address;
// the endpoint itself is the tunnel target), TLS per decision point 4
// (direct on, tunnel off, loopback exempt; an explicit URL scheme overrides
// the default), and credentials resolved at dial time via ResolveCredential
// (B1/B10: secrets never live in config; a rotated profile takes effect on
// reconnect).
func redisDialOptions(cfg PersistConfig) (*redis.Options, error) {
	ep := strings.TrimSpace(cfg.Endpoint)
	if ep == "" {
		return nil, fmt.Errorf("persist: redis requires Endpoint (host:port)")
	}
	opts := &redis.Options{DialTimeout: netOpTimeout}
	var explicitTLS *bool
	if strings.Contains(ep, "://") {
		u, err := redis.ParseURL(ep)
		if err != nil {
			return nil, fmt.Errorf("persist: redis endpoint %q: %w", cfg.Endpoint, err)
		}
		opts.Addr = u.Addr
		opts.TLSConfig = u.TLSConfig
		explicitTLS = boolPtr(u.TLSConfig != nil)
	} else {
		opts.Addr = ep
	}
	if cfg.TunnelRef != "" {
		// B7 wiring: dial the tunnel's local forward address; the endpoint
		// (without scheme) is the target as reachable from the SSH host.
		local, err := ResolveTunnel(cfg.TunnelRef, opts.Addr)
		if err != nil {
			return nil, err
		}
		opts.Addr = local
	}
	cred, err := ResolveCredential(cfg.CredentialRef)
	if err != nil {
		return nil, err
	}
	opts.Username = cred.Username
	opts.Password = cred.Password
	// Decision-point-4 default applies only when the endpoint did not
	// explicitly choose a scheme; the explicit scheme wins either way.
	if explicitTLS == nil {
		opts.TLSConfig = tlsConfigFor(cfg)
	} else if *explicitTLS && opts.TLSConfig == nil {
		opts.TLSConfig = tlsConfigFor(cfg)
	}
	return opts, nil
}

func boolPtr(b bool) *bool { return &b }

// newRedisPersist is the B1-registry factory for BackendRedis.
func newRedisPersist(cfg PersistConfig) (Persist, error) {
	opts, err := redisDialOptions(cfg)
	if err != nil {
		return nil, err
	}
	key := netHandleKey(opts.Username+"\x00"+opts.Password, cfg, opts.TLSConfig != nil)
	h, err := acquireRedis(key, opts)
	if err != nil {
		return nil, err
	}
	return &RedisPersist{h: h, key: key, ns: backendNamespace(cfg)}, nil
}

// Close releases this instance's reference to the shared client; the client
// is closed with the last reference. Optional capability, not part of Persist.
func (p *RedisPersist) Close() error {
	return p.h.release(p.key)
}

// docKey/appKey map a document name into its kind-segmented redis key.
func (p *RedisPersist) docKey(name string) string    { return p.ns + ":" + redisDocKind + ":" + name }
func (p *RedisPersist) appKey(name string) string    { return p.ns + ":" + redisAppKind + ":" + name }
func (p *RedisPersist) docPrefix(name string) string { return p.docKey(name) + "/" }
func (p *RedisPersist) appPrefix(name string) string { return p.appKey(name) + "/" }

// scanCollect returns all keys matching a glob pattern via SCAN (KEYS is
// forbidden: it blocks the server). SCAN can return duplicates during
// rehashing, so results are deduped. literal is the exact byte prefix the
// caller intends: document names may contain glob metacharacters ('*', '?',
// '[') — validateName permits them — so results are re-filtered by literal
// prefix to keep List/Delete semantics exact instead of glob-fuzzy.
func (p *RedisPersist) scanCollect(ctx context.Context, match, literal string) ([]string, error) {
	var keys []string
	seen := map[string]bool{}
	it := p.h.c.Scan(ctx, 0, match, 200).Iterator()
	for it.Next(ctx) {
		k := it.Val()
		if strings.HasPrefix(k, literal) && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	if err := it.Err(); err != nil {
		return nil, fmt.Errorf("persist: redis scan %q: %w", match, err)
	}
	return keys, nil
}

func (p *RedisPersist) Save(name string, v any) error {
	if err := validateName(name); err != nil {
		return err
	}
	data, err := encode(v)
	if err != nil {
		return fmt.Errorf("persist: encode: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	if err := p.h.c.Set(ctx, p.docKey(name), data, 0).Err(); err != nil {
		return fmt.Errorf("persist: redis set %q: %w", name, err)
	}
	return nil
}

func (p *RedisPersist) Load(name string, v any) error {
	if err := validateName(name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	data, err := p.h.c.Get(ctx, p.docKey(name)).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrNotExist
	}
	if err != nil {
		return fmt.Errorf("persist: redis get %q: %w", name, err)
	}
	if err := decode(data, v); err != nil {
		return fmt.Errorf("persist: decode %q: %w", name, err)
	}
	return nil
}

// Delete removes the document for name, its append payload, and cascades into
// the <name>/ sub-document and sub-append namespaces, mirroring FSPersist's
// <name>.json + <name>/ reclaim. It is idempotent: deleting a missing name is
// a no-op.
func (p *RedisPersist) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	keys := []string{p.docKey(name), p.appKey(name)}
	for _, kind := range []struct{ match, literal string }{
		{p.docPrefix(name) + "*", p.docPrefix(name)},
		{p.appPrefix(name) + "*", p.appPrefix(name)},
	} {
		sub, err := p.scanCollect(ctx, kind.match, kind.literal)
		if err != nil {
			return err
		}
		keys = append(keys, sub...)
	}
	if len(keys) > 0 {
		if err := p.h.c.Del(ctx, keys...).Err(); err != nil {
			return fmt.Errorf("persist: redis delete %q: %w", name, err)
		}
	}
	return nil
}

// List implements Lister with the same hierarchical semantics as
// FSPersist/LevelDBPersist: prefix addresses a directory in the name tree, so
// List("ws") and List("ws/") enumerate the ws/ subtree and never return the
// document named "ws" itself; List("") enumerates every document in the
// namespace. Append payloads are never returned.
func (p *RedisPersist) List(prefix string) ([]string, error) {
	scan := ""
	if prefix != "" {
		scan = strings.TrimSuffix(prefix, "/") + "/"
	}
	full := p.ns + ":" + redisDocKind + ":" + scan
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	keys, err := p.scanCollect(ctx, full+"*", full)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		names = append(names, strings.TrimPrefix(k, p.ns+":"+redisDocKind+":"))
	}
	return names, nil
}

// Append implements Appender (workflow decision point 6): the payload of one
// call is appended verbatim to a single key, so records keep write order and
// a single Append call is atomic server-side. The caller is responsible for
// self-delimiting records (trailing '\n' per line).
func (p *RedisPersist) Append(name string, data []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	if err := p.h.c.Append(ctx, p.appKey(name), string(data)).Err(); err != nil {
		return fmt.Errorf("persist: redis append %q: %w", name, err)
	}
	return nil
}

// appendPayload reads back the raw append payload for name; test companion to
// the Appender contract (RedisPersist has no BasePather, so the suite's
// readback subtests skip).
func (p *RedisPersist) appendPayload(name string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	data, err := p.h.c.Get(ctx, p.appKey(name)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}
