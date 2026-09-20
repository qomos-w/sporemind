package desktop

import (
	"net"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/logging"
)

func TestIpv4LANPriority(t *testing.T) {
	cases := []struct {
		ip   string
		want int
	}{
		{"192.168.1.10", 30},
		{"10.0.0.5", 20},
		{"172.20.0.1", 10},
		{"169.254.1.1", 1},
		{"8.8.8.8", 1},
		{"127.0.0.1", 1},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			got := ipv4LANPriority(net.ParseIP(tc.ip))
			if got != tc.want {
				t.Fatalf("ipv4LANPriority(%s) = %d, want %d", tc.ip, got, tc.want)
			}
		})
	}
}

func TestWriteConsoleLog(t *testing.T) {
	store, err := logging.NewConsoleStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	app := &App{consoleStore: store}
	app.WriteConsoleLog("warn", "frontend warning", "App.tsx:42", 1752200000000)

	entries, err := store.Tail(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one persisted console entry, got %d", len(entries))
	}
	got := entries[0]
	if got.Level != "warn" || got.Message != "frontend warning" || got.Location != "App.tsx:42" || got.Time != 1752200000000 {
		t.Errorf("unexpected console entry: %+v", got)
	}
}

func TestWaitGatewayChan(t *testing.T) {
	closed := make(chan struct{})
	close(closed)

	if !waitGatewayChan(closed, 10*time.Millisecond) {
		t.Fatal("closed readiness channel must report ready immediately")
	}
	if !waitGatewayChan(nil, 10*time.Millisecond) {
		t.Fatal("nil readiness channel (no gateway configured) must not wait")
	}
	if waitGatewayChan(make(chan struct{}), 10*time.Millisecond) {
		t.Fatal("never-closing readiness channel must time out as not-ready")
	}

	// Unbound App (no runtime handle) reports not-ready instead of panicking.
	if (&App{}).WaitGatewayReady(1) {
		t.Fatal("App without handle must report not-ready")
	}
}

func TestSameDocumentReloadDecision(t *testing.T) {
	// Navigating to the same document must trigger Reload (the tokenized same-URL
	// path), while a different document uses SetURL. The decision is driven by
	// sameDocument, which normalises so redirect-finalised URLs are not seen as
	// different.
	cases := []struct {
		name       string
		currentURL string
		targetURL  string
		wantSame   bool
	}{
		{"identical", "https://example.com", "https://example.com", true},
		{"trailing slash", "https://example.com/a", "https://example.com/a/", true},
		{"fragment only", "https://example.com/a#x", "https://example.com/a", true},
		{"default port", "https://example.com:443/a", "https://example.com/a", true},
		{"different host", "https://example.com", "https://other.com", false},
		{"different path", "https://example.com/a", "https://example.com/b", false},
		{"empty current loads fresh", "", "https://example.com", false},
		{"file drive-letter host canonicalized", "file:///D:/web/page.html", "file://D:/web/page.html", true},
		{"file identical", "file:///D:/web/page.html", "file:///D:/web/page.html", true},
		{"file different path", "file:///D:/web/a.html", "file:///D:/web/b.html", false},
		{"file localhost host not a drive", "file://localhost/D:/x.html", "file:///D:/x.html", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.currentURL != "" && sameDocument(tc.targetURL, tc.currentURL)
			if got != tc.wantSame {
				t.Fatalf("sameDocument(%q,%q) same=%v, want %v", tc.targetURL, tc.currentURL, got, tc.wantSame)
			}
		})
	}
}

func TestBrowserProfilePath(t *testing.T) {
	global := browserProfilePath("global", "session-1")
	if !strings.Contains(global, "browser-profiles") || !strings.Contains(global, "global") {
		t.Errorf("global profile should be shared, got %q", global)
	}
	if strings.Contains(global, "session-1") {
		t.Errorf("global profile should not contain session id, got %q", global)
	}

	app := browserProfilePath("app", "session-2")
	if !strings.Contains(app, "app-session-2") {
		t.Errorf("app profile should be session-scoped, got %q", app)
	}

	indep := browserProfilePath("independent", "session-3")
	expected := filepath.FromSlash("browser-profiles/session-3")
	if !strings.Contains(indep, expected) {
		t.Errorf("independent profile should use the instance id directly, got %q", indep)
	}
}

func TestAppendSharedCacheArg(t *testing.T) {
	base := len(browserBaseArgs)
	args := appendSharedCacheArg(slices.Clone(browserBaseArgs))
	if len(args) != base+1 {
		t.Fatalf("appendSharedCacheArg should append exactly one arg, got %d", len(args))
	}
	arg := args[len(args)-1]
	if !strings.HasPrefix(arg, `--disk-cache-dir="`) || !strings.HasSuffix(arg, `"`) {
		t.Errorf("cache dir value must be quoted (args are space-joined), got %q", arg)
	}
	dir := strings.TrimSuffix(strings.TrimPrefix(arg, `--disk-cache-dir="`), `"`)
	if want := filepath.Join("browser-profiles", "global", "shared-cache"); !strings.Contains(dir, filepath.FromSlash(want)) {
		t.Errorf("shared cache dir should live under the global profile, got %q", dir)
	}
}
