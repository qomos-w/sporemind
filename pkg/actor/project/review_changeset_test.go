package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"github.com/qomos-w/sporemind/pkg/util"
)

// reviewChangesetTestActor creates a project.Actor backed by a real git repo
// and returns it with a worktree already bound to a test agent.
func reviewChangesetTestActor(t *testing.T) (*Actor, *testutil.FakeCtx, string, string) {
	t.Helper()
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	// Create a worktree and bind an agent.
	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "review-test"})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	agentID := "019fd32f831b00000000000000000999"
	if err := a.bindWorktree(agentID, wt.ID); err != nil {
		t.Fatalf("bind worktree: %v", err)
	}
	// Record parent for authorization tests.
	a.worktreeParentMu.Lock()
	if a.agentParent == nil {
		a.agentParent = make(map[string]string)
	}
	a.agentParent[agentID] = "019fd32f831b00000000000000000888"
	a.worktreeParentMu.Unlock()

	return a, ctx, wt.Path, agentID
}

// TestReviewChangeset_DisplayNameRejectedWithFormatContract pins the ID
// format contract: workspace.agent_review/agent_terminate accept either the
// canonical hex actor id or the display name, but the project-side review
// maps (agentWorktree/agentParent/review bindings) are keyed by the hex id
// only. A display name must fail fast with the format contract — not with
// the misleading authorization or "not bound to a worktree" errors.
func TestReviewChangeset_DisplayNameRejectedWithFormatContract(t *testing.T) {
	a, ctx, _, agentID := reviewChangesetTestActor(t)

	_, err := a.handleReviewChangesetSummary(ctx, gen.ProjectReviewChangesetReq{
		AgentActorID: "Worker#0001",
		CallerAgentID: "019fd32f831b00000000000000000888",
	})
	if err == nil {
		t.Fatal("expected display name to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "canonical hex actor id") {
		t.Errorf("expected format-contract error, got %v", err)
	}
	if strings.Contains(err.Error(), "direct parent") || strings.Contains(err.Error(), "not bound to a worktree") {
		t.Errorf("display name must not surface misleading auth/worktree errors, got %v", err)
	}

	err = func() error {
		_, e := a.handleReviewChangesetFileContent(ctx, gen.ProjectReviewFileContentReq{
			AgentActorID:  "Worker#0001",
			FilePath:      "main.go",
			CallerAgentID: "019fd32f831b00000000000000000888",
		})
		return e
	}()
	if err == nil || !strings.Contains(err.Error(), "canonical hex actor id") {
		t.Errorf("file_content must reject display name with format contract, got %v", err)
	}

	err = a.handleReviewChangesetFreeze(ctx, gen.ProjectReviewChangesetReq{
		AgentActorID: "Worker#0001",
	})
	if err == nil || !strings.Contains(err.Error(), "canonical hex actor id") {
		t.Errorf("freeze must reject display name with format contract, got %v", err)
	}

	// The hex id still passes the guard and reaches the normal authorization
	// path (which then succeeds for the recorded direct parent).
	if _, err := a.handleReviewChangesetSummary(ctx, gen.ProjectReviewChangesetReq{
		AgentActorID:  agentID,
		CallerAgentID: "019fd32f831b00000000000000000888",
	}); err != nil {
		t.Errorf("hex id must pass the format guard, got %v", err)
	}
}

// generateAndFinalize runs generateReviewChangesetSync synchronously, then
// applies the result via handleReviewChangesetFinalize. This bypasses the
// goroutine + self-invoke path (which requires a real actor framework) and
// tests the actual logic: generation → finalize → card data + file cache.
// The task card is the durable binding target (persist.Persist).
func generateAndFinalize(t *testing.T, a *Actor, agentID string) gen.ProjectReviewChangesetSummaryResp {
	t.Helper()
	return generateAndFinalizeOpts(t, a, agentID, "", 0)
}

// simulateFreeze mirrors handleReviewChangesetFreeze's synchronous side
// effects: assigns the next generation from the persisted binding record and
// writes a preparing-state card data entry. Returns the assigned generation.
func simulateFreeze(t *testing.T, a *Actor, agentID, taskCardID string) int32 {
	t.Helper()
	binding := a.loadReviewBinding(agentID)
	binding.Gen++
	if taskCardID != "" {
		binding.TaskCardID = taskCardID
	}
	a.saveReviewBinding(agentID, binding)
	if binding.TaskCardID != "" {
		now := time.Now().UTC().Format(time.RFC3339Nano)
		a.writeReviewChangesetCardData(binding.TaskCardID, preparingCardData(now, binding.Gen, agentID))
	}
	return binding.Gen
}

// generateAndFinalizeWithCard is like generateAndFinalize but also passes a
// taskCardID so card data projection can be tested. The generation token is
// assigned via the persisted binding (mirroring the freeze handler).
func generateAndFinalizeWithCard(t *testing.T, a *Actor, agentID, taskCardID string, genToken int32) gen.ProjectReviewChangesetSummaryResp {
	t.Helper()
	wtID, wtPath, err := a.resolveWorktreeForAgent(agentID)
	if err != nil {
		t.Fatalf("resolve worktree: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Simulate freeze: set the binding to the given generation token and
	// write preparing card data.
	a.saveReviewBinding(agentID, reviewBindingRecord{TaskCardID: taskCardID, Gen: genToken})
	if taskCardID != "" {
		a.writeReviewChangesetCardData(taskCardID, preparingCardData(now, genToken, agentID))
	}

	// Generate synchronously.
	baseBranch := a.reviewBaseBranch(wtID)
	cs, err := generateReviewChangesetSync(agentID, wtPath, baseBranch, "", reviewChangesetDefaultTestTimeout)
	if err != nil {
		_ = a.handleReviewChangesetFinalize(nil, gen.ProjectReviewChangesetFinalizeReq{
			AgentActorID: agentID,
			TaskCardID:   taskCardID,
			WorktreeID:   wtID,
			Generation:   genToken,
			ErrMsg:       err.Error(),
		})
		t.Fatalf("generate failed: %v", err)
	}
	cs.worktreeID = wtID
	cs.generatedAt = now

	summary := cs.toSummaryResp()
	if err := a.handleReviewChangesetFinalize(nil, gen.ProjectReviewChangesetFinalizeReq{
		AgentActorID: agentID,
		TaskCardID:   taskCardID,
		WorktreeID:   wtID,
		Generation:   genToken,
		Summary:      &summary,
		FileContents: cs.fileContents,
	}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	return summary
}

func generateAndFinalizeOpts(t *testing.T, a *Actor, agentID, testCmd string, testTimeoutMs int32) gen.ProjectReviewChangesetSummaryResp {
	t.Helper()
	wtID, wtPath, err := a.resolveWorktreeForAgent(agentID)
	if err != nil {
		t.Fatalf("resolve worktree: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Simulate freeze: assign the next generation via the persisted binding
	// (mirrors handleReviewChangesetFreeze). Default to a per-agent task
	// card when none was bound, so the card-data path is always exercised.
	binding := a.loadReviewBinding(agentID)
	binding.Gen++
	if binding.TaskCardID == "" {
		binding.TaskCardID = "review-default-task-" + agentID
		card := &CardRecord{
			Title:  binding.TaskCardID,
			Type:   "task",
			Status: "pending_review",
			Raw:    "---\nid: " + binding.TaskCardID + "\ntype: task\nstatus: pending_review\n---\n\nbody\n",
		}
		if err := a.store.Save(card); err != nil {
			t.Fatal(err)
		}
		a.writeReviewChangesetCardData(binding.TaskCardID, preparingCardData(now, binding.Gen, agentID))
	}
	a.saveReviewBinding(agentID, binding)
	genToken := binding.Gen

	// Generate synchronously.
	baseBranch := a.reviewBaseBranch(wtID)
	testTimeout := reviewChangesetDefaultTestTimeout
	if testTimeoutMs > 0 {
		testTimeout = time.Duration(testTimeoutMs) * time.Millisecond
	}
	cs, err := generateReviewChangesetSync(agentID, wtPath, baseBranch, testCmd, testTimeout)
	if err != nil {
		// Apply failure via finalize.
		_ = a.handleReviewChangesetFinalize(nil, gen.ProjectReviewChangesetFinalizeReq{
			AgentActorID: agentID,
			WorktreeID:   wtID,
			Generation:   genToken,
			ErrMsg:       err.Error(),
		})
		t.Fatalf("generate failed: %v", err)
	}
	cs.worktreeID = wtID
	cs.generatedAt = now

	// Apply success via finalize (simulates owner-loop self-invoke).
	summary := cs.toSummaryResp()
	if err := a.handleReviewChangesetFinalize(nil, gen.ProjectReviewChangesetFinalizeReq{
		AgentActorID: agentID,
		WorktreeID:   wtID,
		Generation:   genToken,
		Summary:      &summary,
		FileContents: cs.fileContents,
	}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	return summary
}

// gitCommitInWorktree stages and commits all changes in the given worktree path.
func gitCommitInWorktree(t *testing.T, wtPath, msg string) {
	t.Helper()
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", msg},
	} {
		cmd := exec.Command("git", args...)
		util.HideWindow(cmd)
		cmd.Dir = wtPath
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, wtPath, err, out)
		}
	}
}

func TestReviewChangeset_BasicCommittedChanges(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	if err := os.WriteFile(filepath.Join(wtPath, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "add main.go")

	resp := generateAndFinalize(t, a, agentID)
	if resp.AgentActorID != agentID {
		t.Errorf("AgentActorID=%q, want %q", resp.AgentActorID, agentID)
	}
	if resp.Status != "ready" {
		t.Errorf("Status=%q, want ready", resp.Status)
	}
	if resp.Head == "" {
		t.Error("expected non-empty Head")
	}
	if len(resp.Commits) == 0 {
		t.Error("expected at least one commit")
	}
	found := false
	for _, f := range resp.Files {
		if f.Path == "main.go" {
			found = true
		}
	}
	if !found {
		t.Error("main.go not found in changeset files")
	}
	if resp.Stats.TrackedChanged == 0 {
		t.Error("expected TrackedChanged > 0")
	}
}

// TestReviewChangeset_ParentBranchBaseline verifies that a workflow worker
// (worktree with ParentWorktreeID) is diffed against the parent worktree's
// current branch, not the repo's default branch. The owner branch typically
// carries sibling workers' merged changes; a default-branch baseline would
// fold those into this worker's changeset.
func TestReviewChangeset_ParentBranchBaseline(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	owner, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "wf-owner-baseline"})
	if err != nil {
		t.Fatalf("create owner worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(owner.Path, "owner.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, owner.Path, "owner change")

	worker, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "Worker-baseline",
		ParentWorktreeID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create worker worktree: %v", err)
	}
	if worker.ParentWorktreeID != owner.ID {
		t.Fatalf("worker.ParentWorktreeID = %q, want %q", worker.ParentWorktreeID, owner.ID)
	}
	agentID := "019fd32f831b00000000000000000999"
	if err := a.bindWorktree(agentID, worker.ID); err != nil {
		t.Fatalf("bind worktree: %v", err)
	}

	if err := os.WriteFile(filepath.Join(worker.Path, "worker.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, worker.Path, "worker change")

	resp := generateAndFinalize(t, a, agentID)
	hasWorker, hasOwner := false, false
	for _, f := range resp.Files {
		switch f.Path {
		case "worker.go":
			hasWorker = true
		case "owner.go":
			hasOwner = true
		}
	}
	if !hasWorker {
		t.Error("worker.go not in changeset files")
	}
	if hasOwner {
		t.Error("owner.go leaked into worker changeset: baseline should be the parent worktree branch, not the default branch")
	}
}

// TestReviewBaseBranch_UsesParentCurrentBranch verifies that reviewBaseBranch
// returns the parent worktree's current branch (not the repo default branch)
// for a workflow worker, so the changeset is measured against the owner branch.
func TestReviewBaseBranch_UsesParentCurrentBranch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	def, err := defaultBranch(dir)
	if err != nil {
		t.Fatalf("defaultBranch: %v", err)
	}

	owner, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "wf-owner-rbb"})
	if err != nil {
		t.Fatalf("create owner worktree: %v", err)
	}
	worker, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-worker-rbb",
		ParentWorktreeID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create worker worktree: %v", err)
	}

	got := a.reviewBaseBranch(worker.ID)
	if got != owner.Branch {
		t.Fatalf("reviewBaseBranch = %q, want parent branch %q", got, owner.Branch)
	}
	if got == def {
		t.Fatalf("reviewBaseBranch returned default branch %q for a worktree with a live parent", def)
	}
}

// TestReviewBaseBranch_ParentDestroyed_FallsBackToDefault verifies the
// degradation path: when the parent worktree has been discarded (destroyed),
// reviewBaseBranch falls back to the repo's default branch.
func TestReviewBaseBranch_ParentDestroyed_FallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	def, err := defaultBranch(dir)
	if err != nil {
		t.Fatalf("defaultBranch: %v", err)
	}

	owner, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "wf-owner-rbb-destroy"})
	if err != nil {
		t.Fatalf("create owner worktree: %v", err)
	}
	worker, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-worker-rbb-destroy",
		ParentWorktreeID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create worker worktree: %v", err)
	}

	if err := a.discardWorktreeByID(nil, owner.ID, true); err != nil {
		t.Fatalf("discard owner worktree: %v", err)
	}

	got := a.reviewBaseBranch(worker.ID)
	if got != def {
		t.Fatalf("reviewBaseBranch = %q after parent destroyed, want default %q", got, def)
	}
}

// TestReviewBaseBranch_ParentDetachedHead_FallsBackToDefault verifies the
// degradation path: when the parent worktree sits on a detached HEAD, git
// branch --show-current yields nothing and reviewBaseBranch falls back to the
// repo's default branch.
func TestReviewBaseBranch_ParentDetachedHead_FallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	def, err := defaultBranch(dir)
	if err != nil {
		t.Fatalf("defaultBranch: %v", err)
	}

	owner, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "wf-owner-rbb-detach"})
	if err != nil {
		t.Fatalf("create owner worktree: %v", err)
	}
	worker, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{
		Name:             "wf-worker-rbb-detach",
		ParentWorktreeID: owner.ID,
	})
	if err != nil {
		t.Fatalf("create worker worktree: %v", err)
	}

	if _, err := gitRun(nil, owner.Path, "checkout", "--detach"); err != nil {
		t.Fatalf("detach parent HEAD: %v", err)
	}
	if branch, err := gitBranchShowCurrent(owner.Path); branch != "" || err != nil {
		t.Fatalf("expected detached HEAD (no current branch), got branch=%q err=%v", branch, err)
	}

	got := a.reviewBaseBranch(worker.ID)
	if got != def {
		t.Fatalf("reviewBaseBranch = %q after parent detached HEAD, want default %q", got, def)
	}
}

// TestReviewBaseBranch_UnknownWorktreeID_FallsBackToDefault verifies that an
// unknown worktree ID (no recorded parent, e.g. a plain worker or a stale ID)
// degrades to the repo's default branch.
func TestReviewBaseBranch_UnknownWorktreeID_FallsBackToDefault(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)
	def, err := defaultBranch(dir)
	if err != nil {
		t.Fatalf("defaultBranch: %v", err)
	}

	if got := a.reviewBaseBranch("no-such-worktree"); got != def {
		t.Fatalf("reviewBaseBranch(unknown) = %q, want default %q", got, def)
	}
}

func TestReviewChangeset_DirtyWorkingTree(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	if err := os.WriteFile(filepath.Join(wtPath, "dirty.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	resp := generateAndFinalize(t, a, agentID)
	if !resp.IsDirty {
		t.Error("expected IsDirty=true for uncommitted changes")
	}
}

func TestReviewChangeset_MixedCommittedUncommitted(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	if err := os.WriteFile(filepath.Join(wtPath, "committed.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "committed change")

	if err := os.WriteFile(filepath.Join(wtPath, "uncommitted.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	resp := generateAndFinalize(t, a, agentID)
	if !resp.IsDirty {
		t.Error("expected IsDirty=true")
	}
	paths := make(map[string]bool)
	for _, f := range resp.Files {
		paths[f.Path] = true
	}
	for _, f := range resp.UntrackedFiles {
		paths[f.Path] = true
	}
	if !paths["committed.go"] {
		t.Error("committed.go not in changeset")
	}
}

func TestReviewChangeset_UntrackedFiles(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filepath.Join(wtPath, "untracked.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	resp := generateAndFinalize(t, a, agentID)
	found := false
	for _, f := range resp.UntrackedFiles {
		if f.Path == "untracked.txt" {
			found = true
			if !f.IsText {
				t.Error("untracked.txt should be text")
			}
		}
	}
	if !found {
		t.Error("untracked.txt not found in untracked files")
	}
	if resp.Stats.Untracked == 0 {
		t.Error("expected Untracked > 0")
	}
}

func TestReviewChangeset_BinaryFile(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	binaryData := []byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x0d, 0x0a, 0x1a, 0x0a}
	if err := os.WriteFile(filepath.Join(wtPath, "image.bin"), binaryData, 0644); err != nil {
		t.Fatal(err)
	}

	resp := generateAndFinalize(t, a, agentID)
	found := false
	for _, f := range resp.UntrackedFiles {
		if f.Path == "image.bin" {
			found = true
			if !f.IsBinary {
				t.Error("image.bin should be binary")
			}
		}
	}
	if !found {
		t.Error("image.bin not found in untracked files")
	}
}

func TestReviewChangeset_GeneratedFile(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	if err := os.WriteFile(filepath.Join(wtPath, "types.gen.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "add generated file")

	resp := generateAndFinalize(t, a, agentID)
	found := false
	for _, f := range resp.Files {
		if f.Path == "types.gen.go" {
			found = true
			if !f.IsGenerated {
				t.Error("types.gen.go should be marked as generated")
			}
		}
	}
	if !found {
		t.Error("types.gen.go not found in changeset files")
	}
}

func TestReviewChangeset_Frozen(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	if err := os.WriteFile(filepath.Join(wtPath, "v1.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "v1")

	resp1 := generateAndFinalize(t, a, agentID)
	head1 := resp1.Head

	if err := os.WriteFile(filepath.Join(wtPath, "v2.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "v2")

	// Summary without ForceRefresh returns the cached (frozen) snapshot.
	anonCtx := testutil.AdminCtx(testutil.GenActorID())
	resp2, err := a.handleReviewChangesetSummary(anonCtx, gen.ProjectReviewChangesetReq{AgentActorID: agentID})
	if err != nil {
		t.Fatalf("changeset 2: %v", err)
	}
	if resp2.Head != head1 {
		t.Errorf("frozen snapshot changed: Head %q → %q", head1, resp2.Head)
	}
}

func TestReviewChangeset_ForceRefresh(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	if err := os.WriteFile(filepath.Join(wtPath, "v1.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "v1")
	resp1 := generateAndFinalize(t, a, agentID)

	if err := os.WriteFile(filepath.Join(wtPath, "v2.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "v2")

	// ForceRefresh triggers a new async generation (which won't finalize in
	// test mode), so we manually finalize to verify the refresh logic.
	anonCtx := testutil.AdminCtx(testutil.GenActorID())
	if _, err := a.handleReviewChangesetSummary(anonCtx, gen.ProjectReviewChangesetReq{
		AgentActorID: agentID,
		ForceRefresh: true,
	}); err != nil {
		t.Fatalf("force refresh trigger: %v", err)
	}
	// Wait briefly for the goroutine to finish its git operations.
	time.Sleep(500 * time.Millisecond)
	resp2 := generateAndFinalize(t, a, agentID)
	if resp2.Head == resp1.Head {
		t.Error("force refresh should produce a new HEAD")
	}
}

func TestReviewChangeset_FileContentPagination(t *testing.T) {
	a, ctx, wtPath, agentID := reviewChangesetTestActor(t)

	var sb strings.Builder
	for i := 1; i <= 300; i++ {
		sb.WriteString("line content\n")
	}
	if err := os.WriteFile(filepath.Join(wtPath, "big.go"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "add big file")

	generateAndFinalize(t, a, agentID)

	page1, err := a.handleReviewChangesetFileContent(ctx, gen.ProjectReviewFileContentReq{
		AgentActorID: agentID,
		FilePath:     "big.go",
		Limit:        50,
	})
	if err != nil {
		t.Fatalf("file content page 1: %v", err)
	}
	if page1.StartLine != 1 {
		t.Errorf("StartLine=%d, want 1", page1.StartLine)
	}
	if page1.NumLines > 50 {
		t.Errorf("NumLines=%d, want <= 50", page1.NumLines)
	}
	if !page1.Truncated {
		t.Error("expected Truncated=true for first page")
	}
}

func TestReviewChangeset_CachedAfterGeneration(t *testing.T) {
	a, _, _, agentID := reviewChangesetTestActor(t)

	generateAndFinalize(t, a, agentID)

	// The snapshot must be readable via the persisted binding + card data.
	binding := a.loadReviewBinding(agentID)
	if binding.TaskCardID == "" {
		t.Fatal("binding should record a task card after generation")
	}
	data, ok := a.readReviewChangesetByTask(binding.TaskCardID)
	if !ok {
		t.Fatal("card data should hold the snapshot after generation")
	}
	if data.Status != reviewChangesetStatusReady {
		t.Errorf("status=%q, want ready", data.Status)
	}

	if err := a.handleReviewChangesetClear(nil, gen.ProjectReviewChangesetReq{AgentActorID: agentID}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, ok := a.readReviewChangesetByTask(binding.TaskCardID); ok {
		t.Error("card data should be removed after clear")
	}
}

func TestReviewChangeset_Authorization_NonHumanNoCaller(t *testing.T) {
	a, _, _, agentID := reviewChangesetTestActor(t)

	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	_, err := a.handleReviewChangesetSummary(anonCtx, gen.ProjectReviewChangesetReq{AgentActorID: agentID})
	if err == nil {
		t.Error("expected authorization error for anonymous caller without CallerAgentId")
	}
}

func TestReviewChangeset_Authorization_WrongParent(t *testing.T) {
	a, _, _, agentID := reviewChangesetTestActor(t)

	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	_, err := a.handleReviewChangesetSummary(anonCtx, gen.ProjectReviewChangesetReq{
		AgentActorID:  agentID,
		CallerAgentID: "some-other-agent",
	})
	if err == nil {
		t.Error("expected authorization error for wrong parent")
	}
}

func TestReviewChangeset_Authorization_CorrectParent(t *testing.T) {
	a, _, _, agentID := reviewChangesetTestActor(t)

	generateAndFinalize(t, a, agentID)

	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	resp, err := a.handleReviewChangesetSummary(anonCtx, gen.ProjectReviewChangesetReq{
		AgentActorID:  agentID,
		CallerAgentID: "019fd32f831b00000000000000000888",
	})
	if err != nil {
		t.Fatalf("expected success for correct parent, got: %v", err)
	}
	if resp.AgentActorID != agentID {
		t.Errorf("AgentActorID=%q, want %q", resp.AgentActorID, agentID)
	}
}

func TestReviewChangeset_NotBoundToWorktree(t *testing.T) {
	a, ctx, _, _ := reviewChangesetTestActor(t)

	_, err := a.handleReviewChangesetSummary(ctx, gen.ProjectReviewChangesetReq{
		AgentActorID: "nonexistent-agent",
	})
	if err == nil {
		t.Error("expected error for agent not bound to worktree")
	}
}

func TestReviewChangeset_DeletedFile(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	if err := os.Remove(filepath.Join(wtPath, "README.md")); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "delete README")

	resp := generateAndFinalize(t, a, agentID)
	found := false
	for _, f := range resp.Files {
		if f.Path == "README.md" && f.Status == "deleted" {
			found = true
		}
	}
	if !found {
		t.Error("README.md deletion not detected in changeset")
	}
}

func TestReviewChangeset_RenameDetection(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	oldPath := filepath.Join(wtPath, "README.md")
	newPath := filepath.Join(wtPath, "README-renamed.md")
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "rename README")

	resp := generateAndFinalize(t, a, agentID)
	foundRenamed := false
	for _, f := range resp.Files {
		if f.Path == "README-renamed.md" {
			foundRenamed = true
		}
	}
	if !foundRenamed {
		t.Error("renamed file not detected in changeset")
	}
}

func TestReviewChangeset_TeardownClearsCache(t *testing.T) {
	a, ctx, _, agentID := reviewChangesetTestActor(t)

	generateAndFinalize(t, a, agentID)
	binding := a.loadReviewBinding(agentID)

	if err := a.releaseWorktreeBinding(ctx, agentID, false); err != nil {
		t.Fatalf("release binding: %v", err)
	}

	if data, ok := a.readReviewChangesetByTask(binding.TaskCardID); ok && data.AgentActorID == agentID {
		t.Error("card data should be cleared after worktree release")
	}
	if _, ok := a.lookupReviewFileContent(agentID, "x"); ok {
		t.Error("file-contents cache should be cleared after worktree release")
	}
}

// ── New tests: freeze preparing state, test execution, card data, second review ──

// TestReviewChangeset_FreezeNoWorktreeSkipsFreeze pins the depth-in-defense
// guard: a no-worktree agent (writes directly into the main repo, no
// isolated child branch) must NOT receive a preparing card-data placeholder.
// The freeze handler returns nil (no error) — the ready_for_review flow
// proceeds, the parent reviewer inspects the card body / assessment instead
// of a per-branch diff, and no spurious preparing entry pollutes the card
// store. Mirrors the agent-side a.activeWorkflowWorktreeID()=="" skip.
func TestReviewChangeset_FreezeNoWorktreeSkipsFreeze(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	// Agent that is NOT bound to a worktree (writes into the main repo).
	const agentID = "019fd32f831b00000000000000000fff"
	taskCardID := "freeze-noworktree-task"
	card := &CardRecord{
		Title:  taskCardID,
		Type:   "task",
		Status: "pending_review",
		Raw:    "---\nid: " + taskCardID + "\ntype: task\nstatus: pending_review\n---\n\nbody\n",
	}
	if err := a.store.Save(card); err != nil {
		t.Fatal(err)
	}

	ctx := testutil.AdminCtx(testutil.GenActorID())
	if err := a.handleReviewChangesetFreeze(ctx, gen.ProjectReviewChangesetReq{
		AgentActorID: agentID,
		TaskCardID:   taskCardID,
	}); err != nil {
		t.Fatalf("no-worktree freeze must be a silent no-op, got error: %v", err)
	}

	// The card data must NOT carry a preparing entry.
	if _, ok := a.readReviewChangesetByTask(taskCardID); ok {
		t.Fatal("no-worktree freeze must not write a preparing card data entry")
	}
	// The persisted binding must NOT be advanced.
	binding := a.loadReviewBinding(agentID)
	if binding.Gen != 0 {
		t.Errorf("no-worktree freeze must not bump binding gen, got %d", binding.Gen)
	}
	if binding.TaskCardID != "" {
		t.Errorf("no-worktree freeze must not stamp binding.TaskCardID, got %q", binding.TaskCardID)
	}
	// A second freeze for the same no-worktree agent must remain a no-op
	// (idempotent + quiet).
	if err := a.handleReviewChangesetFreeze(ctx, gen.ProjectReviewChangesetReq{
		AgentActorID: agentID,
		TaskCardID:   taskCardID,
	}); err != nil {
		t.Fatalf("repeat no-worktree freeze must be a silent no-op, got error: %v", err)
	}
	if _, ok := a.readReviewChangesetByTask(taskCardID); ok {
		t.Fatal("repeat no-worktree freeze must not write a preparing card data entry")
	}
}

func TestReviewChangeset_FreezePreparesPlaceholder(t *testing.T) {
	a, _, _, agentID := reviewChangesetTestActor(t)

	taskCardID := "freeze-placeholder-task"
	card := &CardRecord{
		Title:  taskCardID,
		Type:   "task",
		Status: "pending_review",
		Raw:    "---\nid: " + taskCardID + "\ntype: task\nstatus: pending_review\n---\n\nbody\n",
	}
	if err := a.store.Save(card); err != nil {
		t.Fatal(err)
	}

	ctx := testutil.AdminCtx(testutil.GenActorID())
	if err := a.handleReviewChangesetFreeze(ctx, gen.ProjectReviewChangesetReq{
		AgentActorID: agentID,
		TaskCardID:   taskCardID,
	}); err != nil {
		t.Fatalf("freeze: %v", err)
	}

	// The preparing state must be immediately visible via the persisted
	// binding + card data.
	binding := a.loadReviewBinding(agentID)
	if binding.Gen == 0 {
		t.Fatal("freeze should have assigned a generation")
	}
	if binding.TaskCardID != taskCardID {
		t.Fatalf("binding.TaskCardID=%q, want %q", binding.TaskCardID, taskCardID)
	}
	data, ok := a.readReviewChangesetByTask(taskCardID)
	if !ok {
		t.Fatal("preparing card data not written immediately")
	}
	if data.Status != reviewChangesetStatusPreparing {
		t.Errorf("status=%q, want preparing", data.Status)
	}

	// Wait for the goroutine to finish (it won't finalize in test mode,
	// but we need it to exit cleanly).
	time.Sleep(500 * time.Millisecond)
}

func TestReviewChangeset_FreezeIdempotentWhenReady(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	if err := os.WriteFile(filepath.Join(wtPath, "file.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "add file")
	generateAndFinalize(t, a, agentID)
	bindingBefore := a.loadReviewBinding(agentID)

	ctx := testutil.AdminCtx(testutil.GenActorID())
	// Second freeze without ForceRefresh should be a no-op (snapshot already ready).
	if err := a.handleReviewChangesetFreeze(ctx, gen.ProjectReviewChangesetReq{AgentActorID: agentID}); err != nil {
		t.Fatalf("second freeze: %v", err)
	}

	bindingAfter := a.loadReviewBinding(agentID)
	if bindingAfter.Gen != bindingBefore.Gen {
		t.Errorf("idempotent freeze should not bump generation: %d → %d", bindingBefore.Gen, bindingAfter.Gen)
	}
	data, ok := a.readReviewChangesetByTask(bindingAfter.TaskCardID)
	if !ok {
		t.Fatal("changeset missing after idempotent freeze")
	}
	if data.Status != reviewChangesetStatusReady {
		t.Errorf("status=%q, want ready", data.Status)
	}
}

func TestReviewChangeset_TestSkippedByDefault(t *testing.T) {
	a, _, _, agentID := reviewChangesetTestActor(t)

	resp := generateAndFinalize(t, a, agentID)
	if !resp.TestResult.Skipped {
		t.Error("expected TestResult.Skipped=true when no test command is provided")
	}
}

func TestReviewChangeset_TestCommandExecuted(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	scriptContent := "#!/bin/sh\necho 'test passed'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(wtPath, "run-tests.sh"), []byte(scriptContent), 0755); err != nil {
		t.Fatal(err)
	}

	resp := generateAndFinalizeOpts(t, a, agentID, "sh run-tests.sh", 60000)
	if resp.TestResult.Skipped {
		t.Error("expected test to run, not be skipped")
	}
	if resp.TestResult.Command != "sh run-tests.sh" {
		t.Errorf("Command=%q, want 'sh run-tests.sh'", resp.TestResult.Command)
	}
	if !resp.TestResult.Success {
		t.Error("expected test success")
	}
	if !strings.Contains(resp.TestResult.Output, "test passed") {
		t.Errorf("expected output to contain 'test passed', got %q", resp.TestResult.Output)
	}
}

func TestReviewChangeset_SecondReviewCycle(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	// First review cycle.
	if err := os.WriteFile(filepath.Join(wtPath, "v1.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "v1")
	resp1 := generateAndFinalize(t, a, agentID)
	head1 := resp1.Head

	// Simulate reject: clear.
	if err := a.handleReviewChangesetClear(nil, gen.ProjectReviewChangesetReq{AgentActorID: agentID}); err != nil {
		t.Fatalf("clear: %v", err)
	}

	// Worker continues.
	if err := os.WriteFile(filepath.Join(wtPath, "v2.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "v2")

	// Second review cycle.
	resp2 := generateAndFinalize(t, a, agentID)
	if resp2.Head == head1 {
		t.Error("second review cycle should produce a new HEAD")
	}
	foundV2 := false
	for _, f := range resp2.Files {
		if f.Path == "v2.go" {
			foundV2 = true
		}
	}
	if !foundV2 {
		t.Error("v2.go should appear in second review cycle snapshot")
	}
}

func TestReviewChangeset_Authorization_ForgedCallerAgentID(t *testing.T) {
	a, _, _, agentID := reviewChangesetTestActor(t)

	// A forged CallerAgentID matching the parent succeeds at the handler
	// level (the handler cannot distinguish turn-engine injection from a
	// direct call). Forge prevention is at the turn engine level
	// (overwriteField), tested in turn_engine_execute_test.go.
	anonCtx := testutil.AnonCtx(testutil.GenActorID())
	_, err := a.handleReviewChangesetSummary(anonCtx, gen.ProjectReviewChangesetReq{
		AgentActorID:  agentID,
		CallerAgentID: "019fd32f831b00000000000000000888",
	})
	if err != nil {
		t.Fatalf("unexpected error with matching parent CallerAgentID: %v", err)
	}
}

// ── Card data projection tests ──

func TestReviewChangeset_CardData_PreparingToReady(t *testing.T) {
	a, _, _, agentID := reviewChangesetTestActor(t)

	taskCardID := "test-task-card"
	card := &CardRecord{
		Title:  taskCardID,
		Type:   "task",
		Status: "pending_review",
		Raw:    "---\nid: " + taskCardID + "\ntype: task\nstatus: pending_review\n---\n\nbody\n",
	}
	if err := a.store.Save(card); err != nil {
		t.Fatal(err)
	}

	// Simulate freeze handler: bind generation 1 and write preparing card
	// data.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	a.saveReviewBinding(agentID, reviewBindingRecord{TaskCardID: taskCardID, Gen: 1})
	a.writeReviewChangesetCardData(taskCardID, preparingCardData(now, 1, agentID))

	saved, _ := a.store.Get(taskCardID)
	rcJSON := saved.Data["reviewChangeset"].(string)
	if !strings.Contains(rcJSON, "preparing") {
		t.Fatalf("expected 'preparing' in card data, got %q", rcJSON)
	}

	// Simulate finalize with ready snapshot + card data update.
	generateAndFinalizeWithCard(t, a, agentID, taskCardID, 1)

	saved, _ = a.store.Get(taskCardID)
	rcJSON = saved.Data["reviewChangeset"].(string)
	if !strings.Contains(rcJSON, "ready") {
		t.Errorf("expected 'ready' in card data, got %q", rcJSON)
	}
	if !strings.Contains(rcJSON, "baseline") {
		t.Errorf("expected 'baseline' in card data, got %q", rcJSON)
	}
	if !strings.Contains(rcJSON, "files") {
		t.Errorf("expected 'files' in card data, got %q", rcJSON)
	}
}

func TestReviewChangeset_CardData_FailedGeneration(t *testing.T) {
	a, _, _, agentID := reviewChangesetTestActor(t)

	taskCardID := "test-task-failed"
	card := &CardRecord{
		Title:  taskCardID,
		Type:   "task",
		Status: "pending_review",
		Raw:    "---\nid: " + taskCardID + "\ntype: task\nstatus: pending_review\n---\n\nbody\n",
	}
	if err := a.store.Save(card); err != nil {
		t.Fatal(err)
	}

	// Simulate freeze: bind generation 1 and write preparing card data.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	a.saveReviewBinding(agentID, reviewBindingRecord{TaskCardID: taskCardID, Gen: 1})
	a.writeReviewChangesetCardData(taskCardID, preparingCardData(now, 1, agentID))

	// Simulate a failed finalize.
	if err := a.handleReviewChangesetFinalize(nil, gen.ProjectReviewChangesetFinalizeReq{
		AgentActorID: agentID,
		TaskCardID:   taskCardID,
		Generation:   1,
		ErrMsg:       "git command failed",
	}); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	saved, err := a.store.Get(taskCardID)
	if err != nil {
		t.Fatal(err)
	}
	rcJSON, ok := saved.Data["reviewChangeset"].(string)
	if !ok {
		t.Fatal("card data missing reviewChangeset")
	}
	if !strings.Contains(rcJSON, "failed") {
		t.Errorf("expected 'failed' in card data, got %q", rcJSON)
	}
	if !strings.Contains(rcJSON, "git command failed") {
		t.Errorf("expected error message 'git command failed' in card data, got %q", rcJSON)
	}
}

func TestReviewChangeset_CardData_ClearedOnReject(t *testing.T) {
	a, _, _, agentID := reviewChangesetTestActor(t)

	taskCardID := "test-task-clear"
	card := &CardRecord{
		Title:  taskCardID,
		Type:   "task",
		Status: "pending_review",
		Raw:    "---\nid: " + taskCardID + "\ntype: task\nstatus: pending_review\n---\n\nbody\n",
	}
	if err := a.store.Save(card); err != nil {
		t.Fatal(err)
	}

	// Write card data (simulating finalize).
	a.writeReviewChangesetCardData(taskCardID, reviewChangesetCardData{
		Status:        "ready",
		Head:          "abc123",
		AgentActorID:  agentID,
	})

	// Verify data was written.
	saved, _ := a.store.Get(taskCardID)
	if saved.Data == nil || saved.Data["reviewChangeset"] == nil {
		t.Fatal("card data should have been written")
	}

	// Clear changeset (simulates reject).
	if err := a.handleReviewChangesetClear(nil, gen.ProjectReviewChangesetReq{
		AgentActorID: agentID,
		TaskCardID:   taskCardID,
	}); err != nil {
		t.Fatalf("clear: %v", err)
	}

	// Card data should be cleared.
	saved, _ = a.store.Get(taskCardID)
	if saved.Data != nil && saved.Data["reviewChangeset"] != nil {
		t.Error("card data should be cleared after changeset clear")
	}
}

func TestReviewChangeset_CardData_ClearedOnTeardown(t *testing.T) {
	a, ctx, _, agentID := reviewChangesetTestActor(t)

	taskCardID := "test-task-teardown"
	card := &CardRecord{
		Title:  taskCardID,
		Type:   "task",
		Status: "pending_review",
		Raw:    "---\nid: " + taskCardID + "\ntype: task\nstatus: pending_review\n---\n\nbody\n",
	}
	if err := a.store.Save(card); err != nil {
		t.Fatal(err)
	}

	// Generate and finalize with card data.
	generateAndFinalize(t, a, agentID)
	a.writeReviewChangesetCardData(taskCardID, reviewChangesetCardData{Status: "ready", Head: "abc", AgentActorID: agentID})

	// Record the binding so teardown can resolve the task card.
	a.saveReviewBinding(agentID, reviewBindingRecord{TaskCardID: taskCardID, Gen: a.loadReviewBinding(agentID).Gen})

	// Release worktree (teardown path).
	if err := a.releaseWorktreeBinding(ctx, agentID, false); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Card data should be cleared.
	saved, _ := a.store.Get(taskCardID)
	if saved.Data != nil && saved.Data["reviewChangeset"] != nil {
		t.Error("card data should be cleared after teardown")
	}
}

func TestReviewChangeset_CardData_NoFullDiffInData(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	if err := os.WriteFile(filepath.Join(wtPath, "main.go"), []byte("package main\n\nfunc Foo() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "add main.go")

	taskCardID := "test-task-nodiff"
	card := &CardRecord{
		Title:  taskCardID,
		Type:   "task",
		Status: "pending_review",
		Raw:    "---\nid: " + taskCardID + "\ntype: task\nstatus: pending_review\n---\n\nbody\n",
	}
	if err := a.store.Save(card); err != nil {
		t.Fatal(err)
	}

	// Finalize with task card ID.
	resp := generateAndFinalizeWithCard(t, a, agentID, taskCardID, 1)
	summary := resp
	_ = summary // summary is already applied via generateAndFinalizeWithCard

	saved, _ := a.store.Get(taskCardID)
	rcJSON, ok := saved.Data["reviewChangeset"].(string)
	if !ok {
		t.Fatal("card data missing reviewChangeset")
	}
	// Must NOT contain full diff content.
	if strings.Contains(rcJSON, "func Foo") {
		t.Errorf("card data should not contain full diff content: %q", rcJSON)
	}
	// Must contain file path.
	if !strings.Contains(rcJSON, "main.go") {
		t.Errorf("card data should contain file path 'main.go': %q", rcJSON)
	}
}

// ── Generation token / stale-job race tests ──

// TestReviewChangeset_StaleFinalizeAfterClear_PreventsResurrection verifies that
// after a clear (reject), a stale ready finalize from the old freeze does NOT
// write to the task card custom data.
func TestReviewChangeset_StaleFinalizeAfterClear_PreventsResurrection(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	// Create a file and commit so generation produces a non-empty snapshot.
	if err := os.WriteFile(filepath.Join(wtPath, "stale.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "add stale.go")

	taskCardID := "stale-clear-task"
	card := &CardRecord{
		Title:  taskCardID,
		Type:   "task",
		Status: "pending_review",
		Raw:    "---\nid: " + taskCardID + "\ntype: task\nstatus: pending_review\n---\n\nbody\n",
	}
	if err := a.store.Save(card); err != nil {
		t.Fatal(err)
	}

	// Freeze: bind generation 1 and write preparing card data.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	a.saveReviewBinding(agentID, reviewBindingRecord{TaskCardID: taskCardID, Gen: 1})
	a.writeReviewChangesetCardData(taskCardID, preparingCardData(now, 1, agentID))

	// Clear (simulates reject): removes card data, bumps the binding gen to 2.
	if err := a.handleReviewChangesetClear(nil, gen.ProjectReviewChangesetReq{
		AgentActorID: agentID,
		TaskCardID:   taskCardID,
	}); err != nil {
		t.Fatalf("clear: %v", err)
	}

	// Verify card data is cleared.
	saved, _ := a.store.Get(taskCardID)
	if saved.Data != nil && saved.Data["reviewChangeset"] != nil {
		t.Fatal("card data should be cleared after clear")
	}

	// Stale finalize arrives with the old generation token (1).
	// Generate the snapshot synchronously so we have a real summary.
	root, _ := a.rootPath()
	baseBranch, _ := defaultBranch(root)
	cs, err := generateReviewChangesetSync(agentID, wtPath, baseBranch, "", reviewChangesetDefaultTestTimeout)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	cs.generatedAt = now
	summary := cs.toSummaryResp()

	_ = a.handleReviewChangesetFinalize(nil, gen.ProjectReviewChangesetFinalizeReq{
		AgentActorID: agentID,
		TaskCardID:   taskCardID,
		Generation:   1, // stale — binding gen is now 2
		Summary:      &summary,
		FileContents: cs.fileContents,
	})

	// Verify card data is still cleared.
	saved, _ = a.store.Get(taskCardID)
	if saved.Data != nil && saved.Data["reviewChangeset"] != nil {
		t.Fatal("stale finalize should NOT have written card data")
	}
}

// TestReviewChangeset_StaleFailedFinalizeAfterClear_PreventsResurrection verifies
// that after a clear, a stale FAILED finalize also does not write to card data.
func TestReviewChangeset_StaleFailedFinalizeAfterClear_PreventsResurrection(t *testing.T) {
	a, _, _, agentID := reviewChangesetTestActor(t)

	taskCardID := "stale-failed-clear-task"
	card := &CardRecord{
		Title:  taskCardID,
		Type:   "task",
		Status: "pending_review",
		Raw:    "---\nid: " + taskCardID + "\ntype: task\nstatus: pending_review\n---\n\nbody\n",
	}
	if err := a.store.Save(card); err != nil {
		t.Fatal(err)
	}

	// Freeze: bind generation 1 and write preparing card data.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	a.saveReviewBinding(agentID, reviewBindingRecord{TaskCardID: taskCardID, Gen: 1})
	a.writeReviewChangesetCardData(taskCardID, preparingCardData(now, 1, agentID))

	// Clear: bumps the binding gen to 2, removes card data.
	_ = a.handleReviewChangesetClear(nil, gen.ProjectReviewChangesetReq{
		AgentActorID: agentID,
		TaskCardID:   taskCardID,
	})

	// Stale failed finalize with generation 1.
	_ = a.handleReviewChangesetFinalize(nil, gen.ProjectReviewChangesetFinalizeReq{
		AgentActorID: agentID,
		TaskCardID:   taskCardID,
		Generation:   1,
		ErrMsg:       "git command failed",
	})

	// Verify card data is still cleared.
	saved, _ := a.store.Get(taskCardID)
	if saved.Data != nil && saved.Data["reviewChangeset"] != nil {
		t.Fatal("stale failed finalize should NOT have written card data")
	}
}

// TestReviewChangeset_FreezeA_ThenFreezeB_AFinalizeIgnored verifies that when
// freeze A is superseded by freeze B (force-refresh or second review cycle),
// a late-arriving finalize from A is ignored while B's finalize is applied.
func TestReviewChangeset_FreezeA_ThenFreezeB_AFinalizeIgnored(t *testing.T) {
	a, _, wtPath, agentID := reviewChangesetTestActor(t)

	// Create a file and commit.
	if err := os.WriteFile(filepath.Join(wtPath, "version_a.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "add version_a.go")

	taskCardID := "freeze-ab-task"
	card := &CardRecord{
		Title:  taskCardID,
		Type:   "task",
		Status: "pending_review",
		Raw:    "---\nid: " + taskCardID + "\ntype: task\nstatus: pending_review\n---\n\nbody\n",
	}
	if err := a.store.Save(card); err != nil {
		t.Fatal(err)
	}

	// Freeze A: bind generation 1 and write preparing card data.
	nowA := time.Now().UTC().Format(time.RFC3339Nano)
	a.saveReviewBinding(agentID, reviewBindingRecord{TaskCardID: taskCardID, Gen: 1})
	a.writeReviewChangesetCardData(taskCardID, preparingCardData(nowA, 1, agentID))

	// Make a new commit so freeze B will have different content.
	if err := os.WriteFile(filepath.Join(wtPath, "version_b.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "add version_b.go")

	// Freeze B: generation 2 (simulates force-refresh or second review
	// cycle). Applies B's snapshot through finalize.
	generateAndFinalizeWithCard(t, a, agentID, taskCardID, 2)

	// Verify B's snapshot is in the card data with generation 2.
	dataB, ok := a.readReviewChangesetByTask(taskCardID)
	if !ok {
		t.Fatal("freeze B should have a ready snapshot in the card data")
	}
	if dataB.Generation != 2 {
		t.Errorf("expected generation 2, got %d", dataB.Generation)
	}
	if dataB.Status != "ready" {
		t.Errorf("expected status ready, got %q", dataB.Status)
	}

	// Now A's stale finalize arrives with generation 1.
	root, _ := a.rootPath()
	baseBranch, _ := defaultBranch(root)
	// Regenerate with the state at time A (but we can't easily undo commits,
	// so we just use the current state — the point is the generation mismatch).
	csA, err := generateReviewChangesetSync(agentID, wtPath, baseBranch, "", reviewChangesetDefaultTestTimeout)
	if err != nil {
		t.Fatalf("generate A: %v", err)
	}
	csA.generatedAt = nowA
	summaryA := csA.toSummaryResp()

	_ = a.handleReviewChangesetFinalize(nil, gen.ProjectReviewChangesetFinalizeReq{
		AgentActorID: agentID,
		TaskCardID:   taskCardID,
		Generation:   1, // stale — current gen is 2
		Summary:      &summaryA,
		FileContents: csA.fileContents,
	})

	// Verify B's snapshot is still in the card data (A did not overwrite it).
	dataB2, ok := a.readReviewChangesetByTask(taskCardID)
	if !ok {
		t.Fatal("freeze B's snapshot should still be in the card data after stale A finalize")
	}
	if dataB2.Generation != 2 {
		t.Errorf("expected generation 2 (B's), got %d — A's stale finalize overwrote it", dataB2.Generation)
	}
	if dataB2.Status != "ready" {
		t.Errorf("expected status ready, got %q", dataB2.Status)
	}
}

// TestReviewChangeset_ReleaseWorktree_StaleFinalizeIgnored verifies that after
// releaseWorktreeBinding (teardown), a stale finalize does not resurrect the
// snapshot or card data.
func TestReviewChangeset_ReleaseWorktree_StaleFinalizeIgnored(t *testing.T) {
	a, ctx, wtPath, agentID := reviewChangesetTestActor(t)

	// Create a file and commit.
	if err := os.WriteFile(filepath.Join(wtPath, "teardown.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitCommitInWorktree(t, wtPath, "add teardown.go")

	taskCardID := "teardown-stale-task"
	card := &CardRecord{
		Title:  taskCardID,
		Type:   "task",
		Status: "pending_review",
		Raw:    "---\nid: " + taskCardID + "\ntype: task\nstatus: pending_review\n---\n\nbody\n",
	}
	if err := a.store.Save(card); err != nil {
		t.Fatal(err)
	}

	// Freeze: bind generation 1 and write preparing card data.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	a.saveReviewBinding(agentID, reviewBindingRecord{TaskCardID: taskCardID, Gen: 1})
	a.writeReviewChangesetCardData(taskCardID, preparingCardData(now, 1, agentID))

	// Generate the snapshot BEFORE releasing the worktree (the worktree
	// directory will be removed on release, so we must capture it now).
	root, _ := a.rootPath()
	baseBranch, _ := defaultBranch(root)
	cs, err := generateReviewChangesetSync(agentID, wtPath, baseBranch, "", reviewChangesetDefaultTestTimeout)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	cs.generatedAt = now
	summary := cs.toSummaryResp()

	// Release worktree (teardown path) — clears card data, deletes the
	// binding record.
	if err := a.releaseWorktreeBinding(ctx, agentID, false); err != nil {
		t.Fatalf("release: %v", err)
	}

	// Verify card data is cleared.
	saved, _ := a.store.Get(taskCardID)
	if saved.Data != nil && saved.Data["reviewChangeset"] != nil {
		t.Fatal("card data should be cleared after teardown")
	}

	// Stale finalize with generation 1 arrives.
	_ = a.handleReviewChangesetFinalize(nil, gen.ProjectReviewChangesetFinalizeReq{
		AgentActorID: agentID,
		TaskCardID:   taskCardID,
		Generation:   1, // stale — binding was deleted by teardown
		Summary:      &summary,
		FileContents: cs.fileContents,
	})

	// Verify card data is still cleared.
	saved, _ = a.store.Get(taskCardID)
	if saved.Data != nil && saved.Data["reviewChangeset"] != nil {
		t.Fatal("stale finalize should NOT have written card data after teardown")
	}
}

func TestSetCardDataFieldInRawKeepsCloserOnOwnLine(t *testing.T) {
	// Input whose closing --- is glued to the last data value (produced by the
	// old create_task_card category writer). Inserting a new data field must
	// not perpetuate the glue: the frontend parser rejects a glued closer.
	glued := "---\nid: t\ntype: task\ndata:\n  category: \"review\"---\n\nBody."
	got := setCardDataFieldInRaw(glued, "review_evidence", `["e1"]`)
	if !strings.Contains(got, "\n---\n") {
		t.Errorf("closer not on its own line:\n%s", got)
	}

	// Healthy input stays healthy when the field lands last.
	healthy := "---\nid: t\ntype: task\ndata:\n  category: \"review\"\n---\n\nBody."
	got = setCardDataStringInRaw(healthy, "ownerAgentId", "agent-1")
	if !strings.Contains(got, "\n---\n") {
		t.Errorf("healthy input got glued closer:\n%s", got)
	}
}
