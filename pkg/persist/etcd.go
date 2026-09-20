package persist

import (
	"context"
	"crypto/tls"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// BackendETCD is the network kv backend backed by an etcd cluster (B4).
// Every document is a JSON string value; documents, append sequence
// counters, and append records live in disjoint key ranges under one
// namespace (workflow decision point 6: Appender = sequential keys):
//
//	/<ns>/d/<name>             document (Save/Load/Delete)
//	/<ns>/d/<name>/*           sub-document cascade namespace (Delete reclaims)
//	/<ns>/s/<name>             append sequence counter
//	/<ns>/a/<name>/<seq>       append record (zero-padded, lexicographic = write order)
//	/<ns>/a/<name>/*           append cascade namespace (Delete reclaims)
//
// <ns> is backendNamespace(cfg): Database when set, else Prefix. WithPrefix
// ranges give Lister enumeration and Delete cascade natively. etcd is
// gRPC/TCP: a B3 tunnel is just a local port to dial (ssh -L semantics), no
// special handling.
const BackendETCD BackendType = "etcd"

// Key kind segments, mirroring B2 goleveldb's d:/s:/a: disjoint ranges.
const (
	etcdDocKind = "d"
	etcdSeqKind = "s"
	etcdRecKind = "a"
)

// etcd append retry bound: each Compare-and-Put attempt loses only under
// concurrent Appends on the same name; a bound turns a pathological livelock
// into an explicit error instead of an unbounded stall.
const etcdAppendMaxAttempts = 64

// etcdMu/etcdHandles refcounts open clients by dial parameters, same
// principle as redisHandles and B2's ldbHandles.
var (
	etcdMu      sync.Mutex
	etcdHandles = map[string]*etcdHandle{}
)

type etcdHandle struct {
	c    *clientv3.Client
	refs int
}

// acquireEtcd returns a shared client for cfg keyed by key, dialing and
// health-checking (Status on the endpoint) a fresh client when this is the
// first reference. clientv3 dials lazily, so without the probe a dead server
// would surface as a mid-call error instead of the required explicit startup
// failure (约束面: never a silent fallback).
func acquireEtcd(key string, cfg clientv3.Config) (*etcdHandle, error) {
	etcdMu.Lock()
	defer etcdMu.Unlock()
	if h, ok := etcdHandles[key]; ok {
		h.refs++
		return h, nil
	}
	c, err := clientv3.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("persist: etcd dial %s: %w", strings.Join(cfg.Endpoints, ","), err)
	}
	// The probe uses its own short deadline: the client's retry interceptor
	// otherwise retries transport errors until the context expires, turning a
	// refused connection into a long stall before the explicit startup error.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Status(ctx, cfg.Endpoints[0]); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("persist: etcd status %s: %w", cfg.Endpoints[0], err)
	}
	h := &etcdHandle{c: c, refs: 1}
	etcdHandles[key] = h
	return h, nil
}

// release drops one reference; the client is closed with the last one.
func (h *etcdHandle) release(key string) error {
	etcdMu.Lock()
	if cur, ok := etcdHandles[key]; ok && cur == h {
		if h.refs > 1 {
			h.refs--
			etcdMu.Unlock()
			return nil
		}
		delete(etcdHandles, key)
	}
	if h.refs <= 1 {
		h.refs = 0
		etcdMu.Unlock()
		return h.c.Close()
	}
	h.refs--
	etcdMu.Unlock()
	return nil
}

// EtcdPersist implements Persist on a remote etcd cluster.
type EtcdPersist struct {
	h   *etcdHandle
	key string // share key in etcdHandles
	ns  string // key namespace
}

// etcdClientConfig builds the clientv3 dial config from cfg: endpoint
// parsing (bare host:port; an explicit http:// or https:// scheme overrides
// the decision-point-4 TLS default), tunnel resolution (B7: a TunnelRef
// routes the dial through the resolver's local forward address; the endpoint
// itself is the tunnel target), TLS per workflow decision point 4 (direct
// on, tunnel off, loopback exempt), and credentials resolved at dial time
// via ResolveCredential. It returns the normalized endpoint for health
// checking.
func etcdClientConfig(cfg PersistConfig) (clientv3.Config, error) {
	ep := strings.TrimSpace(cfg.Endpoint)
	if ep == "" {
		return clientv3.Config{}, fmt.Errorf("persist: etcd requires Endpoint (host:port)")
	}
	tlsOn := backendTLS(cfg)
	if strings.HasPrefix(ep, "http://") {
		ep = strings.TrimPrefix(ep, "http://")
		tlsOn = false
	}
	if strings.HasPrefix(ep, "https://") {
		ep = strings.TrimPrefix(ep, "https://")
		tlsOn = true
	}
	if cfg.TunnelRef != "" {
		// B7 wiring: dial the tunnel's local forward address; the endpoint
		// (without scheme) is the target as reachable from the SSH host.
		local, err := ResolveTunnel(cfg.TunnelRef, ep)
		if err != nil {
			return clientv3.Config{}, err
		}
		ep = local
	}
	cred, err := ResolveCredential(cfg.CredentialRef)
	if err != nil {
		return clientv3.Config{}, err
	}
	conf := clientv3.Config{
		Endpoints:   []string{ep},
		DialTimeout: 5 * time.Second,
		Username:    cred.Username,
		Password:    cred.Password,
	}
	if tlsOn {
		conf.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return conf, nil
}

// newEtcdPersist is the B1-registry factory for BackendETCD.
func newEtcdPersist(cfg PersistConfig) (Persist, error) {
	conf, err := etcdClientConfig(cfg)
	if err != nil {
		return nil, err
	}
	key := netHandleKey(conf.Username+"\x00"+conf.Password, cfg, conf.TLS != nil)
	h, err := acquireEtcd(key, conf)
	if err != nil {
		return nil, err
	}
	return &EtcdPersist{h: h, key: key, ns: backendNamespace(cfg)}, nil
}

// Close releases this instance's reference to the shared client; the client
// is closed with the last reference. Optional capability, not part of Persist.
func (p *EtcdPersist) Close() error {
	return p.h.release(p.key)
}

func (p *EtcdPersist) docKey(name string) string    { return "/" + p.ns + "/" + etcdDocKind + "/" + name }
func (p *EtcdPersist) seqKey(name string) string    { return "/" + p.ns + "/" + etcdSeqKind + "/" + name }
func (p *EtcdPersist) docPrefix(name string) string { return p.docKey(name) + "/" }
func (p *EtcdPersist) recPrefix(name string) string {
	return "/" + p.ns + "/" + etcdRecKind + "/" + name + "/"
}

func (p *EtcdPersist) Save(name string, v any) error {
	if err := validateName(name); err != nil {
		return err
	}
	data, err := encode(v)
	if err != nil {
		return fmt.Errorf("persist: encode: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	if _, err := p.h.c.Put(ctx, p.docKey(name), string(data)); err != nil {
		return fmt.Errorf("persist: etcd put %q: %w", name, err)
	}
	return nil
}

func (p *EtcdPersist) Load(name string, v any) error {
	if err := validateName(name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	resp, err := p.h.c.Get(ctx, p.docKey(name))
	if err != nil {
		return fmt.Errorf("persist: etcd get %q: %w", name, err)
	}
	if len(resp.Kvs) == 0 {
		return ErrNotExist
	}
	if err := decode(resp.Kvs[0].Value, v); err != nil {
		return fmt.Errorf("persist: decode %q: %w", name, err)
	}
	return nil
}

// Delete removes the document, its append sequence counter and records, and
// cascades into the <name>/ sub-document namespace, mirroring FSPersist's
// <name>.json + <name>/ reclaim. It is idempotent: deleting a missing name is
// a no-op.
func (p *EtcdPersist) Delete(name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	_, err := p.h.c.Txn(ctx).Then(
		clientv3.OpDelete(p.docKey(name)),
		clientv3.OpDelete(p.seqKey(name)),
		clientv3.OpDelete(p.docPrefix(name), clientv3.WithPrefix()),
		clientv3.OpDelete(p.recPrefix(name), clientv3.WithPrefix()),
	).Commit()
	if err != nil {
		return fmt.Errorf("persist: etcd delete %q: %w", name, err)
	}
	return nil
}

// List implements Lister with the same hierarchical semantics as
// FSPersist/LevelDBPersist: prefix addresses a directory in the name tree, so
// List("ws") and List("ws/") enumerate the ws/ subtree and never return the
// document named "ws" itself; List("") enumerates every document in the
// namespace. Sequence counters and append records are never returned.
func (p *EtcdPersist) List(prefix string) ([]string, error) {
	scan := ""
	if prefix != "" {
		scan = strings.TrimSuffix(prefix, "/") + "/"
	}
	full := "/" + p.ns + "/" + etcdDocKind + "/" + scan
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	resp, err := p.h.c.Get(ctx, full, clientv3.WithPrefix(), clientv3.WithKeysOnly())
	if err != nil {
		return nil, fmt.Errorf("persist: etcd list %q: %w", prefix, err)
	}
	names := make([]string, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		names = append(names, strings.TrimPrefix(string(kv.Key), "/"+p.ns+"/"+etcdDocKind+"/"))
	}
	return names, nil
}

// Append implements Appender (workflow decision point 6): each call writes
// one record under <name>/<seq>, where seq is a per-name counter bumped in
// the same Compare transaction. A Compare on the counter's revision makes
// concurrent Appends serialize server-side: a lost race retries with a fresh
// read instead of overwriting a record, so every record lands exactly once
// and lexicographic key order equals write order. The caller is responsible
// for self-delimiting records (trailing '\n' per line).
func (p *EtcdPersist) Append(name string, data []byte) error {
	if err := validateName(name); err != nil {
		return err
	}
	seqKey := p.seqKey(name)
	recBase := p.recPrefix(name)
	for attempt := 0; attempt < etcdAppendMaxAttempts; attempt++ {
		rctx, rcancel := context.WithTimeout(context.Background(), netOpTimeout)
		gresp, err := p.h.c.Get(rctx, seqKey)
		rcancel()
		if err != nil {
			return fmt.Errorf("persist: etcd append seq %q: %w", name, err)
		}
		var seq uint64
		var cmp clientv3.Cmp
		if len(gresp.Kvs) == 0 {
			seq = 1
			cmp = clientv3.Compare(clientv3.CreateRevision(seqKey), "=", 0)
		} else {
			n, err := strconv.ParseUint(string(gresp.Kvs[0].Value), 10, 64)
			if err != nil {
				return fmt.Errorf("persist: etcd append seq %q: corrupt counter %q: %w", name, gresp.Kvs[0].Value, err)
			}
			seq = n + 1
			cmp = clientv3.Compare(clientv3.ModRevision(seqKey), "=", gresp.Kvs[0].ModRevision)
		}
		recKey := recBase + fmt.Sprintf("%020d", seq)
		tctx, tcancel := context.WithTimeout(context.Background(), netOpTimeout)
		tresp, err := p.h.c.Txn(tctx).If(cmp).Then(
			clientv3.OpPut(seqKey, strconv.FormatUint(seq, 10)),
			clientv3.OpPut(recKey, string(data)),
		).Commit()
		tcancel()
		if err != nil {
			return fmt.Errorf("persist: etcd append %q: %w", name, err)
		}
		if tresp.Succeeded {
			return nil
		}
		// Lost the race against a concurrent Append: re-read and retry.
	}
	return fmt.Errorf("persist: etcd append %q: exceeded %d concurrency retries", name, etcdAppendMaxAttempts)
}

// appendRecords reads back the append records for name in write order; test
// companion to the Appender contract (EtcdPersist has no BasePather, so the
// suite's readback subtests skip).
func (p *EtcdPersist) appendRecords(name string) ([][]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), netOpTimeout)
	defer cancel()
	resp, err := p.h.c.Get(ctx, p.recPrefix(name), clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	out := make([][]byte, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		out = append(out, kv.Value)
	}
	return out, nil
}
