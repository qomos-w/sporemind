package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateCopySource_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	writeFileIn(t, root, "safe.txt", "ok")

	cases := []struct {
		name string
		src  string
	}{
		{"dotdot root", ".."},
		{"dotdot prefix", "../outside.txt"},
		{"nested dotdot", "pkg/../../outside.txt"},
		{"absolute", filepath.Join(root, "safe.txt")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validateCopySource(root, tc.src); err == nil {
				t.Fatalf("expected error for %q", tc.src)
			}
		})
	}
}

func TestValidateCopySource_RejectsDependencyTrees(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{
		"web/node_modules/pkg/index.js",
		"rust/target/debug/lib.dll",
		"py/venv/bin/python",
		"py2/.venv/bin/python",
		"go/vendor/example.com/f.go",
		"web/dist/bundle.js",
		"build/output.bin",
		"__pycache__/mod.cpython.pyc",
	} {
		writeFileIn(t, root, rel, "x")
	}
	writeFileIn(t, root, "src/main.go", "ok")

	cases := []struct {
		src    string
		wantOK bool
	}{
		{"src/main.go", true},
		{"web/node_modules/pkg/index.js", false},
		{"rust/target/debug/lib.dll", false},
		{"py/venv/bin/python", false},
		{"py2/.venv/bin/python", false},
		{"go/vendor/example.com/f.go", false},
		{"web/dist/bundle.js", false},
		{"build/output.bin", false},
		{"__pycache__/mod.cpython.pyc", false},
	}

	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			got, err := validateCopySource(root, tc.src)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("expected ok for %q, got %v", tc.src, err)
				}
				if got == "" {
					t.Fatalf("expected non-empty resolved path for %q", tc.src)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error for %q", tc.src)
				}
			}
		})
	}
}

func TestValidateCopySource_RejectsSymlinkOutside(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFileIn(t, outside, "secret.txt", "secret")

	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	_, err := validateCopySource(root, "link.txt")
	if err == nil {
		t.Fatal("expected error for symlink pointing outside main root")
	}
	if !strings.Contains(err.Error(), "outside main root") {
		t.Fatalf("expected outside-main-root error, got %v", err)
	}
}

func TestValidateCopySource_RejectsAllSymlinkComponents(t *testing.T) {
	// Even a symlink that stays inside mainRoot is rejected: junctions cannot
	// be distinguished from safe symlinks without following them, and
	// EvalSymlinks does not follow Windows junctions at all.
	root := t.TempDir()
	writeFileIn(t, root, "real.txt", "real")
	if err := os.Symlink(filepath.Join(root, "real.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	_, err := validateCopySource(root, "link.txt")
	if err == nil {
		t.Fatal("expected error for inside-root symlink component")
	}
	if !strings.Contains(err.Error(), "symlink/junction") {
		t.Fatalf("expected symlink/junction error, got %v", err)
	}
}

// makeJunction (see worktree_safe_remove_test.go) creates a Windows directory
// junction with no privilege required; the junction tests below skip on other
// platforms.
func mustJunction(t *testing.T, target, link string) {
	t.Helper()
	if err := makeJunction(t, target, link); err != nil {
		t.Skipf("junctions unavailable: %v", err)
	}
}

func TestValidateCopySource_RejectsJunctionComponent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFileIn(t, outside, "secret.txt", "secret")

	mustJunction(t, outside, filepath.Join(root, "linkdir"))

	_, err := validateCopySource(root, filepath.Join("linkdir", "secret.txt"))
	if err == nil {
		t.Fatal("expected error for junction source component")
	}
	if !strings.Contains(err.Error(), "symlink/junction") {
		t.Fatalf("expected symlink/junction error, got %v", err)
	}
}

func TestValidateCopyDest_RejectsJunctionComponent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFileIn(t, outside, "stash.txt", "x")

	mustJunction(t, outside, filepath.Join(root, "linkdir"))

	err := validateCopyDest(root, filepath.Join("linkdir", "stash.txt"))
	if err == nil {
		t.Fatal("expected error for junction dest component")
	}
	if !strings.Contains(err.Error(), "symlink/junction") {
		t.Fatalf("expected symlink/junction error, got %v", err)
	}
}

func TestValidateCopyDest_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	writeFileIn(t, root, "existing.txt", "x")

	cases := []struct {
		name string
		dest string
	}{
		{"dotdot root", ".."},
		{"dotdot prefix", "../outside.txt"},
		{"nested dotdot", "pkg/../../outside.txt"},
		{"absolute", filepath.Join(root, "existing.txt")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateCopyDest(root, tc.dest); err == nil {
				t.Fatalf("expected error for %q", tc.dest)
			}
		})
	}
}

func TestValidateCopyDest_RejectsSymlinkComponent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFileIn(t, outside, "real.txt", "real")

	// Create a symlink inside the worktree pointing somewhere else.
	linkDir := filepath.Join(root, "linkdir")
	if err := os.Symlink(outside, linkDir); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	if err := validateCopyDest(root, "linkdir/file.txt"); err == nil {
		t.Fatal("expected error for destination path containing symlink")
	}

	// A symlink as the final component is also rejected.
	if err := validateCopyDest(root, "linkdir"); err == nil {
		t.Fatal("expected error for destination path that is itself a symlink")
	}
}

func TestValidateCopyDest_AcceptsMissingTail(t *testing.T) {
	root := t.TempDir()
	writeFileIn(t, root, "existing.txt", "x")

	if err := validateCopyDest(root, "newdir/newfile.txt"); err != nil {
		t.Fatalf("unexpected error for missing-tail destination: %v", err)
	}
}

func TestValidateCopyDest_AcceptsExistingFile(t *testing.T) {
	root := t.TempDir()
	writeFileIn(t, root, "existing.txt", "x")

	if err := validateCopyDest(root, "existing.txt"); err != nil {
		t.Fatalf("unexpected error for existing-file destination: %v", err)
	}
}

func TestCheckOverwriteSafe(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := t.TempDir()
	initGitRepo(t, repo)
	writeFileIn(t, repo, "shared.go", "v1")
	writeFileIn(t, repo, "remove.go", "v1")
	gitRunMust(t, repo, "add", ".")
	gitRunMust(t, repo, "commit", "-m", "add files")

	// Create a real git worktree (not under .git) to mirror production layout.
	wt := filepath.Join(t.TempDir(), "wt")
	gitRunMust(t, repo, "worktree", "add", "-b", "feat", wt, "HEAD")

	// 1. File identical to merge-base → safe.
	safe, err := checkOverwriteSafe(repo, wt, "shared.go")
	if err != nil {
		t.Fatalf("check shared.go unchanged: %v", err)
	}
	if !safe {
		t.Fatal("shared.go unchanged: expected safe=true")
	}

	// 2. Modified in worktree → unsafe.
	writeFileIn(t, wt, "shared.go", "v2-worker")
	safe, err = checkOverwriteSafe(repo, wt, "shared.go")
	if err != nil {
		t.Fatalf("check shared.go modified: %v", err)
	}
	if safe {
		t.Fatal("shared.go modified: expected safe=false")
	}

	// 3. Staged/committed change → unsafe.
	writeFileIn(t, wt, "staged.go", "worker-staged")
	gitRunMust(t, wt, "add", "staged.go")
	gitRunMust(t, wt, "commit", "-m", "add staged.go")
	safe, err = checkOverwriteSafe(repo, wt, "staged.go")
	if err != nil {
		t.Fatalf("check staged.go committed: %v", err)
	}
	if safe {
		t.Fatal("staged.go committed: expected safe=false")
	}

	// 4. Untracked file on disk → unsafe.
	writeFileIn(t, wt, "untracked.go", "untracked-worker")
	safe, err = checkOverwriteSafe(repo, wt, "untracked.go")
	if err != nil {
		t.Fatalf("check untracked.go: %v", err)
	}
	if safe {
		t.Fatal("untracked.go present: expected safe=false")
	}

	// 5. Tracked file deleted in worktree → unsafe.
	if err := os.Remove(filepath.Join(wt, "remove.go")); err != nil {
		t.Fatal(err)
	}
	safe, err = checkOverwriteSafe(repo, wt, "remove.go")
	if err != nil {
		t.Fatalf("check remove.go deleted: %v", err)
	}
	if safe {
		t.Fatal("remove.go deleted: expected safe=false")
	}
}

func TestCheckOverwriteSafe_WorktreeAdvanced(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repo := t.TempDir()
	initGitRepo(t, repo)
	writeFileIn(t, repo, "file.go", "v1")
	gitRunMust(t, repo, "add", "file.go")
	gitRunMust(t, repo, "commit", "-m", "base")

	// Main advances after worktree creation; merge-base stays at the base commit.
	wt := filepath.Join(t.TempDir(), "wt")
	gitRunMust(t, repo, "worktree", "add", "-b", "feat", wt, "HEAD")
	writeFileIn(t, repo, "main-advance.txt", "x")
	gitRunMust(t, repo, "add", "main-advance.txt")
	gitRunMust(t, repo, "commit", "-m", "main advances")

	// file.go unchanged in worktree → still safe despite main advancing.
	safe, err := checkOverwriteSafe(repo, wt, "file.go")
	if err != nil {
		t.Fatalf("check file.go: %v", err)
	}
	if !safe {
		t.Fatal("file.go unchanged relative to merge-base: expected safe=true")
	}

	// Worker modifies file.go → unsafe.
	writeFileIn(t, wt, "file.go", "v2")
	safe, err = checkOverwriteSafe(repo, wt, "file.go")
	if err != nil {
		t.Fatalf("check file.go modified: %v", err)
	}
	if safe {
		t.Fatal("file.go modified: expected safe=false")
	}
}
