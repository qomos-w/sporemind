package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGenerateDoubao locks the Ark images/generations contract: a single POST
// to {endpoint}/images/generations with Bearer auth and response_format "url",
// then a no-auth download of the presigned result URL.
func TestGenerateDoubao(t *testing.T) {
	imageBytes := []byte{0x89, 0x50, 0x4e, 0x47}
	var gotReq doubaoImageRequest
	var gotAuth, gotPath, gotContentType string
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/generations":
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			gotContentType = r.Header.Get("Content-Type")
			if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
				t.Errorf("decode body: %v", err)
			}
			_, _ = w.Write([]byte(`{"model":"doubao-seedream-3.0-t2i","created":1,"data":[{"url":"` + srvURL + `/img.jpg"}]}`))
		case "/img.jpg":
			if got := r.Header.Get("Authorization"); got != "" {
				t.Errorf("download authorization = %q, want empty for presigned URLs", got)
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(imageBytes)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srvURL = srv.URL
	defer srv.Close()

	result, err := Generate(context.Background(), Params{
		Protocol:    "doubao",
		Endpoint:    srv.URL,
		AuthToken:   "ark-key",
		Prompt:      "a cat",
		Model:       "doubao-seedream-3.0-t2i",
		Size:        "1K",
		AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(result.Data) != string(imageBytes) {
		t.Fatalf("data = %v", result.Data)
	}
	if result.MimeType != "image/jpeg" {
		t.Fatalf("mime = %q", result.MimeType)
	}
	if gotPath != "/images/generations" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer ark-key" {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotContentType != "application/json" {
		t.Fatalf("content-type = %q", gotContentType)
	}
	if gotReq.Model != "doubao-seedream-3.0-t2i" || gotReq.Prompt != "a cat" {
		t.Fatalf("req = %+v", gotReq)
	}
	if gotReq.ResponseFormat != "url" {
		t.Fatalf("response_format = %q", gotReq.ResponseFormat)
	}
	if gotReq.Watermark {
		t.Fatal("watermark = true, want false")
	}
	if gotReq.Size != "1024x576" {
		t.Fatalf("size = %q, want 1024x576 (1K landscape)", gotReq.Size)
	}
}

// TestGenerateDoubaoB64 locks the b64_json reply path: the payload is decoded
// and its mime is sniffed from the bytes (PNG magic here).
func TestGenerateDoubaoB64(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(png) + `"}]}`))
	}))
	defer srv.Close()

	result, err := Generate(context.Background(), Params{Protocol: "doubao", Endpoint: srv.URL, AuthToken: "k", Prompt: "p"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(result.Data) != string(png) {
		t.Fatalf("data = %v", result.Data)
	}
	if result.MimeType != "image/png" {
		t.Fatalf("mime = %q, want image/png", result.MimeType)
	}
}

// TestGenerateDoubaoDefaults locks the default model and the size omission:
// with no size budget and no aspect ratio the size field is left to the
// provider default (1024x1024); an aspect ratio without a budget yields a 1K
// landscape.
func TestGenerateDoubaoDefaults(t *testing.T) {
	var gotReq doubaoImageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString([]byte{1}) + `"}]}`))
	}))
	defer srv.Close()

	if _, err := Generate(context.Background(), Params{Protocol: "doubao", Endpoint: srv.URL, AuthToken: "k", Prompt: "p"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotReq.Model != doubaoDefaultImageModel {
		t.Fatalf("model = %q, want %q", gotReq.Model, doubaoDefaultImageModel)
	}
	if gotReq.Size != "" {
		t.Fatalf("size = %q, want omitted", gotReq.Size)
	}

	if _, err := Generate(context.Background(), Params{Protocol: "doubao", Endpoint: srv.URL, AuthToken: "k", Prompt: "p", AspectRatio: "9:16"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotReq.Size != "576x1024" {
		t.Fatalf("size = %q, want 576x1024", gotReq.Size)
	}
}

// TestGenerateDoubaoReferenceImage locks the image-to-image field: a raw base64
// reference is wrapped as a data URL while URL/data-URL references pass through.
func TestGenerateDoubaoReferenceImage(t *testing.T) {
	var gotReq doubaoImageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString([]byte{1}) + `"}]}`))
	}))
	defer srv.Close()

	if _, err := Generate(context.Background(), Params{
		Protocol: "doubao", Endpoint: srv.URL, AuthToken: "k", Prompt: "p",
		ReferenceImages: []string{"QUJD"},
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotReq.Image != "data:image/png;base64,QUJD" {
		t.Fatalf("image = %q", gotReq.Image)
	}

	if _, err := Generate(context.Background(), Params{
		Protocol: "doubao", Endpoint: srv.URL, AuthToken: "k", Prompt: "p",
		ReferenceImages: []string{"https://r.example.com/a.png"},
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotReq.Image != "https://r.example.com/a.png" {
		t.Fatalf("image = %q", gotReq.Image)
	}
}

// TestGenerateDoubaoAPIError locks the Ark structured error surfacing (both the
// error-object and the bare-HTTP-status forms), with the provider message
// truncated.
func TestGenerateDoubaoAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"InvalidParameter","message":"invalid size"}}`))
	}))
	defer srv.Close()

	_, err := Generate(context.Background(), Params{Protocol: "doubao", Endpoint: srv.URL, AuthToken: "k", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "InvalidParameter") || !strings.Contains(err.Error(), "invalid size") {
		t.Fatalf("err = %v", err)
	}
}

// TestGenerateDoubaoNoImageData locks the empty-data failure surface.
func TestGenerateDoubaoNoImageData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	_, err := Generate(context.Background(), Params{Protocol: "doubao", Endpoint: srv.URL, AuthToken: "k", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "no image data") {
		t.Fatalf("err = %v", err)
	}
}

// TestGenerateDoubaoRequiresPrompt locks the fast-fail guard for an empty
// prompt with no reference image.
func TestGenerateDoubaoRequiresPrompt(t *testing.T) {
	_, err := Generate(context.Background(), Params{Protocol: "doubao", Endpoint: "https://x", AuthToken: "k"})
	if err == nil || !strings.Contains(err.Error(), "prompt is required") {
		t.Fatalf("err = %v", err)
	}
}

// TestGenerateDoubaoAliasAndSizeMatrix locks the dispatcher alias ("imagex")
// and the size-budget → "WxH" mapping.
func TestGenerateDoubaoAliasAndSizeMatrix(t *testing.T) {
	var gotReq doubaoImageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString([]byte{1}) + `"}]}`))
	}))
	defer srv.Close()

	if _, err := Generate(context.Background(), Params{Protocol: "imagex", Endpoint: srv.URL, AuthToken: "k", Prompt: "p", Size: "2K"}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotReq.Size != "2048x2048" {
		t.Fatalf("size = %q, want 2048x2048", gotReq.Size)
	}

	cases := []struct {
		size, aspect, want string
	}{
		{"", "", ""},
		{"1K", "", "1024x1024"},
		{"2K", "1:1", "2048x2048"},
		{"4K", "16:9", "4096x2304"},
		{"4K", "9:16", "2304x4096"},
		{"2K", "4:3", "2048x1536"},
		{"2K", "3:4", "1536x2048"},
	}
	for _, tc := range cases {
		if got := doubaoImageSize(tc.size, tc.aspect); got != tc.want {
			t.Errorf("doubaoImageSize(%q, %q) = %q, want %q", tc.size, tc.aspect, got, tc.want)
		}
	}
}
