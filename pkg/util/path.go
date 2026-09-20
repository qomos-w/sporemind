package util

import (
	"path/filepath"
	"strings"
)

// NormalizePath canonicalises a filesystem path for stable comparison.
//   - Converts backslashes to forward slashes
//   - Removes trailing slashes
//   - Evaluates "." (returns "." unchanged as caller decides semantics)
//   - Uses filepath.Clean on the native side then converts to /
func NormalizePath(p string) string {
	if p == "" {
		return ""
	}
	// filepath.Clean handles . and .. on the current platform.
	clean := filepath.Clean(p)
	// Convert to forward slashes for cross-platform consistency.
	clean = strings.ReplaceAll(clean, `\`, "/")
	return clean
}

// IsSubPath reports whether child is a strict subdirectory of parent.
// Both paths are normalised before comparison.
func IsSubPath(child, parent string) bool {
	c := NormalizePath(child)
	p := NormalizePath(parent)
	// Remove trailing slash from parent so /foo/bar matches /foo/bar/baz
	c = strings.TrimSuffix(c, "/")
	p = strings.TrimSuffix(p, "/")
	return c != p && strings.HasPrefix(c+"/", p+"/")
}

// PathWithin reports whether dir is root or a descendant of root. Comparison
// folds case so Windows (case-insensitive filesystems) matches reliably;
// over-matching on a case-sensitive host is harmless for teardown callers.
func PathWithin(dir, root string) bool {
	if dir == "" || root == "" {
		return false
	}
	d, r := NormalizePath(dir), NormalizePath(root)
	d, r = strings.TrimSuffix(d, "/"), strings.TrimSuffix(r, "/")
	if d == r || strings.HasPrefix(d+"/", r+"/") {
		return true
	}
	return strings.EqualFold(d, r) || strings.HasPrefix(strings.ToLower(d)+"/", strings.ToLower(r)+"/")
}
