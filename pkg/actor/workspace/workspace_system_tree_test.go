package workspace

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/gospore/ref"
)

// handleSystemTree is PureContext; its pluginhost RPC + dir scans must be
// absorbed by the short-TTL cache so UI polling doesn't repeat the work.
func TestHandleSystemTreeCachesWithinTTL(t *testing.T) {
	a, ctx := freshActor(t)
	var pluginLookups atomic.Int32
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "pluginhost" {
			pluginLookups.Add(1)
		}
		return nil, false
	}

	if _, err := a.handleSystemTree(ctx); err != nil {
		t.Fatalf("first handleSystemTree: %v", err)
	}
	if _, err := a.handleSystemTree(ctx); err != nil {
		t.Fatalf("second handleSystemTree: %v", err)
	}
	if got := pluginLookups.Load(); got != 1 {
		t.Fatalf("pluginhost lookups = %d after 2 calls within TTL, want 1 (cached)", got)
	}
}

func TestHandleSystemTreeRebuildsAfterTTL(t *testing.T) {
	a, ctx := freshActor(t)
	var pluginLookups atomic.Int32
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		if name == "pluginhost" {
			pluginLookups.Add(1)
		}
		return nil, false
	}

	// Seed a stale cache entry; the next call must rebuild.
	a.systemTreeCache.Store(systemTreeCacheEntry{at: time.Now().Add(-systemTreeCacheTTL - time.Second)})
	if _, err := a.handleSystemTree(ctx); err != nil {
		t.Fatalf("handleSystemTree: %v", err)
	}
	if got := pluginLookups.Load(); got != 1 {
		t.Fatalf("pluginhost lookups = %d after stale cache, want 1 (rebuilt)", got)
	}
}
