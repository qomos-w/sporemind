package util

import "testing"

// TestPathWithin pins the at-or-under containment predicate: equality,
// descendant, sibling-prefix boundary, empty inputs, and the Windows
// case-insensitivity fold.
func TestPathWithin(t *testing.T) {
	tests := []struct {
		dir, root string
		want      bool
	}{
		{"/w/wt", "/w/wt", true},
		{"/w/wt/pkg", "/w/wt", true},
		{"/w/wt/pkg/deep", "/w/wt", true},
		{"/w/wt-other", "/w/wt", false},
		{"/w", "/w/wt", false},
		{"", "/w/wt", false},
		{"/w/wt", "", false},
		{"C:\\w\\wt\\pkg", "C:\\w\\wt", true},
		{"C:\\w\\wt-other", "C:\\w\\wt", false},
	}
	for _, tt := range tests {
		if got := PathWithin(tt.dir, tt.root); got != tt.want {
			t.Errorf("PathWithin(%q, %q) = %v, want %v", tt.dir, tt.root, got, tt.want)
		}
	}
	// Case fold: Windows paths with different casing still match.
	if !PathWithin("/W/WT/PKG", "/w/wt") {
		t.Error("PathWithin folded-case = false, want true (Windows paths are case-insensitive)")
	}
}
