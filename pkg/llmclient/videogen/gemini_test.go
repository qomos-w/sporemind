package videogen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func setFastPoll(t *testing.T) {
	t.Helper()
	prev := pollInterval
	pollInterval = 5 * time.Millisecond
	t.Cleanup(func() { pollInterval = prev })
}

// TestNormalizeGeminiEndpoint pins the same relay-BaseURL immunity as imagegen:
// trailing /v1 or /v1beta is stripped before the native /v1beta path appends.
func TestNormalizeGeminiEndpoint(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", geminiDefaultEndpoint},
		{"http://127.0.0.1:8045/", "http://127.0.0.1:8045"},
		{"http://127.0.0.1:8045/v1", "http://127.0.0.1:8045"},
		{"https://rolldek.com/v1beta", "https://rolldek.com"},
		{"https://rolldek.com/api", "https://rolldek.com/api"},
	}
	for _, tc := range cases {
		if got := normalizeGeminiEndpoint(tc.in); got != tc.want {
			t.Errorf("normalizeGeminiEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGenerateGemini_Base64Result(t *testing.T) {
	setFastPoll(t)
	polls := 0
	var submitPath, submitAuth string
	var submitted struct {
		Instances []map[string]string `json:"instances"`
		Parameters struct {
			AspectRatio     string `json:"aspectRatio"`
			DurationSeconds int    `json:"durationSeconds"`
		} `json:"parameters"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ":predictLongRunning"):
			submitPath = r.URL.Path
			submitAuth = r.Header.Get("x-goog-api-key")
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Errorf("decode submit body: %v", err)
			}
			_, _ = w.Write([]byte(`{"name":"models/veo-3.0-generate-001/operations/op-1","done":false}`))
		case strings.Contains(r.URL.Path, "/operations/op-1"):
			polls++
			if polls < 2 {
				_, _ = w.Write([]byte(`{"name":"models/veo-3.0-generate-001/operations/op-1","done":false}`))
				return
			}
			_, _ = w.Write([]byte(`{"name":"models/veo-3.0-generate-001/operations/op-1","done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"bytesBase64Encoded":"dmlkZW8tYnl0ZXM=","mimeType":"video/mp4"}}]}}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	result, err := GenerateGemini(context.Background(), Params{
		Prompt:      "a city at night",
		Model:       "veo-3.0-generate-001",
		Endpoint:    server.URL,
		AuthToken:   "gemini-key",
		Duration:    5,
		AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if submitPath != "/v1beta/models/veo-3.0-generate-001:predictLongRunning" {
		t.Fatalf("submit path = %q", submitPath)
	}
	if submitAuth != "gemini-key" {
		t.Fatalf("auth header = %q, want gemini-key", submitAuth)
	}
	if len(submitted.Instances) != 1 || submitted.Instances[0]["prompt"] != "a city at night" {
		t.Fatalf("unexpected instances: %+v", submitted.Instances)
	}
	if submitted.Parameters.AspectRatio != "16:9" || submitted.Parameters.DurationSeconds != 5 {
		t.Fatalf("unexpected parameters: %+v", submitted.Parameters)
	}
	if polls != 2 {
		t.Fatalf("polls = %d, want 2", polls)
	}
	if string(result.Data) != "video-bytes" {
		t.Fatalf("data = %q, want video-bytes", result.Data)
	}
	if result.MimeType != "video/mp4" {
		t.Fatalf("mime = %q", result.MimeType)
	}
}

func TestGenerateGemini_URLResult(t *testing.T) {
	setFastPoll(t)
	fileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("mp4-bytes"))
	}))
	defer fileServer.Close()

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":predictLongRunning") {
			_, _ = w.Write([]byte(`{"name":"models/veo-3.0-generate-001/operations/op-2","done":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"models/veo-3.0-generate-001/operations/op-2","done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"uri":"` + fileServer.URL + `/video.mp4"}}]}}}`))
	}))
	defer apiServer.Close()

	result, err := GenerateGemini(context.Background(), Params{
		Prompt:    "a poster",
		Model:     "veo-3.0-generate-001",
		Endpoint:  apiServer.URL,
		AuthToken: "gemini-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Data) != "mp4-bytes" {
		t.Fatalf("data = %q, want mp4-bytes", result.Data)
	}
	if result.URL != fileServer.URL+"/video.mp4" {
		t.Fatalf("url = %q", result.URL)
	}
	if result.MimeType != "video/mp4" {
		t.Fatalf("mime = %q", result.MimeType)
	}
}

func TestGenerateGemini_GcsUriReturnsClearError(t *testing.T) {
	setFastPoll(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":predictLongRunning") {
			_, _ = w.Write([]byte(`{"name":"models/veo-3.0-generate-001/operations/op-3","done":false}`))
			return
		}
		_, _ = w.Write([]byte(`{"name":"models/veo-3.0-generate-001/operations/op-3","done":true,"response":{"uri":"gs://bucket/object.mp4"}}`))
	}))
	defer server.Close()

	_, err := GenerateGemini(context.Background(), Params{
		Prompt:    "a clip",
		Model:     "veo-3.0-generate-001",
		Endpoint:  server.URL,
		AuthToken: "gemini-key",
	})
	if err == nil || !strings.Contains(err.Error(), "gs://") {
		t.Fatalf("expected gs:// error, got %v", err)
	}
}

func TestGenerateGemini_RequiresModel(t *testing.T) {
	if _, err := GenerateGemini(context.Background(), Params{Prompt: "x"}); err == nil {
		t.Fatal("expected model-required error")
	}
}
