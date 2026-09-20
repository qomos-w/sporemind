package aiaggregator

import (
	"sync"

	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/service/aistatsquery"
)

// statsCache is a read-only snapshot of per-unit telemetry and per-provider
// concurrency usage, refreshed asynchronously by a background goroutine
// (startStatsRefresher). The dispatch path (SmartStrategy) only reads from it
// — never blocks on an actor call. A nil/empty cache simply means "no
// telemetry yet"; the strategy falls back to pool order.
type statsCache struct {
	mu          sync.RWMutex
	entries     map[aistatsquery.Key]aistatsquery.Stats
	providerUse map[string]llmclient.ProviderUsage
}

func newStatsCache() *statsCache {
	return &statsCache{
		entries:     make(map[aistatsquery.Key]aistatsquery.Stats),
		providerUse: make(map[string]llmclient.ProviderUsage),
	}
}

// snapshot returns a copy of the current stats map. Safe for concurrent use.
// The caller may mutate the returned map freely.
func (c *statsCache) snapshot() map[aistatsquery.Key]aistatsquery.Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[aistatsquery.Key]aistatsquery.Stats, len(c.entries))
	for k, v := range c.entries {
		out[k] = v
	}
	return out
}

// store replaces the telemetry contents atomically. Called by the background
// refresher goroutine.
func (c *statsCache) store(fresh map[aistatsquery.Key]aistatsquery.Stats) {
	c.mu.Lock()
	c.entries = fresh
	c.mu.Unlock()
}

// storeProviderUsage replaces the per-provider concurrency snapshot atomically.
func (c *statsCache) storeProviderUsage(use map[string]llmclient.ProviderUsage) {
	c.mu.Lock()
	c.providerUse = use
	c.mu.Unlock()
}

// providerUsage returns the cached concurrency snapshot for a provider.
// Returns a zero-value (Max=0, Inflight=0) when the provider is not
// registered — meaning "no limit".
func (c *statsCache) providerUsage(providerName string) llmclient.ProviderUsage {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.providerUse[providerName]
}

// clear empties the cache. Used on stop or in tests.
func (c *statsCache) clear() {
	c.mu.Lock()
	c.entries = make(map[aistatsquery.Key]aistatsquery.Stats)
	c.providerUse = make(map[string]llmclient.ProviderUsage)
	c.mu.Unlock()
}
