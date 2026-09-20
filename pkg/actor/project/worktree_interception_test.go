package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestEffectiveRoots_NoBinding_ReturnsMainRoots is the backward-compat guard:
// an unbound caller resolves against the project's configured Roots, exactly
// as before worktree isolation existed.
func TestEffectiveRoots_NoBinding_ReturnsMainRoots(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	got := a.effectiveRoots("agent-without-binding")
	if len(got) != 1 || got[0].Path != dir {
		t.Errorf("unbound caller: effectiveRoots = %+v, want main root %q", got, dir)
	}
}

// TestEffectiveRoots_BoundCaller_ReturnsWorktree is the core isolation
// contract: once an agent is bound to a worktree, effectiveRoots returns that
// worktree's path as the single root, so all path resolution targets it.
func TestEffectiveRoots_BoundCaller_ReturnsWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{
		Name:    "feature",
		BaseRef: "HEAD",
	})
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}

	agentA := "019f0000000000000000000000000001"
	if err := a.bindWorktree(agentA, wt.ID); err != nil {
		t.Fatalf("bindWorktree: %v", err)
	}

	got := a.effectiveRoots(agentA)
	if len(got) != 1 {
		t.Fatalf("bound caller: expected 1 effective root, got %d (%+v)", len(got), got)
	}
	if got[0].Path != wt.Path {
		t.Errorf("bound caller: effectiveRoot path = %q, want worktree path %q", got[0].Path, wt.Path)
	}
	// Different unbound caller still sees main root (isolation is per-caller).
	gotOther := a.effectiveRoots("some-other-agent")
	if gotOther[0].Path != dir {
		t.Errorf("unbound caller leaked worktree: got %q, want main %q", gotOther[0].Path, dir)
	}
}

// TestRootPathFor_RoutesByCaller proves the git/shell interception entry point:
// rootPathFor returns the worktree path for a bound caller, main root otherwise.
func TestRootPathFor_RoutesByCaller(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{
		Name:    "feat2",
		BaseRef: "HEAD",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	agentA := "019f0000000000000000000000000002"
	if err := a.bindWorktree(agentA, wt.ID); err != nil {
		t.Fatalf("bind: %v", err)
	}

	boundRoot, err := a.rootPathFor(agentA)
	if err != nil {
		t.Fatalf("rootPathFor(bound): %v", err)
	}
	if boundRoot != wt.Path {
		t.Errorf("rootPathFor(bound) = %q, want %q", boundRoot, wt.Path)
	}

	mainRoot, err := a.rootPathFor("unbound-agent")
	if err != nil {
		t.Fatalf("rootPathFor(unbound): %v", err)
	}
	if mainRoot != dir {
		t.Errorf("rootPathFor(unbound) = %q, want main %q", mainRoot, dir)
	}
}

// TestResolvePathFor_RelativePath_ResolvesAgainstWorktree proves the file
// interception entry point: a relative path from a bound caller resolves
// inside the worktree, not the main root.
func TestResolvePathFor_RelativePath_ResolvesAgainstWorktree(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{
		Name:    "feat3",
		BaseRef: "HEAD",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	agentA := "019f0000000000000000000000000003"
	if err := a.bindWorktree(agentA, wt.ID); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Relative path "src/foo.go" should resolve under the worktree for bound caller.
	root, rel, err := a.resolvePathFor(agentA, "src/foo.go")
	if err != nil {
		t.Fatalf("resolvePathFor(bound): %v", err)
	}
	if root.Path != wt.Path {
		t.Errorf("resolvePathFor(bound) root = %q, want worktree %q", root.Path, wt.Path)
	}
	wantAbs := filepath.Join(wt.Path, "src", "foo.go")
	gotAbs := filepath.Join(root.Path, rel)
	if gotAbs != wantAbs {
		t.Errorf("resolved abs = %q, want %q", gotAbs, wantAbs)
	}
}

// TestBindWorktree_RejectsUnknownID prevents silent misrouting: binding to a
// non-existent worktree must error rather than leave the caller on main root.
func TestBindWorktree_RejectsUnknownID(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	if err := a.bindWorktree("agent-x", "no-such-worktree"); err == nil {
		t.Error("bindWorktree to unknown worktree should error")
	}
}

// TestEffectiveRoots_TwoCallersIndependent is the multi-agent isolation proof
// at the interception layer: two agents bound to two worktrees each resolve to
// their own root, neither leaks to the other or to main.
func TestEffectiveRoots_TwoCallersIndependent(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt1, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "wt1", BaseRef: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	wt2, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "wt2", BaseRef: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	agent1 := "019f0000000000000000000000000011"
	agent2 := "019f0000000000000000000000000022"
	if err := a.bindWorktree(agent1, wt1.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.bindWorktree(agent2, wt2.ID); err != nil {
		t.Fatal(err)
	}

	if r := a.effectiveRoots(agent1)[0].Path; r != wt1.Path {
		t.Errorf("agent1 root = %q, want %q", r, wt1.Path)
	}
	if r := a.effectiveRoots(agent2)[0].Path; r != wt2.Path {
		t.Errorf("agent2 root = %q, want %q", r, wt2.Path)
	}
	// Verify physical isolation: a file written in wt1 must not appear in wt2.
	if err := os.WriteFile(filepath.Join(wt1.Path, "only-in-wt1.txt"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt2.Path, "only-in-wt1.txt")); !os.IsNotExist(err) {
		t.Errorf("wt1 file leaked into wt2")
	}
}

// TestEffectiveRoots_StaleWorktree_FallsBackToMain proves graceful degradation:
// if a bound worktree becomes stale (dir missing), the caller falls back to the
// main root instead of resolving to a non-existent path.
func TestEffectiveRoots_StaleWorktree_FallsBackToMain(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "willgo", BaseRef: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	agentA := "019f0000000000000000000000000033"
	if err := a.bindWorktree(agentA, wt.ID); err != nil {
		t.Fatal(err)
	}

	// Mark the worktree stale (simulate reconcile detecting a missing dir).
	a.worktreeParentMu.Lock()
	wt.Status = "stale"
	a.worktrees[wt.ID] = wt
	a.worktreeParentMu.Unlock()

	got := a.effectiveRoots(agentA)
	if got[0].Path != dir {
		t.Errorf("stale-bound caller: effectiveRoot = %q, want fallback to main %q", got[0].Path, dir)
	}
}

// TestWorktreeIsolation_BoundCallerReadsBrowseAnywhereWritesConfined proves the
// split isolation policy for a worktree-bound caller: read commands are
// browse-anywhere (absolute paths and ".." walks outside the worktree resolve,
// with an advisory note attached), while writes stay hard-contained — not the
// main repo, not another agent's worktree, even with confirm=true. A caller
// whose worktree went inactive is denied entirely.
func TestWorktreeIsolation_BoundCallerReadsBrowseAnywhereWritesConfined(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	wtA, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "iso-a", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create wtA: %v", err)
	}
	wtB, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "iso-b", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create wtB: %v", err)
	}
	const agentA = "agent-a"
	if err := a.bindWorktree(agentA, wtA.ID); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// Relative path resolves inside the caller's own worktree.
	got, err := a.resolveBrowsePathFor(agentA, "pkg/foo.go")
	if err != nil {
		t.Fatalf("relative in own worktree: %v", err)
	}
	wantRel := filepath.Join(wtA.Path, "pkg", "foo.go")
	if got != wantRel {
		t.Errorf("relative: got %q, want %q", got, wantRel)
	}

	// Absolute path inside own worktree: allowed.
	ownAbs := filepath.Join(wtA.Path, "pkg", "bar.go")
	if got, err := a.resolveBrowsePathFor(agentA, ownAbs); err != nil || got != ownAbs {
		t.Errorf("own worktree abs path: got %q err=%v, want %q", got, err, ownAbs)
	}

	// Absolute path into the MAIN repo root: allowed (reads are
	// browse-anywhere), and the advisory note fires.
	mainFile := filepath.Join(dir, "README.md")
	if got, err := a.resolveBrowsePathFor(agentA, mainFile); err != nil || got != mainFile {
		t.Errorf("bound caller main-repo read: got %q err=%v, want %q", got, err, mainFile)
	}
	if note := a.worktreeReadNote(agentA, mainFile); note == "" {
		t.Error("bound caller main-repo read: expected advisory note, got empty")
	}

	// Absolute path into ANOTHER agent's worktree: allowed for reads, noted.
	otherFile := filepath.Join(wtB.Path, "pkg", "x.go")
	if got, err := a.resolveBrowsePathFor(agentA, otherFile); err != nil || got != otherFile {
		t.Errorf("bound caller other-worktree read: got %q err=%v, want %q", got, err, otherFile)
	}
	if note := a.worktreeReadNote(agentA, otherFile); note == "" {
		t.Error("bound caller other-worktree read: expected advisory note, got empty")
	}

	// ".." walk out of the worktree toward the main repo: resolves (joined
	// against the worktree root) and carries the note.
	wantDotDot := filepath.Clean(filepath.Join(wtA.Path, "../../README.md"))
	if got, err := a.resolveBrowsePathFor(agentA, "../../README.md"); err != nil || got != wantDotDot {
		t.Errorf("bound caller '..' walk: got %q err=%v, want %q", got, err, wantDotDot)
	}
	if note := a.worktreeReadNote(agentA, wantDotDot); note == "" {
		t.Error("bound caller '..' walk: expected advisory note, got empty")
	}

	// Inside the worktree: no note.
	if note := a.worktreeReadNote(agentA, filepath.Join(wtA.Path, "pkg", "foo.go")); note != "" {
		t.Errorf("inside-worktree read: unexpected note %q", note)
	}

	// Write/edit/rm escapes denied even with confirm=true.
	if _, err := a.resolveAbsolutePathConfirmFor(agentA, mainFile, true); err == nil {
		t.Error("bound caller write main repo (confirm=true): expected error, got nil")
	}
	if _, err := a.resolveAbsolutePathConfirmFor(agentA, otherFile, true); err == nil {
		t.Error("bound caller write another worktree (confirm=true): expected error, got nil")
	}
	// Write inside own worktree: allowed.
	ownWrite := filepath.Join(wtA.Path, "new.txt")
	if _, err := a.resolveAbsolutePathConfirmFor(agentA, ownWrite, false); err != nil {
		t.Errorf("bound caller write inside own worktree: unexpected error %v", err)
	}

	// UNBOUND caller keeps browse-anywhere (no isolation, main-root operation).
	unbound, err := a.resolveBrowsePathFor("agent-unbound", mainFile)
	if err != nil || unbound != mainFile {
		t.Errorf("unbound caller browse-anywhere broken: got %q err=%v, want %q", unbound, err, mainFile)
	}

	// BINDING-INACTIVE: mark wtA stale while keeping the binding. A bound
	// caller whose worktree is gone must NOT fall back to the main repo
	// (that would leak) — all operations are rejected.
	a.worktreeParentMu.Lock()
	staleA := a.worktrees[wtA.ID]
	staleA.Status = "stale"
	a.worktrees[wtA.ID] = staleA
	a.worktreeParentMu.Unlock()
	if _, err := a.resolveBrowsePathFor(agentA, "pkg/foo.go"); err == nil {
		t.Error("binding-inactive read: expected error, got nil (would fall back to main repo)")
	}
	if _, err := a.resolveAbsolutePathConfirmFor(agentA, ownWrite, false); err == nil {
		t.Error("binding-inactive write: expected error, got nil (would fall back to main repo)")
	}
	if _, err := a.rootPathFor(agentA); err == nil {
		t.Error("binding-inactive rootPathFor: expected error, got nil (git/shell would target main repo)")
	}
}

// compile-time guard: ensure domain is referenced so the import stays if
// future edits remove the only usage above.
var _ = domain.RootDirEntry{}

// containsStr is a tiny helper for test assertions.
func containsStr(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// TestWorktreeIsolation_GlobGrepNoEscape proves file_glob/file_grep cannot
// reach the main repo via ".." segments in the pattern/glob argument. The
// req.Path (root) is fenced by resolveBrowsePathFor, but the pattern is joined
// verbatim in the non-meta glob branch — so a pattern with ".." must be
// rejected outright, and grep's downward walk must never ascend.
func TestWorktreeIsolation_GlobGrepNoEscape(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	a, _ := worktreeTestActor(t, dir)

	// Sentinel file ONLY in the main repo root.
	writeFileIn(t, dir, "MAIN_ONLY_SENTINEL.txt", "main secret")

	wt, err := a.handleWorktreeCreate(testutil.AdminCtx(testutil.GenActorID()), gen.ProjectWorktreeCreateReq{Name: "iso-glob", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("create wt: %v", err)
	}
	agentHex := "019fab0000000000000000000000000a"
	agentKey := bindKey(t, agentHex)
	if err := a.bindWorktree(agentKey, wt.ID); err != nil {
		t.Fatalf("bind: %v", err)
	}
	// worktree-local file so a normal glob proves isolation is not over-broad.
	writeFileIn(t, wt.Path, "pkg/wt_local.go", "package pkg")
	ctx := callerCtx(t, agentHex)

	// 1. Normal glob (no "..") inside worktree: works, returns the local file.
	glob, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{Path: "pkg", Pattern: "*.go"})
	if err != nil {
		t.Fatalf("normal glob: %v", err)
	}
	if !containsStr(glob.Files, "wt_local.go") {
		t.Errorf("normal glob missing worktree file, got %v", glob.Files)
	}

	// 2. Glob with ".." escape pattern from a worktree subdir toward the main
	//    repo sentinel: must NOT return it.
	esc, err := a.handleFileGlob(ctx, domain.FileSystemGlobReq{Path: "pkg", Pattern: "../../../../MAIN_ONLY_SENTINEL.txt"})
	if err != nil {
		t.Fatalf("escape glob: %v", err)
	}
	if containsStr(esc.Files, "MAIN_ONLY") {
		t.Errorf("glob escaped to main repo via '..': %v", esc.Files)
	}

	// 3. Grep with a glob containing ".." toward the main repo sentinel: must
	//    NOT match it (grep walks downward from the fenced root only).
	grep, err := a.handleFileGrep(ctx, domain.FileSystemGrepReq{
		Path:       "pkg",
		Pattern:    "secret",
		Glob:       "../../../../MAIN_ONLY_SENTINEL.txt",
		OutputMode: "files",
	})
	if err != nil {
		t.Fatalf("escape grep: %v", err)
	}
	if containsStr(grep.Files, "MAIN_ONLY") {
		t.Errorf("grep escaped to main repo via '..': %v", grep.Files)
	}

	// 4. List with ".." in the path: reads are browse-anywhere now — the walk
	//    resolves outside the worktree and the result carries the advisory note.
	listed, err := a.handleFileList(ctx, domain.FileSystemListReq{Path: "../.."})
	if err != nil {
		t.Fatalf("list with '..' path: %v", err)
	}
	if !strings.Contains(listed, "[worktree note]") {
		t.Errorf("outside-worktree list missing advisory note, got %q", listed)
	}
}
