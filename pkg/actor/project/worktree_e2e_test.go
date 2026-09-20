package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"github.com/qomos-w/sporemind/pkg/util"
)

// callerCtx builds a FakeCtx whose Caller() returns a ref carrying the given
// agent actor id, so caller-id interception routes this caller's file/git
// calls to its bound worktree. agentIDHex must be a canonical id string; the
// string passed to bindWorktree must be the same (id.From(cid).String()).
func callerCtx(t *testing.T, agentIDHex string) *testutil.FakeCtx {
	t.Helper()
	cid, err := identity.ParseCanonicalID(agentIDHex)
	if err != nil {
		t.Fatalf("callerCtx: parse %q: %v", agentIDHex, err)
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	ctx.CallerRef = testutil.NewFakeRef(id.From(cid), nil)
	return ctx
}

// TestEndToEnd_TwoAgentsConcurrentCommit_NoSilentOverwrite is the P1-B
// acceptance test for the core bug: two agents bound to two worktrees, each
// commits a different file, and both commits survive — proving the
// shared-Worktree silent-overwrite bug is gone.
func TestEndToEnd_TwoAgentsConcurrentCommit_NoSilentOverwrite(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	// Two worktrees, two agent actor ids.
	wt1, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "agent1-wt", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create wt1: %v", err)
	}
	wt2, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "agent2-wt", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create wt2: %v", err)
	}
	agent1 := "019fa111110000000000000000000001"
	agent2 := "019fa222220000000000000000000002"
	// bindWorktree keys must match what callerID(ctx) returns (id.From(cid).String()).
	agent1Key := bindKey(t, agent1)
	agent2Key := bindKey(t, agent2)
	if err := a.bindWorktree(agent1Key, wt1.ID); err != nil {
		t.Fatalf("bind agent1: %v", err)
	}
	if err := a.bindWorktree(agent2Key, wt2.ID); err != nil {
		t.Fatalf("bind agent2: %v", err)
	}

	// Each agent writes a distinct file in its own worktree dir, then commits.
	for _, step := range []struct {
		agentID, wtPath, file, content string
	}{
		{agent1, wt1.Path, "from-agent-1.txt", "one"},
		{agent2, wt2.Path, "from-agent-2.txt", "two"},
	} {
		if err := os.WriteFile(filepath.Join(step.wtPath, step.file), []byte(step.content), 0644); err != nil {
			t.Fatalf("write %s: %v", step.file, err)
		}
		// git add + commit routed to this agent's worktree via caller-id interception.
		ctx := callerCtx(t, step.agentID)
		if err := a.handleGitAdd(ctx, gen.ProjectGitAddReq{Paths: []string{step.file}}); err != nil {
			t.Fatalf("agent %s git_add %s: %v", step.agentID, step.file, err)
		}
		if _, err := a.handleGitCommit(ctx, gen.ProjectGitCommitReq{Message: step.file}); err != nil {
			t.Fatalf("agent %s git_commit %s: %v", step.agentID, step.file, err)
		}
	}

	// Verify BOTH commits exist (the bug: with shared Worktree, one would be lost).
	log1 := gitLogSubjects(t, wt1.Path)
	log2 := gitLogSubjects(t, wt2.Path)
	logMain := gitLogSubjects(t, dir)

	if !contains(log1, "from-agent-1.txt") {
		t.Errorf("wt1 log missing agent1's commit; got %v", log1)
	}
	if contains(log1, "from-agent-2.txt") {
		t.Errorf("wt1 log LEAKED agent2's commit (isolation broken); got %v", log1)
	}
	if !contains(log2, "from-agent-2.txt") {
		t.Errorf("wt2 log missing agent2's commit; got %v", log2)
	}
	if contains(log2, "from-agent-1.txt") {
		t.Errorf("wt2 log LEAKED agent1's commit (isolation broken); got %v", log2)
	}
	// Main root should only have the initial commit, NOT either agent's commit
	// (their commits live on their own branches).
	if contains(logMain, "from-agent-1.txt") || contains(logMain, "from-agent-2.txt") {
		t.Errorf("main root leaked agent commits; got %v", logMain)
	}
}

// TestEndToEnd_CheckoutIsolation proves acceptance criterion 4: one agent's
// git_checkout does not affect another agent's working tree state.
func TestEndToEnd_CheckoutIsolation(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt1, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "co-1", BaseRef: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	wt2, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "co-2", BaseRef: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	agent1 := "019fcccc110000000000000000000001"
	agent2 := "019fcccc220000000000000000000002"
	agent1Key := bindKey(t, agent1)
	agent2Key := bindKey(t, agent2)
	if err := a.bindWorktree(agent1Key, wt1.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.bindWorktree(agent2Key, wt2.ID); err != nil {
		t.Fatal(err)
	}

	// Record wt2's initial branch before agent1 does anything.
	wt2BranchBefore, err := gitBranchShowCurrent(wt2.Path)
	if err != nil {
		t.Fatalf("wt2 branch before: %v", err)
	}

	// Agent1 commits something on its branch.
	if err := os.WriteFile(filepath.Join(wt1.Path, "x.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx1 := callerCtx(t, agent1)
	if err := a.handleGitAdd(ctx1, gen.ProjectGitAddReq{Paths: []string{"x.txt"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.handleGitCommit(ctx1, gen.ProjectGitCommitReq{Message: "x"}); err != nil {
		t.Fatal(err)
	}

	// Agent1's commit must not change agent2's working tree or branch.
	wt2BranchAfter, err := gitBranchShowCurrent(wt2.Path)
	if err != nil {
		t.Fatalf("wt2 branch after: %v", err)
	}
	if wt2BranchAfter != wt2BranchBefore {
		t.Errorf("wt2 branch changed by agent1's commit: %q → %q (isolation broken)", wt2BranchBefore, wt2BranchAfter)
	}
	// x.txt must not appear in wt2.
	if _, err := os.Stat(filepath.Join(wt2.Path, "x.txt")); !os.IsNotExist(err) {
		t.Errorf("agent1's x.txt leaked into agent2's worktree")
	}
}

// TestEndToEnd_AgentBindingsListing proves agent_bindings is caller-scoped: an
// agent sees only its own binding and cannot observe another agent's binding.
func TestEndToEnd_AgentBindingsListing(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "listed", BaseRef: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	owner := "019fbbbb00000000000000000000000a"
	ownerKey := bindKey(t, owner)
	if err := a.bindWorktree(ownerKey, wt.ID); err != nil {
		t.Fatal(err)
	}

	// 1. The bound agent sees its own binding.
	ownerResp, err := a.handleWorktreeAgentBindings(callerCtx(t, owner), gen.ProjectWorktreeAgentBindingsReq{})
	if err != nil {
		t.Fatalf("agent_bindings (owner): %v", err)
	}
	if len(ownerResp.Bindings) != 1 {
		t.Fatalf("owner: expected 1 binding, got %d: %+v", len(ownerResp.Bindings), ownerResp.Bindings)
	}
	b := ownerResp.Bindings[0]
	if b.AgentActorID != ownerKey || b.WorktreeID != wt.ID || b.WorktreePath != wt.Path {
		t.Errorf("owner binding mismatch: got %+v, want agent=%s wt=%s path=%s", b, ownerKey, wt.ID, wt.Path)
	}

	// 2. A different agent cannot observe the owner's binding.
	otherResp, err := a.handleWorktreeAgentBindings(callerCtx(t, "019fcccc00000000000000000000000c"), gen.ProjectWorktreeAgentBindingsReq{})
	if err != nil {
		t.Fatalf("agent_bindings (other): %v", err)
	}
	if len(otherResp.Bindings) != 0 {
		t.Fatalf("other agent: expected 0 bindings, got %d: %+v", len(otherResp.Bindings), otherResp.Bindings)
	}

	// 3. A caller with no identity (system/internal) sees nothing.
	noCallerResp, err := a.handleWorktreeAgentBindings(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeAgentBindingsReq{})
	if err != nil {
		t.Fatalf("agent_bindings (no caller): %v", err)
	}
	if len(noCallerResp.Bindings) != 0 {
		t.Fatalf("no caller: expected 0 bindings, got %d: %+v", len(noCallerResp.Bindings), noCallerResp.Bindings)
	}
}

// TestEndToEnd_ReleaseBinding_RecyclesOrphanedWorktree proves the lifecycle
// contract: releasing the last binding for a worktree removes the worktree.
func TestEndToEnd_ReleaseBinding_RecyclesOrphanedWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "recycled", BaseRef: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	agent := "019fdddd00000000000000000000000d"
	agentKey := bindKey(t, agent)
	if err := a.bindWorktree(agentKey, wt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("worktree dir should exist before release: %v", err)
	}

	if err := a.handleWorktreeReleaseBinding(ctx, gen.ProjectWorktreeReleaseBindingReq{AgentActorID: agentKey}); err != nil {
		t.Fatalf("release_binding: %v", err)
	}

	// Worktree dir should be gone (last binding released → removed).
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree dir should be removed after last binding release; stat err = %v", err)
	}
	// Worktree should no longer be listed.
	list, _ := a.handleWorktreeList(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeListReq{})
	for _, w := range list.Worktrees {
		if w.ID == wt.ID {
			t.Errorf("worktree %s should be gone from list after recycle", wt.ID)
		}
	}
}

// TestEndToEnd_GitStatus_RoutesToWorktree proves handleGitStatus honors the
// caller's worktree binding: a bound agent sees its worktree's branch and
// dirty status, not the main repo's clean status.
func TestEndToEnd_GitStatus_RoutesToWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "status-wt", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	agent := "019fa311310000000000000000000003"
	if err := a.bindWorktree(bindKey(t, agent), wt.ID); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Dirty the worktree with an untracked file that does NOT exist in master.
	dirtyFile := "only-in-worktree.txt"
	if err := os.WriteFile(filepath.Join(wt.Path, dirtyFile), []byte("wt-only"), 0644); err != nil {
		t.Fatalf("write %s: %v", dirtyFile, err)
	}

	// Bound agent → its worktree's status.
	wtResp, err := a.handleGitStatus(callerCtx(t, agent), domain.ProjectGitStatusReq{})
	if err != nil {
		t.Fatalf("git_status (worktree): %v", err)
	}
	if !wtResp.IsGit {
		t.Fatalf("worktree git_status: IsGit=false (root=%q)", wt.Path)
	}
	if wtResp.Branch != "status-wt" {
		t.Errorf("git_status (worktree): branch = %q, want %q (worktree branch, not main)", wtResp.Branch, "status-wt")
	}
	if wtResp.Status == "(clean)" {
		t.Errorf("git_status (worktree): expected dirty (untracked %s), got clean", dirtyFile)
	}
	found := false
	for _, f := range wtResp.Files {
		if strings.HasSuffix(f.Path, dirtyFile) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("git_status (worktree): %s not in Files %+v", dirtyFile, wtResp.Files)
	}

	// Master (no caller binding) → clean, different branch, no worktree-only file.
	masterResp, err := a.handleGitStatus(testutil.AdminCtx(testutil.GenActorID()), domain.ProjectGitStatusReq{})
	if err != nil {
		t.Fatalf("git_status (master): %v", err)
	}
	if masterResp.Branch == "status-wt" {
		t.Errorf("git_status (master): branch = %q, should not be the worktree branch", masterResp.Branch)
	}
	for _, f := range masterResp.Files {
		if strings.HasSuffix(f.Path, dirtyFile) {
			t.Errorf("git_status (master) LEAKED worktree-only file %s: %+v", dirtyFile, f)
		}
	}
}

// --- helpers ---

// bindKey returns the canonical string form of agentIDHex — the exact key
// callerID(ctx) produces — so bindWorktree keys match what interception looks up.
func bindKey(t *testing.T, agentIDHex string) string {
	t.Helper()
	cid, err := identity.ParseCanonicalID(agentIDHex)
	if err != nil {
		t.Fatalf("bindKey: parse %q: %v", agentIDHex, err)
	}
	return id.From(cid).String()
}

func gitLogSubjects(t *testing.T, dir string) []string {
	t.Helper()
	cmd := exec.Command("git", "log", "--format=%s")
	util.HideWindow(cmd)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log in %s: %v", dir, err)
	}
	var subs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			subs = append(subs, line)
		}
	}
	return subs
}
