package aiaggregator

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/service/aistatsquery"
)

func TestStatsCache_SnapshotReturnsCopy(t *testing.T) {
	cache := newStatsCache()
	cache.store(map[aistatsquery.Key]aistatsquery.Stats{
		aistatsquery.MakeKey("a", "m"): {Provider: "a", Model: "m", Requests: 10},
	})

	snap := cache.snapshot()
	if st, ok := snap[aistatsquery.MakeKey("a", "m")]; !ok || st.Requests != 10 {
		t.Fatalf("expected 10 requests, got %+v", st)
	}

	// Mutate the returned map; the cache should be unaffected.
	snap[aistatsquery.MakeKey("a", "m")] = aistatsquery.Stats{Requests: 999}
	snap2 := cache.snapshot()
	if st := snap2[aistatsquery.MakeKey("a", "m")]; st.Requests != 10 {
		t.Fatalf("cache was mutated via snapshot: got %d, want 10", st.Requests)
	}
}

func TestStatsCache_EmptyByDefault(t *testing.T) {
	cache := newStatsCache()
	snap := cache.snapshot()
	if len(snap) != 0 {
		t.Fatalf("expected empty snapshot, got %d entries", len(snap))
	}
}

func TestStatsCache_Clear(t *testing.T) {
	cache := newStatsCache()
	cache.store(map[aistatsquery.Key]aistatsquery.Stats{
		aistatsquery.MakeKey("a", "m"): {Provider: "a", Model: "m"},
	})
	cache.clear()
	if len(cache.snapshot()) != 0 {
		t.Fatal("expected empty after clear")
	}
}

func TestStatsCache_StoreReplaces(t *testing.T) {
	cache := newStatsCache()
	cache.store(map[aistatsquery.Key]aistatsquery.Stats{
		aistatsquery.MakeKey("a", "m"): {Requests: 1},
	})
	cache.store(map[aistatsquery.Key]aistatsquery.Stats{
		aistatsquery.MakeKey("b", "n"): {Requests: 2},
	})
	snap := cache.snapshot()
	if _, ok := snap[aistatsquery.MakeKey("a", "m")]; ok {
		t.Fatal("old entry should be gone after store")
	}
	if st := snap[aistatsquery.MakeKey("b", "n")]; st.Requests != 2 {
		t.Fatalf("expected 2, got %d", st.Requests)
	}
}

func TestStatsCache_ProviderUsageStoreAndRead(t *testing.T) {
	cache := newStatsCache()
	cache.storeProviderUsage(map[string]llmclient.ProviderUsage{
		"p1": {Inflight: 3, Max: 10},
	})
	if u := cache.providerUsage("p1"); u.Inflight != 3 || u.Max != 10 {
		t.Fatalf("expected {3,10}, got %+v", u)
	}
	if u := cache.providerUsage("unknown"); u.Inflight != 0 || u.Max != 0 {
		t.Fatalf("expected zero-value for unknown, got %+v", u)
	}
}

func TestStatsCache_ProviderUsageReplaces(t *testing.T) {
	cache := newStatsCache()
	cache.storeProviderUsage(map[string]llmclient.ProviderUsage{
		"p1": {Inflight: 5, Max: 10},
	})
	cache.storeProviderUsage(map[string]llmclient.ProviderUsage{
		"p2": {Inflight: 1, Max: 2},
	})
	if u := cache.providerUsage("p1"); u.Max != 0 {
		t.Fatalf("old provider should be gone, got %+v", u)
	}
	if u := cache.providerUsage("p2"); u.Inflight != 1 || u.Max != 2 {
		t.Fatalf("expected {1,2}, got %+v", u)
	}
}
