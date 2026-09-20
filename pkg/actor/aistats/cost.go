package aistats

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// costIndex maintains the provider/model rate table. Rates are versioned so
// historical records can be re-costed if needed. All persistence goes through
// the persist.Persist contract: document names costs/<safeFilename>, where
// safeFilename replaces path separators in the provider--model key.
type costIndex struct {
	mu       sync.RWMutex
	store    persist.Persist
	rates    map[string]gen.AIStatsCostRate // key = provider + "/" + model
	versions map[string]int64               // per-key version counter
}

func newCostIndex(store persist.Persist) *costIndex {
	return &costIndex{
		store:    store,
		rates:    make(map[string]gen.AIStatsCostRate),
		versions: make(map[string]int64),
	}
}

func (ci *costIndex) key(provider, model string) string {
	return provider + "/" + model
}

func (ci *costIndex) configure(rate gen.AIStatsCostRate) (int64, error) {
	key := ci.key(rate.Provider, rate.Model)
	ci.mu.Lock()
	defer ci.mu.Unlock()

	ci.versions[key]++
	rate.Version = ci.versions[key]
	rate.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	ci.rates[key] = rate

	if err := ci.saveLocked(rate); err != nil {
		return 0, err
	}
	return rate.Version, nil
}

func (ci *costIndex) get(provider, model string) (gen.AIStatsCostRate, bool) {
	ci.mu.RLock()
	defer ci.mu.RUnlock()
	rate, ok := ci.rates[ci.key(provider, model)]
	return rate, ok
}

func (ci *costIndex) list(provider, model string) []gen.AIStatsCostRate {
	ci.mu.RLock()
	defer ci.mu.RUnlock()

	var out []gen.AIStatsCostRate
	for key, rate := range ci.rates {
		parts := strings.SplitN(key, "/", 2)
		if len(parts) != 2 {
			continue
		}
		if provider != "" && parts[0] != provider {
			continue
		}
		if model != "" && parts[1] != model {
			continue
		}
		out = append(out, rate)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Model < out[j].Model
	})
	return out
}

func (ci *costIndex) load() error {
	ci.mu.Lock()
	defer ci.mu.Unlock()

	lister, ok := ci.store.(persist.Lister)
	if !ok {
		return fmt.Errorf("aistats: cost index requires Lister backend")
	}
	names, err := lister.List("costs/")
	if err != nil {
		return fmt.Errorf("aistats: list cost rates: %w", err)
	}
	for _, name := range names {
		var rate gen.AIStatsCostRate
		if err := ci.store.Load(name, &rate); err != nil {
			if errors.Is(err, persist.ErrNotExist) {
				continue
			}
			return fmt.Errorf("aistats: load cost rate %s: %w", name, err)
		}
		key := ci.key(rate.Provider, rate.Model)
		ci.rates[key] = rate
		if rate.Version > ci.versions[key] {
			ci.versions[key] = rate.Version
		}
	}
	return nil
}

func (ci *costIndex) saveLocked(rate gen.AIStatsCostRate) error {
	name := "costs/" + safeFilename(rate.Provider+"--"+rate.Model)
	if err := ci.store.Save(name, rate); err != nil {
		return fmt.Errorf("aistats: save cost rate: %w", err)
	}
	return nil
}

func safeFilename(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "/", "_"), "\\", "_")
}

// toLLMCostRate converts the schema cost rate into the llmclient calculator's
// shape. CacheWrite defaults to 2*Input when unset, matching Anthropic pricing.
func toLLMCostRate(rate gen.AIStatsCostRate) llmclient.CostRate {
	cr := llmclient.CostRate{
		Input:      rate.CostInput,
		Output:     rate.CostOutput,
		CacheRead:  rate.CostCacheRead,
		CacheWrite: rate.CostCacheWrite,
	}
	for _, tier := range rate.Tiers {
		cr.Tiers = append(cr.Tiers, llmclient.CostTier{
			InputTokensAbove: tier.InputTokensAbove,
			Input:            tier.CostInput,
			Output:           tier.CostOutput,
			CacheRead:        tier.CostCacheRead,
			CacheWrite:       tier.CostCacheWrite,
		})
	}
	return cr
}

// costForUsage returns the cost for a usage under the configured rate, or zero
// if no rate is configured.
func (ci *costIndex) costForUsage(provider, model string, usage llmclient.Usage) llmclient.Cost {
	cr := llmclient.CostRate{}
	if rate, ok := ci.get(provider, model); ok {
		cr = toLLMCostRate(rate)
	}
	return llmclient.CalculateCost(usage, cr)
}
