package project

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/testutil"
	"github.com/qomos-w/sporemind/pkg/util"
)

// initGitRepo creates a fresh git repo at dir with an initial commit so
// worktree operations have a base to branch from.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init")
	util.HideWindow(cmd)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	// Set user config to avoid "Author identity unknown" error.
	for _, kv := range []string{"user.name test", "user.email test@test"} {
		parts := strings.SplitN(kv, " ", 2)
		cfg := exec.Command("git", "config", parts[0], parts[1])
		util.HideWindow(cfg)
		cfg.Dir = dir
		if o, e := cfg.CombinedOutput(); e != nil {
			t.Fatalf("git config %s: %v\n%s", parts[0], e, o)
		}
	}
	// Initial commit so there is a HEAD to branch from.
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("# test"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	add := exec.Command("git", "add", ".")
	util.HideWindow(add)
	add.Dir = dir
	if o, e := add.CombinedOutput(); e != nil {
		t.Fatalf("git add: %v\n%s", e, o)
	}
	commit := exec.Command("git", "commit", "-m", "initial")
	util.HideWindow(commit)
	commit.Dir = dir
	if o, e := commit.CombinedOutput(); e != nil {
		t.Fatalf("git commit: %v\n%s", e, o)
	}
}

// worktreeTestActor creates a project.Actor backed by a real git repo.
func worktreeTestActor(t *testing.T, dir string) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	// Isolate the actor data directory so worktree checkouts and state
	// files are written to a temp directory, not the real data dir.
	dataDir := t.TempDir()
	config.SetDataDirForTest(dataDir)
	t.Cleanup(config.ResetForTest)
	a := &Actor{
		initialPath:  dir,
		store:        newFSCardStore(filepath.Join(dir, ".sporecode", "wiki")),
		persistStore: persist.NewFSPersist(t.TempDir()),
	}
	ctx := testutil.AdminCtx(testutil.GenActorID())
	if err := a.OnInit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.OnStart(ctx); err != nil {
		t.Fatal(err)
	}
	return a, ctx
}

func TestWorktreeCreate_List(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "feature-x"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if wt.ID == "" {
		t.Fatal("create: expected non-empty ID")
	}
	if wt.Name != "feature-x" {
		t.Fatalf("create: Name=%q, want feature-x", wt.Name)
	}
	if wt.Branch != "feature-x" {
		t.Fatalf("create: Branch=%q, want feature-x", wt.Branch)
	}
	if wt.Status != "active" {
		t.Fatalf("create: Status=%q, want active", wt.Status)
	}

	// Path exists
	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		t.Fatalf("create: worktree path %s does not exist", wt.Path)
	}

	// Branch exists
	branchCmd := exec.Command("git", "branch", "--list", "feature-x")
	util.HideWindow(branchCmd)
	branchCmd.Dir = dir
	branchOut, err := branchCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if !strings.Contains(string(branchOut), "feature-x") {
		t.Fatalf("branch feature-x not found:\n%s", branchOut)
	}

	// list
	resp, err := a.handleWorktreeList(nil, gen.ProjectWorktreeListReq{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(resp.Worktrees) != 1 {
		t.Fatalf("list: got %d worktrees, want 1", len(resp.Worktrees))
	}
	if resp.Worktrees[0].ID != wt.ID {
		t.Fatalf("list: ID mismatch: %q vs %q", resp.Worktrees[0].ID, wt.ID)
	}
}

func TestWorktreeGet_ByID(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	created, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "bugfix"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := a.handleWorktreeGet(nil, gen.ProjectWorktreeGetReq{WorktreeID: created.ID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("get: ID=%q, want %q", got.ID, created.ID)
	}
	if got.Name != "bugfix" {
		t.Fatalf("get: Name=%q, want bugfix", got.Name)
	}
}

func TestWorktreeDiscard_Clean(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	created, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "clean-up"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := a.handleWorktreeDiscard(nil, gen.ProjectWorktreeDiscardReq{WorktreeID: created.ID}); err != nil {
		t.Fatalf("discard clean: %v", err)
	}

	// Must be removed from the map
	if _, err := a.handleWorktreeGet(nil, gen.ProjectWorktreeGetReq{WorktreeID: created.ID}); err == nil {
		t.Fatal("discard: worktree still in map after discard")
	}

	// Path must be gone
	if _, err := os.Stat(created.Path); !os.IsNotExist(err) {
		t.Fatalf("discard: worktree dir still exists at %s", created.Path)
	}
}

func TestWorktreeDiscard_Dirty(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	created, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "dirty-branch"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Create an uncommitted file in the worktree
	dirtyFile := filepath.Join(created.Path, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("uncommitted"), 0644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	// Force=false should fail
	err = a.handleWorktreeDiscard(nil, gen.ProjectWorktreeDiscardReq{WorktreeID: created.ID, Force: false})
	if err == nil {
		t.Fatal("discard dirty: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("discard dirty: expected 'uncommitted changes' error, got: %v", err)
	}

	// Force=true should succeed
	if err := a.handleWorktreeDiscard(nil, gen.ProjectWorktreeDiscardReq{WorktreeID: created.ID, Force: true}); err != nil {
		t.Fatalf("discard dirty force: %v", err)
	}

	// Path must be gone
	if _, err := os.Stat(created.Path); !os.IsNotExist(err) {
		t.Fatalf("discard dirty force: worktree dir still exists")
	}
}

func TestWorktreePersist_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt1, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "persist-1"})
	if err != nil {
		t.Fatalf("create 1: %v", err)
	}
	wt2, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "persist-2"})
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}

	// Create writes through to per-worktree manifests on disk; no bulk save.

	// Load into a fresh actor
	a2 := &Actor{
		initialPath:  dir,
		actorID:      a.actorID,
		store:        newFSCardStore(filepath.Join(dir, ".sporecode", "wiki")),
		persistStore: persist.NewFSPersist(t.TempDir()),
	}
	if err := a2.loadWorktreeCache(); err != nil {
		t.Fatalf("loadWorktreeCache: %v", err)
	}

	if len(a2.worktrees) != 2 {
		t.Fatalf("loadWorktreeCache: got %d worktrees, want 2", len(a2.worktrees))
	}
	if a2.worktrees[wt1.ID].Name != "persist-1" {
		t.Fatalf("loadWorktreeCache: wt1.Name=%q, want persist-1", a2.worktrees[wt1.ID].Name)
	}
	if a2.worktrees[wt2.ID].Name != "persist-2" {
		t.Fatalf("loadWorktreeCache: wt2.Name=%q, want persist-2", a2.worktrees[wt2.ID].Name)
	}
}

// TestWorktreePersist_BindingsSurviveReload proves the durability fix: after a
// restart the agent→worktree binding is restored from worktrees.json, so
// effectiveRoots still routes the caller into its worktree (not the main repo).
func TestWorktreePersist_BindingsSurviveReload(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "bind-rt"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const agentKey = "agent/restart-recovery/000000000000000000000002"
	if err := a.bindWorktree(agentKey, wt.ID); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Snapshot the routed root before "restart".
	wantRoot := a.effectiveRoots(agentKey)
	if len(wantRoot) != 1 || wantRoot[0].Path != wt.Path {
		t.Fatalf("pre-restart effectiveRoots = %+v, want [%s]", wantRoot, wt.Path)
	}

	// Binding mutations go through persistWorktreeManifest so the manifest
	// carries the agent→worktree mapping.
	if err := a.persistWorktreeManifest(wt.ID); err != nil {
		t.Fatalf("persistWorktreeManifest: %v", err)
	}

	// Fresh actor simulates a restart: no in-memory binding, only manifests.
	a2 := &Actor{
		initialPath:  dir,
		actorID:      a.actorID,
		store:        newFSCardStore(filepath.Join(dir, ".sporecode", "wiki")),
		persistStore: persist.NewFSPersist(t.TempDir()),
	}
	if err := a2.loadWorktreeCache(); err != nil {
		t.Fatalf("loadWorktreeCache: %v", err)
	}

	// Binding must be restored.
	a2.bindingMu.RLock()
	gotWtID, ok := a2.agentWorktree[agentKey]
	a2.bindingMu.RUnlock()
	if !ok {
		t.Fatal("loadWorktreeCache: agentWorktree binding not restored")
	}
	if gotWtID != wt.ID {
		t.Fatalf("restored binding = %q, want %q", gotWtID, wt.ID)
	}

	// And routing must still hit the worktree path.
	gotRoot := a2.effectiveRoots(agentKey)
	if len(gotRoot) != 1 || gotRoot[0].Path != wt.Path {
		t.Fatalf("post-restart effectiveRoots = %+v, want [%s]", gotRoot, wt.Path)
	}
}

// TestWorktreePersist_LegacyArrayStillLoads proves backward compatibility: an
// old install's worktrees-state.json (a bare JSON array) is imported into
// per-worktree manifests on the first loadWorktreeCache, then removed.
func TestWorktreePersist_LegacyArrayStillLoads(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "legacy"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Drop the freshly-written per-worktree manifests so only the legacy
	// file feeds the migration.
	if err := os.RemoveAll(a.manifestStoreBase()); err != nil {
		t.Fatalf("clear manifests: %v", err)
	}

	// Overwrite the legacy state file with the legacy bare-array format.
	legacyPath := filepath.Join(a.worktreesStateDir(), "worktrees-state.json")
	legacy, err := json.Marshal([]gen.ProjectWorktree{wt})
	if err != nil {
		t.Fatalf("marshal legacy: %v", err)
	}
	if err := os.MkdirAll(a.worktreesStateDir(), 0755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(legacyPath, legacy, 0644); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	a2 := &Actor{
		initialPath:  dir,
		actorID:      a.actorID,
		store:        newFSCardStore(filepath.Join(dir, ".sporecode", "wiki")),
		persistStore: persist.NewFSPersist(t.TempDir()),
	}
	if err := a2.loadWorktreeCache(); err != nil {
		t.Fatalf("loadWorktreeCache legacy: %v", err)
	}
	if len(a2.worktrees) != 1 {
		t.Fatalf("legacy load: got %d worktrees, want 1", len(a2.worktrees))
	}
	if _, ok := a2.worktrees[wt.ID]; !ok {
		t.Fatalf("legacy load: worktree %s not restored", wt.ID)
	}
	// The legacy file must have been removed after migration.
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("legacy load: legacy file not removed after migration")
	}
}

func TestWorktreeReconcile_Stale(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	created, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "stale-test"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Manually remove the worktree directory to simulate a stale worktree
	if err := os.RemoveAll(created.Path); err != nil {
		t.Fatalf("remove worktree dir: %v", err)
	}

	changed, err := a.reconcileWorktrees()
	if err != nil {
		t.Fatalf("reconcileWorktrees: %v", err)
	}
	if !changed {
		t.Fatal("reconcileWorktrees: expected changed=true, got false")
	}

	wt := a.worktrees[created.ID]
	if wt.Status != "stale" {
		t.Fatalf("reconcileWorktrees: Status=%q, want stale", wt.Status)
	}

	// Worktree must NOT be deleted from the map
	if _, ok := a.worktrees[created.ID]; !ok {
		t.Fatal("reconcileWorktrees: worktree was deleted instead of marked stale")
	}
}

func TestWorktreeIsolation(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	a, _ := worktreeTestActor(t, dir)

	// Create a file A in the main root and commit it
	fileA := filepath.Join(dir, "fileA.txt")
	if err := os.WriteFile(fileA, []byte("main"), 0644); err != nil {
		t.Fatalf("write fileA: %v", err)
	}
	add := exec.Command("git", "add", ".")
	util.HideWindow(add)
	add.Dir = dir
	if o, e := add.CombinedOutput(); e != nil {
		t.Fatalf("git add: %v\n%s", e, o)
	}
	commit := exec.Command("git", "commit", "-m", "add fileA")
	util.HideWindow(commit)
	commit.Dir = dir
	if o, e := commit.CombinedOutput(); e != nil {
		t.Fatalf("git commit: %v\n%s", e, o)
	}

	// Create a worktree
	created, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "isolation-test"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Write file B in the worktree and commit it
	fileB := filepath.Join(created.Path, "fileB.txt")
	if err := os.WriteFile(fileB, []byte("worktree"), 0644); err != nil {
		t.Fatalf("write fileB: %v", err)
	}
	addWT := exec.Command("git", "add", ".")
	util.HideWindow(addWT)
	addWT.Dir = created.Path
	if o, e := addWT.CombinedOutput(); e != nil {
		t.Fatalf("worktree git add: %v\n%s", e, o)
	}
	commitWT := exec.Command("git", "commit", "-m", "add fileB in worktree")
	util.HideWindow(commitWT)
	commitWT.Dir = created.Path
	if o, e := commitWT.CombinedOutput(); e != nil {
		t.Fatalf("worktree git commit: %v\n%s", e, o)
	}

	// Check: main root log should NOT contain fileB commit
	logMain := exec.Command("git", "log", "--oneline")
	util.HideWindow(logMain)
	logMain.Dir = dir
	mainLog, _ := logMain.Output()
	if strings.Contains(string(mainLog), "fileB") {
		t.Fatalf("main root log contains worktree commit (no isolation):\n%s", mainLog)
	}

	// Check: worktree log SHOULD contain fileB commit
	logWT := exec.Command("git", "log", "--oneline")
	util.HideWindow(logWT)
	logWT.Dir = created.Path
	wtLog, _ := logWT.Output()
	if !strings.Contains(string(wtLog), "fileB") {
		t.Fatalf("worktree log missing its own commit:\n%s", wtLog)
	}

	// Check: worktree status should NOT show fileA changes
	statusWT := exec.Command("git", "status", "--porcelain")
	util.HideWindow(statusWT)
	statusWT.Dir = created.Path
	wtStatus, _ := statusWT.Output()
	if strings.Contains(string(wtStatus), "fileA") {
		t.Fatalf("worktree status shows main-root fileA changes (no isolation):\n%s", wtStatus)
	}
}

func TestWorktreeCreate_EmptyName(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	_, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: ""})
	if err == nil {
		t.Fatal("create with empty name: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Name required") {
		t.Fatalf("create with empty name: expected 'Name required', got: %v", err)
	}
}

// TestWorktreeManifest_UnderActorDir verifies the per-worktree manifest files
// live under the project actor's settings directory (<ActorDataDir>/project/
// <actor-id>/manifests/), co-locating with worktree checkouts so they are
// removed together when the actor is destroyed. The manifest must not be
// tracked by git.
func TestWorktreeManifest_UnderActorDir(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, ctx := worktreeTestActor(t, dir)

	// worktreeTestActor sets config.SetDataDirForTest internally.
	dataDir := config.DataDir()

	// Manifest base is under <ActorDataDir>/project/<actorID>/manifests
	wantDir := filepath.Join(dataDir, ".actors", "project", a.actorID, "manifests")
	if got := a.manifestStoreBase(); got != wantDir {
		t.Fatalf("manifestStoreBase: got %q, want %q", got, wantDir)
	}

	// Creating a worktree writes through its per-worktree manifest file.
	if _, err := a.handleWorktreeCreate(nil, gen.ProjectWorktreeCreateReq{Name: "path-probe"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(a.worktrees) != 1 {
		t.Fatalf("expected 1 worktree in cache, got %d", len(a.worktrees))
	}
	var created gen.ProjectWorktree
	for _, wt := range a.worktrees {
		created = wt
	}
	var m worktreeManifest
	if err := a.manifestStore().Load(created.ID, &m); err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if m.Name != "path-probe" {
		t.Fatalf("manifest Name=%q, want path-probe", m.Name)
	}

	// The manifest dir must not be tracked by git (it is outside the repo).
	cmd := exec.Command("git", "-C", dir, "ls-files", "--error-unmatch", wantDir)
	util.HideWindow(cmd)
	if err := cmd.Run(); err == nil {
		t.Fatal("manifest file must NOT be tracked by git, but ls-files matched it")
	}

	// The worktree checkout directory must also be under the actor settings dir.
	actorDir := filepath.Join(dataDir, ".actors", "project", a.actorID)
	if !strings.HasPrefix(created.Path, actorDir) {
		t.Fatalf("worktree path %q is not under actor settings dir %q", created.Path, actorDir)
	}
	_ = ctx // suppress unused warning
}
