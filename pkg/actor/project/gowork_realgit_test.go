package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateWorktreeGoWork_RealGitWorktree is the "real creation" regression
// test for the worktree go.work generator. Unlike the synthetic unit tests in
// gowork_test.go, this test performs a genuine git worktree creation at the
// production layout <root>/.git/wt/<id> and then runs the real generator,
// asserting the four acceptance checks from task card T1:
//
//  1. a go.work file exists in the newly created worktree;
//  2. it mirrors the main repo go.work use directives;
//  3. it rewrites relative replace paths (../sib -> ../../../../sib);
//  4. the generated go.work is gitignored and absent from git status.
func TestGenerateWorktreeGoWork_RealGitWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in test environment")
	}

	tmp := t.TempDir()
	root := filepath.Join(tmp, "sporemind")
	wtID := "00000000-0000-0000-0000-0000000000t1"
	wtPath := filepath.Join(tmp, "wt-store", wtID)
	sib := filepath.Join(tmp, "sib") // sibling module outside the repo: ../sib from root

	mustGit(t, "", "init", root)
	mustGit(t, root, "config", "user.email", "t1@test.local")
	mustGit(t, root, "config", "user.name", "T1 test")

	// Root module: go.mod with a sibling replace, go.work with use directives,
	// .gitignore covering go.work, and a submodule so the mirrored use lines
	// point at a real module.
	writeFile(t, filepath.Join(root, "go.mod"), `module example.com/main

go 1.25.0

require example.com/sib v0.0.0

replace example.com/sib => ../sib
`)
	writeFile(t, filepath.Join(root, "go.work"), `go 1.25.0

use ./.

use ./sporemind-plugin-sdk
`)
	writeFile(t, filepath.Join(root, ".gitignore"), "go.work\ngo.work.sum\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nimport _ \"example.com/sib\"\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "sporemind-plugin-sdk", "go.mod"), "module example.com/sdk\n\ngo 1.25.0\n")
	// Sibling module (the replace target ../sib).
	writeFile(t, filepath.Join(sib, "go.mod"), "module example.com/sib\n\ngo 1.25.0\n")
	writeFile(t, filepath.Join(sib, "sib.go"), "package sib\n")

	mustGit(t, root, "add", "-A")
	mustGit(t, root, "commit", "-m", "init")

	// Real git worktree add at an arbitrary path (not under .git),
	// mirroring gitWorktreeAdd("worktree add -b <branch> <path> HEAD").
	mustGit(t, root, "worktree", "add", "-b", "t1-branch", wtPath, "HEAD")

	if err := generateWorktreeGoWork(root, wtPath); err != nil {
		t.Fatalf("generateWorktreeGoWork: %v", err)
	}

	// Check 1: go.work exists.
	data, err := os.ReadFile(filepath.Join(wtPath, "go.work"))
	if err != nil {
		t.Fatalf("read generated go.work: %v", err)
	}
	content := string(data)
	if len(strings.TrimSpace(content)) == 0 {
		t.Fatal("generated go.work is empty")
	}

	// Check 2: use directives mirrored from the main repo go.work.
	for _, want := range []string{"use ./.", "use ./sporemind-plugin-sdk"} {
		if !strings.Contains(content, want) {
			t.Errorf("go.work missing %q, got:\n%s", want, content)
		}
	}

	// Check 3: replace paths rewritten relative to the worktree using
	// dynamic filepath.Rel (wtPath is at tmp/wt-store/<id>, sib is at
	// tmp/sib, root is at tmp/sporemind; ../sib from root = tmp/sib).
	relSib, err := filepath.Rel(wtPath, filepath.Join(root, "..", "sib"))
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}
	wantReplace := "replace example.com/sib => " + filepath.ToSlash(relSib)
	if !strings.Contains(content, wantReplace) {
		t.Errorf("go.work missing rewritten replace %q, got:\n%s", wantReplace, content)
	}
	if strings.Contains(content, "=> ../sib") {
		t.Errorf("go.work contains non-rewritten ../sib, got:\n%s", content)
	}

	// Functional resolution: go must resolve example.com/sib via the rewritten
	// replace to the sibling directory, not to the worktree-local ../sib.
	// Also verify the main module resolves to the worktree directory (not the
	// main repo) — proving go tools compile the worktree's own files.
	if _, err := exec.LookPath("go"); err == nil {
		cmd := exec.Command("go", "list", "-m", "-f", "{{.Replace.Dir}}", "example.com/sib")
		cmd.Dir = wtPath
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("go list -m example.com/sib in worktree: %v", err)
		}
		got := strings.TrimSpace(string(out))
		if filepath.Clean(got) != filepath.Clean(sib) {
			t.Errorf("go resolved example.com/sib to %q, want %q", got, sib)
		}

		// T2: go tools must see the worktree as the main module directory.
		mainCmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "example.com/main")
		mainCmd.Dir = wtPath
		mainOut, err := mainCmd.Output()
		if err != nil {
			t.Fatalf("go list -m example.com/main in worktree: %v", err)
		}
		mainDir := strings.TrimSpace(string(mainOut))
		if filepath.Clean(mainDir) != filepath.Clean(wtPath) {
			t.Errorf("go resolved main module to %q, want worktree %q (D1: tools must compile worktree files, not main repo)", mainDir, wtPath)
		}
	}

	// Check 4: go.work is gitignored and invisible to git status.
	if err := exec.Command("git", "-C", wtPath, "check-ignore", "-q", "go.work").Run(); err != nil {
		t.Errorf("git check-ignore go.work in worktree: %v (want exit 0 = ignored)", err)
	}
	status, err := exec.Command("git", "-C", wtPath, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status --porcelain in worktree: %v", err)
	}
	if len(strings.TrimSpace(string(status))) != 0 {
		t.Errorf("worktree git status not clean after generation, got:\n%s", status)
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

