package aiaggregator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/service/aistatsquery"
)

// newStrategy builds the selection strategy for an aggregator from its
// configured name. Unknown / empty names fall back to round-robin so the
// default system aggregator keeps its existing behavior. "round_robin" is the
// canonical wire name (aimanager.normalizeStrategy maps the legacy "round-robin"
// alias to it before it reaches here).
func newStrategy(name string) Strategy {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "fallback", "priority":
		return NewFallbackStrategy()
	case "round_robin", "round-robin", "roundrobin":
		return NewRoundRobinStrategy()
	default:
		return NewRoundRobinStrategy()
	}
}

// resolveStrategy builds the strategy for the given name. When the name is
// "smart" or one of its aliases ("latency-aware" / "latency_aware"), it returns
// a SmartStrategy backed by the aggregator's local stats cache (populated by a
// background refresher, not by synchronous aistats queries). Other names fall
// through to newStrategy. aimanager.normalizeStrategy canonicalizes names to
// "smart" before they reach here, but the aliases are recognized too so the
// strategy never silently degrades to round-robin on a non-canonical input.
func (a *Actor) resolveStrategy(name string) Strategy {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "standard", "sticky":
		cache := a.ensureAssignmentCache()
		return NewStandardStrategy(NewRoundRobinStrategy(), cache.count)
	case "smart", "latency-aware", "latency_aware":
		cache := a.ensureStatsCache()
		return NewSmartStrategy(
			func(keys []aistatsquery.Key) map[aistatsquery.Key]aistatsquery.Stats {
				snap := cache.snapshot()
				out := make(map[aistatsquery.Key]aistatsquery.Stats, len(keys))
				for _, k := range keys {
					if s, ok := snap[k]; ok {
						out[k] = s
					} else {
						p, m := k.Split()
						out[k] = aistatsquery.Stats{Provider: p, Model: m}
					}
				}
				return out
			},
			func(providerName string) llmclient.ProviderUsage {
				return cache.providerUsage(providerName)
			},
		)
	default:
		return newStrategy(name)
	}
}

// statsRefreshInterval is the background aistats telemetry refresh interval.
const statsRefreshInterval = 10 * time.Second

// providerUsageRefreshInterval is the per-provider concurrency snapshot
// refresh interval. Shorter than stats because the gate is in-process (no
// actor call) and capacity changes are time-sensitive for strategy decisions.
const providerUsageRefreshInterval = 2 * time.Second

// startStatsRefresher launches a background goroutine that periodically pulls
// aistats aggregates and per-provider concurrency snapshots into the local
// statsCache. The goroutine exits when lifecycleCtx is cancelled. If aistats
// is unavailable the aistats refresh is skipped (the cache stays empty and
// SmartStrategy falls back to pool order).
func (a *Actor) startStatsRefresher() {
	svc := a.ensureStatsService()
	cache := a.ensureStatsCache()
	if a.lifecycleCtx == nil {
		return
	}
	go func() {
		statsTicker := time.NewTicker(statsRefreshInterval)
		defer statsTicker.Stop()
		usageTicker := time.NewTicker(providerUsageRefreshInterval)
		defer usageTicker.Stop()
		// Immediate first fetch so the cache is warm before the first tick.
		if svc != nil {
			a.refreshStats(svc, cache)
		}
		a.refreshProviderUsage(cache)
		for {
			select {
			case <-a.lifecycleCtx.Done():
				return
			case <-statsTicker.C:
				if svc != nil {
					a.refreshStats(svc, cache)
				}
			case <-usageTicker.C:
				a.refreshProviderUsage(cache)
			}
		}
	}()
}

// refreshStats pulls a full aggregates snapshot from aistats and stores it
// in the cache. Errors are silently ignored — stale or empty data is always
// safe for the strategy.
func (a *Actor) refreshStats(svc *aistatsquery.Service, cache *statsCache) {
	if a.actorCtx == nil {
		return
	}
	ctx, cancel := context.WithTimeout(a.lifecycleCtx, 2*time.Second)
	defer cancel()
	all, err := svc.QueryAllAggregates(ctx)
	if err != nil {
		return
	}
	cache.store(all)
}

// refreshProviderUsage snapshots the global ProviderGate's per-provider
// concurrency into the local cache. This is an in-process call (no actor
// invocation) so it is effectively free.
func (a *Actor) refreshProviderUsage(cache *statsCache) {
	cache.storeProviderUsage(llmclient.DefaultProviderGate.Snapshot())
}

// ensureStatsService lazily builds the aistatsquery.Service for smart strategy.
func (a *Actor) ensureStatsService() *aistatsquery.Service {
	a.statsSvcMu.Lock()
	defer a.statsSvcMu.Unlock()
	if a.statsSvc != nil {
		return a.statsSvc
	}
	if a.actorCtx == nil {
		return nil
	}
	a.statsSvc = aistatsquery.New(func() (ref.Ref, error) {
		if r, ok := a.actorCtx.LookupService("aistats"); ok {
			return r, nil
		}
		return nil, fmt.Errorf("aistats actor not available")
	})
	return a.statsSvc
}

// ensureStatsCache lazily builds the local stats cache.
func (a *Actor) ensureStatsCache() *statsCache {
	a.statsSvcMu.Lock()
	defer a.statsSvcMu.Unlock()
	if a.statsCache == nil {
		a.statsCache = newStatsCache()
	}
	return a.statsCache
}

// assignmentRefreshInterval is the provider-assignment snapshot refresh
// interval. Slightly longer than providerUsage because it requires a
// cross-actor call to aimanager.
const assignmentRefreshInterval = 5 * time.Second

// ensureAssignmentCache lazily builds the local assignment-count cache.
func (a *Actor) ensureAssignmentCache() *assignmentCache {
	a.assignmentCacheMu.Lock()
	defer a.assignmentCacheMu.Unlock()
	if a.assignmentCache == nil {
		a.assignmentCache = newAssignmentCache()
	}
	return a.assignmentCache
}

// startAssignmentRefresher launches a background goroutine that periodically
// pulls the provider→assignment-count snapshot from aimanager into the local
// assignmentCache. The goroutine exits when lifecycleCtx is cancelled. If
// aimanager is unreachable the cache stays empty and StandardStrategy treats all
// providers as equally loaded.
func (a *Actor) startAssignmentRefresher() {
	cache := a.ensureAssignmentCache()
	if a.lifecycleCtx == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(assignmentRefreshInterval)
		defer ticker.Stop()
		a.refreshAssignments(cache)
		for {
			select {
			case <-a.lifecycleCtx.Done():
				return
			case <-ticker.C:
				a.refreshAssignments(cache)
			}
		}
	}()
}

// refreshAssignments pulls the provider→assignment-count snapshot from
// aimanager.provider_assignments. Errors are silently ignored — stale or
// empty data is always safe for the strategy.
func (a *Actor) refreshAssignments(cache *assignmentCache) {
	ctx, cancel := context.WithTimeout(a.lifecycleCtx, 2*time.Second)
	defer cancel()
	call := a.aimanagerRef.Invoke(ctx, "aimanager.provider_assignments", nil)
	result, err := call.Final(ctx)
	_ = call.Close()
	if err != nil {
		return
	}
	counts := decodeAssignmentsResp(result)
	cache.store(counts)
}

// matchUnits filters the pool to the units eligible for a request. A unit is
// the atomic selection primitive (model, provider). When BOTH fields are empty
// the request is "auto-pick": every pool unit is eligible and the strategy
// decides. When BOTH are set, only the exact (model, provider) matches. A
// model without a provider (naked-model) is rejected — it would silently
// match across providers and break the Unit invariant. Aggregator-ref entries
// (non-empty AggregatorID) carry no concrete (model, provider); they are only
// eligible under auto-pick, never under a pinned unit request.
func matchUnits(unit domain.ModelUnit, units []CallableUnit) []CallableUnit {
	if unit.Model != "" && unit.Provider == "" {
		return nil // naked-model selection is forbidden
	}
	pinned := unit.Model != "" || unit.Provider != ""
	var matched []CallableUnit
	for _, u := range units {
		if u.AggregatorID != "" {
			// Aggregator refs participate only in auto-pick.
			if pinned {
				continue
			}
			matched = append(matched, u)
			continue
		}
		if unit.Model != "" && u.Model != unit.Model {
			continue
		}
		if unit.Provider != "" && u.ProviderName != unit.Provider {
			continue
		}
		matched = append(matched, u)
	}
	return matched
}

// noMatchError is the canonical "pool exhausted" error returned by every
// strategy when no candidate satisfies the request, so callers can detect
// retryability uniformly.
func noMatchError(unit domain.ModelUnit) error {
	return fmt.Errorf("no callable unit for model %q provider %q", unit.Model, unit.Provider)
}

// pinKey builds the (agent, slot) affinity key shared by the assignment-sticky
// strategies (RoundRobin per-agent pins, StandardStrategy). Returns empty when
// AgentID is empty (no meaningful affinity possible).
func pinKey(req SelectRequest) string {
	if req.AgentID == "" {
		return ""
	}
	return req.AgentID + "|" + req.SlotKind
}

// FallbackStrategy picks the first pool-order unit matching the request and
// never rotates. The pool is pre-filtered for cooldown by chatUnits, so the
// first match is by definition the highest-priority currently-healthy unit.
// This models a stable primary→backup failover order.
type FallbackStrategy struct{}

func NewFallbackStrategy() *FallbackStrategy { return &FallbackStrategy{} }

func (s *FallbackStrategy) Select(req SelectRequest, units []CallableUnit) (*CallableUnit, error) {
	matched := matchUnits(req.Unit, units)
	if len(matched) == 0 {
		return nil, noMatchError(req.Unit)
	}
	return &matched[0], nil
}
