package lspserver

import (
	"path/filepath"
	"runtime"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

// TestFileUriToPath pins URI→path decoding for the shutdown-by-root prefix
// match: plain and percent-encoded drive roots, Unix roots, and non-file URIs.
func TestFileUriToPath(t *testing.T) {
	tests := []struct {
		uri  string
		want string
		ok   bool
	}{
		{"file:///wt/proj", "/wt/proj", true},
		{"file:///wt/proj/deep", "/wt/proj/deep", true},
		{"file:///c%3A/wt", "/c:/wt", true}, // percent-encoded colon
		{"http://x/y", "", false},
		{"not-a-uri", "", false},
	}
	for _, tt := range tests {
		got, ok := fileUriToPath(tt.uri)
		if ok != tt.ok {
			t.Errorf("fileUriToPath(%q) ok = %v, want %v", tt.uri, ok, tt.ok)
			continue
		}
		if !ok {
			continue
		}
		want := tt.want
		if runtime.GOOS == "windows" {
			// Windows: drive-shaped paths ("/c:/x") strip the leading slash,
			// and separators come back native.
			if len(want) > 2 && want[0] == '/' && want[2] == ':' {
				want = want[1:]
			}
			want = filepath.FromSlash(want)
		}
		if got != want {
			t.Errorf("fileUriToPath(%q) = %q, want %q", tt.uri, got, want)
		}
	}
}

// TestHandleShutdownByRoot shuts down every engine rooted under the given
// path and leaves engines elsewhere. No match is not an error.
func TestHandleShutdownByRoot(t *testing.T) {
	srvA := &fakeServer{}
	srvADeep := &fakeServer{}
	srvB := &fakeServer{}
	a := &Actor{
		servers: map[string]map[string]*rootServer{
			"file:///wt/proj":      {"go": {server: srvA}},
			"file:///wt/proj/deep": {"typescript": {server: srvADeep}},
			"file:///elsewhere":    {"go": {server: srvB}},
		},
		enabled: defaultEnabled(),
	}
	ctx := testutil.HumanCtx(testutil.GenActorID())

	resp, err := a.handleShutdownByRoot(ctx, gen.LspShutdownByRootReq{RootPath: "/wt/proj"})
	if err != nil {
		t.Fatalf("handleShutdownByRoot: %v", err)
	}
	if len(resp.Shutdown) != 2 {
		t.Fatalf("Shutdown = %v, want 2 roots", resp.Shutdown)
	}
	if _, ok := a.servers["file:///wt/proj"]; ok {
		t.Error("root under the worktree path should be removed")
	}
	if _, ok := a.servers["file:///wt/proj/deep"]; ok {
		t.Error("deep root under the worktree path should be removed")
	}
	if _, ok := a.servers["file:///elsewhere"]; !ok {
		t.Error("unrelated root must survive")
	}

	// No match is best-effort: not an error.
	resp, err = a.handleShutdownByRoot(ctx, gen.LspShutdownByRootReq{RootPath: "/no/such/path"})
	if err != nil {
		t.Fatalf("handleShutdownByRoot(no match): %v", err)
	}
	if len(resp.Shutdown) != 0 {
		t.Fatalf("Shutdown = %v, want none", resp.Shutdown)
	}

	if _, err := a.handleShutdownByRoot(ctx, gen.LspShutdownByRootReq{RootPath: ""}); err == nil {
		t.Fatal("empty rootPath must be rejected")
	}
}
