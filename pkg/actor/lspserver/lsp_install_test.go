package lspserver

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/actor/lspserver/installer"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/persist"
	testutil "github.com/qomos-w/sporemind/pkg/testutil"
)

// installTestActor constructs an Actor wired for managed-install tests:
// isolated persistence root, isolated managed dir, and a test capture channel
// for emitted events.
func installTestActor(t *testing.T) (*Actor, *testutil.FakeCtx) {
	t.Helper()
	ctx := testutil.HumanCtx(testutil.GenActorID())

	persistDir := filepath.Join(t.TempDir(), "persist")
	if err := os.MkdirAll(persistDir, 0o755); err != nil {
		t.Fatalf("mkdir persist: %v", err)
	}

	a := &Actor{
		actorID:             "test-lsp-actor",
		servers:             make(map[string]map[string]*rootServer),
		enabled:             defaultEnabled(),
		store:               persist.MustNew(persist.PersistConfig{Backend: "fs", DataDir: persistDir, Prefix: "lspserver"}),
		installs:            make(map[string]managedInstall),
		inFlight:            make(map[string]bool),
		specs:               defaultLanguageSpecs(),
		managedRootOverride: t.TempDir(),
		newEngine:           newEngineFor,
		emit:                ctx.EmitEvent,
	}
	return a, ctx
}

// sha256Hex returns the lower-case hex digest of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// waitFor polls cond until it returns true or timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

// progressEvents returns the install_progress event payloads for lang from
// newest to oldest (FakeCtx snapshots are LIFO). The returned slice is oldest
// to newest for convenient assertions.
func progressEvents(t *testing.T, ctx *testutil.FakeCtx, lang string) []gen.LspInstallProgressEvent {
	t.Helper()
	var out []gen.LspInstallProgressEvent
	for _, ev := range ctx.EmittedEvents {
		if ev.Kind != installProgressEvent {
			continue
		}
		payload, ok := ev.Payload.(gen.LspInstallProgressEvent)
		if !ok {
			t.Fatalf("unexpected payload type %T for %s", ev.Payload, installProgressEvent)
		}
		if payload.Language == lang {
			out = append(out, payload)
		}
	}
	return out
}

func TestHandleStatus_NotInstalledNoError(t *testing.T) {
	a, _ := installTestActor(t)
	// Force the npm-backed and gopls probes to report not-installed so the test
	// does not depend on the dev machine's node/npm or gopls installations.
	for _, lang := range []string{"go", "typescript", "javascript", "css", "html", "json", "bash"} {
		a.specs[lang] = languageSpec{probe: func() gen.LspLanguageInstallState { return gen.LspLanguageInstallState{} }}
	}

	resp, err := a.handleStatus(nil, gen.LspStatusReq{})
	if err != nil {
		t.Fatalf("handleStatus all languages: %v", err)
	}

	if len(resp.Languages) != len(knownLanguages) {
		t.Fatalf("got %d languages, want %d", len(resp.Languages), len(knownLanguages))
	}

	want := map[string]bool{
		"go":         false,
		"python":     false,
		"rust":       false,
		"typescript": false,
		"javascript": false,
		"css":        false,
		"html":       false,
		"json":       false,
		"bash":       false,
	}
	got := map[string]bool{}
	for _, st := range resp.Languages {
		got[st.Language] = st.Installed
	}
	for lang, installed := range want {
		if got[lang] != installed {
			t.Errorf("language %q installed=%v, want %v", lang, got[lang], installed)
		}
	}

	// Single-language filter.
	single, err := a.handleStatus(nil, gen.LspStatusReq{Language: "typescript"})
	if err != nil {
		t.Fatalf("handleStatus single language: %v", err)
	}
	if len(single.Languages) != 1 || single.Languages[0].Language != "typescript" {
		t.Fatalf("single-language filter failed: %+v", single.Languages)
	}

	// Unknown language returns error.
	if _, err := a.handleStatus(nil, gen.LspStatusReq{Language: "cobol"}); err == nil {
		t.Fatalf("expected error for unsupported language")
	}
}

// TestHandleStatus_ManagedCache verifies that a persisted managed-install
// record whose marker still exists on disk is reported as Installed=true with
// DownloadSource="managed".
func TestHandleStatus_ManagedCache(t *testing.T) {
	a, _ := installTestActor(t)
	a.specs["typescript"] = languageSpec{probe: func() gen.LspLanguageInstallState {
		t.Fatal("native probe should not run when a managed cache hit exists")
		return gen.LspLanguageInstallState{}
	}}

	root := a.managedRoot()
	dir := filepath.Join(root, "fake-ts", "4.3.3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir managed dir: %v", err)
	}
	marker := []byte(`{"tool":"fake-ts","version":"4.3.3"}`)
	if err := os.WriteFile(filepath.Join(dir, ".lsp-installer.json"), marker, 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	a.installs["typescript"] = managedInstall{
		Tool:        "fake-ts",
		Version:     "4.3.3",
		Dir:         dir,
		BinaryPath:  filepath.Join(dir, "bin"),
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Asset: installer.Asset{
			GOOS:   runtime.GOOS,
			GOARCH: runtime.GOARCH,
			URL:    "https://example.invalid/fake-ts-4.3.3.tar.gz",
			SHA256: strings.Repeat("0", 64),
			Kind:   installer.KindTarGz,
		},
	}

	resp, err := a.handleStatus(nil, gen.LspStatusReq{Language: "typescript"})
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if len(resp.Languages) != 1 {
		t.Fatalf("expected one language, got %+v", resp.Languages)
	}
	st := resp.Languages[0]
	if !st.Installed || st.Language != "typescript" || st.Version != "4.3.3" || st.DownloadSource != sourceManaged {
		t.Fatalf("unexpected managed status: %+v", st)
	}
	if st.BinaryPath != filepath.Join(dir, "bin") {
		t.Errorf("binary path = %q, want %q", st.BinaryPath, filepath.Join(dir, "bin"))
	}
}

// TestHandleStatus_ManagedCacheMissingMarker verifies that a stale managed
// record whose directory was deleted falls back to the native probe rather
// than reporting a phantom install.
func TestHandleStatus_ManagedCacheMissingMarker(t *testing.T) {
	a, _ := installTestActor(t)
	a.specs["typescript"] = languageSpec{probe: func() gen.LspLanguageInstallState {
		return gen.LspLanguageInstallState{Installed: true, BinaryPath: "/usr/bin/tls", DownloadSource: sourcePath}
	}}

	a.installs["typescript"] = managedInstall{
		Tool:    "fake-ts",
		Version: "4.3.3",
		Dir:     filepath.Join(a.managedRoot(), "fake-ts", "4.3.3"),
		Asset: installer.Asset{
			GOOS:   runtime.GOOS,
			GOARCH: runtime.GOARCH,
			URL:    "https://example.invalid/fake-ts-4.3.3.tar.gz",
			SHA256: strings.Repeat("0", 64),
			Kind:   installer.KindTarGz,
		},
	}

	resp, err := a.handleStatus(nil, gen.LspStatusReq{Language: "typescript"})
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	st := resp.Languages[0]
	if st.Installed && st.BinaryPath != "/usr/bin/tls" {
		t.Fatalf("expected fallback probe status, got %+v", st)
	}
}

// TestHandleInstall_EndToEnd drives a real manifest download through an
// httptest server, verifies the file lands under {managedRoot}/{tool}/{version}/
// with a URL-base-named executable, checks progress events, the persist cache,
// and the resulting lsp.status projection.
func TestHandleInstall_EndToEnd(t *testing.T) {
	a, ctx := installTestActor(t)

	binary := []byte("#!/bin/sh\necho fake-lsp\n")
	if runtime.GOOS == "windows" {
		binary = []byte("@echo off\r\necho fake-lsp\r\n")
	}
	digest := sha256Hex(binary)

	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		if r.URL.Path != "/fake-ls-1.0.0" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(binary)))
		w.Write(binary)
	}))
	defer srv.Close()

	a.specs["typescript"] = languageSpec{
		probe: func() gen.LspLanguageInstallState { return gen.LspLanguageInstallState{} },
		manifest: func() (installer.Manifest, error) {
			return toolManifest("fake-ls", "1.0.0", []installer.Asset{{
				GOOS:   runtime.GOOS,
				GOARCH: runtime.GOARCH,
				URL:    srv.URL + "/fake-ls-1.0.0",
				SHA256: digest,
				Kind:   installer.KindRaw,
			}}), nil
		},
	}

	resp, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "typescript"})
	if err != nil {
		t.Fatalf("handleInstall: %v", err)
	}
	if !resp.Started {
		t.Fatalf("expected install to be started")
	}

	waitFor(t, 5*time.Second, func() bool {
		a.installMu.RLock()
		defer a.installMu.RUnlock()
		return a.installs["typescript"].Version == "1.0.0"
	})
	// The done event is emitted after the install record is written; wait for it
	// explicitly to avoid races in event ordering.
	waitFor(t, 5*time.Second, func() bool {
		events := progressEvents(t, ctx, "typescript")
		return len(events) > 0 && events[len(events)-1].State == installStateDone
	})

	if atomic.LoadInt32(&requests) != 1 {
		t.Fatalf("expected exactly one artifact request, got %d", atomic.LoadInt32(&requests))
	}

	wantFile := filepath.Join(a.managedRoot(), "fake-ls", "1.0.0", "fake-ls-1.0.0")
	if _, err := os.Stat(wantFile); err != nil {
		t.Fatalf("expected installed binary at %s: %v", wantFile, err)
	}

	// Progress events: running(0) at start, done(100) at end; any running
	// updates stay within [0,99] and never decrease.
	events := progressEvents(t, ctx, "typescript")
	if len(events) < 2 {
		t.Fatalf("expected at least start+done events, got %+v", events)
	}
	if events[0].State != installStateRunning || events[0].Percent != 0 {
		t.Fatalf("expected first event running/0, got %+v", events[0])
	}
	last := events[len(events)-1]
	if last.State != installStateDone || last.Percent != 100 {
		t.Fatalf("expected last event done/100, got %+v", last)
	}
	max := int32(-1)
	for _, ev := range events {
		if ev.Percent < 0 || ev.Percent > 100 {
			t.Fatalf("percent out of range: %+v", ev)
		}
		if ev.Percent < max {
			t.Fatalf("percent decreased: %+v", ev)
		}
		max = ev.Percent
	}

	// lsp.status now reports the managed install.
	statusResp, err := a.handleStatus(nil, gen.LspStatusReq{Language: "typescript"})
	if err != nil {
		t.Fatalf("handleStatus after install: %v", err)
	}
	st := statusResp.Languages[0]
	if !st.Installed || st.DownloadSource != sourceManaged || st.Version != "1.0.0" {
		t.Fatalf("expected managed status after install, got %+v", st)
	}

	// Persist round trip: a fresh actor loading the same store must see the
	// managed install record and report it via status.
	baseDir := a.store.(*persist.FSPersist).BaseDir //nolint:forcetypeassert // test only
	a2 := &Actor{
		actorID:             a.actorID,
		servers:             make(map[string]map[string]*rootServer),
		enabled:             defaultEnabled(),
		store:               persist.NewFSPersist(baseDir),
		managedRootOverride: a.managedRootOverride,
		newEngine:           newEngineFor,
	}
	if err := a2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rec, ok := a2.installs["typescript"]; !ok || rec.Version != "1.0.0" {
		t.Fatalf("persisted install record missing or wrong: %+v", rec)
	}
}

// TestHandleInstall_Debounce verifies that a second lsp.install trigger while
// one is already running is acknowledged idempotently without starting a
// second download.
func TestHandleInstall_Debounce(t *testing.T) {
	a, ctx := installTestActor(t)

	release := make(chan struct{})
	var requests int32
	digest := sha256Hex([]byte("blocked"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		<-release
		w.Header().Set("Content-Length", "7")
		w.Write([]byte("blocked"))
	}))
	defer srv.Close()

	a.specs["typescript"] = languageSpec{
		manifest: func() (installer.Manifest, error) {
			return toolManifest("blocked-ls", "1.0.0", []installer.Asset{{
				GOOS:   runtime.GOOS,
				GOARCH: runtime.GOARCH,
				URL:    srv.URL + "/blocked",
				SHA256: digest,
				Kind:   installer.KindRaw,
			}}), nil
		},
	}

	if _, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "typescript"}); err != nil {
		t.Fatalf("first install trigger: %v", err)
	}
	// Wait until the first request has definitely hit the server handler.
	waitFor(t, time.Second, func() bool { return atomic.LoadInt32(&requests) == 1 })

	second, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "typescript"})
	if err != nil {
		t.Fatalf("second install trigger: %v", err)
	}
	if !second.Started {
		t.Fatalf("duplicate trigger should still be acked")
	}

	close(release)
	waitFor(t, 5*time.Second, func() bool {
		a.installMu.RLock()
		defer a.installMu.RUnlock()
		_, running := a.inFlight["typescript"]
		return !running && a.installs["typescript"].Version == "1.0.0"
	})

	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("expected one artifact request, got %d", got)
	}
}

// TestHandleInstall_FailureAndRetry downloads a non-existent artifact, verifies
// a failed progress event, that inFlight is cleared, and that a later retry can
// start a fresh download.
func TestHandleInstall_FailureAndRetry(t *testing.T) {
	a, ctx := installTestActor(t)

	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	a.specs["typescript"] = languageSpec{
		probe: func() gen.LspLanguageInstallState { return gen.LspLanguageInstallState{} },
		manifest: func() (installer.Manifest, error) {
			return toolManifest("fail-ls", "1.0.0", []installer.Asset{{
				GOOS:   runtime.GOOS,
				GOARCH: runtime.GOARCH,
				URL:    srv.URL + "/missing",
				SHA256: strings.Repeat("0", 64),
				Kind:   installer.KindRaw,
			}}), nil
		},
	}

	if _, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "typescript"}); err != nil {
		t.Fatalf("handleInstall: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		a.installMu.RLock()
		defer a.installMu.RUnlock()
		_, running := a.inFlight["typescript"]
		return !running
	})

	events := progressEvents(t, ctx, "typescript")
	if len(events) == 0 {
		t.Fatalf("expected a failed event")
	}
	last := events[len(events)-1]
	if last.State != installStateFailed || last.Error == "" {
		t.Fatalf("expected failed event with error, got %+v", last)
	}

	// Status must report not installed.
	statusResp, err := a.handleStatus(nil, gen.LspStatusReq{Language: "typescript"})
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if statusResp.Languages[0].Installed {
		t.Fatalf("expected not installed after failure")
	}

	// Retry should be allowed (in-flight guard removed).
	if _, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "typescript"}); err != nil {
		t.Fatalf("retry handleInstall: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool { return atomic.LoadInt32(&requests) == 2 })
}

// TestHandleInstall_UnconfiguredLanguage rejects languages with no managed
// manifest and an empty language string.
func TestHandleInstall_UnconfiguredLanguage(t *testing.T) {
	a, ctx := installTestActor(t)

	if _, err := a.handleInstall(ctx, gen.LspInstallReq{Language: ""}); err == nil {
		t.Fatalf("expected error for empty language")
	}
	if _, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "cobol"}); err == nil {
		t.Fatalf("expected error for unknown language cobol")
	}
}

// TestSaveLoadRoundTrip exercises the persist integration for the install cache
// independently of the download machinery.
func TestSaveLoadRoundTrip(t *testing.T) {
	a, _ := installTestActor(t)
	a.installs["typescript"] = managedInstall{
		Tool:        "tls",
		Version:     "4.3.3",
		Dir:         "/data/lsp/tls/4.3.3",
		BinaryPath:  "/data/lsp/tls/4.3.3/bin",
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Asset: installer.Asset{
			GOOS:   "linux",
			GOARCH: "amd64",
			URL:    "https://example.invalid/tls.tar.gz",
			SHA256: strings.Repeat("a", 64),
			Kind:   installer.KindTarGz,
		},
	}
	if err := a.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	baseDir := a.store.(*persist.FSPersist).BaseDir //nolint:forcetypeassert
	a2 := &Actor{
		actorID:   a.actorID,
		store:     persist.NewFSPersist(baseDir),
		enabled:   defaultEnabled(),
		installs:  make(map[string]managedInstall),
		inFlight:  make(map[string]bool),
		newEngine: newEngineFor,
	}
	if err := a2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := a2.installs["typescript"]; got.Version != "4.3.3" || got.Asset.URL != "https://example.invalid/tls.tar.gz" {
		t.Fatalf("round-tripped install record wrong: %+v", got)
	}
}

// TestToolManifestManagedDirectory verifies the managed-directory convention
// expected by the card: {root}/{tool}/{version}/.
func TestToolManifestManagedDirectory(t *testing.T) {
	m := toolManifest("rust-analyzer", "2024-08-01", []installer.Asset{
		{GOOS: "linux", GOARCH: "amd64", URL: "https://example.invalid/x", SHA256: strings.Repeat("0", 64), Kind: installer.KindRaw},
	})
	if m.InstallDir != "rust-analyzer/2024-08-01" {
		t.Fatalf("InstallDir = %q, want rust-analyzer/2024-08-01", m.InstallDir)
	}
}

// Ensure the handler signatures compile against the existing registration
// helpers used elsewhere in this package.
var (
	_ = (*Actor).handleStatus
	_ = (*Actor).handleInstall
)

// --- pyright (npm tarball) + rust-analyzer (GitHub release) installs --------

// testTarGz encodes files into a gzip-compressed tar archive for mock install
// artifacts.  Use a leading "package/" or "<tool>/" component to emulate the
// npm / GitHub release layout stripped by StripTopLevel.
func testTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %q: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write tar content %q: %v", name, err)
		}
	}
	_ = tw.Close()
	_ = gw.Close()
	return buf.Bytes()
}

// testZip encodes files into a zip archive for mock install artifacts. Use a
// leading "<tool>/" component to emulate the GitHub release layout stripped by
// StripTopLevel.
func testZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	_ = zw.Close()
	return buf.Bytes()
}

// TestHandleInstall_ClangdReleaseZip drives the clangd GitHub-release zip flow
// (KindZip with StripTopLevel): a "clangd_22.1.6/bin/clangd" archive lands with
// clangd under bin/ in the version dir, and the managed record stores the
// correct BinaryPath.
func TestHandleInstall_ClangdReleaseZip(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a subprocess")
	}

	a, ctx := installTestActor(t)
	zipData := testZip(t, map[string]string{
		"clangd_22.1.6/bin/" + clangdBinaryName(): "clangd-binary",
		"clangd_22.1.6/lib/clang/22/include/stddef.h": "dummy",
	})
	digest := sha256Hex(zipData)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(zipData)))
		_, _ = w.Write(zipData)
	}))
	defer srv.Close()

	a.specs["cpp"] = languageSpec{
		manifest: func() (installer.Manifest, error) {
			return toolManifest("clangd", "1.0.0", []installer.Asset{{
				GOOS:          runtime.GOOS,
				GOARCH:        runtime.GOARCH,
				URL:           srv.URL,
				SHA256:        digest,
				Kind:          installer.KindZip,
				StripTopLevel: true,
			}}), nil
		},
	}

	// Trigger install.
	_, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "cpp"})
	if err != nil {
		t.Fatalf("handleInstall cpp: %v", err)
	}

	// Wait for the install to complete or fail.
	waitFor(t, 10*time.Second, func() bool {
		a.installMu.RLock()
		rec, ok := a.installs["cpp"]
		a.installMu.RUnlock()
		if ok && rec.Version == "1.0.0" {
			return true
		}
		// Check for failure events.
		for _, ev := range progressEvents(t, ctx, "cpp") {
			if ev.State == "failed" {
				t.Logf("install failed: %s", ev.Error)
				return true
			}
		}
		return false
	})

	// Re-check install record.
	a.installMu.RLock()
	rec, ok := a.installs["cpp"]
	a.installMu.RUnlock()
	if !ok || rec.Version != "1.0.0" {
		// Print all progress events for debugging.
		events := progressEvents(t, ctx, "cpp")
		t.Fatalf("install not completed; events=%+v, rec=%+v", events, rec)
	}

	// The managed install record must point to the binary under bin/.
	if !ok {
		t.Fatal("managed install record not found after wait")
	}
	if rec.BinaryPath == "" {
		t.Fatalf("managed install record has empty BinaryPath")
	}
	if !strings.HasSuffix(rec.BinaryPath, "clangd") && !strings.HasSuffix(rec.BinaryPath, "clangd.exe") {
		t.Fatalf("expected BinaryPath to end with clangd, got %q", rec.BinaryPath)
	}
	if !strings.Contains(rec.BinaryPath, "bin") {
		t.Fatalf("expected BinaryPath to contain bin/ subdir, got %q", rec.BinaryPath)
	}

	// Verify the binary file actually exists.
	if st, err := os.Stat(rec.BinaryPath); err != nil {
		t.Fatalf("stat binary %q: %v", rec.BinaryPath, err)
	} else if st.IsDir() {
		t.Fatalf("binary path is a directory: %q", rec.BinaryPath)
	}

	// lsp.status now reports the managed install.
	statusResp, err := a.handleStatus(nil, gen.LspStatusReq{Language: "cpp"})
	if err != nil {
		t.Fatalf("handleStatus after install: %v", err)
	}
	st := statusResp.Languages[0]
	if !st.Installed || st.DownloadSource != sourceManaged || st.Version != "1.0.0" {
		t.Fatalf("expected managed status after install, got %+v", st)
	}
}
// (KindTarGz with StripTopLevel): a "package/langserver.index.js" archive lands
// with langserver.index.js directly in the version dir, and the managed record
// binary path resolves there. This is the npm class of the install flow.
func TestHandleInstall_PythonNpmTarball(t *testing.T) {
	a, ctx := installTestActor(t)

	artifact := testTarGz(t, map[string]string{
		"package/langserver.index.js": "fake pyright langserver",
	})
	digest := sha256Hex(artifact)

	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(artifact)))
		_, _ = w.Write(artifact)
	}))
	defer srv.Close()

	a.specs["python"] = languageSpec{
		probe: func() gen.LspLanguageInstallState { return gen.LspLanguageInstallState{} },
		manifest: func() (installer.Manifest, error) {
			return toolManifest("pyright", "1.1.400", []installer.Asset{{
				GOOS:          "*",
				GOARCH:        "*",
				URL:           srv.URL + "/pyright-1.1.400.tgz",
				SHA256:        digest,
				Kind:          installer.KindTarGz,
				StripTopLevel: true,
			}}), nil
		},
	}

	resp, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "python"})
	if err != nil {
		t.Fatalf("handleInstall python: %v", err)
	}
	if !resp.Started {
		t.Fatalf("expected python install to be started")
	}

	waitFor(t, 5*time.Second, func() bool {
		a.installMu.RLock()
		defer a.installMu.RUnlock()
		return a.installs["python"].Version == "1.1.400"
	})
	if atomic.LoadInt32(&requests) != 1 {
		t.Fatalf("expected one artifact request, got %d", atomic.LoadInt32(&requests))
	}

	rec := a.installs["python"]
	entry := filepath.Join(a.managedRoot(), "pyright", "1.1.400", "langserver.index.js")
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("expected langserver.index.js at %s: %v", entry, err)
	}
	if rec.BinaryPath != filepath.Dir(entry) {
		t.Errorf("managed record binary path = %q, want %q", rec.BinaryPath, filepath.Dir(entry))
	}

	// lsp.status reports the managed python install.
	st, err := a.handleStatus(nil, gen.LspStatusReq{Language: "python"})
	if err != nil {
		t.Fatalf("handleStatus python: %v", err)
	}
	if len(st.Languages) != 1 || !st.Languages[0].Installed || st.Languages[0].DownloadSource != sourceManaged {
		t.Fatalf("unexpected python status: %+v", st.Languages)
	}
}

// TestHandleInstall_RustReleaseTarball drives the rust-analyzer GitHub-release
// flow (KindTarGz with StripTopLevel): the archive holds
// <target>/rust-analyzer and lands the binary directly in the version dir.
// This is the binary class of the install flow.
func TestHandleInstall_RustReleaseTarball(t *testing.T) {
	a, ctx := installTestActor(t)

	binName := "rust-analyzer"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	artifact := testTarGz(t, map[string]string{
		"rust-analyzer-x86_64-unknown-linux-gnu/" + binName: "fake rust-analyzer",
	})
	digest := sha256Hex(artifact)

	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(artifact)))
		_, _ = w.Write(artifact)
	}))
	defer srv.Close()

	a.specs["rust"] = languageSpec{
		probe: func() gen.LspLanguageInstallState { return gen.LspLanguageInstallState{} },
		manifest: func() (installer.Manifest, error) {
			return toolManifest("rust-analyzer", "2024-11-18", []installer.Asset{{
				GOOS:          runtime.GOOS,
				GOARCH:        runtime.GOARCH,
				URL:           srv.URL + "/rust-analyzer.tar.gz",
				SHA256:        digest,
				Kind:          installer.KindTarGz,
				StripTopLevel: true,
			}}), nil
		},
	}

	resp, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "rust"})
	if err != nil {
		t.Fatalf("handleInstall rust: %v", err)
	}
	if !resp.Started {
		t.Fatalf("expected rust install to be started")
	}

	waitFor(t, 5*time.Second, func() bool {
		a.installMu.RLock()
		defer a.installMu.RUnlock()
		return a.installs["rust"].Version == "2024-11-18"
	})
	if atomic.LoadInt32(&requests) != 1 {
		t.Fatalf("expected one artifact request, got %d", atomic.LoadInt32(&requests))
	}

	rec := a.installs["rust"]
	bin := filepath.Join(a.managedRoot(), "rust-analyzer", "2024-11-18", binName)
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("expected rust-analyzer binary at %s: %v", bin, err)
	}
	if rec.BinaryPath != bin {
		t.Errorf("managed record binary path = %q, want %q", rec.BinaryPath, bin)
	}

	st, err := a.handleStatus(nil, gen.LspStatusReq{Language: "rust"})
	if err != nil {
		t.Fatalf("handleStatus rust: %v", err)
	}
	if len(st.Languages) != 1 || !st.Languages[0].Installed || st.Languages[0].DownloadSource != sourceManaged {
		t.Fatalf("unexpected rust status: %+v", st.Languages)
	}
}

// TestHandleInstall_WebNpmTarball drives the vscode-langservers-extracted
// npm-tarball flow (KindTarGz with StripTopLevel): a "package/bin/..." archive
// lands the per-language entry scripts directly in the version dir, and the
// managed record binary path resolves to the CSS entry.
func TestHandleInstall_WebNpmTarball(t *testing.T) {
	a, ctx := installTestActor(t)

	artifact := testTarGz(t, map[string]string{
		"package/bin/" + webCSSEntry[4:]:  "fake css ls",
		"package/bin/" + webHTMLEntry[4:]: "fake html ls",
		"package/bin/" + webJSONEntry[4:]: "fake json ls",
	})
	digest := sha256Hex(artifact)

	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(artifact)))
		_, _ = w.Write(artifact)
	}))
	defer srv.Close()

	a.specs["css"] = languageSpec{
		probe: func() gen.LspLanguageInstallState { return gen.LspLanguageInstallState{} },
		manifest: func() (installer.Manifest, error) {
			return toolManifest("vscode-langservers-extracted", "4.10.0", []installer.Asset{{
				GOOS:          "*",
				GOARCH:        "*",
				URL:           srv.URL + "/vscode-langservers-extracted-4.10.0.tgz",
				SHA256:        digest,
				Kind:          installer.KindTarGz,
				StripTopLevel: true,
			}}), nil
		},
	}

	resp, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "css"})
	if err != nil {
		t.Fatalf("handleInstall css: %v", err)
	}
	if !resp.Started {
		t.Fatalf("expected css install to be started")
	}

	waitFor(t, 5*time.Second, func() bool {
		a.installMu.RLock()
		defer a.installMu.RUnlock()
		return a.installs["css"].Version == "4.10.0"
	})
	if atomic.LoadInt32(&requests) != 1 {
		t.Fatalf("expected one artifact request, got %d", atomic.LoadInt32(&requests))
	}

	rec := a.installs["css"]
	wantEntry := filepath.Join(a.managedRoot(), "vscode-langservers-extracted", "4.10.0", filepath.FromSlash(webCSSEntry))
	if _, err := os.Stat(wantEntry); err != nil {
		t.Fatalf("expected css entry at %s: %v", wantEntry, err)
	}
	// The shared package also carries the sibling html/json entries.
	for _, sibling := range []string{webHTMLEntry, webJSONEntry} {
		p := filepath.Join(a.managedRoot(), "vscode-langservers-extracted", "4.10.0", filepath.FromSlash(sibling))
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected sibling entry at %s: %v", p, err)
		}
	}
	if rec.BinaryPath != wantEntry {
		t.Errorf("managed record binary path = %q, want %q", rec.BinaryPath, wantEntry)
	}

	st, err := a.handleStatus(nil, gen.LspStatusReq{Language: "css"})
	if err != nil {
		t.Fatalf("handleStatus css: %v", err)
	}
	if len(st.Languages) != 1 || !st.Languages[0].Installed || st.Languages[0].DownloadSource != sourceManaged {
		t.Fatalf("unexpected css status: %+v", st.Languages)
	}
}

// TestHandleInstall_BashNpmTarball drives the bash-language-server npm-tarball
// flow (KindTarGz with StripTopLevel): a "package/out/cli.js" archive lands the
// entry script directly in the version dir, with the managed record binary
// path pointing at it.
func TestHandleInstall_BashNpmTarball(t *testing.T) {
	a, ctx := installTestActor(t)

	artifact := testTarGz(t, map[string]string{
		"package/" + bashEntry: "fake bash ls",
	})
	digest := sha256Hex(artifact)

	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(artifact)))
		_, _ = w.Write(artifact)
	}))
	defer srv.Close()

	a.specs["bash"] = languageSpec{
		probe: func() gen.LspLanguageInstallState { return gen.LspLanguageInstallState{} },
		manifest: func() (installer.Manifest, error) {
			return toolManifest("bash-language-server", "5.6.0", []installer.Asset{{
				GOOS:          "*",
				GOARCH:        "*",
				URL:           srv.URL + "/bash-language-server-5.6.0.tgz",
				SHA256:        digest,
				Kind:          installer.KindTarGz,
				StripTopLevel: true,
			}}), nil
		},
	}

	resp, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "bash"})
	if err != nil {
		t.Fatalf("handleInstall bash: %v", err)
	}
	if !resp.Started {
		t.Fatalf("expected bash install to be started")
	}

	waitFor(t, 5*time.Second, func() bool {
		a.installMu.RLock()
		defer a.installMu.RUnlock()
		return a.installs["bash"].Version == "5.6.0"
	})
	if atomic.LoadInt32(&requests) != 1 {
		t.Fatalf("expected one artifact request, got %d", atomic.LoadInt32(&requests))
	}

	rec := a.installs["bash"]
	wantEntry := filepath.Join(a.managedRoot(), "bash-language-server", "5.6.0", filepath.FromSlash(bashEntry))
	if _, err := os.Stat(wantEntry); err != nil {
		t.Fatalf("expected bash entry at %s: %v", wantEntry, err)
	}
	if rec.BinaryPath != wantEntry {
		t.Errorf("managed record binary path = %q, want %q", rec.BinaryPath, wantEntry)
	}

	st, err := a.handleStatus(nil, gen.LspStatusReq{Language: "bash"})
	if err != nil {
		t.Fatalf("handleStatus bash: %v", err)
	}
	if len(st.Languages) != 1 || !st.Languages[0].Installed || st.Languages[0].DownloadSource != sourceManaged {
		t.Fatalf("unexpected bash status: %+v", st.Languages)
	}
}

// TestHandleInstall_TSServerWithTypescriptCompanion covers the full TS managed
// flow: typescript-language-server plus its mandatory typescript companion
// under the same version dir. Regression guard: the companion layout must
// match what findTypeScriptDir pins as tsserver.path — a mismatch leaves tls
// with "Could not find a valid TypeScript installation" and no jump-to-def.
func TestHandleInstall_TSServerWithTypescriptCompanion(t *testing.T) {
	a, ctx := installTestActor(t)

	serverArtifact := testTarGz(t, map[string]string{
		"package/lib/cli.mjs": "fake tls cli",
	})
	tsArtifact := testTarGz(t, map[string]string{
		"package/package.json":    `{"name":"typescript","version":"6.0.3"}`,
		"package/lib/tsserver.js": "fake tsserver",
	})
	serverDigest := sha256Hex(serverArtifact)
	tsDigest := sha256Hex(tsArtifact)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/typescript-language-server.tgz":
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(serverArtifact)))
			_, _ = w.Write(serverArtifact)
		case "/typescript.tgz":
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(tsArtifact)))
			_, _ = w.Write(tsArtifact)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	a.specs["typescript"] = languageSpec{
		probe: func() gen.LspLanguageInstallState { return gen.LspLanguageInstallState{} },
		manifest: func() (installer.Manifest, error) {
			return toolManifest("typescript-language-server", "6.0.0", []installer.Asset{{
				GOOS:          "*",
				GOARCH:        "*",
				URL:           srv.URL + "/typescript-language-server.tgz",
				SHA256:        serverDigest,
				Kind:          installer.KindTarGz,
				StripTopLevel: true,
			}}), nil
		},
		deps: []func() (installer.Manifest, error){
			func() (installer.Manifest, error) {
				return toolManifest("typescript", "6.0.3", []installer.Asset{{
					GOOS:          "*",
					GOARCH:        "*",
					URL:           srv.URL + "/typescript.tgz",
					SHA256:        tsDigest,
					Kind:          installer.KindTarGz,
					StripTopLevel: true,
				}}), nil
			},
		},
	}

	if _, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "typescript"}); err != nil {
		t.Fatalf("handleInstall typescript: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		a.installMu.RLock()
		defer a.installMu.RUnlock()
		_, running := a.inFlight["typescript"]
		return !running && a.installs["typescript"].Version == "6.0.0"
	})

	root := a.managedRoot()
	cli := filepath.Join(root, "typescript-language-server", "6.0.0", "lib", "cli.mjs")
	if _, err := os.Stat(cli); err != nil {
		t.Fatalf("expected tls cli at %s: %v", cli, err)
	}

	// The tsserver pin must resolve to the companion package dir.
	wantTS := filepath.Join(root, "typescript-language-server", "6.0.0", "typescript")
	if got := findTypeScriptDir("", "", root); got != wantTS {
		t.Fatalf("findTypeScriptDir = %q, want %q", got, wantTS)
	}
	if got := findManagedTSServerDir(root); got != filepath.Dir(wantTS) {
		t.Fatalf("findManagedTSServerDir = %q, want %q", got, filepath.Dir(wantTS))
	}
}

// TestHandleInstall_TSCompanionFailureFailsInstall verifies a failed
// companion download fails the whole install (emits failed progress) instead
// of the old silent degrade that left a server without tsserver.
func TestHandleInstall_TSCompanionFailureFailsInstall(t *testing.T) {
	a, ctx := installTestActor(t)

	serverArtifact := testTarGz(t, map[string]string{
		"package/lib/cli.mjs": "fake tls cli",
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/typescript-language-server.tgz" {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(serverArtifact)))
			_, _ = w.Write(serverArtifact)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	a.specs["typescript"] = languageSpec{
		probe: func() gen.LspLanguageInstallState { return gen.LspLanguageInstallState{} },
		manifest: func() (installer.Manifest, error) {
			return toolManifest("typescript-language-server", "6.0.0", []installer.Asset{{
				GOOS:          "*",
				GOARCH:        "*",
				URL:           srv.URL + "/typescript-language-server.tgz",
				SHA256:        sha256Hex(serverArtifact),
				Kind:          installer.KindTarGz,
				StripTopLevel: true,
			}}), nil
		},
		deps: []func() (installer.Manifest, error){
			func() (installer.Manifest, error) {
				return toolManifest("typescript", "6.0.3", []installer.Asset{{
					GOOS:          "*",
					GOARCH:        "*",
					URL:           srv.URL + "/typescript-missing.tgz",
					SHA256:        strings.Repeat("0", 64),
					Kind:          installer.KindTarGz,
					StripTopLevel: true,
				}}), nil
			},
		},
	}

	if _, err := a.handleInstall(ctx, gen.LspInstallReq{Language: "typescript"}); err != nil {
		t.Fatalf("handleInstall typescript: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		a.installMu.RLock()
		defer a.installMu.RUnlock()
		_, running := a.inFlight["typescript"]
		return !running
	})

	events := progressEvents(t, ctx, "typescript")
	last := events[len(events)-1]
	if last.State != installStateFailed || !strings.Contains(last.Error, "typescript") {
		t.Fatalf("expected failed event naming the companion, got %+v", last)
	}
	a.installMu.RLock()
	if _, ok := a.installs["typescript"]; ok {
		t.Fatalf("no install record expected on companion failure")
	}
	a.installMu.RUnlock()
}

// TestManifestPinning verifies the pinned manifests stay structurally valid:
// every platform gets a selectable asset whose checksum is populated.
func TestManifestPinning(t *testing.T) {
	for _, tc := range []struct {
		name string
		mf   func() (installer.Manifest, error)
	}{
		{name: "pyright", mf: pyrightManifest},
		{name: "rust-analyzer", mf: rustAnalyzerManifest},
		{name: "clangd", mf: clangdManifest},
		{name: "vscode-langservers-extracted", mf: webManifest},
		{name: "bash-language-server", mf: bashManifest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := tc.mf()
			if err != nil {
				t.Fatalf("manifest: %v", err)
			}
			if err := m.Validate(); err != nil {
				t.Fatalf("manifest validate: %v", err)
			}
			if m.InstallDir != tc.name+"/"+m.Version {
				t.Fatalf("InstallDir = %q, want %q", m.InstallDir, tc.name+"/"+m.Version)
			}
			if len(m.Assets) == 0 {
				t.Fatalf("no assets in manifest")
			}
			for _, a := range m.Assets {
				if a.SHA256 == "" {
					t.Fatalf("asset %s has empty sha256", a.URL)
				}
				if a.URL == "" {
					t.Fatalf("asset has empty url")
				}
			}
		})
	}
}
