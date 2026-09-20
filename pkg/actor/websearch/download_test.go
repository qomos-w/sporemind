package websearch

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// newDownloadTestActor returns an actor wired with the dedicated download
// HTTP client, mirroring what OnInit does for the real actor.
func newDownloadTestActor(t *testing.T) *Actor {
	t.Helper()
	a := newTestActor(t)
	a.downloadHTTP = &http.Client{Timeout: downloadHTTPTimeout}
	return a
}

func TestHandleDownload_Success(t *testing.T) {
	body := "hello download payload"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	a := newDownloadTestActor(t)
	savePath := filepath.Join(t.TempDir(), "file.bin")

	resp, err := a.handleDownload(nil, gen.WebDownloadReq{URL: server.URL, SavePath: savePath})
	if err != nil {
		t.Fatalf("handleDownload: %v", err)
	}

	if resp.SavedPath != savePath {
		t.Errorf("SavedPath = %q, want %q", resp.SavedPath, savePath)
	}
	if resp.BytesDownloaded != int32(len(body)) {
		t.Errorf("BytesDownloaded = %d, want %d", resp.BytesDownloaded, len(body))
	}
	if resp.ContentType != "application/octet-stream" {
		t.Errorf("ContentType = %q, want %q", resp.ContentType, "application/octet-stream")
	}
	if resp.Truncated {
		t.Error("Truncated should be false for a small body")
	}

	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(got) != body {
		t.Errorf("file content = %q, want %q", got, body)
	}
}

func TestHandleDownload_CreatesParentDirs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "nested")
	}))
	defer server.Close()

	a := newDownloadTestActor(t)
	savePath := filepath.Join(t.TempDir(), "a", "b", "c", "file.txt")

	if _, err := a.handleDownload(nil, gen.WebDownloadReq{URL: server.URL, SavePath: savePath}); err != nil {
		t.Fatalf("handleDownload: %v", err)
	}
	if _, err := os.Stat(savePath); err != nil {
		t.Errorf("saved file should exist: %v", err)
	}
}

func TestHandleDownload_EmptyURL(t *testing.T) {
	a := newDownloadTestActor(t)
	savePath := filepath.Join(t.TempDir(), "file.bin")

	_, err := a.handleDownload(nil, gen.WebDownloadReq{URL: "", SavePath: savePath})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
	if !strings.Contains(err.Error(), "url is required") {
		t.Errorf("unexpected error: %v", err)
	}

	// Whitespace-only URL is trimmed and rejected too.
	if _, err := a.handleDownload(nil, gen.WebDownloadReq{URL: "   ", SavePath: savePath}); err == nil {
		t.Error("expected error for whitespace-only URL")
	}
}

func TestHandleDownload_EmptySavePath(t *testing.T) {
	a := newDownloadTestActor(t)

	_, err := a.handleDownload(nil, gen.WebDownloadReq{URL: "http://example.com/file", SavePath: ""})
	if err == nil {
		t.Fatal("expected error for empty SavePath")
	}
	if !strings.Contains(err.Error(), "savePath is required") {
		t.Errorf("unexpected error: %v", err)
	}

	if _, err := a.handleDownload(nil, gen.WebDownloadReq{URL: "http://example.com/file", SavePath: "  "}); err == nil {
		t.Error("expected error for whitespace-only SavePath")
	}
}

func TestHandleDownload_HTTP404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()

	a := newDownloadTestActor(t)
	savePath := filepath.Join(t.TempDir(), "file.bin")

	_, err := a.handleDownload(nil, gen.WebDownloadReq{URL: server.URL, SavePath: savePath})
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("unexpected error: %v", err)
	}
	// The handler must fail before creating the output file.
	if _, statErr := os.Stat(savePath); !os.IsNotExist(statErr) {
		t.Errorf("file should not be created on 404, stat err = %v", statErr)
	}
}

func TestHandleDownload_HTTP500(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	}))
	defer server.Close()

	a := newDownloadTestActor(t)

	_, err := a.handleDownload(nil, gen.WebDownloadReq{URL: server.URL, SavePath: filepath.Join(t.TempDir(), "file.bin")})
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleDownload_Non2xxStatus(t *testing.T) {
	// 503 exercises a non-2xx status that is neither 404 nor 500.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "unavailable")
	}))
	defer server.Close()

	a := newDownloadTestActor(t)

	_, err := a.handleDownload(nil, gen.WebDownloadReq{URL: server.URL, SavePath: filepath.Join(t.TempDir(), "file.bin")})
	if err == nil {
		t.Fatal("expected error for HTTP 503")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleDownload_MaxBytesTruncation(t *testing.T) {
	body := strings.Repeat("0123456789", 10) // 100 bytes
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	a := newDownloadTestActor(t)
	savePath := filepath.Join(t.TempDir(), "truncated.bin")

	resp, err := a.handleDownload(nil, gen.WebDownloadReq{URL: server.URL, SavePath: savePath, MaxBytes: 25})
	if err != nil {
		t.Fatalf("handleDownload: %v", err)
	}

	if !resp.Truncated {
		t.Error("Truncated should be true when body exceeds MaxBytes")
	}
	if resp.BytesDownloaded != 25 {
		t.Errorf("BytesDownloaded = %d, want 25", resp.BytesDownloaded)
	}

	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if len(got) != 25 {
		t.Fatalf("file length = %d, want 25", len(got))
	}
	if string(got) != body[:25] {
		t.Errorf("file content = %q, want %q", got, body[:25])
	}
}

func TestHandleDownload_MaxBytesExactFitNotTruncated(t *testing.T) {
	// A body of exactly MaxBytes bytes must NOT be flagged truncated: the
	// one-byte probe after the copy hits EOF.
	body := strings.Repeat("x", 30)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	a := newDownloadTestActor(t)
	savePath := filepath.Join(t.TempDir(), "exact.bin")

	resp, err := a.handleDownload(nil, gen.WebDownloadReq{URL: server.URL, SavePath: savePath, MaxBytes: 30})
	if err != nil {
		t.Fatalf("handleDownload: %v", err)
	}

	if resp.Truncated {
		t.Error("Truncated should be false when body is exactly MaxBytes")
	}
	if resp.BytesDownloaded != 30 {
		t.Errorf("BytesDownloaded = %d, want 30", resp.BytesDownloaded)
	}
	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(got) != body {
		t.Errorf("file content = %q, want %q", got, body)
	}
}

func TestHandleDownload_DefaultMaxBytesAllowsSmallBody(t *testing.T) {
	// MaxBytes <= 0 falls back to defaultMaxDownloadBytes; a small body
	// downloads whole and is not truncated.
	body := "tiny"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer server.Close()

	a := newDownloadTestActor(t)

	resp, err := a.handleDownload(nil, gen.WebDownloadReq{URL: server.URL, SavePath: filepath.Join(t.TempDir(), "tiny.bin")})
	if err != nil {
		t.Fatalf("handleDownload: %v", err)
	}
	if resp.BytesDownloaded != int32(len(body)) || resp.Truncated {
		t.Errorf("resp = %+v, want whole body, not truncated", resp)
	}
}

func TestHandleDownload_RedirectFollow(t *testing.T) {
	targetBody := "redirect target payload"
	targetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, targetBody)
	}))
	defer targetServer.Close()

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetServer.URL+"/final.bin", http.StatusFound)
	}))
	defer redirectServer.Close()

	a := newDownloadTestActor(t)
	savePath := filepath.Join(t.TempDir(), "redirected.bin")

	resp, err := a.handleDownload(nil, gen.WebDownloadReq{URL: redirectServer.URL, SavePath: savePath})
	if err != nil {
		t.Fatalf("handleDownload: %v", err)
	}
	if resp.Truncated {
		t.Error("Truncated should be false")
	}
	if resp.BytesDownloaded != int32(len(targetBody)) {
		t.Errorf("BytesDownloaded = %d, want %d", resp.BytesDownloaded, len(targetBody))
	}

	got, err := os.ReadFile(savePath)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if string(got) != targetBody {
		t.Errorf("file content = %q, want %q", got, targetBody)
	}
}
