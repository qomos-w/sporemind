package project

// Phase 3 tests: workflow owner worktree binding —
//   - setKeyInDataBlock inserts/replaces data.ownerWorktreeId in frontmatter
//   - workflow_create_worktree stamps data.ownerWorktreeId into the map card
//   - workflow_stop_merge_worktree rebases the owner worktree to latest main
//     before merging back, and fails cleanly on conflicts / dirty worktrees

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/util"
)

// gitRunMust runs git in dir and fails the test on error.
func gitRunMust(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	util.HideWindow(cmd)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func TestSetKeyInDataBlock_OwnerWorktreeID(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "insert into existing data block",
			raw:  "---\nid: m\ntype: workflow\ndata:\n  ownerAgentId: a-1\n---\n\nBody.",
			want: "ownerWorktreeId: wt-1",
		},
		{
			name: "replace existing worktree id",
			raw:  "---\nid: m\ntype: workflow\ndata:\n  ownerWorktreeId: wt-old\n---\n\nBody.",
			want: "ownerWorktreeId: wt-1",
		},
		{
			name: "add data block when missing",
			raw:  "---\nid: m\ntype: workflow\n---\n\nBody.",
			want: "ownerWorktreeId: wt-1",
		},
		{
			name: "preserve sibling keys",
			raw:  "---\nid: m\ntype: workflow\ndata:\n  include:\n    - a\n  ownerAgentId: a-1\n---\n\nBody.",
			want: "ownerAgentId: a-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := setKeyInDataBlock(tt.raw, "ownerWorktreeId", "wt-1")
			if !strings.Contains(result, tt.want) {
				t.Errorf("result does not contain %q:\n%s", tt.want, result)
			}
			if strings.Contains(result, "ownerWorktreeId: wt-old") {
				t.Error("old worktree id not replaced")
			}
			// The frontmatter must remain parseable: the helper is
			// validated by the same validateCard the handler runs.
			if err := validateCard("m", result); err != nil {
				t.Errorf("result fails card validation: %v\n%s", err, result)
			}
		})
	}
}

// TestHandleWorkflowCreateWorktree_StampsMapCard verifies the callable creates
// the owner worktree, binds the owner agent, and stamps data.ownerWorktreeId
// into the workflow map card's frontmatter.
func TestHandleWorkflowCreateWorktree_StampsMapCard(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	if _, err := a.handleWikiCreateMap(ctx, domain.WikiCreateMapReq{ID: "wf-phase3"}); err != nil {
		t.Fatalf("create map: %v", err)
	}

	resp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-phase3",
		AgentActorID:  "owner-agent-1",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	if resp.WorktreeID == "" {
		t.Fatal("expected non-empty worktree ID")
	}

	card, err := a.store.Get("wf-phase3")
	if err != nil {
		t.Fatalf("fetch map card: %v", err)
	}
	got, _ := card.Data["ownerWorktreeId"].(string)
	if got != resp.WorktreeID {
		t.Fatalf("data.ownerWorktreeId = %q, want %q", got, resp.WorktreeID)
	}

	// Idempotent stamping: writing the same worktree ID again is a no-op.
	if err := a.stampMapOwnerWorktree(ctx, "wf-phase3", resp.WorktreeID); err != nil {
		t.Fatalf("re-stamp map card: %v", err)
	}
	card2, _ := a.store.Get("wf-phase3")
	got2, _ := card2.Data["ownerWorktreeId"].(string)
	if got2 != resp.WorktreeID {
		t.Fatalf("data.ownerWorktreeId after re-stamp = %q, want %q", got2, resp.WorktreeID)
	}
	if strings.Count(card2.Raw, "ownerWorktreeId") != 1 {
		t.Fatalf("ownerWorktreeId must appear exactly once after re-stamp:\n%s", card2.Raw)
	}
}

// TestRebaseWorktreeToMain_SkipsWhenBaseIsAncestor verifies that an unchanged
// base does not rewrite the worktree commit identity.
func TestRebaseWorktreeToMain_SkipsWhenBaseIsAncestor(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "no-rebase-wt"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "owner.txt"), []byte("owner"), 0644); err != nil {
		t.Fatalf("write worktree file: %v", err)
	}
	gitRunMust(t, wt.Path, "add", "owner.txt")
	gitRunMust(t, wt.Path, "commit", "-m", "owner work")
	before := strings.TrimSpace(gitRunMust(t, wt.Path, "rev-parse", "HEAD"))

	if err := a.rebaseWorktreeToMain(wt.ID, ""); err != nil {
		t.Fatalf("rebase worktree: %v", err)
	}
	after := strings.TrimSpace(gitRunMust(t, wt.Path, "rev-parse", "HEAD"))
	if after != before {
		t.Fatalf("worktree commit was rewritten without base changes: before=%s after=%s", before, after)
	}
}

// TestHandleWorkflowStopMergeWorktree_RebasesToMain verifies that stopping a
// workflow merges the owner worktree branch after rebasing onto a main branch
// that has advanced since the worktree was created.
func TestHandleWorkflowStopMergeWorktree_RebasesToMain(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	// Create the owner worktree from the initial HEAD.
	resp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-rebase",
		AgentActorID:  "owner-1",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}

	// Owner does work in its worktree: commit a change on wf/<map>.
	wt, ok := a.worktrees[resp.WorktreeID]
	if !ok {
		t.Fatalf("worktree %q not registered", resp.WorktreeID)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "wf-work.txt"), []byte("owner work"), 0644); err != nil {
		t.Fatalf("write owner file: %v", err)
	}
	gitCommitInWorktree(t, wt.Path, "owner work commit")

	// Meanwhile main advances with an unrelated change.
	if err := os.WriteFile(filepath.Join(dir, "main-advance.txt"), []byte("main work"), 0644); err != nil {
		t.Fatalf("write main file: %v", err)
	}
	gitCommitInWorktree(t, dir, "main advanced")

	// stop_merge_worktree must rebase wf/<map> onto the new main HEAD, then
	// merge it into main.
	stopResp, err := a.handleWorkflowStopMergeWorktree(ctx, gen.ProjectWorkflowStopMergeWorktreeReq{
		WorktreeID: resp.WorktreeID,
	})
	if err != nil {
		t.Fatalf("workflow_stop_merge_worktree: %v", err)
	}
	if stopResp.Status != "merged" {
		t.Fatalf("status = %q, want merged", stopResp.Status)
	}

	// Both the owner work and the main advance must be present on main.
	out := gitRunMust(t, dir, "log", "--oneline", "--all")
	if !strings.Contains(out, "owner work commit") {
		t.Errorf("owner commit missing from history:\n%s", out)
	}
	if !strings.Contains(out, "main advanced") {
		t.Errorf("main advance missing from history:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "wf-work.txt")); os.IsNotExist(err) {
		t.Error("owner file missing on main after merge")
	}
}

// TestHandleWorkflowStopMergeWorktree_RequireSyncedRejectsOutdated verifies
// the workflow_stop path: RequireSynced=true must NOT auto-rebase. When main
// advanced since the worktree branched off, the callable rejects with
// Status="outdated" (and an error naming the rebase) without touching git
// state — the owner is expected to rebase itself and retry.
func TestHandleWorkflowStopMergeWorktree_RequireSyncedRejectsOutdated(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	resp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-outdated",
		AgentActorID:  "owner-1",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	wt, ok := a.worktrees[resp.WorktreeID]
	if !ok {
		t.Fatalf("worktree %q not registered", resp.WorktreeID)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "wf-work.txt"), []byte("owner work"), 0644); err != nil {
		t.Fatalf("write owner file: %v", err)
	}
	gitCommitInWorktree(t, wt.Path, "owner work commit")
	before := strings.TrimSpace(gitRunMust(t, wt.Path, "rev-parse", "HEAD"))

	// Main advances with an unrelated change the worktree does not contain.
	if err := os.WriteFile(filepath.Join(dir, "main-advance.txt"), []byte("main work"), 0644); err != nil {
		t.Fatalf("write main file: %v", err)
	}
	gitCommitInWorktree(t, dir, "main advanced")

	stopResp, stopErr := a.handleWorkflowStopMergeWorktree(ctx, gen.ProjectWorkflowStopMergeWorktreeReq{
		WorktreeID:    resp.WorktreeID,
		RequireSynced: true,
	})
	if stopErr == nil {
		t.Fatal("expected outdated rejection, got success")
	}
	if stopResp.Status != "outdated" {
		t.Fatalf("status = %q, want outdated", stopResp.Status)
	}
	base, branchErr := defaultBranch(dir)
	if branchErr != nil {
		t.Fatalf("resolve default branch: %v", branchErr)
	}
	if !strings.Contains(stopErr.Error(), "rebase") || !strings.Contains(stopErr.Error(), base) {
		t.Fatalf("error should guide the owner to rebase onto %q: %v", base, stopErr)
	}
	// No git state change: the worktree branch HEAD is untouched and the
	// owner commit must not have leaked onto main.
	after := strings.TrimSpace(gitRunMust(t, wt.Path, "rev-parse", "HEAD"))
	if after != before {
		t.Fatalf("worktree HEAD rewritten despite no auto-rebase: before=%s after=%s", before, after)
	}
	logOut := gitRunMust(t, dir, "log", "--oneline", "--first-parent", base)
	if strings.Contains(logOut, "owner work commit") {
		t.Errorf("owner commit merged onto main despite outdated rejection:\n%s", logOut)
	}
	if _, ok := a.worktrees[resp.WorktreeID]; !ok {
		t.Error("worktree was discarded despite outdated rejection")
	}
}

// TestHandleWorkflowStopMergeWorktree_RequireSyncedMergesAfterManualRebase
// verifies the happy path of the workflow_stop contract: after the owner
// rebases its worktree onto the latest base HEAD itself, RequireSynced=true
// verifies (no rebase attempt) and merges cleanly.
func TestHandleWorkflowStopMergeWorktree_RequireSyncedMergesAfterManualRebase(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	resp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-synced",
		AgentActorID:  "owner-1",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	wt, ok := a.worktrees[resp.WorktreeID]
	if !ok {
		t.Fatalf("worktree %q not registered", resp.WorktreeID)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "wf-work.txt"), []byte("owner work"), 0644); err != nil {
		t.Fatalf("write owner file: %v", err)
	}
	gitCommitInWorktree(t, wt.Path, "owner work commit")
	if err := os.WriteFile(filepath.Join(dir, "main-advance.txt"), []byte("main work"), 0644); err != nil {
		t.Fatalf("write main file: %v", err)
	}
	gitCommitInWorktree(t, dir, "main advanced")

	// The owner syncs the worktree itself, exactly as the prompt instructs.
	base, err := defaultBranch(dir)
	if err != nil {
		t.Fatalf("resolve default branch: %v", err)
	}
	gitRunMust(t, wt.Path, "rebase", base)

	stopResp, err := a.handleWorkflowStopMergeWorktree(ctx, gen.ProjectWorkflowStopMergeWorktreeReq{
		WorktreeID:    resp.WorktreeID,
		RequireSynced: true,
	})
	if err != nil {
		t.Fatalf("workflow_stop_merge_worktree: %v", err)
	}
	if stopResp.Status != "merged" {
		t.Fatalf("status = %q, want merged", stopResp.Status)
	}
	if _, err := os.Stat(filepath.Join(dir, "wf-work.txt")); os.IsNotExist(err) {
		t.Error("owner file missing on main after merge")
	}
	if _, err := os.Stat(filepath.Join(dir, "main-advance.txt")); os.IsNotExist(err) {
		t.Error("main advance missing after merge")
	}
}

// TestHandleWorkflowStopMergeWorktree_DirtyWorktreeFails verifies that a
// workflow owner worktree with uncommitted changes cannot be stopped: the
// caller must commit or discard first.
func TestHandleWorkflowStopMergeWorktree_DirtyWorktreeFails(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	resp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-dirty",
		AgentActorID:  "owner-1",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}
	wt, ok := a.worktrees[resp.WorktreeID]
	if !ok {
		t.Fatalf("worktree %q not registered", resp.WorktreeID)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "uncommitted.txt"), []byte("dirty"), 0644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	_, err = a.handleWorkflowStopMergeWorktree(ctx, gen.ProjectWorkflowStopMergeWorktreeReq{
		WorktreeID: resp.WorktreeID,
	})
	if err == nil {
		t.Fatal("expected error for dirty owner worktree")
	}
	if !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("error should mention uncommitted changes, got: %v", err)
	}
}

// TestHandleWorkflowStopMergeWorktree_UnknownWorktree verifies the guard for a
// worktree the actor does not know about.
func TestHandleWorkflowStopMergeWorktree_UnknownWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	_, err := a.handleWorkflowStopMergeWorktree(nil, gen.ProjectWorkflowStopMergeWorktreeReq{
		WorktreeID: "no-such-worktree",
	})
	if err == nil {
		t.Fatal("expected error for unknown worktree")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error should mention not found, got: %v", err)
	}
}

// TestHandleWorkflowStopMergeWorktree_CleansAgentBindings verifies that after a
// successful workflow_stop merge, the owner's agentWorktree binding is removed
// so subsequent file/git operations are not blocked by a stale binding.
func TestHandleWorkflowStopMergeWorktree_CleansAgentBindings(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	resp, err := a.handleWorkflowCreateWorktree(ctx, gen.ProjectWorkflowCreateWorktreeReq{
		WorkflowMapID: "wf-clean",
		AgentActorID:  "owner-clean",
	})
	if err != nil {
		t.Fatalf("workflow_create_worktree: %v", err)
	}

	if err := a.bindWorktree("owner-clean", resp.WorktreeID); err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}

	wtID, ok := a.callerBoundWorktree("owner-clean")
	if !ok || wtID != resp.WorktreeID {
		t.Fatalf("expected binding to %q, got %q ok=%v", resp.WorktreeID, wtID, ok)
	}

	wt := a.worktrees[resp.WorktreeID]
	if err := os.WriteFile(filepath.Join(wt.Path, "clean-test.txt"), []byte("clean"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	gitCommitInWorktree(t, wt.Path, "clean test commit")

	stopResp, err := a.handleWorkflowStopMergeWorktree(ctx, gen.ProjectWorkflowStopMergeWorktreeReq{
		WorktreeID: resp.WorktreeID,
	})
	if err != nil {
		t.Fatalf("workflow_stop_merge_worktree: %v", err)
	}
	if stopResp.Status != "merged" {
		t.Fatalf("status = %q, want merged", stopResp.Status)
	}

	_, ok = a.callerBoundWorktree("owner-clean")
	if ok {
		t.Error("expected agentWorktree binding to be cleared after workflow_stop merge")
	}

	if _, exists := a.worktrees[resp.WorktreeID]; exists {
		t.Error("expected worktree record to be deleted after merge")
	}
}
