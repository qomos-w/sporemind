package codegen

import (
	"fmt"
	"path"
	"strings"
)

// NormalizeAppDir validates an optional app subdirectory (AppDir) and returns
// its canonical slash-separated form relative to the project root. An empty
// input or "." normalizes to "" which means the project root itself — the
// pre-subdirectory default behavior.
//
// Rejected values fail loudly with a descriptive error (never silently
// rewritten), following the lspserver installer's install-name precedent:
//   - absolute paths ("/x", "\\x") and Windows drive prefixes ("C:...")
//   - any ".." component (parent traversal out of the project root)
//   - any "." component other than the whole-string "." form
//   - backslash separators and NUL bytes
//   - non-canonical forms ("a//b", "a/", "./a", trailing/leading spaces)
func NormalizeAppDir(appDir string) (string, error) {
	s := strings.TrimSpace(appDir)
	if s == "" || s == "." {
		return "", nil
	}
	if strings.ContainsRune(s, 0) {
		return "", fmt.Errorf("app dir %q contains NUL byte", appDir)
	}
	if strings.Contains(s, "\\") {
		return "", fmt.Errorf("app dir %q must use forward slashes, not backslashes", appDir)
	}
	if strings.HasPrefix(s, "/") {
		return "", fmt.Errorf("app dir %q must be relative to the project root, not absolute", appDir)
	}
	if i := strings.Index(s, ":"); i >= 0 {
		return "", fmt.Errorf("app dir %q must not contain ':' (drive letters are absolute paths)", appDir)
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" {
			return "", fmt.Errorf("app dir %q has an empty path component", appDir)
		}
		if part == "." || part == ".." {
			return "", fmt.Errorf("app dir %q must not contain %q components", appDir, part)
		}
	}
	if clean := path.Clean(s); clean != s {
		return "", fmt.Errorf("app dir %q is not canonical; use %q", appDir, clean)
	}
	return s, nil
}

// JoinAppDir appends a validated app dir to a project root, returning the
// directory codegen/build operations should operate on. An empty appDir
// returns root unchanged.
func JoinAppDir(projectRoot, appDir string) (string, error) {
	normalized, err := NormalizeAppDir(appDir)
	if err != nil {
		return "", err
	}
	if normalized == "" {
		return projectRoot, nil
	}
	return strings.TrimSuffix(projectRoot, "/") + "/" + normalized, nil
}

// RelToRoot prefixes a codegen-relative file name with the validated app dir
// so the result is relative to the project root (the frame protected-files
// and dev_generate responses use). Empty appDir returns name unchanged.
func RelToRoot(appDir, name string) (string, error) {
	normalized, err := NormalizeAppDir(appDir)
	if err != nil {
		return "", err
	}
	if normalized == "" {
		return name, nil
	}
	return normalized + "/" + name, nil
}
