package project

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// ── ParentWorktreeID creation / derivation ──

func TestWorktreeCreate_WithParentDerivesFromParentBranch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "parent-wt"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	// Write + commit a file in the parent worktree so its branch HEAD advances.
	if err := os.WriteFile(filepath.Join(parent.Path, "parent-commit.txt"), []byte("parent content"), 0644); err != nil {
		t.Fatalf("write parent file: %v", err)
	}
	gitCommitInWorktree(t, parent.Path, "parent commit")

	// Create child with ParentWorktreeID set and no explicit BaseRef.
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "child-wt",
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if child.ParentWorktreeID != parent.ID {
		t.Fatalf("child.ParentWorktreeID=%q, want %q", child.ParentWorktreeID, parent.ID)
	}
	if child.BaseRef == "" || child.BaseRef == "HEAD" {
		t.Fatalf("child.BaseRef=%q, should be set to parent branch HEAD (not empty or HEAD)", child.BaseRef)
	}

	// The child must see the parent's committed file (derived from parent HEAD).
	if _, err := os.Stat(filepath.Join(child.Path, "parent-commit.txt")); os.IsNotExist(err) {
		t.Fatalf("child worktree missing parent-committed file: derivation from parent branch HEAD failed")
	}
}

func TestWorktreeCreate_ParentNotFound(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	_, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "orphan-child",
		ParentWorktreeID: "nonexistent-parent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent parent worktree")
	}
	if !strings.Contains(err.Error(), "parent worktree") {
		t.Fatalf("error should mention parent worktree, got: %v", err)
	}
}

func TestWorktreeCreate_BranchUniquenessPreCheck(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	if _, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "dup-branch"}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Second create with the same name/branch must fail the pre-check.
	if _, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "dup-branch"}); err == nil {
		t.Fatal("expected branch uniqueness error for duplicate branch name")
	}
}

func TestWorktreeCreate_WorkflowMapIDPersisted(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:          "wf-wt",
		WorkflowMapID: "map-1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if wt.WorkflowMapID != "map-1" {
		t.Fatalf("WorkflowMapID=%q, want map-1", wt.WorkflowMapID)
	}
}

// ── mergeChildIntoParent ──

func TestMergeChildIntoParent_Success(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "p-wt"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "c-wt",
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Child commits a change.
	if err := os.WriteFile(filepath.Join(child.Path, "child.txt"), []byte("child"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child work")

	merged, conflictFiles, err := a.mergeChildIntoParent(child.ID, parent.ID)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !merged {
		t.Fatal("expected merged=true")
	}
	if len(conflictFiles) != 0 {
		t.Fatalf("expected no conflict files, got %v", conflictFiles)
	}

	// Child worktree should be removed from metadata and disk.
	if _, ok := a.worktrees[child.ID]; ok {
		t.Fatal("child worktree should be deleted from metadata after merge")
	}
	if _, err := os.Stat(child.Path); !os.IsNotExist(err) {
		t.Fatal("child worktree dir should be removed after merge")
	}

	// Parent should contain the child's commit.
	if _, err := os.Stat(filepath.Join(parent.Path, "child.txt")); os.IsNotExist(err) {
		t.Fatal("parent branch should contain the child's committed file")
	}
}

func TestMergeChildIntoParent_DirtyChildRejected(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "p-wt"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "c-wt",
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Child has an uncommitted change.
	if err := os.WriteFile(filepath.Join(child.Path, "uncommitted.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatal(err)
	}

	_, _, err = a.mergeChildIntoParent(child.ID, parent.ID)
	if err == nil {
		t.Fatal("expected error for dirty child")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("error should mention uncommitted changes, got: %v", err)
	}
	// Child worktree must be intact after rejected merge.
	if _, ok := a.worktrees[child.ID]; !ok {
		t.Fatal("child worktree must remain in metadata after rejected merge")
	}
}

func TestMergeChildIntoParent_ConflictAbortsAndKeepsChild(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "p-wt"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "c-wt",
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Both modify the same file differently.
	if err := os.WriteFile(filepath.Join(parent.Path, "same.txt"), []byte("parent version"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, parent.Path, "parent edits same.txt")

	if err := os.WriteFile(filepath.Join(child.Path, "same.txt"), []byte("child version"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child edits same.txt")

	merged, conflictFiles, err := a.mergeChildIntoParent(child.ID, parent.ID)
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if merged {
		t.Fatal("expected merged=false on conflict")
	}
	if len(conflictFiles) == 0 {
		t.Log("note: conflict file names not parsed from this git version")
	} else {
		found := false
		for _, f := range conflictFiles {
			if strings.Contains(f, "same.txt") {
				found = true
			}
		}
		if !found {
			t.Fatalf("conflict files should include same.txt, got %v", conflictFiles)
		}
	}

	// Child worktree must be intact.
	if _, ok := a.worktrees[child.ID]; !ok {
		t.Fatal("child worktree must remain after conflict")
	}
	if _, err := os.Stat(child.Path); os.IsNotExist(err) {
		t.Fatal("child worktree dir must remain after conflict")
	}
	// Parent must be back to pre-merge state (no mid-merge state).
	dirty, err := gitWorktreeHasUncommitted(parent.Path)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("parent should be clean after merge abort")
	}
}

func TestMergeChildIntoParent_MissingWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "p-wt"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	_, _, err = a.mergeChildIntoParent("missing-child", parent.ID)
	if err == nil {
		t.Fatal("expected error for missing child worktree")
	}
	_, _, err = a.mergeChildIntoParent("x", "missing-parent")
	if err == nil {
		t.Fatal("expected error for missing parent worktree")
	}
}

func TestMergeChildIntoParent_RestoresRuntimeState(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	// Commit a runtime-state file on the main branch so it exists in HEAD.
	runtimeRel := ".sporecode/wiki/state-probe.md"
	runtimeAbs := filepath.Join(dir, runtimeRel)
	if err := os.MkdirAll(filepath.Dir(runtimeAbs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimeAbs, []byte("parent runtime state"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, dir, "add runtime state")

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "p-wt", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "c-wt",
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Child modifies the runtime-state file and commits.
	if err := os.MkdirAll(filepath.Join(child.Path, ".sporecode", "wiki"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child.Path, runtimeRel), []byte("child runtime state (clobber)"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child clobbers runtime state")

	// Parent diverges with a different change so the branches diverge
	// (without this, git fast-forwards and there's no merge commit).
	if err := os.WriteFile(filepath.Join(parent.Path, "parent-feature.txt"), []byte("parent feature"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, parent.Path, "parent adds feature file")

	merged, _, err := a.mergeChildIntoParent(child.ID, parent.ID)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !merged {
		t.Fatal("expected merged=true")
	}

	// After the merge, the parent worktree's runtime-state file should be
	// restored to the parent's pre-merge version (not the child's clobber).
	data, err := os.ReadFile(filepath.Join(parent.Path, runtimeRel))
	if err != nil {
		t.Fatalf("read runtime state after merge: %v", err)
	}
	if !strings.Contains(string(data), "parent runtime state") {
		t.Fatalf("runtime state not restored: got %q", string(data))
	}

	// Parent worktree should be clean (restored files were committed via --amend).
	dirty, err := gitWorktreeHasUncommitted(parent.Path)
	if err != nil {
		t.Fatalf("check parent clean: %v", err)
	}
	if dirty {
		t.Fatal("parent worktree should be clean after runtime state restore + amend")
	}
}

// ── rebaseToParent ──

func TestRebaseToParent_Success(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "p-wt"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "c-wt",
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Child commits its own work.
	if err := os.WriteFile(filepath.Join(child.Path, "child.txt"), []byte("child"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child work")

	// Parent advances with new commits.
	if err := os.WriteFile(filepath.Join(parent.Path, "parent-new.txt"), []byte("parent work"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, parent.Path, "parent work")

	conflictFiles, err := a.rebaseToParent(child.ID, parent.ID)
	if err != nil {
		t.Fatalf("rebase: %v", err)
	}
	if len(conflictFiles) != 0 {
		t.Fatalf("expected no conflicts, got %v", conflictFiles)
	}

	// Child should now contain the parent's new commit.
	if _, err := os.Stat(filepath.Join(child.Path, "parent-new.txt")); os.IsNotExist(err) {
		t.Fatalf("child should contain parent's new file after rebase")
	}
	// Child's own work must be preserved.
	if _, err := os.Stat(filepath.Join(child.Path, "child.txt")); os.IsNotExist(err) {
		t.Fatalf("child's own file should survive rebase")
	}
}

func TestRebaseToParent_ConflictAborts(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "p-wt"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "c-wt",
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Child commits a file.
	if err := os.WriteFile(filepath.Join(child.Path, "same.txt"), []byte("child"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, child.Path, "child same.txt")

	// Parent also commits the same file differently.
	if err := os.WriteFile(filepath.Join(parent.Path, "same.txt"), []byte("parent"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, parent.Path, "parent same.txt")

	_, err = a.rebaseToParent(child.ID, parent.ID)
	if err == nil {
		t.Fatal("expected rebase conflict error")
	}

	// Child must not be left mid-rebase; git status should be clean (aborted).
	dirty, err := gitWorktreeHasUncommitted(child.Path)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("child should be clean after rebase abort (no mid-rebase state)")
	}
}

func TestRebaseToParent_MissingWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "p-wt"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := a.rebaseToParent("missing-child", parent.ID); err == nil {
		t.Fatal("expected error for missing child")
	}
	if _, err := a.rebaseToParent("x", "missing-parent"); err == nil {
		t.Fatal("expected error for missing parent")
	}
}

// ── Owner stale protection + recursive child stale marking ──

func TestReleaseBinding_OwnerWorktreeMarkedStaleNotRemoved(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	// Create a workflow owner worktree (WorkflowMapID set, no ParentWorktreeID).
	owner, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:          "wf-owner",
		WorkflowMapID: "map-1",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	// Create a child.
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-child",
		ParentWorktreeID: owner.ID,
		WorkflowMapID:    "map-1",
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	const ownerAgent = "agent/owner/000000000000000000000001"
	if err := a.bindWorktree(ownerAgent, owner.ID); err != nil {
		t.Fatalf("bind owner: %v", err)
	}

	// Release the owner agent's binding without a workflow_stop.
	if err := a.releaseWorktreeBinding(ctx, ownerAgent, false); err != nil {
		t.Fatalf("release binding: %v", err)
	}

	// The owner worktree must still exist, marked stale, not deleted.
	got, ok := a.worktrees[owner.ID]
	if !ok {
		t.Fatal("owner worktree must remain in metadata after release (stale protection)")
	}
	if got.Status != "stale" {
		t.Fatalf("owner worktree Status=%q, want stale", got.Status)
	}
	// The owner worktree directory must still exist on disk.
	if _, err := os.Stat(owner.Path); os.IsNotExist(err) {
		t.Fatal("owner worktree dir must remain on disk (stale protection)")
	}

	// Child worktree must be recursively marked stale.
	gotChild, ok := a.worktrees[child.ID]
	if !ok {
		t.Fatal("child worktree should still exist")
	}
	if gotChild.Status != "stale" {
		t.Fatalf("child worktree Status=%q, want stale (recursive marking)", gotChild.Status)
	}
}

func TestReleaseBinding_ForceDelete_RemovesOwnerWorktreeAndClearsStamp(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	// Create a workflow owner worktree.
	owner, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:          "wf-owner-force",
		WorkflowMapID: "map-force",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}

	// Stamp ownerWorktreeId into a mock map card.
	if err := a.store.Save(&CardRecord{
		Title: "map-force",
		Type:  "workflow",
		Raw:   "---\nid: map-force\ntype: workflow\ndata:\n  ownerWorktreeId: " + owner.ID + "\n---\n\nbody",
	}); err != nil {
		t.Fatalf("save card: %v", err)
	}

	const ownerAgent = "agent/owner/0000000000000000000000ff"
	if err := a.bindWorktree(ownerAgent, owner.ID); err != nil {
		t.Fatalf("bind owner: %v", err)
	}

	// Release with forceDelete=true (explicit agent deletion path).
	if err := a.releaseWorktreeBinding(ctx, ownerAgent, true); err != nil {
		t.Fatalf("release binding: %v", err)
	}

	// The owner worktree must be physically removed, not marked stale.
	if _, ok := a.worktrees[owner.ID]; ok {
		t.Fatal("owner worktree must be removed when forceDelete=true")
	}
	// The worktree directory must be gone from disk.
	if _, err := os.Stat(owner.Path); !os.IsNotExist(err) {
		t.Fatal("owner worktree dir must be removed from disk when forceDelete=true")
	}

	// The map card's ownerWorktreeId stamp must be cleared.
	card, err := a.store.Get("map-force")
	if err != nil {
		t.Fatalf("get card: %v", err)
	}
	if strings.Contains(card.Raw, "ownerWorktreeId") {
		t.Errorf("ownerWorktreeId should be cleared from card raw:\n%s", card.Raw)
	}
}

func TestReleaseBinding_NonOwnerWorktreeStillRemoved(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	// Plain worktree without WorkflowMapID → normal force-remove on release.
	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "plain-wt"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const agentID = "agent/plain/000000000000000000000001"
	if err := a.bindWorktree(agentID, wt.ID); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := a.releaseWorktreeBinding(ctx, agentID, false); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, ok := a.worktrees[wt.ID]; ok {
		t.Fatal("non-owner worktree must be removed after release")
	}
}

// ── bindWorktree existing-binding check ──

func TestBindWorktree_RejectsRebindToDifferentWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt1, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "wt-one"})
	if err != nil {
		t.Fatalf("create wt1: %v", err)
	}
	wt2, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "wt-two"})
	if err != nil {
		t.Fatalf("create wt2: %v", err)
	}
	const agentID = "agent/rebind/000000000000000000000001"
	if err := a.bindWorktree(agentID, wt1.ID); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	err = a.bindWorktree(agentID, wt2.ID)
	if err == nil {
		t.Fatal("expected rebind error for already-bound agent")
	}
	if !strings.Contains(err.Error(), "already bound") {
		t.Fatalf("error should mention existing binding, got: %v", err)
	}
	// Original binding must be preserved.
	if got := a.agentWorktree[agentID]; got != wt1.ID {
		t.Fatalf("binding after failed rebind: %q, want %q", got, wt1.ID)
	}
}

func TestBindWorktree_SameWorktreeIdempotent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "wt-ido"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const agentID = "agent/ido/000000000000000000000001"
	if err := a.bindWorktree(agentID, wt.ID); err != nil {
		t.Fatalf("first bind: %v", err)
	}
	// Re-binding to the same worktree is idempotent (no error).
	if err := a.bindWorktree(agentID, wt.ID); err != nil {
		t.Fatalf("rebind same worktree should be idempotent: %v", err)
	}
}

// ── reconcileWorktrees reverse discovery + agent liveness ──

func TestReconcile_ReverseDiscoversExternalWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	// Create a worktree directly via git (bypassing the actor) to simulate an
	// external worktree that the actor metadata does not know about.
	extPath := filepath.Join(dir, "ext-wt")
	if _, err := gitRun(nil, dir, "worktree", "add", "-b", "ext-branch", extPath); err != nil {
		t.Fatalf("git worktree add: %v", err)
	}

	// Manually remove the actor's knowledge of worktrees so reverse discovery
	// is the only path that can find it.
	a.worktreeParentMu.Lock()
	a.bindingMu.Lock()
	a.worktrees = map[string]gen.ProjectWorktree{}
	a.agentWorktree = map[string]string{}
	a.bindingMu.Unlock()
	a.worktreeParentMu.Unlock()

	changed, err := a.reverseDiscoverWorktrees()
	if err != nil {
		t.Fatalf("reverse discovery: %v", err)
	}
	if !changed {
		t.Fatal("expected reverse discovery to add the external worktree")
	}
	found := false
	for _, wt := range a.worktrees {
		if filepath.Clean(wt.Path) == filepath.Clean(extPath) {
			found = true
			if wt.Status != "stale" {
				t.Fatalf("discovered external worktree Status=%q, want stale", wt.Status)
			}
		}
	}
	if !found {
		t.Fatal("external worktree not discovered in metadata")
	}
}

// TestReconcile_BindingSurvivesRestartWithUnloadedAgent covers crash recovery:
// after restart agents come back lazy-unloaded, so they cannot be resolved via
// the actor system. Reconcile must keep their bindings and leave the worktree
// active — only an explicit release call may clear a binding.
func TestReconcile_BindingSurvivesRestartWithUnloadedAgent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "liveness-wt"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Bind a caller the FakeCtx cannot resolve (lazy-unloaded agent).
	const unloadedAgent = "01a000000000000000000000000000ff"
	a.bindingMu.Lock()
	if a.agentWorktree == nil {
		a.agentWorktree = make(map[string]string)
	}
	a.agentWorktree[unloadedAgent] = wt.ID
	a.bindingMu.Unlock()

	changed, err := a.reverseDiscoverWorktrees()
	if err != nil {
		t.Fatalf("reverse discovery: %v", err)
	}
	if changed {
		t.Fatal("reconcile must not touch state when all worktrees are known")
	}
	a.worktreeParentMu.RLock()
	a.bindingMu.RLock()
	_, stillBound := a.agentWorktree[unloadedAgent]
	wtState := a.worktrees[wt.ID].Status
	a.bindingMu.RUnlock()
	a.worktreeParentMu.RUnlock()
	if !stillBound {
		t.Fatal("binding of an unloaded agent must survive reconcile across restart")
	}
	if wtState != "active" {
		t.Fatalf("worktree Status=%q, want active while its agent is merely unloaded", wtState)
	}
}

// TestReconcile_SharedBindingsSurviveSweep covers fork-child worktree
// inheritance: parent and transient child share one binding entry per actor.
// Neither binding may be swept by reconcile — cleanup belongs to the explicit
// release path when each agent is destroyed.
func TestReconcile_SharedBindingsSurviveSweep(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "shared-wt"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const parentID = "01a000000000000000000000000000aa"
	const childID = "01a000000000000000000000000000bb"

	a.bindingMu.Lock()
	if a.agentWorktree == nil {
		a.agentWorktree = make(map[string]string)
	}
	a.agentWorktree[parentID] = wt.ID
	a.agentWorktree[childID] = wt.ID
	a.bindingMu.Unlock()

	changed, err := a.reverseDiscoverWorktrees()
	if err != nil {
		t.Fatalf("reverse discovery: %v", err)
	}
	if changed {
		t.Fatal("reconcile must not touch state when all worktrees are known")
	}
	a.worktreeParentMu.RLock()
	a.bindingMu.RLock()
	_, childBound := a.agentWorktree[childID]
	_, parentBound := a.agentWorktree[parentID]
	wtState := a.worktrees[wt.ID].Status
	a.bindingMu.RUnlock()
	a.worktreeParentMu.RUnlock()
	if !childBound || !parentBound {
		t.Fatal("both shared bindings must survive reconcile across restart")
	}
	if wtState != "active" {
		t.Fatalf("shared worktree Status=%q, want active", wtState)
	}
}

// ── symlink detection ──

func TestDetectSymlinkInWalk_RejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}
	if err := detectSymlinkInWalk(dir); err == nil {
		t.Fatal("expected symlink detection error")
	}
}

func TestDetectSymlinkInWalk_CleanDirOK(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub", "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "nested", "b.txt"), []byte("y"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := detectSymlinkInWalk(dir); err != nil {
		t.Fatalf("clean dir should pass detection, got: %v", err)
	}
}

// ── mergeChildIntoParent "gone" convergence ──

// TestMergeToParent_ChildGoneFromRegistry proves the double-completion
// incident shape: a first merge removes the child (registry entry deleted),
// then a second review approve re-merges the same child ID. The second call
// must report Status="gone" with no error so the workspace approve path
// proceeds to teardown instead of auto-rejecting with a false-positive
// conflict and resurrecting the worker on a removed worktree.
func TestMergeToParent_ChildGoneFromRegistry(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "p-gone"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "c-gone",
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// First completion: merge succeeds and removes the child.
	if _, err := a.handleWorktreeMergeToParent(nil, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: parent.ID,
	}); err != nil {
		t.Fatalf("first merge: %v", err)
	}

	// Second completion (retry / double approve) on the same child ID.
	resp, err := a.handleWorktreeMergeToParent(nil, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("second merge must not error on gone child: %v", err)
	}
	if resp.Status != "gone" {
		t.Fatalf("second merge Status=%q, want gone", resp.Status)
	}
}

// TestMergeToParent_ChildDeadCheckout proves the stale-registry shape: the
// child's checkout was removed out-of-band (git no longer knows the path)
// while the registry entry survives. The merge must purge the entry, report
// "gone", and not error with "must be run in a work tree".
func TestMergeToParent_ChildDeadCheckout(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "p-dead"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "c-dead",
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Out-of-band removal: raw git removes the checkout, registry untouched.
	gitOut(t, dir, "worktree", "remove", "--force", child.Path)
	if _, ok := a.worktrees[child.ID]; !ok {
		t.Fatal("precondition: registry entry should still exist after out-of-band removal")
	}

	resp, err := a.handleWorktreeMergeToParent(nil, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("merge on dead checkout: %v", err)
	}
	if resp.Status != "gone" {
		t.Fatalf("Status=%q, want gone", resp.Status)
	}
	if _, ok := a.worktrees[child.ID]; ok {
		t.Fatal("dead child worktree should be purged from registry")
	}
}

// TestMergeToParent_ChildAlreadyMerged proves that when the child's branch is
// already an ancestor of the parent HEAD (e.g. from a prior partial merge),
// the merge is treated as already done — the child worktree is cleaned up
// and the status is "merged", not "conflict". Without the ancestor check,
// `git merge --no-ff` on an already-merged branch would fail or conflict,
// causing a false-positive auto-reject on review approve.
func TestMergeToParent_ChildAlreadyMerged(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "p-merged"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "c-merged",
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// Commit a file in the child so it has divergent content.
	if err := os.WriteFile(filepath.Join(child.Path, "child-file.txt"), []byte("child work"), 0644); err != nil {
		t.Fatalf("write child file: %v", err)
	}
	gitCommitInWorktree(t, child.Path, "child commit")

	// Manually merge the child branch into the parent, simulating a prior
	// out-of-band merge that carried the child's commits.
	gitOut(t, parent.Path, "merge", "--no-ff", "-m", "manual pre-merge", child.Branch)

	// Now the project merge is called — child is already an ancestor of
	// parent HEAD. It must detect this and return "merged", not "conflict".
	resp, err := a.handleWorktreeMergeToParent(nil, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("merge already-merged child: %v", err)
	}
	if resp.Status != "merged" {
		t.Fatalf("Status=%q, want merged", resp.Status)
	}
	if _, ok := a.worktrees[child.ID]; ok {
		t.Fatal("already-merged child worktree should be removed from registry")
	}
}

// TestMergeToParent_ChildSameHeadAsParent proves the same-HEAD case: when
// child and parent are at the identical commit (no divergent work), the merge
// is a no-op and the child is cleaned up as "merged".
func TestMergeToParent_ChildSameHeadAsParent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	parent, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "p-same"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "c-same",
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	// No commits in child — both at the same base HEAD. Merge must detect
	// same-HEAD and clean up without conflict.
	resp, err := a.handleWorktreeMergeToParent(nil, gen.ProjectWorktreeMergeToParentReq{
		ChildWorktreeID:  child.ID,
		ParentWorktreeID: parent.ID,
	})
	if err != nil {
		t.Fatalf("merge same-HEAD child: %v", err)
	}
	if resp.Status != "merged" {
		t.Fatalf("Status=%q, want merged", resp.Status)
	}
	if _, ok := a.worktrees[child.ID]; ok {
		t.Fatal("same-HEAD child worktree should be removed from registry")
	}
}

// TestDiscardDeadWorktree_Converges proves discardWorktreeByID tolerates a
// dead checkout (git already removed it) instead of failing forever on
// "is not a working tree", and clears the agent binding.
func TestDiscardDeadWorktree_Converges(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	agent := "019fb006000000000000000000000007"

	entered, err := a.handleWorktreeEnter(callerCtx(t, agent), gen.ProjectWorktreeEnterReq{Name: "dead-discard"})
	if err != nil {
		t.Fatalf("enter: %v", err)
	}
	wtID := entered.Worktree.ID
	path := entered.Worktree.Path

	gitOut(t, dir, "worktree", "remove", "--force", path)

	if err := a.discardWorktreeByID(nil, wtID, true); err != nil {
		t.Fatalf("discard dead worktree: %v", err)
	}
	if _, ok := a.worktrees[wtID]; ok {
		t.Fatal("discarded dead worktree should be purged from registry")
	}
	if _, ok := a.callerBoundWorktree(bindKey(t, agent)); ok {
		t.Fatal("discard should clear the agent binding")
	}
}