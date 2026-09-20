package mediagen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// setFastPoll shortens the poll interval so polling tests run quickly, and
// restores it on cleanup.
func setFastPoll(t *testing.T) {
	t.Helper()
	prev := pollInterval
	pollInterval = 5 * time.Millisecond
	t.Cleanup(func() { pollInterval = prev })
}

// writeJSON writes a JSON value and aborts the test on encoder failure.
func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatalf("write json: %v", err)
	}
}

// ---- synchronous direct return ------------------------------------------

func TestGenerate_SyncImageB64(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a}
	var (
		gotPath, gotAuth string
		gotBody          map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{
				{"b64_json": base64.StdEncoding.EncodeToString(png)},
			},
		})
	}))
	defer srv.Close()

	res, err := Generate(context.Background(), Params{
		Kind: KindImage, Prompt: "a cat", Model: "gpt-image-1", Endpoint: srv.URL, AuthToken: "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/images/generations" {
		t.Errorf("path = %q, want /images/generations", gotPath)
	}
	if gotAuth != "Bearer secret" {
		t.Errorf("authorization = %q, want Bearer secret", gotAuth)
	}
	if gotBody["model"] != "gpt-image-1" {
		t.Errorf("model = %v", gotBody["model"])
	}
	if gotBody["prompt"] != "a cat" {
		t.Errorf("prompt = %v", gotBody["prompt"])
	}
	if !bytes.Equal(res.Data, png) {
		t.Errorf("data = %v, want %v", res.Data, png)
	}
	if res.MimeType != "image/png" {
		t.Errorf("mimeType = %q, want image/png", res.MimeType)
	}
}

func TestGenerate_SyncImageURLDownload(t *testing.T) {
	want := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	mux := http.NewServeMux()
	var downloaded int32
	mux.HandleFunc("/images/generations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{
				{"url": "http://" + r.Host + "/file/img.png"},
			},
		})
	})
	mux.HandleFunc("/file/img.png", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&downloaded, 1)
		w.Write(want)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := Generate(context.Background(), Params{
		Kind: KindImage, Model: "m", Endpoint: srv.URL, AuthToken: "t",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(res.Data, want) {
		t.Errorf("data = %v, want %v", res.Data, want)
	}
	if res.URL == "" {
		t.Errorf("source URL not recorded")
	}
	if atomic.LoadInt32(&downloaded) != 1 {
		t.Errorf("downloaded %d times, want 1", downloaded)
	}
}

// ---- request body field conventions --------------------------------------

func TestGenerate_ImageBodyUsesImageURLs(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString([]byte("x"))}},
		})
	}))
	defer srv.Close()

	if _, err := Generate(context.Background(), Params{
		Kind: KindImage, Model: "m", Endpoint: srv.URL,
		ReferenceImages: []string{"http://example.com/a.png", "http://example.com/b.png"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	urls, ok := gotBody["image_urls"].([]any)
	if !ok {
		t.Fatalf("image_urls missing; body=%v", gotBody)
	}
	if len(urls) != 2 {
		t.Errorf("image_urls len = %d, want 2", len(urls))
	}
	if _, ok := gotBody["referenceImages"]; ok {
		t.Errorf("referenceImages must not appear in image body")
	}
}

func TestGenerate_VideoBodyUsesReferenceFields(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/video/generations" {
			t.Errorf("path = %q, want /video/generations", r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString([]byte("v"))}},
		})
	}))
	defer srv.Close()

	if _, err := Generate(context.Background(), Params{
		Kind: KindVideo, Model: "video-model", Prompt: "dance", Duration: 5,
		Endpoint:        srv.URL,
		ReferenceImages: []string{"http://example.com/i.png"},
		ReferenceAudios: []string{"http://example.com/a.wav"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotBody["model"] != "video-model" {
		t.Errorf("model = %v", gotBody["model"])
	}
	if gotBody["duration"].(float64) != 5 {
		t.Errorf("duration = %v, want 5", gotBody["duration"])
	}
	imgs, ok := gotBody["referenceImages"].([]any)
	if !ok || len(imgs) != 1 {
		t.Errorf("referenceImages = %v", gotBody["referenceImages"])
	}
	auds, ok := gotBody["referenceAudios"].([]any)
	if !ok || len(auds) != 1 {
		t.Errorf("referenceAudios = %v", gotBody["referenceAudios"])
	}
	if _, ok := gotBody["image_urls"]; ok {
		t.Errorf("image_urls must not appear in video body")
	}
}

// ---- asynchronous task polling -------------------------------------------

func TestGenerate_TaskPollSuccess(t *testing.T) {
	setFastPoll(t)
	var polls int32
	video := []byte{0x10, 0x20, 0x30, 0x40, 0x50}
	mux := http.NewServeMux()
	mux.HandleFunc("/video/generations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"code": 200,
			"data": map[string]any{"id": "task-7", "status": "submitted"},
		})
	})
	mux.HandleFunc("/tasks/task-7", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&polls, 1)
		if n < 3 {
			writeJSON(t, w, map[string]any{
				"data": map[string]any{"id": "task-7", "status": "processing"},
			})
			return
		}
		writeJSON(t, w, map[string]any{
			"data": map[string]any{
				"id":     "task-7",
				"status": "succeeded",
				"output": map[string]any{"url": "http://" + r.Host + "/file/v.mp4"},
			},
		})
	})
	mux.HandleFunc("/file/v.mp4", func(w http.ResponseWriter, r *http.Request) {
		w.Write(video)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := Generate(context.Background(), Params{
		Kind: KindVideo, Model: "vm", Endpoint: srv.URL, AuthToken: "t",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(res.Data, video) {
		t.Errorf("data = %v, want %v", res.Data, video)
	}
	if res.MimeType != "video/mp4" {
		t.Errorf("mimeType = %q, want video/mp4", res.MimeType)
	}
	if got := atomic.LoadInt32(&polls); got < 3 {
		t.Errorf("polled %d times, want >= 3", got)
	}
}

func TestGenerate_TaskPollFailure(t *testing.T) {
	setFastPoll(t)
	var polls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/images/generations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"data": map[string]any{"id": "t-fail", "status": "submitted"},
		})
	})
	mux.HandleFunc("/tasks/t-fail", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&polls, 1)
		if n < 2 {
			writeJSON(t, w, map[string]any{
				"data": map[string]any{"id": "t-fail", "status": "processing"},
			})
			return
		}
		writeJSON(t, w, map[string]any{
			"data": map[string]any{
				"id": "t-fail", "status": "failed", "error": "content policy violation",
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := Generate(context.Background(), Params{
		Kind: KindImage, Endpoint: srv.URL, AuthToken: "t",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "content policy violation") {
		t.Errorf("error should include failure cause, got: %v", err)
	}
	if got := atomic.LoadInt32(&polls); got < 2 {
		t.Errorf("polled %d times, want >= 2", got)
	}
}

func TestGenerate_TaskPollTimeout(t *testing.T) {
	setFastPoll(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/images/generations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"data": map[string]any{"id": "t-slow", "status": "submitted"},
		})
	})
	mux.HandleFunc("/tasks/t-slow", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"data": map[string]any{"id": "t-slow", "status": "processing"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()

	_, err := Generate(ctx, Params{Kind: KindImage, Endpoint: srv.URL, AuthToken: "t"})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error should wrap DeadlineExceeded, got: %v", err)
	}
}

// ---- validation ----------------------------------------------------------

func TestGenerate_EmptyEndpoint(t *testing.T) {
	if _, err := Generate(context.Background(), Params{Kind: KindImage}); err == nil {
		t.Fatal("expected error for empty endpoint")
	}
}

func TestGenerate_UnsupportedKind(t *testing.T) {
	if _, err := Generate(context.Background(), Params{Kind: "audio", Endpoint: "http://x"}); err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}

func TestGenerate_TrailingSlashEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/images/generations", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"data": []map[string]any{{"b64_json": base64.StdEncoding.EncodeToString([]byte("ok"))}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := Generate(context.Background(), Params{
		Kind: KindImage, Endpoint: srv.URL + "/", AuthToken: "t",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(res.Data, []byte("ok")) {
		t.Errorf("data = %q", string(res.Data))
	}
}

// ---- pure helpers --------------------------------------------------------

func TestExtractMedia_NestedShapes(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("Z"))

	// Array form: [{"b64_json": ...}].
	if b, _, _, ok := extractMedia(json.RawMessage(`[{"b64_json":"`+b64+`"}]`), KindImage); !ok || b != b64 {
		t.Errorf("array b64: got b64=%q ok=%v", b, ok)
	}
	// Nested object container: {output:{url:...}}.
	if _, u, _, ok := extractMedia(json.RawMessage(`{"output":{"url":"https://cdn/x.png"}}`), KindImage); !ok || u != "https://cdn/x.png" {
		t.Errorf("nested output url: got %q ok=%v", u, ok)
	}
	// String-typed result container: {result:"https://..."}.
	if _, u, _, ok := extractMedia(json.RawMessage(`{"result":"https://cdn/v.mp4"}`), KindVideo); !ok || u != "https://cdn/v.mp4" {
		t.Errorf("string result url: got %q ok=%v", u, ok)
	}
	// A pending task descriptor must not be mistaken for media.
	if _, _, _, ok := extractMedia(json.RawMessage(`{"id":"t","status":"processing"}`), KindImage); ok {
		t.Errorf("task descriptor should not yield media")
	}
}
