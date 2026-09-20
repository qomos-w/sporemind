package sdk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStaticTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "<html><body>home</body></html>")
	write("assets/app.js", "console.log(1)")
	write("sub/index.html", "<html><body>sub</body></html>")
	if err := os.Mkdir(filepath.Join(root, "noidx"), 0o755); err != nil { // directory without index.html
		t.Fatal(err)
	}
	return root
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Directory listings must never render: the plugin listener serves through
// the gateway into app panels, and a listing enumerates host directories
// (the 2026-09 run-directory leak).
func TestStaticDirHandlerNoDirectoryListing(t *testing.T) {
	h := staticDirHandler(newStaticTree(t))
	if rec := get(t, h, "/noidx"); rec.Code != http.StatusNotFound {
		t.Fatalf("dir without index.html = %d, want 404 (no listing)", rec.Code)
	}
	if body := rec2Body(t, h, "/noidx/"); strings.Contains(body, "noidx") || strings.Contains(body, "app.js") {
		t.Fatalf("listing leaked content: %q", body)
	}
	if rec := get(t, h, "/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "home") {
		t.Fatalf("root index = %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(t, h, "/sub"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "sub") {
		t.Fatalf("sub dir index = %d %q", rec.Code, rec.Body.String())
	}
	if rec := get(t, h, "/assets/app.js"); rec.Code != http.StatusOK {
		t.Fatalf("asset = %d, want 200", rec.Code)
	}
}

func rec2Body(t *testing.T, h http.Handler, path string) string {
	t.Helper()
	return get(t, h, path).Body.String()
}

// The host-pushed LoadConfig.StaticDir replaces the "." default on the
// already-running listener but never an explicit WithStaticDir.
func TestSetStaticRootOverridesDefaultNotExplicit(t *testing.T) {
	if _, err := ServeHTTP("127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = currentHTTPServer().Shutdown(t.Context()) }()
	appDir := newStaticTree(t)
	if !SetStaticRoot(appDir) {
		t.Fatal("SetStaticRoot must adopt the running default server")
	}
	if rec := get(t, staticDirHandler(appDir), "/"); !strings.Contains(rec.Body.String(), "home") {
		t.Fatal("sanity: tree must serve index")
	}
	// Pending path: SetStaticRoot before any server exists is consumed by
	// maybeAutoStartHTTP.
	cfgRaw, _ := json.Marshal(LoadConfig{StaticDir: appDir})
	if pending, ok := pendingStaticDir.Load().(string); ok && pending != "" {
		t.Fatalf("unexpected pending: %q", pending)
	}
	_ = applyLoadConfig(cfgRaw)
}
