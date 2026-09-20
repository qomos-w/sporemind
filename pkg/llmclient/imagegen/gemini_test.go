package imagegen

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestNormalizeGeminiEndpoint pins the relay-BaseURL immunity: a trailing
// /v1 (OpenAI-style) or /v1beta (native) suffix is stripped before the native
// /v1beta path is appended; other suffixes pass through untouched.
func TestNormalizeGeminiEndpoint(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", geminiDefaultEndpoint},
		{"  ", geminiDefaultEndpoint},
		{"http://127.0.0.1:8045", "http://127.0.0.1:8045"},
		{"http://127.0.0.1:8045/", "http://127.0.0.1:8045"},
		{"http://127.0.0.1:8045/v1", "http://127.0.0.1:8045"},
		{"http://127.0.0.1:8045/v1/", "http://127.0.0.1:8045"},
		{"http://127.0.0.1:8045/V1", "http://127.0.0.1:8045"},
		{"https://rolldek.com/v1beta/", "https://rolldek.com"},
		{"https://rolldek.com/api", "https://rolldek.com/api"}, // unknown prefix kept
		{geminiDefaultEndpoint, geminiDefaultEndpoint},
	}
	for _, tc := range cases {
		if got := normalizeGeminiEndpoint(tc.in); got != tc.want {
			t.Errorf("normalizeGeminiEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestGenerateGeminiEndpointV1SuffixTrimmed is the 404/405 regression: a
// resolved unit endpoint carrying a trailing /v1 must still POST the native
// path exactly once (/v1beta/models/{model}:generateContent).
func TestGenerateGeminiEndpointV1SuffixTrimmed(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b64 := base64.StdEncoding.EncodeToString([]byte("img"))
		fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"data":"%s","mimeType":"image/png"}}]}}]}`, b64)
	}))
	defer srv.Close()

	if _, err := generateGemini(context.Background(), Params{
		Prompt: "a cat", Model: "gemini-3.1-flash-image-preview",
		Endpoint: srv.URL + "/v1", Protocol: "gemini",
	}); err != nil {
		t.Fatalf("generateGemini: %v", err)
	}
	if want := "/v1beta/models/gemini-3.1-flash-image-preview:generateContent"; gotPath != want {
		t.Fatalf("request path = %q, want %q", gotPath, want)
	}
}

func TestMapGeminiSize(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"1K", "1K"},
		{"2K", "2K"},
		{"4K", "4K"},
		{"", "1K"},
		{"unknown", "1K"},
	}
	for _, tc := range tests {
		if got := mapGeminiSize(tc.in); got != tc.want {
			t.Fatalf("mapGeminiSize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestGenerateGeminiSkipsThoughtParts locks the final-image convention under
// TEXT+IMAGE: text parts are ignored, thought=true draft images are skipped,
// and the last non-thought inlineData is returned.
func TestGenerateGeminiSkipsThoughtParts(t *testing.T) {
	draftB64 := base64.StdEncoding.EncodeToString([]byte("draft"))
	finalB64 := base64.StdEncoding.EncodeToString([]byte("final"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[`+
			`{"text":"Here is your image..."},`+
			`{"inlineData":{"data":"%s","mimeType":"image/png"},"thought":true},`+
			`{"inlineData":{"data":"%s","mimeType":"image/png"},"thought":false}`+
			`]}}]}`, draftB64, finalB64)
	}))
	defer srv.Close()

	res, err := generateGemini(context.Background(), Params{
		Prompt:   "a cat",
		Model:    "gemini-3.1-flash-image-preview",
		Endpoint: srv.URL,
		Protocol: "gemini",
	})
	if err != nil {
		t.Fatalf("generateGemini: %v", err)
	}
	if string(res.Data) != "final" {
		t.Fatalf("result data = %q, want %q (last non-thought image)", res.Data, "final")
	}
}

func TestGenerateGeminiErrorBodyTruncated(t *testing.T) {
	longErr := strings.Repeat("err", 500) // 1500 bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, longErr)
	}))
	defer srv.Close()

	_, err := generateGemini(context.Background(), Params{
		Prompt:   "a cat",
		Model:    "gemini-3.1-flash-image-preview",
		Endpoint: srv.URL,
		Protocol: "gemini",
	})
	if err == nil {
		t.Fatal("expected error from 400 response")
	}
	msg := err.Error()
	if !strings.Contains(msg, "...") {
		t.Fatalf("error %q does not contain truncation marker", msg)
	}
	if len(msg) > 700 {
		t.Fatalf("error length %d exceeds truncation budget (msg starts %q)", len(msg), msg[:80])
	}
	if !strings.Contains(msg, `model "gemini-3.1-flash-image-preview"`) || !strings.Contains(msg, srv.URL) {
		t.Fatalf("error %q does not carry model and endpoint", msg)
	}
}

// TestGenerateGeminiEmptyModel guards the optional account default model: an
// empty model must fail with an actionable message, not a cryptic 404 from
// /v1beta/models/:generateContent.
func TestGenerateGeminiEmptyModel(t *testing.T) {
	_, err := generateGemini(context.Background(), Params{
		Prompt:   "a cat",
		Endpoint: "https://generativelanguage.googleapis.com",
		Protocol: "gemini",
	})
	if err == nil {
		t.Fatal("expected error for empty model")
	}
	if !strings.Contains(err.Error(), "no image model configured") {
		t.Fatalf("error %q does not mention missing model config", err)
	}
}

// TestGenerateGeminiWithInputImage is the single-image compatibility
// regression: InputImage keeps producing exactly one inlineData part plus the
// prompt text part, with the imageConfig mapping applied.
func TestGenerateGeminiWithInputImage(t *testing.T) {
	imgData := "fake-png"
	var req geminiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode gemini request: %v", err)
		}
		// Return a valid Gemini response containing the image.
		w.Header().Set("Content-Type", "application/json")
		b64 := base64.StdEncoding.EncodeToString([]byte(imgData))
		fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"data":"%s","mimeType":"image/png"}}]}}]}`, b64)
	}))
	defer srv.Close()

	res, err := generateGemini(context.Background(), Params{
		Prompt:      "a cat",
		Model:       "gemini-3.1-flash-image-preview",
		Endpoint:    srv.URL,
		Protocol:    "gemini",
		Size:        "2K",
		AspectRatio: "16:9",
		InputImage:  base64.StdEncoding.EncodeToString([]byte("source-png")),
	})
	if err != nil {
		t.Fatalf("generateGemini: %v", err)
	}
	if string(res.Data) != imgData {
		t.Fatalf("result data = %q, want %q", res.Data, imgData)
	}
	if res.MimeType != "image/png" {
		t.Fatalf("result mime = %q, want image/png", res.MimeType)
	}

	parts := req.Contents[0].Parts
	if len(parts) != 2 {
		t.Fatalf("parts = %d, want 2 (one inline image + prompt text)", len(parts))
	}
	if parts[0].InlineData == nil || parts[0].InlineData.Data != base64.StdEncoding.EncodeToString([]byte("source-png")) {
		t.Fatalf("part[0] = %+v, want InputImage inlineData", parts[0])
	}
	if parts[0].InlineData.MimeType != "image/png" {
		t.Fatalf("part[0] mime = %q, want image/png", parts[0].InlineData.MimeType)
	}
	if parts[1].Text != "a cat" || parts[1].InlineData != nil {
		t.Fatalf("part[1] = %+v, want prompt text part", parts[1])
	}
	if cfg := req.GenerationConfig.ImageConfig; cfg.ImageSize != "2K" || cfg.AspectRatio != "16:9" {
		t.Fatalf("imageConfig = %+v, want imageSize 2K aspectRatio 16:9", cfg)
	}
}

// TestGenerateGeminiMultiReferenceImages locks multi-reference support: every
// base64 reference becomes one inlineData part (data URLs contribute their
// declared mime type), URL references are skipped (inlineData is base64 only),
// and the prompt remains the final text part.
func TestGenerateGeminiMultiReferenceImages(t *testing.T) {
	refA := base64.StdEncoding.EncodeToString([]byte("ref-a"))
	refB := base64.StdEncoding.EncodeToString([]byte("ref-b"))
	var req geminiRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode gemini request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		out := base64.StdEncoding.EncodeToString([]byte("out"))
		fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"data":"%s","mimeType":"image/png"}}]}}]}`, out)
	}))
	defer srv.Close()

	res, err := generateGemini(context.Background(), Params{
		Prompt:   "a cat",
		Model:    "gemini-3.1-flash-image-preview",
		Endpoint: srv.URL,
		Protocol: "gemini",
		ReferenceImages: []string{
			refA,
			"data:image/jpeg;base64," + refB,
			"https://example.com/ref.png", // URL: skipped, cannot ride inlineData
		},
	})
	if err != nil {
		t.Fatalf("generateGemini: %v", err)
	}
	if string(res.Data) != "out" {
		t.Fatalf("result data = %q, want %q", res.Data, "out")
	}

	parts := req.Contents[0].Parts
	if len(parts) != 3 {
		t.Fatalf("parts = %d, want 3 (two inline images + prompt text)", len(parts))
	}
	if parts[0].InlineData == nil || parts[0].InlineData.Data != refA {
		t.Fatalf("part[0] = %+v, want raw-base64 ref-a inlineData", parts[0])
	}
	if parts[0].InlineData.MimeType != "image/png" {
		t.Fatalf("part[0] mime = %q, want image/png", parts[0].InlineData.MimeType)
	}
	if parts[1].InlineData == nil || parts[1].InlineData.Data != refB {
		t.Fatalf("part[1] = %+v, want data-URL ref-b payload", parts[1])
	}
	if parts[1].InlineData.MimeType != "image/jpeg" {
		t.Fatalf("part[1] mime = %q, want image/jpeg from data URL prefix", parts[1].InlineData.MimeType)
	}
	if parts[2].Text != "a cat" || parts[2].InlineData != nil {
		t.Fatalf("part[2] = %+v, want prompt text part", parts[2])
	}
	if mods := req.GenerationConfig.ResponseModalities; len(mods) != 2 || mods[0] != "TEXT" || mods[1] != "IMAGE" {
		t.Fatalf("responseModalities = %v, want [TEXT IMAGE]", mods)
	}
}
