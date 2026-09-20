package project

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// goWorkReplacePattern matches "replace <module> => <path>" lines in go.mod.
var goWorkReplacePattern = regexp.MustCompile(`^replace\s+(\S+)\s+=>\s+(\.\.?/[^/\s].*|/?\S+)\s*$`)

// generateWorktreeGoWork writes a go.work file inside the worktree directory
// so that go tools (go test, go build) compile the worktree's own files
// instead of being hijacked by the main repository's go.work.
//
// The worktree lives at an arbitrary path (the project actor's settings
// directory), not at a fixed depth below the repo root. The main repo's
// go.mod has replace directives pointing to sibling directories (e.g.
// ../gospore); these paths must be rewritten to be relative to the
// worktree root using filepath.Rel so they resolve regardless of the
// worktree's location.
//
// The generated go.work mirrors the main repo's go.work use directives and
// rewrites all relative replace paths to be relative to the worktree root.
func generateWorktreeGoWork(root, wtPath string) error {
	goModData, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil // no go.mod → nothing to do
	}

	// Collect replace directives with relative paths from go.mod.
	var rewrites []string
	for _, line := range strings.Split(string(goModData), "\n") {
		line = strings.TrimSpace(line)
		m := goWorkReplacePattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		mod, target := m[1], m[2]
		if !strings.HasPrefix(target, "../") && !strings.HasPrefix(target, "./") {
			rewrites = append(rewrites, fmt.Sprintf("replace %s => %s", mod, target))
			continue
		}
		rewrites = append(rewrites, fmt.Sprintf("replace %s => %s", mod, adjustReplacePath(root, wtPath, target)))
	}

	// Read the main repo's go.work to mirror use directives; if absent,
	// default to just "use ./."
	useLines := []string{"use ./."}
	if goWorkData, rerr := os.ReadFile(filepath.Join(root, "go.work")); rerr == nil {
		var parsed []string
		for _, line := range strings.Split(string(goWorkData), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "use ") {
				parsed = append(parsed, line)
			}
		}
		if len(parsed) > 0 {
			useLines = parsed
		}
	}

	// Read go directive from go.mod for the go version line.
	goVersion := "go 1.25.0"
	for _, line := range strings.Split(string(goModData), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "go ") {
			goVersion = line
			break
		}
	}

	var sb strings.Builder
	sb.WriteString(goVersion)
	sb.WriteString("\n\n")
	for _, u := range useLines {
		sb.WriteString(u)
		sb.WriteString("\n\n")
	}
	for _, r := range rewrites {
		sb.WriteString(r)
		sb.WriteString("\n")
	}

	return os.WriteFile(filepath.Join(wtPath, "go.work"), []byte(sb.String()), 0o644)
}

// adjustReplacePath rewrites a relative replace target path (relative to the
// main repo root) to be relative to the worktree root at wtPath. Uses
// filepath.Rel for dynamic depth calculation so it works regardless of
// where the worktree is located. If filepath.Rel fails (e.g. cross-drive on
// Windows), the original target is returned as a fallback.
func adjustReplacePath(root, wtPath, target string) string {
	if !strings.HasPrefix(target, "../") && !strings.HasPrefix(target, "./") {
		return target
	}
	absTarget := filepath.Join(root, target)
	rel, err := filepath.Rel(wtPath, absTarget)
	if err != nil {
		return target
	}
	return filepath.ToSlash(rel)
}