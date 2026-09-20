package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/buildinfo"
)

// resetUpdater isolates tests from the package-level updater state.
func resetUpdater(t *testing.T) {
	t.Helper()
	updater.mu.Lock()
	updater.status = UpdateStatus{}
	updater.lastLogAt = time.Time{}
	updater.lastLogPhase = ""
	updater.mu.Unlock()
}

func TestNormalizePublicVersion(t *testing.T) {
	cases := []struct{ in, want string }{
		{"0.21", "v0.21"},
		{"v0.21", "v0.21"},
		{"1.2.3-beta.1", "v1.2.3-beta.1"},
		{" 0.1.0 ", "v0.1.0"},
		{"dev", ""},
		{"", ""},
		{"abc", ""},
	}
	for _, tc := range cases {
		if got := normalizePublicVersion(tc.in); got != tc.want {
			t.Errorf("normalizePublicVersion(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestUpdateAvailable(t *testing.T) {
	old := buildinfo.PublicVersion
	t.Cleanup(func() { buildinfo.PublicVersion = old })
	buildinfo.PublicVersion = "0.20"

	cases := []struct {
		remote *AppUpdateMeta
		want   bool
	}{
		{nil, false},
		{&AppUpdateMeta{PublicVersion: "0.21"}, true},
		{&AppUpdateMeta{PublicVersion: "0.20"}, false},
		{&AppUpdateMeta{PublicVersion: "0.19"}, false},
		{&AppUpdateMeta{PublicVersion: ""}, false},
		{&AppUpdateMeta{PublicVersion: "dev"}, false},
	}
	for i, tc := range cases {
		if got := updateAvailable(tc.remote); got != tc.want {
			t.Errorf("case %d: updateAvailable(%+v) = %v, want %v", i, tc.remote, got, tc.want)
		}
	}

	// Dev / missing local version never reports an update.
	buildinfo.PublicVersion = ""
	if updateAvailable(&AppUpdateMeta{PublicVersion: "0.99"}) {
		t.Error("expected no update when local public version is empty")
	}
}

func TestUpdateCheckAllowed(t *testing.T) {
	oldType := buildinfo.BuildType
	t.Cleanup(func() {
		buildinfo.BuildType = oldType
		os.Unsetenv("SPOREMIND_ENABLE_UPDATE")
	})

	buildinfo.BuildType = "release"
	if !updateCheckAllowed() {
		t.Error("release build should allow update checks")
	}

	buildinfo.BuildType = "beta"
	if updateCheckAllowed() {
		t.Error("beta build should not allow update checks (autoUpdate flag off)")
	}

	buildinfo.BuildType = "dev"
	if updateCheckAllowed() {
		t.Error("dev build without env opt-in should not allow update checks")
	}
	t.Setenv("SPOREMIND_ENABLE_UPDATE", "1")
	if updateCheckAllowed() {
		t.Error("dev build still gated off: autoUpdate flag is false for dev")
	}
}

func TestCheckUpdateFetch(t *testing.T) {
	resetUpdater(t)
	oldType := buildinfo.BuildType
	oldPub := buildinfo.PublicVersion
	t.Cleanup(func() {
		buildinfo.BuildType = oldType
		buildinfo.PublicVersion = oldPub
		os.Unsetenv("SPOREMIND_UPDATE_BASE_URL")
	})
	buildinfo.BuildType = "release"
	buildinfo.PublicVersion = "0.20"

	meta := AppUpdateMeta{
		PublicVersion: "0.21",
		Channel:       "stable",
		URL:           "http://example.invalid/sporemind-0.21-stable.exe",
		File:          "sporemind-0.21-stable.exe",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/releases/stable/latest.json") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"public_version":"%s","channel":"stable","url":"%s","file":"%s","size":123}`,
			meta.PublicVersion, meta.URL, meta.File)
	}))
	defer srv.Close()
	t.Setenv("SPOREMIND_UPDATE_BASE_URL", srv.URL)

	a := &App{}
	status, err := a.CheckUpdate()
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if status.Phase != "available" || !status.UpdateAvailable {
		t.Fatalf("expected available, got %+v", status)
	}
	if status.Remote == nil || status.Remote.PublicVersion != "0.21" {
		t.Fatalf("expected remote 0.21, got %+v", status.Remote)
	}
}

func TestCheckUpdateNoNewerVersion(t *testing.T) {
	resetUpdater(t)
	oldType := buildinfo.BuildType
	oldPub := buildinfo.PublicVersion
	t.Cleanup(func() {
		buildinfo.BuildType = oldType
		buildinfo.PublicVersion = oldPub
		os.Unsetenv("SPOREMIND_UPDATE_BASE_URL")
	})
	buildinfo.BuildType = "release"
	buildinfo.PublicVersion = "0.21"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"public_version":"0.21","channel":"stable","file":"sporemind-0.21-stable.exe"}`)
	}))
	defer srv.Close()
	t.Setenv("SPOREMIND_UPDATE_BASE_URL", srv.URL)

	a := &App{}
	status, err := a.CheckUpdate()
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if status.UpdateAvailable {
		t.Fatalf("expected no update, got %+v", status)
	}
	if status.Phase != "idle" {
		t.Fatalf("expected idle phase, got %q", status.Phase)
	}
}

func TestCheckUpdateHTTPError(t *testing.T) {
	resetUpdater(t)
	oldType := buildinfo.BuildType
	t.Cleanup(func() {
		buildinfo.BuildType = oldType
		os.Unsetenv("SPOREMIND_UPDATE_BASE_URL")
	})
	buildinfo.BuildType = "release"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("SPOREMIND_UPDATE_BASE_URL", srv.URL)

	a := &App{}
	status, err := a.CheckUpdate()
	if err != nil {
		t.Fatalf("CheckUpdate returned unexpected error: %v", err)
	}
	if status.Phase != "error" {
		t.Fatalf("expected error phase, got %q", status.Phase)
	}
	if status.Error == "" {
		t.Fatal("expected error message")
	}
}

func TestTruncateUpdateErr(t *testing.T) {
	long := strings.Repeat("x", 600)
	got := truncateUpdateErr(long)
	if len(got) != 512+len("...(truncated)") {
		t.Errorf("truncateUpdateErr length = %d", len(got))
	}
}

func TestInstallUpdateRequiresReady(t *testing.T) {
	resetUpdater(t)
	a := &App{}
	if err := a.InstallUpdate(); err == nil {
		t.Fatal("expected error when no update ready")
	}
}

func TestRevealUpdateRequiresDownload(t *testing.T) {
	resetUpdater(t)
	a := &App{}
	if err := a.RevealUpdateInFolder(); err == nil {
		t.Fatal("expected error when no download path")
	}
}

func TestDownloadUpdateBadSha(t *testing.T) {
	resetUpdater(t)
	oldType := buildinfo.BuildType
	t.Cleanup(func() { buildinfo.BuildType = oldType })
	buildinfo.BuildType = "release"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not the real installer"))
	}))
	defer srv.Close()

	updater.mu.Lock()
	updater.status = UpdateStatus{
		Phase:           "available",
		UpdateAvailable: true,
		Remote: &AppUpdateMeta{
			PublicVersion: "0.99",
			URL:           srv.URL + "/sporemind-0.99-stable.exe",
			File:          "sporemind-0.99-stable.exe",
			Sha256:        "0000000000000000000000000000000000000000000000000000000000000000",
		},
	}
	updater.mu.Unlock()

	a := &App{}
	status, err := a.DownloadUpdate()
	if err == nil {
		t.Fatal("expected sha mismatch error")
	}
	if status.Phase != "error" {
		t.Fatalf("expected error phase, got %+v", status)
	}
}

func TestDownloadUpdateSuccess(t *testing.T) {
	resetUpdater(t)
	oldType := buildinfo.BuildType
	t.Cleanup(func() { buildinfo.BuildType = oldType })
	buildinfo.BuildType = "release"

	payload := []byte("fake installer bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	// Redirect the Downloads lookup into a temp dir.
	tmp := t.TempDir()
	t.Setenv("USERPROFILE", tmp)
	t.Setenv("HOME", tmp)

	fileName := "sporemind-0.99-stable.exe"
	sum := sha256Hex(payload)
	updater.mu.Lock()
	updater.status = UpdateStatus{
		Phase:           "available",
		UpdateAvailable: true,
		Remote: &AppUpdateMeta{
			PublicVersion: "0.99",
			URL:           srv.URL + "/" + fileName,
			File:          fileName,
			Size:          int64(len(payload)),
			Sha256:        sum,
		},
	}
	updater.mu.Unlock()

	a := &App{}
	status, err := a.DownloadUpdate()
	if err != nil {
		t.Fatalf("DownloadUpdate: %v", err)
	}
	if status.Phase != "ready" {
		t.Fatalf("expected ready phase, got %+v", status)
	}
	if filepath.Base(status.Path) != fileName {
		t.Fatalf("downloaded file name = %q, want %q", status.Path, fileName)
	}
	got, err := os.ReadFile(status.Path)
	if err != nil {
		t.Fatalf("read downloaded file: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("downloaded bytes mismatch: got %q", got)
	}
}

func sha256Hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
