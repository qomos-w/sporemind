package aiaggregator

import "sync"

// assignmentCache is a read-only snapshot of the provider→assignment-count
// map maintained by aimanager. It is refreshed asynchronously by a background
// goroutine (startAssignmentRefresher) so the dispatch path (StandardStrategy)
// never blocks on a cross-actor call. A nil/empty cache simply means "no
// assignment data yet"; the strategy treats all providers as equally loaded.
type assignmentCache struct {
	mu     sync.RWMutex
	counts map[string]int
}

func newAssignmentCache() *assignmentCache {
	return &assignmentCache{counts: make(map[string]int)}
}

// count returns the cached assignment count for a provider. Returns 0 when the
// provider is not in the snapshot — meaning "no agents currently assigned".
func (c *assignmentCache) count(providerName string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.counts[providerName]
}

// store replaces the count map atomically. Called by the background refresher.
func (c *assignmentCache) store(fresh map[string]int) {
	c.mu.Lock()
	c.counts = fresh
	c.mu.Unlock()
}

// decodeAssignmentsResp extracts the counts map from a cross-actor call result.
// The result comes back as a serialized map; this handles common forms.
func decodeAssignmentsResp(result any) map[string]int {
	if result == nil {
		return make(map[string]int)
	}
	switch v := result.(type) {
	case map[string]int:
		return v
	case map[string]any:
		out := make(map[string]int, len(v))
		for k, val := range v {
			switch n := val.(type) {
			case int:
				out[k] = n
			case float64:
				out[k] = int(n)
			case int64:
				out[k] = int(n)
			}
		}
		return out
	}
	return make(map[string]int)
}
