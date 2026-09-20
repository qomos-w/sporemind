package project

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// copyDependencyDirs are path components that identify dependency or derived
// artifact trees. A copy source is rejected if any of its path components
// matches this set, so we never pull node_modules, build output, virtual
// environments, etc. into a worktree under the main-repo→worktree copy path.
// It extends the defaultSkipDirs convention used by fileops.go / fileops_glob_ignore_test.go.
var copyDependencyDirs = func() map[string]bool {
	m := make(map[string]bool, len(defaultSkipDirs)+8)
	for k, v := range defaultSkipDirs {
		m[k] = v
	}
	for _, k := range []string{
		"target",        // Rust / Go build output
		"venv", ".venv", // Python virtual environments
		"site-packages", ".tox", // Python ecosystem
		".mypy_cache", ".pytest_cache", // Python test cache
		".gradle", // Gradle build cache
	} {
		m[k] = true
	}
	return m
}()

// isReparsePoint reports whether the Lstat result describes a symlink or other
// reparse point. Windows directory junctions report os.ModeIrregular (Go maps
// only IO_REPARSE_TAG_SYMLINK to ModeSymlink, and filepath.EvalSymlinks does
// not follow junctions), so both bits must be checked by mask.
func isReparsePoint(fi os.FileInfo) bool {
	m := fi.Mode()
	return m&(os.ModeSymlink|os.ModeIrregular) != 0
}

// validateCopySource checks that src (relative to mainRoot) is a legitimate
// source path for a main-repo→worktree copy. It rejects absolute paths, ".."
// traversal, paths containing any symlink/junction component, paths that
// escape mainRoot after symlink resolution, and paths that live inside
// dependency trees (node_modules, target, venv, etc.).
// On success it returns the resolved absolute source path.
func validateCopySource(mainRoot, src string) (string, error) {
	if filepath.IsAbs(src) {
		return "", fmt.Errorf("copy source must be relative: %q", src)
	}

	clean := filepath.Clean(src)
	sep := string(filepath.Separator)
	if clean == ".." || strings.HasPrefix(clean, ".."+sep) {
		return "", fmt.Errorf("copy source escapes root via ..: %q", src)
	}

	// Reject dependency trees by path component in the input path.
	for _, p := range strings.Split(clean, sep) {
		if copyDependencyDirs[p] {
			return "", fmt.Errorf("copy source is inside dependency tree %q: %q", p, src)
		}
	}

	// Resolve the main root itself in case it contains links/junctions.
	rootAbs, err := filepath.EvalSymlinks(mainRoot)
	if err != nil {
		return "", fmt.Errorf("copy source: cannot resolve main root %q: %w", mainRoot, err)
	}
	rootAbs = filepath.Clean(rootAbs)

	joined := filepath.Join(rootAbs, clean)

	// Reject any existing path component that is a symlink or reparse point.
	// Junctions on Windows do NOT report os.ModeSymlink (Go maps only
	// IO_REPARSE_TAG_SYMLINK to ModeSymlink) and filepath.EvalSymlinks does
	// not follow them, so a junction whose target lives outside mainRoot would
	// otherwise slip past the containment check below while os.ReadFile still
	// reads through it. Catch reparse points explicitly, per component.
	current := rootAbs
	for _, part := range strings.Split(clean, sep) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		fi, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				break
			}
			return "", fmt.Errorf("copy source: cannot stat %q: %w", current, err)
		}
		if isReparsePoint(fi) {
			return "", fmt.Errorf("copy source component is a symlink/junction: %q", current)
		}
	}

	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("copy source does not exist: %q", src)
		}
		return "", fmt.Errorf("copy source: cannot resolve symlinks for %q: %w", src, err)
	}
	resolved = filepath.Clean(resolved)

	// Containment check: the resolved path must stay inside mainRoot.
	relResolved, err := filepath.Rel(rootAbs, resolved)
	if err != nil || strings.HasPrefix(relResolved, "..") {
		return "", fmt.Errorf("copy source resolves outside main root: %q", src)
	}

	// Dependency-tree check on the resolved path too (a symlink may have hidden
	// a node_modules component).
	for _, p := range strings.Split(relResolved, sep) {
		if p == "." || p == "" {
			continue
		}
		if copyDependencyDirs[p] {
			return "", fmt.Errorf("copy source resolves into dependency tree %q: %q", p, src)
		}
	}

	return resolved, nil
}

// validateCopyDest checks that dest (relative to worktreeRoot) is a safe
// destination for a main-repo→worktree copy. It rejects absolute paths, ".."
// traversal, paths that escape the worktree root, and any path component that
// is a symlink or junction (junctions report os.ModeIrregular, see
// isReparsePoint).
func validateCopyDest(worktreeRoot, dest string) error {
	if filepath.IsAbs(dest) {
		return fmt.Errorf("copy destination must be relative: %q", dest)
	}

	clean := filepath.Clean(dest)
	sep := string(filepath.Separator)
	if clean == ".." || strings.HasPrefix(clean, ".."+sep) {
		return fmt.Errorf("copy destination escapes worktree via ..: %q", dest)
	}

	wtAbs, err := filepath.EvalSymlinks(worktreeRoot)
	if err != nil {
		return fmt.Errorf("copy dest: cannot resolve worktree root %q: %w", worktreeRoot, err)
	}
	wtAbs = filepath.Clean(wtAbs)

	abs := filepath.Join(wtAbs, clean)
	if abs != wtAbs && !strings.HasPrefix(abs, wtAbs+sep) {
		return fmt.Errorf("copy destination escapes worktree root: %q", dest)
	}

	// Walk each path component that exists and verify it is not a symlink/junction.
	// Missing tail components are fine — MkdirAll will create them as real dirs.
	current := wtAbs
	for _, part := range strings.Split(clean, sep) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)

		fi, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				// Missing intermediate component: stop checking because
				// anything below it will be created fresh.
				break
			}
			return fmt.Errorf("copy dest: cannot stat %q: %w", current, err)
		}
		if isReparsePoint(fi) {
			return fmt.Errorf("copy destination component is a symlink/junction: %q", current)
		}
	}

	return nil
}

// checkOverwriteSafe reports whether the worker has modified the file at
// relPath (repo-relative) inside the worktree compared to the merge-base
// between the worktree HEAD and the main repo HEAD.
//
// It returns true when the file is identical to the merge-base version (safe
// to overwrite without force), and false when the worker has changed, deleted,
// or added the file relative to the merge-base. Errors are returned for git
// or filesystem failures.
func checkOverwriteSafe(repoPath, worktreeRoot, relPath string) (bool, error) {
	clean := filepath.Clean(relPath)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return false, fmt.Errorf("invalid relative path: %q", relPath)
	}

	mainHead, err := gitRevParseHead(repoPath)
	if err != nil {
		return false, fmt.Errorf("checkOverwriteSafe: cannot read main repo HEAD: %w", err)
	}
	wtHead, err := gitRevParseHead(worktreeRoot)
	if err != nil {
		return false, fmt.Errorf("checkOverwriteSafe: cannot read worktree HEAD: %w", err)
	}

	base, err := gitMergeBase(repoPath, mainHead, wtHead)
	if err != nil {
		return false, fmt.Errorf("checkOverwriteSafe: cannot compute merge-base: %w", err)
	}

	gitPath := filepath.ToSlash(clean)

	// Compare worktree working tree (committed + staged + unstaged) against base.
	changed, err := gitDiffChanges(worktreeRoot, base, gitPath)
	if err != nil {
		return false, err
	}
	if changed {
		return false, nil
	}

	// Check for untracked (and gitignored-untracked) files on disk.
	tracked, err := gitIsTracked(worktreeRoot, gitPath)
	if err != nil {
		return false, err
	}
	if !tracked {
		full := filepath.Join(worktreeRoot, clean)
		if _, err := os.Stat(full); err == nil {
			// An untracked file exists on disk; copying would clobber it.
			return false, nil
		} else if !os.IsNotExist(err) {
			return false, fmt.Errorf("checkOverwriteSafe: cannot stat %q: %w", full, err)
		}
	}

	return true, nil
}

// gitMergeBase returns the merge-base of two commits, running in dir.
func gitMergeBase(dir, a, b string) (string, error) {
	out, err := gitRun(nil, dir, "merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// gitDiffChanges reports whether relPath (forward-slash, git-relative) differs
// between commit and the working tree (committed + staged + unstaged changes).
func gitDiffChanges(dir, commit, relPath string) (bool, error) {
	ctx, cancel := gitCtx(nil)
	defer cancel()
	cmd := gitCmd(ctx, dir, "diff", "--quiet", commit, "--", relPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return false, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return true, nil
	}
	return false, fmt.Errorf("git diff --quiet failed: %s: %w", strings.TrimSpace(string(out)), err)
}

// gitIsTracked reports whether relPath (forward-slash, git-relative) is tracked
// by git in the given working tree.
func gitIsTracked(dir, relPath string) (bool, error) {
	ctx, cancel := gitCtx(nil)
	defer cancel()
	cmd := gitCmd(ctx, dir, "ls-files", "--error-unmatch", "--", relPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("git ls-files --error-unmatch failed: %s: %w", strings.TrimSpace(string(out)), err)
}
