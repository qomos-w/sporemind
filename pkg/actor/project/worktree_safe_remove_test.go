package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/util"
)

// makeJunction creates a Windows directory junction link → target via
// mklink /J, which needs no privilege. Returns an error on non-Windows.
func makeJunction(t *testing.T, target, link string) error {
	t.Helper()
	if runtime.GOOS != "windows" {
		return fmt.Errorf("junctions are windows-only")
	}
	cmd := exec.Command("cmd", "/c", "mklink", "/J", link, target)
	util.HideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mklink /J %s %s: %v: %s", link, target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// TestSafeRemoveWorktreeDir_Guards proves the refusal guards: relative
// paths, the repo root, ancestors of the repo root, and the filesystem root
// are never deleted.
func TestSafeRemoveWorktreeDir_Guards(t *testing.T) {
	root := t.TempDir()
	if err := safeRemoveWorktreeDir(root, "relative/path"); err == nil {
		t.Fatal("relative path should be refused")
	}
	if err := safeRemoveWorktreeDir(root, root); err == nil {
		t.Fatal("repo root should be refused")
	}
	if err := safeRemoveWorktreeDir(root, filepath.Dir(root)); err == nil {
		t.Fatal("ancestor of repo root should be refused")
	}
	fsRoot := filepath.Clean(root)
	for filepath.Dir(fsRoot) != fsRoot {
		fsRoot = filepath.Dir(fsRoot)
	}
	if err := safeRemoveWorktreeDir(root, fsRoot); err == nil {
		t.Fatal("filesystem root should be refused")
	}
}

// TestSafeRemoveWorktreeDir_NeverFollowsSymlink proves the core safety
// property of the orphan-dir cleanup: a symlink (or junction — Go reports
// both as ModeSymlink) inside the removed worktree is unlinked, but its
// target's contents survive untouched. Regression guard for the 2026-08-14
// incident where a recursive removal followed junctions and deleted the
// main repo's node_modules and other real trees.
func TestSafeRemoveWorktreeDir_NeverFollowsSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "precious.txt")
	if err := os.WriteFile(sentinel, []byte("do not delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "worktree", "wt-1")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "tracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(wt, "shared")); err != nil {
		// Junctions need no privilege on Windows and exercise the same
		// reparse-point path as the 2026-08-14 incident.
		if jErr := makeJunction(t, outside, filepath.Join(wt, "shared")); jErr != nil {
			t.Skipf("cannot create symlink (%v) or junction (%v)", err, jErr)
		}
	}
	if err := safeRemoveWorktreeDir(root, wt); err != nil {
		t.Fatalf("safe remove: %v", err)
	}
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone, got err=%v", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "do not delete" {
		t.Fatalf("sentinel outside the worktree was damaged through the link: err=%v data=%q", err, data)
	}
}

// TestGitWorktreeRemove_OrphanDirCleaned simulates the Windows residue
// case: git has deregistered the worktree (its .git/worktrees entry is
// gone) but the checkout dir still lingers on disk. gitWorktreeRemove must
// converge to success and remove the orphan dir, including untracked
// files, via the safe path.
func TestGitWorktreeRemove_OrphanDirCleaned(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)
	gitOut(t, root, "commit", "--allow-empty", "-m", "init")
	wt := filepath.Join(root, "wt-residue")
	gitOut(t, root, "worktree", "add", "-b", "wt-residue", wt)
	if err := os.WriteFile(filepath.Join(wt, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Deregister: delete git's administrative entry but keep the checkout
	// on disk — the state a half-failed Windows removal leaves behind.
	if err := os.RemoveAll(filepath.Join(root, ".git", "worktrees", "wt-residue")); err != nil {
		t.Fatal(err)
	}
	if err := gitWorktreeRemove(root, wt); err != nil {
		t.Fatalf("gitWorktreeRemove should converge on an orphan dir: %v", err)
	}
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Fatalf("orphan dir should be removed, got err=%v", err)
	}
}

// TestGitWorktreeRemove_StillRegisteredNotDeleted proves the flip side of
// the guard: when git still registers the path, a failed remove must NOT
// locally delete the checkout (that would leave git with a stale
// registration pointing at a missing worktree).
func TestGitWorktreeRemove_StillRegisteredNotDeleted(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)
	gitOut(t, root, "commit", "--allow-empty", "-m", "init")
	wt := filepath.Join(root, "wt-keep")
	gitOut(t, root, "worktree", "add", "-b", "wt-keep", wt)
	if err := os.WriteFile(filepath.Join(wt, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The dir exists and stays registered. Point gitWorktreeRemove at the
	// registered path but with a bogus git root: root is not a repository,
	// so gitWorktreeRemove must fail before touching the filesystem and the
	// worktree must survive.
	bogusRoot := t.TempDir()
	if err := gitWorktreeRemove(bogusRoot, wt); err == nil {
		t.Fatal("remove via bogus root should fail")
	}
	if _, err := os.Lstat(wt); err != nil {
		t.Fatalf("still-registered worktree must not be deleted: %v", err)
	}
}

// TestGitWorktreeRemove_NeverFollowsJunction is the full-path regression
// test for the 2026-08-14 incident: removing a REGISTERED worktree whose
// checkout contains a junction pointing at a tree outside the repo must
// unlink the junction, leave the target intact, and deregister the worktree
// from git. The old implementation delegated the delete to
// `git worktree remove --force`, which recursed through the junction.
func TestGitWorktreeRemove_NeverFollowsJunction(t *testing.T) {
	root := t.TempDir()
	initGitRepo(t, root)
	gitOut(t, root, "commit", "--allow-empty", "-m", "init")
	wt := filepath.Join(root, "wt-junction")
	gitOut(t, root, "worktree", "add", "-b", "wt-junction", wt)

	outside := t.TempDir()
	sentinel := filepath.Join(outside, "precious.txt")
	if err := os.WriteFile(sentinel, []byte("do not delete"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(wt, "shared")
	if err := os.Symlink(outside, link); err != nil {
		if jErr := makeJunction(t, outside, link); jErr != nil {
			t.Skipf("cannot create symlink (%v) or junction (%v)", err, jErr)
		}
	}

	if err := gitWorktreeRemove(root, wt); err != nil {
		t.Fatalf("gitWorktreeRemove: %v", err)
	}
	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone, got err=%v", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "do not delete" {
		t.Fatalf("sentinel outside the worktree was damaged through the link: err=%v data=%q", err, data)
	}
	entries, err := gitWorktreeList(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Clean(e.Path) == filepath.Clean(wt) {
			t.Fatalf("worktree should be deregistered, still listed: %+v", e)
		}
	}
}
