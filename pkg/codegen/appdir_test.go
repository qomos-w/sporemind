package codegen

import (
	"strings"
	"testing"
)

func TestNormalizeAppDir(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr string
	}{
		// Accepted forms.
		{in: "", want: ""},
		{in: ".", want: ""},
		{in: "  .  ", want: ""},
		{in: "subdir", want: "subdir"},
		{in: "plugin-dev-example", want: "plugin-dev-example"},
		{in: "a/b", want: "a/b"},
		{in: "a/b/c", want: "a/b/c"},

		// Rejected: traversal.
		{in: "..", wantErr: "must not contain"},
		{in: "../x", wantErr: "must not contain"},
		{in: "a/../b", wantErr: "must not contain"},
		{in: "a/..", wantErr: "must not contain"},

		// Rejected: absolute paths.
		{in: "/abs", wantErr: "relative"},
		{in: "/a/b", wantErr: "relative"},
		{in: "C:apps", wantErr: "':'"},
		{in: "C:\\apps", wantErr: "backslashes"},

		// Rejected: separators and non-canonical forms.
		{in: "a\\b", wantErr: "backslashes"},
		{in: "a//b", wantErr: "empty path component"},
		{in: "a/", wantErr: "empty path component"},
		{in: "./a", wantErr: "must not contain"},
		{in: "a/./b", wantErr: "must not contain"},
		{in: " subdir ", want: "subdir"}, // surrounding whitespace is trimmed

		// Rejected: NUL.
		{in: "a\x00b", wantErr: "NUL"},
	}
	for _, tt := range tests {
		got, err := NormalizeAppDir(tt.in)
		if tt.wantErr != "" {
			if err == nil {
				t.Errorf("NormalizeAppDir(%q) = %q, nil; want error containing %q", tt.in, got, tt.wantErr)
				continue
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NormalizeAppDir(%q) error = %v; want it to contain %q", tt.in, err, tt.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeAppDir(%q) unexpected error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("NormalizeAppDir(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestJoinAppDir(t *testing.T) {
	cases := []struct {
		root, appDir, want string
	}{
		{root: "/proj", appDir: "", want: "/proj"},
		{root: "/proj/", appDir: "", want: "/proj/"},
		{root: "/proj/", appDir: ".", want: "/proj/"}, // empty dir returns root unchanged; handlers pre-trim
		{root: "/proj", appDir: "sub", want: "/proj/sub"},
		{root: "/proj/", appDir: "a/b", want: "/proj/a/b"},
	}
	for _, c := range cases {
		got, err := JoinAppDir(c.root, c.appDir)
		if err != nil {
			t.Fatalf("JoinAppDir(%q, %q): %v", c.root, c.appDir, err)
		}
		if got != c.want {
			t.Errorf("JoinAppDir(%q, %q) = %q; want %q", c.root, c.appDir, got, c.want)
		}
	}
	if _, err := JoinAppDir("/proj", "../evil"); err == nil {
		t.Error("JoinAppDir must reject traversal")
	}
}

func TestRelToRoot(t *testing.T) {
	if got, err := RelToRoot("", "main.gen.go"); err != nil || got != "main.gen.go" {
		t.Errorf("RelToRoot(\"\", ...) = %q, %v; want main.gen.go", got, err)
	}
	if got, err := RelToRoot("sub", "main.gen.go"); err != nil || got != "sub/main.gen.go" {
		t.Errorf("RelToRoot(\"sub\", ...) = %q, %v; want sub/main.gen.go", got, err)
	}
	if _, err := RelToRoot("..", "x"); err == nil {
		t.Error("RelToRoot must reject traversal")
	}
}
