package storeclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newIndexServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/store/index" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const indexFixture = `{
  "generated_at": "2026-09-18T00:00:00Z",
  "store": [{
    "id": "uuid-1", "slug": "daily-tools", "display_name": "Daily Tools",
    "description": "", "publisher": "qomos", "channel": "official",
    "publisher_pubkey": "", "current_version": "0.2.0",
    "versions": [
      {"version": "0.2.0", "sdk_version": "0.3.1", "protocol_version": 2,
       "min_host_version": "0.0.1", "payload_sha256": "aa", "ciphertext_sha256": "bb",
       "signature": "cc", "size": 1234, "changelog": "", "created_at": "2026-09-17T10:00:00Z"},
      {"version": "0.1.0", "sdk_version": "0.3.1", "protocol_version": 2,
       "min_host_version": "", "payload_sha256": "dd", "ciphertext_sha256": "ee",
       "signature": "ff", "size": 10, "changelog": "", "created_at": "2026-09-16T10:00:00Z"}
    ]
  }],
  "community": [{
    "id": "uuid-2", "slug": "owner-repo", "display_name": "Owner Repo", "description": "",
    "repo_url": "https://github.com/owner/repo", "commit_sha": "0123456789abcdef0123456789abcdef01234567",
    "version": "1.0.0", "sdk_version": "0.3.1", "protocol_version": 2,
    "tarball_sha256": "ab", "mirror_url": "/store/community/owner-repo/tarball",
    "manifest": {"id": "app.repo", "name": "Repo", "permissions": ["fs.read"]}
  }]
}`

func TestFetchIndexParsesAndAnnotates(t *testing.T) {
	srv := newIndexServer(t, indexFixture)
	a := &Actor{http: srv.Client()}
	a.state.BaseURL = srv.URL
	a.state.Channel = "dev"

	wire, _, fromCache, err := a.fetchIndex(context.Background(), false)
	if err != nil {
		t.Fatalf("fetchIndex: %v", err)
	}
	if fromCache {
		t.Fatal("first fetch must not be from cache")
	}
	if wire.GeneratedAt != "2026-09-18T00:00:00Z" {
		t.Fatalf("generated_at = %q", wire.GeneratedAt)
	}

	views := indexViews(wire)
	if len(views) != 1 || views[0].Slug != "daily-tools" {
		t.Fatalf("store views = %+v", views)
	}
	if len(views[0].Versions) != 2 {
		t.Fatalf("versions = %+v", views[0].Versions)
	}
	v020 := views[0].Versions[0]
	if !v020.Compatible {
		t.Fatalf("0.2.0 must be compatible, got %q", v020.IncompatibleReason)
	}
	// Mutate the second version to an incompatible SDK and re-annotate to
	// exercise the incompatible branch deterministically.
	wire.Store[0].Versions[1].SdkVersion = "9.9.9"
	badViews := indexViews(wire)
	v010 := badViews[0].Versions[1]
	if v010.Compatible || !strings.Contains(v010.IncompatibleReason, "SDK") {
		t.Fatalf("sdk 9.9.9 must be incompatible, got %+v", v010)
	}

	communities := communityViews(wire)
	if len(communities) != 1 || communities[0].Slug != "owner-repo" {
		t.Fatalf("community views = %+v", communities)
	}
	if !communities[0].Compatible {
		t.Fatalf("community entry must be compatible, got %q", communities[0].IncompatibleReason)
	}
	if communities[0].ManifestID != "app.repo" || communities[0].CommitSha != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("community mapping = %+v", communities[0])
	}

	// Second fetch within TTL hits the cache.
	if _, _, cached, err := a.fetchIndex(context.Background(), false); err != nil || !cached {
		t.Fatalf("second fetch must hit cache (cached=%v err=%v)", cached, err)
	}
}

func TestFetchIndexServesStaleOnError(t *testing.T) {
	srv := newIndexServer(t, indexFixture)
	a := &Actor{http: srv.Client()}
	a.state.BaseURL = srv.URL
	if _, _, _, err := a.fetchIndex(context.Background(), false); err != nil {
		t.Fatalf("initial fetch: %v", err)
	}
	srv.Close() // cloud goes away
	wire, _, cached, err := a.fetchIndex(context.Background(), true)
	if err != nil {
		t.Fatalf("forced fetch with dead cloud must serve stale cache, got %v", err)
	}
	if !cached || len(wire.Store) != 1 {
		t.Fatalf("stale serve failed: cached=%v wire=%+v", cached, wire)
	}
}

func TestPickStoreVersion(t *testing.T) {
	var wire storeIndexWire
	if err := json.Unmarshal([]byte(indexFixture), &wire); err != nil {
		t.Fatal(err)
	}
	if _, v, err := pickStoreVersion(wire, "daily-tools", "0.2.0"); err != nil || v.Version != "0.2.0" {
		t.Fatalf("exact pick: %v %+v", err, v)
	}
	// Default (no version) picks the latest COMPATIBLE version, skipping the
	// newer-incompatible 0.1.0? No — 0.2.0 is newer; use a fixture where the
	// newest is incompatible.
	wire.Store[0].Versions[0].SdkVersion = "9.9.9"
	if _, v, err := pickStoreVersion(wire, "daily-tools", ""); err != nil || v.Version != "0.1.0" {
		t.Fatalf("default pick must skip incompatible newest: %v %+v", err, v)
	}
	if _, _, err := pickStoreVersion(wire, "daily-tools", "9.9.9"); err == nil {
		t.Fatal("unknown version must error")
	}
	if _, _, err := pickStoreVersion(wire, "missing", ""); err == nil {
		t.Fatal("unknown slug must error")
	}
	// Asking explicitly for an incompatible version errors with the reason.
	if _, _, err := pickStoreVersion(wire, "daily-tools", "0.2.0"); err == nil {
		t.Fatal("explicit incompatible version must error")
	}
}

func TestJoinURL(t *testing.T) {
	cases := map[[2]string]string{
		{"http://localhost:8080", "/store/index"}:              "http://localhost:8080/store/index",
		{"http://localhost:8080/", "/store/community/x/tarball"}: "http://localhost:8080/store/community/x/tarball",
		{"https://api.example.com", "https://mirror.example/x"}:  "https://mirror.example/x",
	}
	for in, want := range cases {
		if got := joinURL(in[0], in[1]); got != want {
			t.Errorf("joinURL(%q,%q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

func TestCodeloadURL(t *testing.T) {
	got, err := codeloadURL("https://github.com/owner/repo", "0123456789abcdef0123456789abcdef01234567")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://codeload.github.com/owner/repo/tar.gz/0123456789abcdef0123456789abcdef01234567"
	if got != want {
		t.Fatalf("codeloadURL = %q, want %q", got, want)
	}
	if _, err := codeloadURL("https://gitlab.com/owner/repo", "0123456789abcdef0123456789abcdef01234567"); err == nil {
		t.Fatal("non-github repo_url must be rejected")
	}
}

func TestIndexCacheTTLReasonable(t *testing.T) {
	if indexCacheTTL < 30*time.Second || indexCacheTTL > 5*time.Minute {
		t.Fatalf("indexCacheTTL = %v, want within 30s..5m of the cloud max-age", indexCacheTTL)
	}
}
