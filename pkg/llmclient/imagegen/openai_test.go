package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTruncate(t *testing.T) {
	short := strings.Repeat("a", 100)
	if got := truncate(short); got != short {
		t.Fatalf("truncate(short) = %q, want unchanged", got)
	}
	exact := strings.Repeat("c", 512)
	if got := truncate(exact); got != exact {
		t.Fatalf("truncate(512 bytes) should be unchanged, got len %d", len(got))
	}
	long := strings.Repeat("b", 1000)
	got := truncate(long)
	wantLen := 512 + len("...")
	if len(got) != wantLen {
		t.Fatalf("truncate(long) length = %d, want %d", len(got), wantLen)
	}
	if got[:512] != long[:512] {
		t.Fatal("truncate(long) must keep the first 512 bytes")
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("truncate(long) = %q..., want \"...\" suffix", got)
	}
}

// TestHTTPClientNoClientTimeout locks the acceptance criterion that the
// non-proxy path is bounded by the caller's context, not by a client-level
// timeout that would preempt the ctx budget.
func TestHTTPClientNoClientTimeout(t *testing.T) {
	if httpClient.Timeout != 0 {
		t.Fatalf("httpClient.Timeout = %v, want 0 (ctx governs call budget)", httpClient.Timeout)
	}
}

func TestMapOpenAISize(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		size   string
		aspect string
		want   string
	}{
		{"gpt-image-1 square", "gpt-image-1", "1K", "1:1", "1024x1024"},
		{"gpt-image-1 landscape", "gpt-image-1", "1K", "16:9", "1536x1024"},
		{"gpt-image-1 portrait", "gpt-image-1", "1K", "9:16", "1024x1536"},
		{"gpt-image-1 no aspect", "gpt-image-1", "1K", "", "1024x1024"},
		{"no model square", "", "2K", "1:1", "1024x1024"},
		{"no model landscape", "", "2K", "16:9", "1536x1024"},
		{"no model portrait", "", "4K", "9:16", "1024x1536"},
		{"no model no aspect", "", "", "", "1024x1024"},
		{"unknown size landscape", "gpt-image-1", "8K", "16:9", "1536x1024"},
		{"empty size landscape", "gpt-image-1", "", "16:9", "1536x1024"},
		// gpt-image-2 rejects every explicit size on relay deployments; the
		// field must be omitted so the server default ("auto") applies.
		{"gpt-image-2 square", "gpt-image-2", "1K", "1:1", ""},
		{"gpt-image-2 landscape", "gpt-image-2", "2K", "16:9", ""},
		{"gpt-image-2-pro portrait", "gpt-image-2-pro", "1K", "9:16", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapOpenAISize(tc.model, tc.size, tc.aspect); got != tc.want {
				t.Fatalf("mapOpenAISize(%q, %q, %q) = %q, want %q", tc.model, tc.size, tc.aspect, got, tc.want)
			}
		})
	}
}

func TestMapOpenAIQuality(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"fast", "low"},
		{"balanced", "medium"},
		{"quality", "high"},
		{"", "low"},
		{"unknown", "low"},
	}
	for _, tc := range tests {
		if got := mapOpenAIQuality(tc.in); got != tc.want {
			t.Fatalf("mapOpenAIQuality(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestOpenAIEndpointPath(t *testing.T) {
	tests := []struct {
		name   string
		images []string
		want   string
	}{
		{"no images", nil, "/images/generations"},
		{"empty images", []string{}, "/images/generations"},
		{"single base64", []string{base64.StdEncoding.EncodeToString([]byte("png"))}, "/images/edits"},
		{"multiple base64", []string{"YWJj", "ZGVm"}, "/images/edits"},
		{"data URL", []string{"data:image/png;base64,YWJj"}, "/images/edits"},
		{"URL-only references degrade to generations", []string{"https://example.com/ref.png"}, "/images/generations"},
	}
	for _, tc := range tests {
		if got := openAIEndpointPath(tc.images); got != tc.want {
			t.Fatalf("openAIEndpointPath(%v) = %q, want %q", tc.images, got, tc.want)
		}
	}
}

// TestSplitReferenceImage locks the base64/data-URL/URL taxonomy shared by the
// gemini inlineData and openai edits multipart paths.
func TestSplitReferenceImage(t *testing.T) {
	tests := []struct {
		name     string
		ref      string
		wantData string
		wantMime string
		wantOK   bool
	}{
		{"raw base64", "aGVsbG8=", "aGVsbG8=", "image/png", true},
		{"data URL with declared mime", "data:image/jpeg;base64,aGk=", "aGk=", "image/jpeg", true},
		{"data URL default mime", "data:;base64,aGk=", "aGk=", "image/png", true},
		{"data URL without payload", "data:image/png;base64,", "", "", false},
		{"data URL without comma", "data:image/png;base64", "", "", false},
		{"http URL", "http://example.com/ref.png", "", "", false},
		{"https URL", "https://example.com/ref.png", "", "", false},
		{"empty", "", "", "image/png", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, mime, ok := splitReferenceImage(tc.ref)
			if data != tc.wantData || mime != tc.wantMime || ok != tc.wantOK {
				t.Fatalf("splitReferenceImage(%q) = (%q, %q, %v), want (%q, %q, %v)", tc.ref, data, mime, ok, tc.wantData, tc.wantMime, tc.wantOK)
			}
		})
	}
}

// TestBase64ReferenceImages locks the openai base64-subset filter: URL
// references are dropped, data-URL prefixes stripped, InputImage merged first.
func TestBase64ReferenceImages(t *testing.T) {
	got := base64ReferenceImages([]string{
		"https://example.com/a.png",   // dropped
		"aGVsbG8=",                    // kept
		"data:image/jpeg;base64,aGk=", // stripped to payload
	})
	want := []string{"aGVsbG8=", "aGk="}
	if len(got) != len(want) {
		t.Fatalf("base64ReferenceImages = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("base64ReferenceImages = %v, want %v", got, want)
		}
	}
	if got := base64ReferenceImages([]string{"https://example.com/a.png"}); len(got) != 0 {
		t.Fatalf("URL-only references must yield an empty subset, got %v", got)
	}
}

// TestGenerateOpenAIRouting drives generateOpenAI against a stub server and
// asserts the request lands on /images/generations (JSON) when no base64
// reference is available, and on /images/edits (multipart carrying every base64
// reference as a form file) when one or more are. URL-only references cannot
// enter edits multipart, so they degrade to generations without images.
func TestGenerateOpenAIRouting(t *testing.T) {
	const imgBytes = "fake-png-bytes"
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	tests := []struct {
		name       string
		inputImage string
		refImages  []string
		wantPath   string
		wantMP     bool
		wantFiles  map[string][]string // form-field name → decoded payloads
	}{
		{"generations without input image", "", nil, "/images/generations", false, nil},
		{"edits with single input image", b64(imgBytes), nil, "/images/edits", true, map[string][]string{"image": {imgBytes}}},
		{"edits with two reference images", "", []string{b64("ref-a"), b64("ref-b")}, "/images/edits", true, map[string][]string{"image[]": {"ref-a", "ref-b"}}},
		{"edits mixing input image and references", b64("main"), []string{b64("side-a"), b64("side-b")}, "/images/edits", true, map[string][]string{"image[]": {"main", "side-a", "side-b"}}},
		{"edits with data URL reference", "", []string{"data:image/jpeg;base64," + b64("jpg")}, "/images/edits", true, map[string][]string{"image": {"jpg"}}},
		{"URL-only references degrade to generations", "", []string{"https://example.com/ref.png"}, "/images/generations", false, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotContentType string
			var gotBody []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotContentType = r.Header.Get("Content-Type")
				gotBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"data":[{"b64_json":%q}]}`, base64.StdEncoding.EncodeToString([]byte("out")))
			}))
			defer srv.Close()

			res, err := generateOpenAI(context.Background(), Params{
				Model:           "gpt-image-1",
				Prompt:          "a cat",
				Endpoint:        srv.URL,
				Protocol:        "openai",
				Quality:         "balanced",
				Size:            "2K",
				AspectRatio:     "16:9",
				InputImage:      tc.inputImage,
				ReferenceImages: tc.refImages,
			})
			if err != nil {
				t.Fatalf("generateOpenAI: %v", err)
			}
			if string(res.Data) != "out" {
				t.Fatalf("result data = %q, want %q", res.Data, "out")
			}
			if gotPath != tc.wantPath {
				t.Fatalf("request path = %q, want %q", gotPath, tc.wantPath)
			}
			isMP := strings.HasPrefix(gotContentType, "multipart/form-data;")
			if isMP != tc.wantMP {
				t.Fatalf("request is multipart = %v, want %v (content-type %q)", isMP, tc.wantMP, gotContentType)
			}

			if tc.wantMP {
				if !strings.Contains(gotContentType, "boundary=") {
					t.Fatalf("multipart content-type %q missing boundary", gotContentType)
				}
				_, params, err := mime.ParseMediaType(gotContentType)
				if err != nil {
					t.Fatalf("parse media type: %v", err)
				}
				fields, files := parseMultipart(t, gotBody, params["boundary"])
				if len(files) != len(tc.wantFiles) {
					t.Fatalf("multipart files = %v, want %v", files, tc.wantFiles)
				}
				for name, want := range tc.wantFiles {
					got := files[name]
					if len(got) != len(want) {
						t.Fatalf("multipart file %q has %d parts, want %d", name, len(got), len(want))
					}
					for i := range want {
						if string(got[i]) != want[i] {
							t.Fatalf("multipart file %q part %d = %q, want %q", name, i, got[i], want[i])
						}
					}
				}
				wantFields := map[string]string{
					"model":           "gpt-image-1",
					"prompt":          "a cat",
					"n":               "1",
					"size":            "1536x1024",
					"quality":         "medium",
					"response_format": "png",
				}
				for k, want := range wantFields {
					if fields[k] != want {
						t.Fatalf("multipart field %q = %q, want %q", k, fields[k], want)
					}
				}
			} else {
				var req openaiImageRequest
				if err := json.Unmarshal(gotBody, &req); err != nil {
					t.Fatalf("generations body is not JSON: %v", err)
				}
				if req.Model != "gpt-image-1" || req.Prompt != "a cat" || req.N != 1 {
					t.Fatalf("generations request = %+v, want model gpt-image-1 prompt 'a cat' n=1", req)
				}
				if req.Size != "1536x1024" || req.Quality != "medium" || req.OutputFormat != "png" {
					t.Fatalf("generations request = %+v, want size 1536x1024 quality medium output_format png", req)
				}
			}
		})
	}
}

func TestGenerateOpenAIErrorBodyTruncated(t *testing.T) {
	longErr := strings.Repeat("boom", 500) // 2000 bytes
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, longErr)
	}))
	defer srv.Close()

	_, err := generateOpenAI(context.Background(), Params{
		Model:    "gpt-image-1",
		Prompt:   "p",
		Endpoint: srv.URL,
		Protocol: "openai",
	})
	if err == nil {
		t.Fatal("expected error from 400 response")
	}
	msg := err.Error()
	if !strings.Contains(msg, "...") {
		t.Fatalf("error %q does not contain truncation marker", msg)
	}
	if len(msg) > 600 {
		t.Fatalf("error length %d exceeds truncation budget (msg starts %q)", len(msg), msg[:80])
	}
	if strings.Contains(msg[:len(msg)-3], longErr[512:]) {
		t.Fatal("error contains data beyond the 512-byte truncation window")
	}
}

func TestGenerateOpenAIEditsInvalidBase64(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("request must not reach the server when the input image is invalid base64")
	}))
	defer srv.Close()

	_, err := generateOpenAI(context.Background(), Params{
		Model:      "gpt-image-1",
		Prompt:     "p",
		Endpoint:   srv.URL,
		Protocol:   "openai",
		InputImage: "%%%not-base64%%%",
	})
	if err == nil {
		t.Fatal("expected decode error for invalid base64 input image")
	}
	if !strings.Contains(err.Error(), "decode input image") {
		t.Fatalf("error %q does not mention input image decode", err)
	}
}

// TestGenerateOpenAI_URLResponse locks that a data[0].url answer (GLM cogview,
// dall-e, most relays) is downloaded instead of failing as "no image data".
func TestGenerateOpenAI_URLResponse(t *testing.T) {
	imageBytes := []byte("fake-png-bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/generations":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"data":[{"url":"%s/img"}]}`, srvURL(r))
		case "/img":
			w.Header().Set("Content-Type", "image/png")
			w.Write(imageBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	res, err := generateOpenAI(context.Background(), Params{
		Model:    "cogview-4",
		Prompt:   "a cat",
		Endpoint: srv.URL,
		Protocol: "openai",
	})
	if err != nil {
		t.Fatalf("generateOpenAI: %v", err)
	}
	if string(res.Data) != string(imageBytes) {
		t.Fatalf("image bytes = %q, want %q", res.Data, imageBytes)
	}
}

// srvURL rebuilds the server's base URL from an in-flight request (the server
// variable is not assigned yet when the handler closure runs).
func srvURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// TestGenerateOpenAI_ChatFallback locks the OpenRouter-style relay path: the
// images endpoints answer 404 and the image arrives via /chat/completions as
// message.images; base64 reference images ride as image_url data URLs.
func TestGenerateOpenAI_ChatFallback(t *testing.T) {
	imgData := []byte("nano-banana-bytes")
	b64 := base64.StdEncoding.EncodeToString(imgData)
	var chatReq chatImageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/generations":
			http.NotFound(w, r)
		case "/chat/completions":
			if err := json.NewDecoder(r.Body).Decode(&chatReq); err != nil {
				t.Fatalf("decode chat request: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"choices":[{"message":{"content":"ok","images":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}}]}`, b64)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	res, err := generateOpenAI(context.Background(), Params{
		Model:      "google/gemini-2.5-flash-image",
		Prompt:     "a cat",
		Endpoint:   srv.URL,
		Protocol:   "openai",
		InputImage: base64.StdEncoding.EncodeToString([]byte("ref")),
	})
	if err != nil {
		t.Fatalf("generateOpenAI: %v", err)
	}
	if string(res.Data) != string(imgData) {
		t.Fatalf("image bytes = %q, want %q", res.Data, imgData)
	}
	if res.MimeType != "image/png" {
		t.Fatalf("mime = %q, want image/png", res.MimeType)
	}
	if len(chatReq.Messages) != 1 || chatReq.Model != "google/gemini-2.5-flash-image" {
		t.Fatalf("chat request = %+v, want one user message pinned to the model", chatReq)
	}
	parts := chatReq.Messages[0].Content
	if len(parts) != 2 || parts[0].Type != "text" || parts[0].Text != "a cat" {
		t.Fatalf("chat content = %+v, want text prompt first", parts)
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || !strings.HasPrefix(parts[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("reference part = %+v, want image_url data URL", parts[1])
	}
}

// TestGenerateOpenAI_ChatFallbackContentParts locks the multimodal content-part
// response shape (OpenRouter native) and that https image URLs are downloaded.
func TestGenerateOpenAI_ChatFallbackContentParts(t *testing.T) {
	imageBytes := []byte("remote-image")
	var chatPathHit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/images/generations":
			http.NotFound(w, r)
		case "/chat/completions":
			chatPathHit = true
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"choices":[{"message":{"content":[{"type":"text","text":"here"},{"type":"image_url","image_url":{"url":"%s/img"}}]}}]}`, srvURL(r))
		case "/img":
			w.Write(imageBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	res, err := generateOpenAI(context.Background(), Params{
		Model:    "google/gemini-3-pro-image",
		Prompt:   "a cat",
		Endpoint: srv.URL,
		Protocol: "openai",
	})
	if err != nil {
		t.Fatalf("generateOpenAI: %v", err)
	}
	if !chatPathHit {
		t.Fatal("chat fallback was not taken after 404")
	}
	if string(res.Data) != string(imageBytes) {
		t.Fatalf("image bytes = %q, want %q", res.Data, imageBytes)
	}
}

// TestGenerateOpenAI_ErrorCarriesModelAndEndpoint locks error attribution:
// every upstream failure names the model and the endpoint it hit.
func TestGenerateOpenAI_ErrorCarriesModelAndEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":{"message":"insufficient quota"}}`)
	}))
	defer srv.Close()

	_, err := generateOpenAI(context.Background(), Params{
		Model:    "google/gemini-2.5-flash-image",
		Prompt:   "p",
		Endpoint: srv.URL,
		Protocol: "openai",
	})
	if err == nil {
		t.Fatal("expected 403 error")
	}
	msg := err.Error()
	if !strings.Contains(msg, `model "google/gemini-2.5-flash-image"`) || !strings.Contains(msg, srv.URL) {
		t.Fatalf("error %q does not carry model and endpoint", msg)
	}
	if !strings.Contains(msg, "403") {
		t.Fatalf("error %q does not carry status code", msg)
	}
}

// parseMultipart reads a multipart body and returns its form fields and file
// parts, keyed by part name with every file part appended in order (a single
// part name may occur multiple times, e.g. edits' multi-image "image[]").
func parseMultipart(t *testing.T, body []byte, boundary string) (fields map[string]string, files map[string][][]byte) {
	t.Helper()
	fields = map[string]string{}
	files = map[string][][]byte{}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read multipart part: %v", err)
		}
		data, err := io.ReadAll(part)
		if err != nil {
			t.Fatalf("read multipart part body: %v", err)
		}
		if part.FileName() != "" {
			files[part.FormName()] = append(files[part.FormName()], data)
		} else {
			fields[part.FormName()] = string(data)
		}
	}
	return fields, files
}
