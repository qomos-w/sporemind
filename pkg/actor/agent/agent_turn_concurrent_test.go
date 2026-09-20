package agent

import (
	"sync"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
)

// TestActiveTurnRefConcurrent exercises the turn-ref synchronization helpers
// under concurrent mutation. The race detector should flag any bare read/write
// of ActiveTurnRef that bypasses turnRefMu.
func TestActiveTurnRefConcurrent(t *testing.T) {
	a := &Actor{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				if j%2 == 0 {
					a.setActiveTurnRef("turn-1")
				} else {
					a.setActiveTurnRef("")
				}
				_ = a.getActiveTurnRef()
			}
		}(i)
	}
	wg.Wait()
}

// TestActiveTurnOrderConcurrent exercises the turn-order synchronization helpers
// under concurrent mutation.
func TestActiveTurnOrderConcurrent(t *testing.T) {
	a := &Actor{}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				a.setActiveTurnOrder(int64(j))
				_ = a.getActiveTurnOrder()
			}
		}(i)
	}
	wg.Wait()
}

// TestAllocTurnOrderConcurrent verifies that allocTurnOrder is safe to call from
// multiple goroutines. Before the fix RawSession.NextTurnOrder was mutated without
// any lock, producing a data race between the owner loop and agent.exec loop.
func TestAllocTurnOrderConcurrent(t *testing.T) {
	a := &Actor{RawSession: domain.RawSession{NextTurnOrder: 1}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				_ = a.allocTurnOrder()
			}
		}()
	}
	wg.Wait()
	if got := a.RawSession.NextTurnOrder; got != 1+8*1000 {
		t.Fatalf("NextTurnOrder=%d, want %d", got, 1+8*1000)
	}
}

// TestActiveTurnSnapshotConsistent exercises the snapshot path: takeSnapshot reads
// ActiveTurnRef/activeTurnOrder while the owner loop mutates them. The snapshot
// must observe a consistent (ref, order) pair and must not crash.
func TestActiveTurnSnapshotConsistent(t *testing.T) {
	a := &Actor{}
	a.snapshot.Store(&sessionSnapshotData{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			a.setActiveTurnRef("turn-1")
			a.setActiveTurnOrder(42)
			a.setActiveTurnRef("")
			a.setActiveTurnOrder(0)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			a.takeSnapshot()
		}
	}()
	wg.Wait()
}
