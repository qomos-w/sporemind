package project

import (
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/gospore/id"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// Concurrency regression tests for the worktree binding paths.
//
// After statelessness, handleWorktreeEnter / handleWorktreeExit /
// handleWorktreeDiscard / handleWorktreeReleaseBinding all execute on fork
// goroutines and serialize on the nested worktreeParentMu → bindingMu pair.
// These tests hammer those paths concurrently (go test -race) and assert the
// quiescent invariants instead of individual call outcomes, because most
// interleavings legitimately produce errors on the losing side (bind refused
// while another goroutine holds the binding, discard of an already-removed
// worktree, exit of an unbound agent).

// concCallerCtx builds a PureContext whose Caller resolves to agentID, so
// callerID(ctx) returns that agent's actor ID string. Every goroutine gets its
// own FakeCtx; FakeCtx is not safe for concurrent use.
func concCallerCtx(agentID id.ActorID) *testutil.FakeCtx {
	return &testutil.FakeCtx{CallerRef: testutil.NewFakeRef(agentID, nil)}
}

// concGenID derives a distinct canonical actor ID from n, following the
// project_spawn_test.go pattern (deterministic, collision-free per index).
func concGenID(n uint64) id.ActorID {
	return id.NewCanonical(1, 0, func() uint64 { return n }).Next()
}

// newReloadedActor builds a bare actor over the same repo and data dir, used
// to verify persisted worktree metadata reloads correctly (restart simulation,
// same construction as worktree_test.go's round-trip tests).
func newReloadedActor(t *testing.T, dir string, actorID string) *Actor {
	t.Helper()
	return &Actor{
		initialPath:  dir,
		actorID:      actorID,
		store:        newFSCardStore(filepath.Join(dir, ".sporecode", "wiki")),
		persistStore: persist.NewFSPersist(t.TempDir()),
	}
}

// assertWorktreeStateConsistent verifies the quiescent invariants after a
// concurrency hammer:
//   - every surviving binding points at a worktree that still exists in the
//     registry with status "active";
//   - every registry entry is "active" (discard removes entries outright, so
//     no tombstones may survive);
//   - a bound worktree is per-agent exclusive (name starts with the agent ID).
func assertWorktreeStateConsistent(t *testing.T, a *Actor, agentIDs ...string) {
	t.Helper()

	a.bindingMu.RLock()
	bindings := make(map[string]string, len(a.agentWorktree))
	for agent, wtID := range a.agentWorktree {
		bindings[agent] = wtID
	}
	nBindings := len(bindings)
	a.bindingMu.RUnlock()

	a.worktreeParentMu.RLock()
	registry := make(map[string]gen.ProjectWorktree, len(a.worktrees))
	for wtID, wt := range a.worktrees {
		registry[wtID] = wt
	}
	a.worktreeParentMu.RUnlock()

	if nBindings > len(agentIDs) {
		t.Fatalf("agentWorktree has %d bindings, want at most %d", nBindings, len(agentIDs))
	}
	for _, agent := range agentIDs {
		wtID, bound := bindings[agent]
		if !bound {
			continue
		}
		wt, exists := registry[wtID]
		if !exists {
			t.Fatalf("agent %s still bound to worktree %q which is absent from the registry", agent, wtID)
		}
		if wt.Status != "active" {
			t.Fatalf("agent %s bound to worktree %q with status %q, want active", agent, wtID, wt.Status)
		}
		if !strings.HasPrefix(wt.Name, agent) {
			t.Fatalf("agent %s bound to worktree %q named %q, want per-agent exclusive prefix %q-", agent, wtID, wt.Name, agent)
		}
	}
	for wtID, wt := range registry {
		if wt.Status != "active" {
			t.Fatalf("worktree %q survived with status %q, want active (discards leave no tombstones)", wtID, wt.Status)
		}
	}
}

// TestWorktreeConcurrency_EnterExitSameAgent hammers enter/exit for ONE agent
// from many goroutines. Losing races surface as errors (bind refused while
// another goroutine's binding is live, exit of an already-unbound agent) and
// are tolerated; what must hold afterwards is a single coherent binding that
// agrees with the registry, and no panic/race/deadlock anywhere.
func TestWorktreeConcurrency_EnterExitSameAgent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	agent := concGenID(9001).String()
	const workers = 12
	const rounds = 3

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := concCallerCtx(concGenID(9001))
			for r := 0; r < rounds; r++ {
				if (i+r)%2 == 0 {
					// Errors are legal: a concurrent goroutine may hold the
					// binding (bind refused) or the worktree may already be
					// gone (best-effort cleanup lost the race).
					_, _ = a.handleWorktreeEnter(ctx, gen.ProjectWorktreeEnterReq{})
				} else {
					_, _ = a.handleWorktreeExit(ctx, gen.ProjectWorktreeExitReq{Mode: "discard"})
				}
			}
		}(i)
	}
	wg.Wait()

	assertWorktreeStateConsistent(t, a, agent)
}

// TestWorktreeConcurrency_DistinctAgents gives every goroutine its own agent;
// each enter must succeed (per-agent exclusive names never collide), each
// subsequent exit(discard) must complete, and afterwards the binding map and
// the worktree registry must both be fully drained — including on disk, since
// every step persists worktrees.json.
func TestWorktreeConcurrency_DistinctAgents(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	const n = 8
	entered := make([]string, n)
	exited := make([]bool, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			agentID := concGenID(uint64(9100 + i))
			ctx := concCallerCtx(agentID)

			resp, err := a.handleWorktreeEnter(ctx, gen.ProjectWorktreeEnterReq{})
			if err != nil {
				t.Errorf("agent %d enter: %v", i, err)
				return
			}
			if !resp.Created {
				t.Errorf("agent %d enter: Created=false, want true (fresh agent)", i)
			}
			entered[i] = resp.Worktree.ID

			exitResp, err := a.handleWorktreeExit(ctx, gen.ProjectWorktreeExitReq{Mode: "discard"})
			if err != nil {
				t.Errorf("agent %d exit: %v", i, err)
				return
			}
			if exitResp.Status != "completed" || exitResp.Mode != "discard" {
				t.Errorf("agent %d exit: status=%q mode=%q, want completed/discard", i, exitResp.Status, exitResp.Mode)
			}
			exited[i] = true
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if entered[i] == "" {
			t.Fatalf("agent %d never entered", i)
		}
		if !exited[i] {
			t.Fatalf("agent %d never exited cleanly", i)
		}
	}

	a.bindingMu.RLock()
	if got := len(a.agentWorktree); got != 0 {
		a.bindingMu.RUnlock()
		t.Fatalf("agentWorktree has %d leftover bindings after all exits, want 0", got)
	}
	a.bindingMu.RUnlock()

	a.worktreeParentMu.RLock()
	if got := len(a.worktrees); got != 0 {
		a.worktreeParentMu.RUnlock()
		t.Fatalf("worktrees registry has %d leftover entries after all exits, want 0", got)
	}
	a.worktreeParentMu.RUnlock()

	// Metadata completeness across a restart: the persisted manifests must
	// reload with the same (empty) state.
	a2 := newReloadedActor(t, dir, a.actorID)
	if err := a2.loadWorktreeCache(); err != nil {
		t.Fatalf("loadWorktreeCache: %v", err)
	}
	if got := len(a2.worktrees); got != 0 {
		t.Fatalf("reloaded worktrees = %d, want 0", got)
	}
	a2.bindingMu.RLock()
	gotBindings := len(a2.agentWorktree)
	a2.bindingMu.RUnlock()
	if gotBindings != 0 {
		t.Fatalf("reloaded agentWorktree bindings = %d, want 0", gotBindings)
	}
}

// TestWorktreeConcurrency_DiscardRacesEnter pits a looping enter against a
// discarder that keeps removing whatever the agent is currently bound to.
// Either outcome is legitimate (the enterer wins and rebinds, or the discarder
// wins and the next enter recreates); the contract under test is that the
// double-lock choreography never races, panics, or deadlocks. A watchdog turns
// a lock-ordering deadlock into an explicit test failure instead of a hang.
func TestWorktreeConcurrency_DiscardRacesEnter(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	agentID := concGenID(9200)
	agent := agentID.String()
	const enterAttempts = 10

	var enterDone atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx := concCallerCtx(agentID)
		for i := 0; i < enterAttempts; i++ {
			// Bind-refused and cleanup-lost-race errors are both legal here.
			_, _ = a.handleWorktreeEnter(ctx, gen.ProjectWorktreeEnterReq{})
		}
		enterDone.Store(true)
	}()

	go func() {
		for {
			a.bindingMu.RLock()
			wtID, bound := a.agentWorktree[agent]
			a.bindingMu.RUnlock()
			if bound {
				// Force: the worktree may be mid-creation from a concurrent
				// enter; outcome legality is asserted below, not here.
				_ = a.handleWorktreeDiscard(nil, gen.ProjectWorktreeDiscardReq{WorktreeID: wtID, Force: true})
				continue
			}
			if enterDone.Load() {
				return
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()

	select {
	case <-done:
	case <-time.After(120 * time.Second):
		t.Fatal("deadlock suspected: enter-vs-discard hammer did not finish within 120s")
	}

	// Drain any binding established after the discarder observed enterDone.
	a.bindingMu.RLock()
	wtID, bound := a.agentWorktree[agent]
	a.bindingMu.RUnlock()
	if bound {
		_ = a.handleWorktreeDiscard(nil, gen.ProjectWorktreeDiscardReq{WorktreeID: wtID, Force: true})
	}

	assertWorktreeStateConsistent(t, a, agent)
}
