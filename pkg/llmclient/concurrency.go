package llmclient

import (
	"context"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/semaphore"
)

// ProviderUsage is a non-blocking snapshot of one provider's concurrency.
type ProviderUsage struct {
	Inflight int64
	Max      int
}

// ProviderGate is a global per-provider concurrency limiter. Each provider
// has one weighted semaphore sized to its MaxConcurrency. Acquire blocks
// until a slot is free (queue semantics, not reject). A snapshot of current
// usage is available via Snapshot for the strategy layer to consume without
// blocking.
type ProviderGate struct {
	mu    sync.Mutex
	gates map[string]*gateEntry
}

type gateEntry struct {
	sem      *semaphore.Weighted
	max      int
	inflight atomic.Int64
}

func NewProviderGate() *ProviderGate {
	return &ProviderGate{gates: make(map[string]*gateEntry)}
}

// Register creates or updates the semaphore for a provider. Called when
// provider config is applied. If maxConcurrency <= 0 the provider is
// unregistered (no limit).
func (g *ProviderGate) Register(providerName string, maxConcurrency int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if maxConcurrency <= 0 {
		delete(g.gates, providerName)
		return
	}
	if existing, ok := g.gates[providerName]; ok && existing.max == maxConcurrency {
		return
	}
	g.gates[providerName] = &gateEntry{
		sem: semaphore.NewWeighted(int64(maxConcurrency)),
		max: maxConcurrency,
	}
}

// Acquire blocks until a slot is available for the provider, then returns a
// release function the caller must call (typically deferred). If the
// provider is not registered (no MaxConcurrency), it is a no-op pass-through.
// The context cancellation interrupts the wait.
func (g *ProviderGate) Acquire(ctx context.Context, providerName string) (func(), error) {
	g.mu.Lock()
	entry, ok := g.gates[providerName]
	g.mu.Unlock()
	if !ok {
		return func() {}, nil
	}
	if err := entry.sem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	entry.inflight.Add(1)
	return func() {
		entry.inflight.Add(-1)
		entry.sem.Release(1)
	}, nil
}

// Snapshot returns a non-blocking copy of all registered providers' current
// usage. Unregistered providers report Max=0 (no limit).
func (g *ProviderGate) Snapshot() map[string]ProviderUsage {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]ProviderUsage, len(g.gates))
	for name, entry := range g.gates {
		out[name] = ProviderUsage{
			Inflight: entry.inflight.Load(),
			Max:      entry.max,
		}
	}
	return out
}

// ── Legacy per-endpoint semaphore (kept for backward compat, unused by aggregator) ──

func concurrencyKey(endpoint, authToken string) string {
	return endpoint + "\x00" + authToken
}

// RegisterConcurrency ensures a semaphore exists for the given endpoint+token.
func RegisterConcurrency(endpoint, authToken string, n int) {
	if n <= 0 {
		return
	}
	key := concurrencyKey(endpoint, authToken)
	semPool.Store(key, semaphore.NewWeighted(int64(n)))
}

// ConcurrencyClient wraps a Client with per-key semaphore rate limiting.
type ConcurrencyClient struct {
	inner     Client
	endpoint  string
	authToken string
}

// NewConcurrencyClient wraps inner with concurrency control.
func NewConcurrencyClient(inner Client, endpoint, authToken string, maxConcurrency int) *ConcurrencyClient {
	RegisterConcurrency(endpoint, authToken, maxConcurrency)
	return &ConcurrencyClient{inner: inner, endpoint: endpoint, authToken: authToken}
}

func (c *ConcurrencyClient) Stream(ctx context.Context, req Request) (Stream, error) {
	key := concurrencyKey(c.endpoint, c.authToken)
	v, ok := semPool.Load(key)
	if !ok {
		return c.inner.Stream(ctx, req)
	}
	sem := v.(*semaphore.Weighted)
	if err := sem.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	s, err := c.inner.Stream(ctx, req)
	if err != nil {
		sem.Release(1)
		return nil, err
	}
	return &releaseStream{Stream: s, release: func() { sem.Release(1) }}, nil
}

type releaseStream struct {
	Stream
	releaseOnce sync.Once
	release     func()
}

func (s *releaseStream) Close() error {
	s.releaseOnce.Do(s.release)
	return s.Stream.Close()
}
