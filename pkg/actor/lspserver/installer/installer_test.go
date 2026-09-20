package installer

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testEntry is one file or directory entry in an in-memory test archive.
type testEntry struct {
	Name    string
	Content []byte
	Mode    int64 // permission bits (0 → 0644 for files, 0755 for dirs)
	IsDir   bool
}

// mustDigest returns the lowercase hex SHA256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// mustDigest is sha256Hex with t.Helper() for test messages.
func mustDigest(t *testing.T, data []byte) string {
	t.Helper()
	return sha256Hex(data)
}

// buildTarGz encodes entries into a gzip-compressed tar archive.
func buildTarGz(t *testing.T, entries []testEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		mode := e.Mode
		if mode == 0 && !e.IsDir {
			mode = 0o644
		}
		if e.IsDir && mode == 0 {
			mode = 0o755
		}
		hdr := &tar.Header{
			Name:     e.Name,
			Mode:     mode,
			Size:     int64(len(e.Content)),
			Typeflag: tar.TypeReg,
		}
		if e.IsDir {
			hdr.Typeflag = tar.TypeDir
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header %q: %v", e.Name, err)
		}
		if !e.IsDir {
			if _, err := tw.Write(e.Content); err != nil {
				t.Fatalf("write tar content %q: %v", e.Name, err)
			}
		}
	}
	_ = tw.Close()
	_ = gw.Close()
	return buf.Bytes()
}

// buildZip encodes entries into a zip archive.
func buildZip(t *testing.T, entries []testEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		mode := e.Mode
		if mode == 0 && !e.IsDir {
			mode = 0o644
		}
		if e.IsDir && mode == 0 {
			mode = 0o755
		}
		name := strings.TrimSuffix(e.Name, "/")
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		hdr.SetMode(os.FileMode(mode))
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("create zip entry %q: %v", e.Name, err)
		}
		if !e.IsDir {
			if _, err := w.Write(e.Content); err != nil {
				t.Fatalf("write zip content %q: %v", e.Name, err)
			}
		}
	}
	_ = zw.Close()
	return buf.Bytes()
}

// buildGzip gzips a single payload into a gzip-compressed byte stream (the
// rust-analyzer release layout: a raw compressed binary, not a tar).
func buildGzip(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(payload); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// serveBytes serves data on every request and counts hits.
func serveBytes(t *testing.T, data []byte) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// serveCancelling serves an initial chunk, then blocks until the request context
// is cancelled. Used to test mid-download cancellation.
func serveCancelling(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		flusher, ok := w.(http.Flusher)
		if !ok {
			panic("no flusher")
		}
		// Write a chunk so the client sees a first progress callback.
		chunk := body[:min(len(body), 64<<10)]
		_, _ = w.Write(chunk)
		flusher.Flush()
		// Block until the client disconnects.
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newManifest builds a single-platform manifest with a single asset.
func newManifest(tool, version, installDir string, asset Asset) Manifest {
	return Manifest{Tool: tool, Version: version, InstallDir: installDir, Assets: []Asset{asset}}
}

// newAsset builds an Asset pointing at the given server URL, computing the
// SHA256 of data automatically.
func newAsset(srvURL string, kind Kind, strip bool, data []byte) Asset {
	return Asset{
		GOOS:          runtime.GOOS,
		GOARCH:        runtime.GOARCH,
		URL:           srvURL,
		SHA256:        sha256Hex(data),
		Kind:          kind,
		StripTopLevel: strip,
	}
}

// walkRoot returns the set of regular files under root (recursive).
func walkRoot(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	_ = filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			rel, _ := filepath.Rel(root, path)
			out[filepath.ToSlash(rel)] = fi.Size()
		}
		return nil
	})
	return out
}

// readMarker reads the install record from dir.
func readMarker(t *testing.T, dir string) *installRecord {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, markerName))
	if err != nil {
		return nil
	}
	var rec installRecord
	_ = json.Unmarshal(raw, &rec)
	return &rec
}

// hasFiles reports whether dir contains any non-hidden regular files.
func hasFiles(t *testing.T, dir string) bool {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestEnsureTarGzWrappedRoot(t *testing.T) {
	// Build a tar.gz with a wrapper directory ("tool-1.0/") and strip it.
	payload := []byte("#!/bin/sh\necho rust-analyzer")
	archive := buildTarGz(t, []testEntry{
		{Name: "rust-analyzer-1.0/", IsDir: true},
		{Name: "rust-analyzer-1.0/bin/", IsDir: true},
		{Name: "rust-analyzer-1.0/bin/rust-analyzer", Content: payload, Mode: 0o755},
		{Name: "rust-analyzer-1.0/README", Content: []byte("readme content")},
	})
	srv, _ := serveBytes(t, archive)
	m := newManifest("ra", "1.0.0", "ra", newAsset(srv.URL, KindTarGz, true, archive))

	root := t.TempDir()
	res, err := Ensure(context.Background(), root, m, Options{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if res.Status != StatusInstalled {
		t.Fatalf("expected installed, got %s", res.Status)
	}
	if res.Dir == "" {
		t.Fatal("expected non-empty Dir")
	}

	// Verify layout: bin/rust-analyzer, README at root (wrapper stripped).
	binPath := filepath.Join(res.Dir, "bin", "rust-analyzer")
	readmePath := filepath.Join(res.Dir, "README")
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("bin/rust-analyzer not found: %v", err)
	}
	if _, err := os.Stat(readmePath); err != nil {
		t.Fatalf("README not found: %v", err)
	}
	gotContent, _ := os.ReadFile(binPath)
	if string(gotContent) != string(payload) {
		t.Fatalf("bin/rust-analyzer content: got %q, want %q", string(gotContent), string(payload))
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(binPath)
		if fi.Mode()&0o111 == 0 {
			t.Fatalf("bin/rust-analyzer is not executable: mode %o", fi.Mode())
		}
	}

	// Marker record.
	rec := readMarker(t, res.Dir)
	if rec == nil {
		t.Fatal("install record not found")
	}
	if rec.Tool != "ra" || rec.Version != "1.0.0" {
		t.Fatalf("record: got %+v", rec)
	}

	// Second call skips.
	res2, err := Ensure(context.Background(), root, m, Options{})
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if res2.Status != StatusSkipped {
		t.Fatalf("expected skipped, got %s", res2.Status)
	}
}

func TestEnsureZip(t *testing.T) {
	payload := []byte("rust-analyzer.exe")
	archive := buildZip(t, []testEntry{
		{Name: "bin/rust-analyzer.exe", Content: payload, Mode: 0o755},
		{Name: "config.toml", Content: []byte("key=val")},
	})
	srv, _ := serveBytes(t, archive)
	m := newManifest("ra", "2.0.0", "ra", newAsset(srv.URL, KindZip, false, archive))

	root := t.TempDir()
	res, err := Ensure(context.Background(), root, m, Options{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if res.Status != StatusInstalled {
		t.Fatalf("expected installed, got %s", res.Status)
	}
	binPath := filepath.Join(res.Dir, "bin", "rust-analyzer.exe")
	cfgPath := filepath.Join(res.Dir, "config.toml")
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("bin missing: %v", err)
	}
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("config.toml missing: %v", err)
	}
	got, _ := os.ReadFile(binPath)
	if string(got) != string(payload) {
		t.Fatalf("content: got %q, want %q", string(got), string(payload))
	}
}

func TestEnsureRawBinary(t *testing.T) {
	payload := []byte("#!/usr/bin/env node\nconsole.log('hello')")
	srv, _ := serveBytes(t, payload)
	url := srv.URL + "/typescript-language-server?download=1"
	a := newAsset(url, KindRaw, false, payload)
	m := newManifest("ts-ls", "1.0.0", "ts-ls", a)

	root := t.TempDir()
	res, err := Ensure(context.Background(), root, m, Options{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if res.Status != StatusInstalled {
		t.Fatalf("expected installed, got %s", res.Status)
	}
	expectedBase := "typescript-language-server" // query stripped
	fullPath := filepath.Join(res.Dir, expectedBase)
	got, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("read %s: %v", fullPath, err)
	}
	if string(got) != string(payload) {
		t.Fatalf("content: got %q, want %q", string(got), string(payload))
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(fullPath)
		if fi.Mode()&0o111 == 0 {
			t.Fatalf("raw file not executable: mode %o", fi.Mode())
		}
	}
}

func TestEnsureGzipBinary(t *testing.T) {
	payload := []byte("#!/bin/sh\necho rust-analyzer")
	compressed := buildGzip(t, payload)
	srv, _ := serveBytes(t, compressed)
	a := Asset{
		GOOS:        runtime.GOOS,
		GOARCH:      runtime.GOARCH,
		URL:         srv.URL + "/rust-analyzer-x86_64-unknown-linux-gnu.gz",
		SHA256:      sha256Hex(compressed),
		Kind:        KindGzip,
		InstallName: "rust-analyzer",
	}
	m := newManifest("rust-analyzer", "2026-08-17.4", "rust-analyzer", a)

	root := t.TempDir()
	res, err := Ensure(context.Background(), root, m, Options{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if res.Status != StatusInstalled {
		t.Fatalf("expected installed, got %s", res.Status)
	}
	fullPath := filepath.Join(res.Dir, "rust-analyzer")
	got, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("read %s: %v", fullPath, err)
	}
	if string(got) != string(payload) {
		t.Fatalf("content: got %q, want %q", string(got), string(payload))
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(fullPath)
		if fi.Mode()&0o111 == 0 {
			t.Fatalf("gzip file not executable: mode %o", fi.Mode())
		}
	}
	// The compressed artifact must not remain on disk.
	if _, err := os.Stat(filepath.Join(res.Dir, "rust-analyzer-x86_64-unknown-linux-gnu.gz")); !os.IsNotExist(err) {
		t.Fatalf("compressed artifact should not be installed: %v", err)
	}
}

func TestSelectPlatformMatching(t *testing.T) {
	m := Manifest{
		Tool:       "test",
		Version:    "1",
		InstallDir: "test",
		Assets: []Asset{
			{GOOS: "linux", GOARCH: "amd64", URL: "http://a", SHA256: strings.Repeat("a", 64), Kind: KindRaw},
			{GOOS: "linux", GOARCH: "*", URL: "http://b", SHA256: strings.Repeat("b", 64), Kind: KindRaw},
			{GOOS: "*", GOARCH: "amd64", URL: "http://c", SHA256: strings.Repeat("c", 64), Kind: KindRaw},
			{GOOS: "*", GOARCH: "*", URL: "http://d", SHA256: strings.Repeat("d", 64), Kind: KindRaw},
		},
	}
	tests := []struct {
		goos, goarch string
		wantURL      string
	}{
		{"linux", "amd64", "http://a"},
		{"linux", "arm64", "http://b"},
		{"darwin", "amd64", "http://c"},
		{"darwin", "arm64", "http://d"},
	}
	for _, tt := range tests {
		got, err := m.Select(tt.goos, tt.goarch)
		if err != nil {
			t.Fatalf("Select(%q,%q): %v", tt.goos, tt.goarch, err)
		}
		if got.URL != tt.wantURL {
			t.Fatalf("Select(%q,%q): got URL %q, want %q", tt.goos, tt.goarch, got.URL, tt.wantURL)
		}
	}
	// No match: with only the four assets above the */* fallback catches
	// everything. Assert the fallback wins, then use a restricted manifest to
	// verify the error path.
	got, err := m.Select("wasm", "wasm")
	if err != nil {
		t.Fatalf("Select(wasm,wasm): %v", err)
	}
	if got.URL != "http://d" {
		t.Fatalf("Select(wasm,wasm): got URL %q, want http://d (wildcard fallback)", got.URL)
	}
	noFallback := Manifest{Tool: "x", Version: "1", InstallDir: "x", Assets: []Asset{
		{GOOS: "linux", GOARCH: "amd64", URL: "http://a", SHA256: strings.Repeat("a", 64), Kind: KindRaw},
	}}
	_, err = noFallback.Select("windows", "amd64")
	if err == nil || !strings.Contains(err.Error(), "no asset") {
		t.Fatalf("expected no-asset error, got %v", err)
	}
}

func TestEnsureShaMismatchAtomic(t *testing.T) {
	payload := []byte("real-content")
	badDigest := strings.Repeat("ff", 32)
	srv, _ := serveBytes(t, payload)
	a := Asset{
		GOOS:   runtime.GOOS,
		GOARCH: runtime.GOARCH,
		URL:    srv.URL,
		SHA256: badDigest,
		Kind:   KindTarGz,
	}
	m := newManifest("tool", "1", "tool", a)

	root := t.TempDir()
	_, err := Ensure(context.Background(), root, m, Options{})
	if err == nil || !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Fatalf("expected sha256 mismatch, got %v", err)
	}
	// Nothing visible outside .tmp.
	rootFiles := filepath.Join(root, "tool")
	if _, e := os.Stat(rootFiles); !os.IsNotExist(e) {
		t.Fatalf("tool dir should not exist after failed install: %v", e)
	}
	// The .tmp dir should be empty (work cleaned).
	if hasFiles(t, filepath.Join(root, ".tmp")) {
		t.Fatal(".tmp still has files after failed install")
	}
}

func TestEnsureHTTPErrorClean(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	a := newAsset(srv.URL, KindTarGz, false, []byte{})
	m := newManifest("tool", "1", "tool", a)

	root := t.TempDir()
	_, err := Ensure(context.Background(), root, m, Options{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("expected HTTP 404 error, got %v", err)
	}
	if _, e := os.Stat(filepath.Join(root, "tool")); !os.IsNotExist(e) {
		t.Fatal("tool dir should not exist after HTTP error")
	}
}

func TestEnsureCancelMidDownloadAtomic(t *testing.T) {
	payload := make([]byte, 256<<10) // 256 KiB — enough to span multiple chunks
	for i := range payload {
		payload[i] = byte(i % 256)
	}
	srv := serveCancelling(t, payload)
	a := newAsset(srv.URL, KindTarGz, false, payload)
	m := newManifest("tool", "1", "tool", a)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after first progress callback.
	cancelled := false
	opts := Options{
		Progress: func(done, total int64) {
			if !cancelled && done > 0 {
				cancelled = true
				cancel()
			}
		},
	}

	root := t.TempDir()
	_, err := Ensure(ctx, root, m, opts)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Logf("error is %T: %v", err, err)
	}
	// Nothing visible.
	if _, e := os.Stat(filepath.Join(root, "tool")); !os.IsNotExist(e) {
		t.Fatal("tool dir should not exist after cancellation")
	}
}

func TestEnsureReplacesStaleInstall(t *testing.T) {
	payload := []byte("v2-payload")
	archive := buildTarGz(t, []testEntry{
		{Name: "v2-binary", Content: payload},
	})
	srv, _ := serveBytes(t, archive)
	m := newManifest("tool", "2.0.0", "tool", newAsset(srv.URL, KindTarGz, false, archive))

	root := t.TempDir()
	// Pre-create a stale install with old marker and junk file.
	finalDir := filepath.Join(root, "tool")
	os.MkdirAll(finalDir, 0o755)
	os.WriteFile(filepath.Join(finalDir, "stale-junk"), []byte("old"), 0o644)
	oldRec := installRecord{Tool: "tool", Version: "1.0.0", InstalledAt: time.Now().UTC().Format(time.RFC3339)}
	oldRaw, _ := json.Marshal(oldRec)
	os.WriteFile(filepath.Join(finalDir, markerName), oldRaw, 0o644)

	res, err := Ensure(context.Background(), root, m, Options{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if res.Status != StatusInstalled {
		t.Fatalf("expected installed, got %s", res.Status)
	}
	// Stale junk gone.
	if _, err := os.Stat(filepath.Join(finalDir, "stale-junk")); !os.IsNotExist(err) {
		t.Fatal("stale junk file should be gone")
	}
	// New content present.
	got, _ := os.ReadFile(filepath.Join(finalDir, "v2-binary"))
	if string(got) != string(payload) {
		t.Fatalf("got %q, want %q", string(got), string(payload))
	}
	rec := readMarker(t, finalDir)
	if rec == nil || rec.Version != "2.0.0" {
		t.Fatalf("expected version 2.0.0, got %+v", rec)
	}
}

func TestProgressMonotonic(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 100<<10) // 100 KiB
	archive := buildTarGz(t, []testEntry{
		{Name: "bin", Content: payload},
	})
	srv, _ := serveBytes(t, archive)
	m := newManifest("tool", "1", "tool", newAsset(srv.URL, KindTarGz, false, archive))

	var events []int64
	opts := Options{
		Progress: func(done, total int64) {
			events = append(events, done)
		},
	}
	root := t.TempDir()
	_, err := Ensure(context.Background(), root, m, opts)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one progress event")
	}
	for i := 1; i < len(events); i++ {
		if events[i] < events[i-1] {
			t.Fatalf("progress not monotonic at index %d: %d < %d", i, events[i], events[i-1])
		}
	}
	last := events[len(events)-1]
	if last != int64(len(archive)) {
		t.Fatalf("last progress %d != archive size %d", last, len(archive))
	}
}

func TestManifestValidation(t *testing.T) {
	validAsset := Asset{
		GOOS: "linux", GOARCH: "amd64", URL: "http://example.com/a",
		SHA256: strings.Repeat("a", 64), Kind: KindRaw,
	}
	validManifest := Manifest{Tool: "x", Version: "1", InstallDir: "x", Assets: []Asset{validAsset}}
	if err := validManifest.Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(m *Manifest)
	}{
		{"empty tool", func(m *Manifest) { m.Tool = "" }},
		{"tool with slash", func(m *Manifest) { m.Tool = "a/b" }},
		{"empty version", func(m *Manifest) { m.Version = "" }},
		{"empty install dir", func(m *Manifest) { m.InstallDir = "" }},
		{"install dir with ..", func(m *Manifest) { m.InstallDir = "a/../b" }},
		{"install dir absolute", func(m *Manifest) { m.InstallDir = "/usr/local" }},
		{"no assets", func(m *Manifest) { m.Assets = nil }},
		{"bad asset kind", func(m *Manifest) { m.Assets[0].Kind = "exe" }},
		{"bad sha256 length", func(m *Manifest) { m.Assets[0].SHA256 = "abc" }},
		{"bad sha256 chars", func(m *Manifest) { m.Assets[0].SHA256 = strings.Repeat("z", 64) }},
		{"empty goos", func(m *Manifest) { m.Assets[0].GOOS = "" }},
		{"empty goarch", func(m *Manifest) { m.Assets[0].GOARCH = "" }},
		{"no scheme url", func(m *Manifest) { m.Assets[0].URL = "example.com/a" }},
		{"gzip without install name", func(m *Manifest) { m.Assets[0].Kind = KindGzip; m.Assets[0].InstallName = "" }},
		{"gzip with path in install name", func(m *Manifest) { m.Assets[0].Kind = KindGzip; m.Assets[0].InstallName = "dir/binary" }},
		{"install name with ..", func(m *Manifest) { m.Assets[0].InstallName = "../evil" }},
		{"install name with drive letter", func(m *Manifest) { m.Assets[0].InstallName = "C:binary" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := validManifest
			tt.edit(&m)
			if err := m.Validate(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestUnpackTarRejectsTraversal(t *testing.T) {
	entries := []testEntry{
		{Name: "../../evil.txt", Content: []byte("bad")},
	}
	archive := buildTarGz(t, entries)
	srv, _ := serveBytes(t, archive)
	a := newAsset(srv.URL, KindTarGz, false, archive)
	m := newManifest("tool", "1", "tool", a)

	root := t.TempDir()
	_, err := Ensure(context.Background(), root, m, Options{})
	if err == nil || !strings.Contains(err.Error(), "parent traversal") {
		t.Fatalf("expected traversal error, got %v", err)
	}
	if _, e := os.Stat(filepath.Join(root, "tool")); !os.IsNotExist(e) {
		t.Fatal("tool dir should not exist after traversal attempt")
	}
}

func TestUnpackZipRejectsTraversal(t *testing.T) {
	entries := []testEntry{
		{Name: "..\\evil.txt", Content: []byte("bad")},
	}
	archive := buildZip(t, entries)
	srv, _ := serveBytes(t, archive)
	a := newAsset(srv.URL, KindZip, false, archive)
	m := newManifest("tool", "1", "tool", a)

	root := t.TempDir()
	_, err := Ensure(context.Background(), root, m, Options{})
	if err == nil || !strings.Contains(err.Error(), "parent traversal") {
		t.Fatalf("expected traversal error, got %v", err)
	}
	if _, e := os.Stat(filepath.Join(root, "tool")); !os.IsNotExist(e) {
		t.Fatal("tool dir should not exist after traversal attempt")
	}
}

func TestInstalledVersionHelper(t *testing.T) {
	payload := []byte("content")
	archive := buildTarGz(t, []testEntry{
		{Name: "f", Content: payload},
	})
	srv, _ := serveBytes(t, archive)
	m := newManifest("tool", "1.0.0", "tool", newAsset(srv.URL, KindTarGz, false, archive))

	root := t.TempDir()
	// Before install: not found.
	_, ok, err := InstalledVersion(root, m)
	if err != nil {
		t.Fatalf("InstalledVersion: %v", err)
	}
	if ok {
		t.Fatal("expected not installed before Ensure")
	}

	// After install.
	_, _ = Ensure(context.Background(), root, m, Options{})
	ver, ok, err := InstalledVersion(root, m)
	if err != nil {
		t.Fatalf("InstalledVersion after install: %v", err)
	}
	if !ok {
		t.Fatal("expected installed")
	}
	if ver != "1.0.0" {
		t.Fatalf("version: got %q, want 1.0.0", ver)
	}
}

func TestSelectNoMatchListsPlatforms(t *testing.T) {
	m := Manifest{
		Tool: "x", Version: "1", InstallDir: "x",
		Assets: []Asset{
			{GOOS: "linux", GOARCH: "amd64", URL: "http://a", SHA256: strings.Repeat("a", 64), Kind: KindRaw},
		},
	}
	_, err := m.Select("windows", "amd64")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "linux/amd64") {
		t.Fatalf("error should list available platform: %v", err)
	}
}

func TestEnsureSkippedNoServerHit(t *testing.T) {
	payload := []byte("data")
	archive := buildTarGz(t, []testEntry{
		{Name: "f", Content: payload},
	})
	srv, hits := serveBytes(t, archive)
	m := newManifest("tool", "1", "tool", newAsset(srv.URL, KindTarGz, false, archive))

	root := t.TempDir()
	_, err := Ensure(context.Background(), root, m, Options{})
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 hit for first install, got %d", hits.Load())
	}

	// Second call should skip without hitting server.
	_, err = Ensure(context.Background(), root, m, Options{})
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 hit total, got %d", hits.Load())
	}
}

func TestEnsureNoHalfFilesOnLimitsExceeded(t *testing.T) {
	// Build a tar.gz that exceeds the entry limit by having many entries.
	big := make([]byte, 100)
	entries := make([]testEntry, 100)
	for i := range entries {
		entries[i] = testEntry{Name: fmt.Sprintf("f%d", i), Content: big}
	}
	archive := buildTarGz(t, entries)
	srv, _ := serveBytes(t, archive)
	a := newAsset(srv.URL, KindTarGz, false, archive)
	m := newManifest("tool", "1", "tool", a)

	root := t.TempDir()
	opts := Options{Limits: Limits{MaxEntries: 10, MaxCompressedBytes: 1 << 30, MaxUncompressedBytes: 1 << 30}}
	_, err := Ensure(context.Background(), root, m, opts)
	if err == nil || !strings.Contains(err.Error(), "max entries") {
		t.Fatalf("expected max entries error, got %v", err)
	}
	if _, e := os.Stat(filepath.Join(root, "tool")); !os.IsNotExist(e) {
		t.Fatal("tool dir should not exist after limits exceeded")
	}
}

func TestEnsureOverwritesDifferentToolAtSameDir(t *testing.T) {
	// Install tool "a", then install a different tool "b" at the same
	// InstallDir path — the marker mismatch triggers a replacement.
	payload := []byte("payload")
	archive := buildTarGz(t, []testEntry{
		{Name: "binary", Content: payload},
	})
	srv, _ := serveBytes(t, archive)
	a := newAsset(srv.URL, KindTarGz, false, archive)
	manifestA := newManifest("a", "1", "shared", a)
	manifestB := newManifest("b", "1", "shared", a)

	root := t.TempDir()
	res, err := Ensure(context.Background(), root, manifestA, Options{})
	if err != nil {
		t.Fatalf("Ensure a: %v", err)
	}
	if res.Status != StatusInstalled {
		t.Fatalf("expected installed for a, got %s", res.Status)
	}
	// Verify marker says "a"
	rec := readMarker(t, res.Dir)
	if rec.Tool != "a" {
		t.Fatalf("expected record a, got %s", rec.Tool)
	}

	// Install b at same dir.
	resB, err := Ensure(context.Background(), root, manifestB, Options{})
	if err != nil {
		t.Fatalf("Ensure b: %v", err)
	}
	if resB.Status != StatusInstalled {
		t.Fatalf("expected installed for b, got %s", resB.Status)
	}
	recB := readMarker(t, resB.Dir)
	if recB.Tool != "b" {
		t.Fatalf("expected record b, got %s", recB.Tool)
	}
}