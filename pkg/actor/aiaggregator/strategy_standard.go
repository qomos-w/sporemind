package aiaggregator

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// standardTTL is how long a pinned assignment remains valid without being
// refreshed. A dispatch that reuses the pin resets the timestamp.
const standardTTL = 30 * time.Minute

// standardEntry records a pinned unit selection for a (agent, slot) key.
type standardEntry struct {
	unitID string
	at     time.Time
}

// StandardStrategy pins each (agentID, slotKind) to its last-resolved unit for
// provider prompt/KV-cache affinity. On a cache hit (pinned unit still in the
// matched pool and not expired) it returns the pinned unit immediately. On a
// miss or fallback it delegates to the base strategy, but first reorders the
// candidates by provider-assignment count ascending so new/fallback picks
// spread across the least-loaded providers. The reordered list is then passed
// to the base strategy (e.g. RoundRobin) which rotates within same-count tiers.
//
// StandardStrategy does NOT bypass existing feedback loops: the pool it receives
// is already health/token-filtered by selectUnit, and after selection the
// caller still goes through ProviderGate (concurrency) and reports failures via
// markUnitFailure. A cooled pinned unit is simply absent from the pool, so
// standard falls back automatically.
type StandardStrategy struct {
	base            Strategy
	mu              sync.Mutex
	pins            map[string]standardEntry
	assignmentCount func(provider string) int
	now             func() time.Time
}

// NewStandardStrategy wraps a base strategy with cache-affinity pinning.
// assignmentCount returns the cached provider→agent count (0 when no data);
// it never blocks — it reads from the local assignmentCache.
func NewStandardStrategy(base Strategy, assignmentCount func(provider string) int) *StandardStrategy {
	return &StandardStrategy{
		base:            base,
		pins:            make(map[string]standardEntry),
		assignmentCount: assignmentCount,
		now:             time.Now,
	}
}

func (s *StandardStrategy) Select(req SelectRequest, units []CallableUnit) (*CallableUnit, error) {
	key := pinKey(req)

	// Try pinned unit first (cache-affinity fast path).
	if key != "" {
		s.mu.Lock()
		entry, ok := s.pins[key]
		s.mu.Unlock()
		if ok && s.now().Sub(entry.at) < standardTTL {
			for i := range units {
				if units[i].ID == entry.unitID {
					return &units[i], nil
				}
			}
		}
	}

	// Miss or fallback: reorder candidates by provider-assignment count, then
	// delegate to the base strategy. The base (e.g. RoundRobin) rotates within
	// same-count tiers, so load is spread while still cycling over time.
	reordered := reorderUnitsByAssignment(units, s.assignmentCount)
	chosen, err := s.base.Select(req, reordered)
	if err != nil {
		return nil, err
	}

	// Pin the new selection.
	if key != "" && chosen != nil {
		s.mu.Lock()
		s.pins[key] = standardEntry{unitID: chosen.ID, at: s.now()}
		s.mu.Unlock()
	}
	return chosen, nil
}

// reorderUnitsByAssignment sorts units by their provider's assignment count
// ascending (fewest agents first). Stable sort preserves pool order within
// the same count tier, so RoundRobin rotation still works.
func reorderUnitsByAssignment(units []CallableUnit, count func(provider string) int) []CallableUnit {
	if count == nil || len(units) <= 1 {
		return units
	}
	sorted := make([]CallableUnit, len(units))
	copy(sorted, units)
	sort.SliceStable(sorted, func(i, j int) bool {
		ci := count(sorted[i].ProviderName)
		cj := count(sorted[j].ProviderName)
		if ci != cj {
			return ci < cj
		}
		return strings.ToLower(sorted[i].ProviderName) < strings.ToLower(sorted[j].ProviderName)
	})
	return sorted
}
