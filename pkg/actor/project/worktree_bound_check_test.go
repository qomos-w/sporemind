package project

import (
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestWorktreeBoundCheck(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "devflow"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	agentKey := "01a000000000000000000000000000ff"
	a.bindingMu.Lock()
	if a.agentWorktree == nil {
		a.agentWorktree = make(map[string]string)
	}
	a.agentWorktree[agentKey] = wt.ID
	a.bindingMu.Unlock()

	resp, err := a.handleWorktreeBoundCheck(nil, gen.ProjectWorktreeBoundCheckReq{AgentActorID: agentKey})
	if err != nil {
		t.Fatalf("bound check: %v", err)
	}
	if !resp.Bound {
		t.Fatal("Bound=false for a bound agent, want true")
	}
	if resp.WorktreePath != wt.Path {
		t.Fatalf("WorktreePath=%q, want %q", resp.WorktreePath, wt.Path)
	}

	// Unbound agent.
	resp, err = a.handleWorktreeBoundCheck(nil, gen.ProjectWorktreeBoundCheckReq{AgentActorID: "01a00000000000000000000000000fe"})
	if err != nil {
		t.Fatalf("bound check (unbound): %v", err)
	}
	if resp.Bound || resp.WorktreePath != "" {
		t.Fatalf("unbound agent reported Bound=%v Path=%q, want false/empty", resp.Bound, resp.WorktreePath)
	}

	// Stale binding (worktree no longer active) must not count as bound.
	a.worktreeParentMu.Lock()
	stale := a.worktrees[wt.ID]
	stale.Status = "merging"
	a.worktrees[wt.ID] = stale
	a.worktreeParentMu.Unlock()
	resp, err = a.handleWorktreeBoundCheck(nil, gen.ProjectWorktreeBoundCheckReq{AgentActorID: agentKey})
	if err != nil {
		t.Fatalf("bound check (stale): %v", err)
	}
	if resp.Bound {
		t.Fatal("stale binding reported Bound=true, want false")
	}

	// Missing AgentActorID is rejected.
	if _, err := a.handleWorktreeBoundCheck(nil, gen.ProjectWorktreeBoundCheckReq{}); err == nil || !strings.Contains(err.Error(), "AgentActorID required") {
		t.Fatalf("empty AgentActorID: want AgentActorID-required error, got %v", err)
	}
}
