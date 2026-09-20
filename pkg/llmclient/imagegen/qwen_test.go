package imagegen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// shortenQwenPoll makes polling effectively immediate for tests and restores
// the production cadence afterwards.
func shortenQwenPoll(t *testing.T) {
	t.Helper()
	prev := qwenPollInterval
	qwenPollInterval = time.Millisecond
	t.Cleanup(func() { qwenPollInterval = prev })
}

// TestGenerateQwenAsyncTask locks the default-model asynchronous task flow of
// the qwen-image-3.0 family: submit to the image-generation endpoint with an
// X-DashScope-Async header and a messages payload, poll the returned task id
// until SUCCEEDED, then download the image URL from the task result.
func TestGenerateQwenAsyncTask(t *testing.T) {
	shortenQwenPoll(t)
	imageBytes := []byte{0x89, 0x50, 0x4e, 0x47}
	var (
		gotSubmitPath, gotSubmitAuth, gotSubmitAsync string
		gotSubmitBody                                qwenSubmitRequest
		gotPollPath, gotPollAuth, gotPollAsync       string
		pollHits                                     int
	)
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case qwenGenerationPath:
			gotSubmitPath, gotSubmitAuth = r.URL.Path, r.Header.Get("Authorization")
			gotSubmitAsync = r.Header.Get("X-DashScope-Async")
			if err := json.NewDecoder(r.Body).Decode(&gotSubmitBody); err != nil {
				t.Errorf("decode submit: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"output":     map[string]any{"task_id": "task-1", "task_status": "PENDING"},
				"request_id": "req-1",
			})
		case qwenTasksPath + "task-1":
			gotPollPath, gotPollAuth = r.URL.Path, r.Header.Get("Authorization")
			gotPollAsync = r.Header.Get("X-DashScope-Async")
			pollHits++
			if pollHits == 1 {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"output": map[string]any{"task_id": "task-1", "task_status": "RUNNING"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"output": map[string]any{
					"task_id":     "task-1",
					"task_status": "SUCCEEDED",
					"choices": []any{map[string]any{
						"finish_reason": "stop",
						"message": map[string]any{
							"role":    "assistant",
							"content": []any{map[string]any{"image": srvURL + "/out.png", "type": "image"}},
						},
					}},
				},
			})
		case "/out.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBytes)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srvURL = srv.URL
	defer srv.Close()

	result, err := Generate(context.Background(), Params{
		Protocol:    "qwen",
		Endpoint:    srv.URL,
		AuthToken:   "k1",
		Prompt:      "a cat",
		Size:        "1K",
		AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if string(result.Data) != string(imageBytes) || result.MimeType != "image/png" {
		t.Fatalf("result = %d bytes %q", len(result.Data), result.MimeType)
	}
	if gotSubmitPath != qwenGenerationPath {
		t.Fatalf("submit path = %q", gotSubmitPath)
	}
	if gotSubmitAuth != "Bearer k1" {
		t.Fatalf("submit auth = %q", gotSubmitAuth)
	}
	if gotSubmitAsync != "enable" {
		t.Fatalf("submit X-DashScope-Async = %q, want enable", gotSubmitAsync)
	}
	if gotSubmitBody.Model != qwenDefaultImageModel {
		t.Fatalf("model = %q, want the qwen default", gotSubmitBody.Model)
	}
	if gotSubmitBody.Parameters.Size != "1024*576" {
		t.Fatalf("size = %q, want 1024*576", gotSubmitBody.Parameters.Size)
	}
	if gotSubmitBody.Parameters.N != 1 || !gotSubmitBody.Parameters.PromptExtend || gotSubmitBody.Parameters.Watermark {
		t.Fatalf("parameters = %+v", gotSubmitBody.Parameters)
	}
	if len(gotSubmitBody.Input.Messages) != 1 || gotSubmitBody.Input.Prompt != "" {
		t.Fatalf("input = %+v", gotSubmitBody.Input)
	}
	msg := gotSubmitBody.Input.Messages[0]
	if msg.Role != "user" || len(msg.Content) != 1 || msg.Content[0].Text != "a cat" {
		t.Fatalf("messages = %+v", msg)
	}
	if gotPollPath != qwenTasksPath+"task-1" || gotPollAuth != "Bearer k1" {
		t.Fatalf("poll = %q auth %q", gotPollPath, gotPollAuth)
	}
	if gotPollAsync != "" {
		t.Fatalf("poll must not carry the async header, got %q", gotPollAsync)
	}
	if pollHits != 2 {
		t.Fatalf("poll hits = %d, want 2 (PENDING then SUCCEEDED)", pollHits)
	}
}

// TestGenerateQwenLegacySynthesis locks the legacy asynchronous text-to-image
// route (input.prompt on text2image/image-synthesis) and the results[].url
// result shape.
func TestGenerateQwenLegacySynthesis(t *testing.T) {
	shortenQwenPoll(t)
	var (
		gotPath string
		gotBody qwenSubmitRequest
	)
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case qwenSynthesisPath:
			gotPath = r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode submit: %v", err)
			}
			_, _ = w.Write([]byte(`{"output":{"task_id":"t9","task_status":"PENDING"}}`))
		case qwenTasksPath + "t9":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"output": map[string]any{
					"task_id":     "t9",
					"task_status": "SUCCEEDED",
					"results":     []any{map[string]any{"url": srvURL + "/legacy.png"}},
				},
			})
		case "/legacy.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{1, 2, 3})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srvURL = srv.URL
	defer srv.Close()

	result, err := Generate(context.Background(), Params{
		Protocol:    "qwen",
		Endpoint:    srv.URL + "/compatible-mode/v1", // OpenAI-style suffix must be trimmed
		AuthToken:   "k",
		Prompt:      "a dog",
		Model:       "wanx2.1-t2i-turbo",
		AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(result.Data) != 3 {
		t.Fatalf("data = %v", result.Data)
	}
	if gotPath != qwenSynthesisPath {
		t.Fatalf("path = %q", gotPath)
	}
	if gotBody.Input.Prompt != "a dog" || len(gotBody.Input.Messages) != 0 {
		t.Fatalf("input = %+v", gotBody.Input)
	}
	if gotBody.Parameters.Size != "1664*928" {
		t.Fatalf("size = %q, want the legacy 16:9 resolution", gotBody.Parameters.Size)
	}
}

// TestGenerateQwenReferenceImages locks the synchronous multimodal route used
// whenever reference images ride along: the image must be normalized to a data
// URL, the prompt follow it, and no async header be sent (the endpoint is
// synchronous, so the image resolves from the submit response).
func TestGenerateQwenReferenceImages(t *testing.T) {
	shortenQwenPoll(t)
	var (
		gotPath, gotAsync string
		gotBody           qwenSubmitRequest
	)
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case qwenMultimodalPath:
			gotPath, gotAsync = r.URL.Path, r.Header.Get("X-DashScope-Async")
			if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
				t.Errorf("decode submit: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"output": map[string]any{"choices": []any{map[string]any{
					"finish_reason": "stop",
					"message": map[string]any{
						"role":    "assistant",
						"content": []any{map[string]any{"image": srvURL + "/edit.png"}},
					},
				}}},
			})
		case "/edit.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{7})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srvURL = srv.URL
	defer srv.Close()

	result, err := Generate(context.Background(), Params{
		Protocol:        "qwen",
		Endpoint:        srv.URL,
		AuthToken:       "k",
		Prompt:          "make it night",
		Model:           "qwen-image-plus",
		ReferenceImages: []string{"QUJD", "https://example.com/ref.png"},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(result.Data) != 1 {
		t.Fatalf("data = %v", result.Data)
	}
	if gotPath != qwenMultimodalPath {
		t.Fatalf("path = %q, want the multimodal endpoint", gotPath)
	}
	if gotAsync != "" {
		t.Fatalf("multimodal submit must not request async, got %q", gotAsync)
	}
	content := gotBody.Input.Messages[0].Content
	if len(content) != 3 {
		t.Fatalf("content = %+v", content)
	}
	if content[0].Image != "data:image/png;base64,QUJD" {
		t.Fatalf("raw base64 ref = %q, want a data url", content[0].Image)
	}
	if content[1].Image != "https://example.com/ref.png" {
		t.Fatalf("url ref = %q", content[1].Image)
	}
	if content[2].Text != "make it night" {
		t.Fatalf("prompt entry = %+v", content[2])
	}
	if gotBody.Parameters.Size != "1664*928" {
		t.Fatalf("size = %q, want the legacy default resolution", gotBody.Parameters.Size)
	}
}

// TestGenerateQwenSyncOnlyModel locks the sync-only legacy family (qwen-image-2.0)
// onto the multimodal endpoint even without reference images.
func TestGenerateQwenSyncOnlyModel(t *testing.T) {
	shortenQwenPoll(t)
	var gotPath string
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case qwenMultimodalPath:
			gotPath = r.URL.Path
			_, _ = w.Write([]byte(`{"output":{"choices":[{"message":{"content":[{"image":"` + srvURL + `/x.png"}]}}]}}`))
		case "/x.png":
			_, _ = w.Write([]byte{9})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srvURL = srv.URL
	defer srv.Close()

	if _, err := Generate(context.Background(), Params{
		Protocol: "qwen", Endpoint: srv.URL, AuthToken: "k",
		Prompt: "p", Model: "qwen-image-2.0-pro",
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotPath != qwenMultimodalPath {
		t.Fatalf("path = %q", gotPath)
	}
}

// TestGenerateQwenSubmitError locks that a rejected submit surfaces the
// DashScope error code/message instead of a bare envelope decode result.
func TestGenerateQwenSubmitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":"InvalidApiKey","message":"Invalid API-key provided."}`))
	}))
	defer srv.Close()

	_, err := Generate(context.Background(), Params{Protocol: "qwen", Endpoint: srv.URL, AuthToken: "bad", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "InvalidApiKey") {
		t.Fatalf("err = %v", err)
	}
}

// TestGenerateQwenTaskFailure locks the terminal-failure branch of the poll
// loop: FAILED surfaces the task's error code and message.
func TestGenerateQwenTaskFailure(t *testing.T) {
	shortenQwenPoll(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case qwenGenerationPath:
			_, _ = w.Write([]byte(`{"output":{"task_id":"t2","task_status":"PENDING"}}`))
		default:
			_, _ = w.Write([]byte(`{"output":{"task_id":"t2","task_status":"FAILED","code":"InvalidParameter","message":"size is invalid"}}`))
		}
	}))
	defer srv.Close()

	_, err := Generate(context.Background(), Params{Protocol: "qwen", Endpoint: srv.URL, AuthToken: "k", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "InvalidParameter") || !strings.Contains(err.Error(), "size is invalid") {
		t.Fatalf("err = %v", err)
	}
}

// TestGenerateQwenNoTaskID locks that an unrecognized submit response fails
// loudly rather than downloading nothing.
func TestGenerateQwenNoTaskID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"request_id":"r1"}`))
	}))
	defer srv.Close()

	_, err := Generate(context.Background(), Params{Protocol: "qwen", Endpoint: srv.URL, AuthToken: "k", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "no task id") {
		t.Fatalf("err = %v", err)
	}
}

// TestGenerateQwenHTTPError locks that a non-200 submit response carries the
// (truncated) provider body.
func TestGenerateQwenHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"code":"InvalidApiKey"}`))
	}))
	defer srv.Close()

	_, err := Generate(context.Background(), Params{Protocol: "qwen", Endpoint: srv.URL, AuthToken: "bad", Prompt: "p"})
	if err == nil || !strings.Contains(err.Error(), "http 401") {
		t.Fatalf("err = %v", err)
	}
}

func TestQwenSubmitShapeFor(t *testing.T) {
	cases := []struct {
		model  string
		refs   bool
		want   qwenSubmitShape
		reason string
	}{
		{"qwen-image-3.0-pro", false, qwenShapeGeneration, "3.0 family is the async generation route"},
		{"qwen-image-3.0", true, qwenShapeGeneration, "3.0 takes reference images on the async route"},
		{"wan2.7-image-pro", false, qwenShapeGeneration, "wan2.7 image shares the unified route"},
		{"qwen-image-2.0-pro", false, qwenShapeMultimodal, "2.0 is sync-only"},
		{"qwen-image-max", false, qwenShapeMultimodal, "max is sync-only"},
		{"qwen-image-edit", false, qwenShapeMultimodal, "edit is a sync multimodal model"},
		{"qwen-image-plus", false, qwenShapeSynthesis, "plus supports the legacy async route"},
		{"wanx2.1-t2i-turbo", false, qwenShapeSynthesis, "wanx rides the legacy async route"},
		{"qwen-image-plus", true, qwenShapeMultimodal, "references cannot ride the prompt-only endpoint"},
	}
	for _, tc := range cases {
		if got := qwenSubmitShapeFor(tc.model, tc.refs); got != tc.want {
			t.Errorf("qwenSubmitShapeFor(%q, %v) = %v, want %v (%s)", tc.model, tc.refs, got, tc.want, tc.reason)
		}
	}
}

func TestQwenSizes(t *testing.T) {
	custom := []struct {
		size, aspect, want string
	}{
		{"1K", "1:1", "1024*1024"},
		{"1K", "16:9", "1024*576"},
		{"1K", "9:16", "576*1024"},
		{"2K", "16:9", "2048*1152"},
		{"4K", "4:3", "2048*1536"},
		{"4K", "3:4", "1536*2048"},
		{"", "", "1024*1024"},
		{"2K", "weird", "2048*2048"},
	}
	for _, tc := range custom {
		if got := qwenCustomSize(tc.size, tc.aspect); got != tc.want {
			t.Errorf("qwenCustomSize(%q, %q) = %q, want %q", tc.size, tc.aspect, got, tc.want)
		}
	}
	legacy := []struct {
		aspect, want string
	}{
		{"16:9", "1664*928"},
		{"", "1664*928"},
		{"1:1", "1328*1328"},
		{"4:3", "1472*1104"},
		{"3:4", "1104*1472"},
		{"9:16", "928*1664"},
	}
	for _, tc := range legacy {
		// The legacy family validates size against a closed list, so the
		// aspect ratio alone selects the resolution.
		if got := qwenLegacySize(tc.aspect); got != tc.want {
			t.Errorf("qwenLegacySize(%q) = %q, want %q", tc.aspect, got, tc.want)
		}
	}
}

func TestNormalizeQwenEndpoint(t *testing.T) {
	cases := map[string]string{
		"":                                      qwenDefaultEndpoint,
		"https://dashscope.aliyuncs.com":        "https://dashscope.aliyuncs.com",
		"https://dashscope.aliyuncs.com/":       "https://dashscope.aliyuncs.com",
		"https://dashscope.aliyuncs.com/v1":     "https://dashscope.aliyuncs.com",
		"https://dashscope.aliyuncs.com/api/v1": "https://dashscope.aliyuncs.com",
		"https://dashscope.aliyuncs.com/compatible-mode/v1": "https://dashscope.aliyuncs.com",
		" https://dashscope-intl.aliyuncs.com/v1/ ":         "https://dashscope-intl.aliyuncs.com",
	}
	for in, want := range cases {
		if got := normalizeQwenEndpoint(in); got != want {
			t.Errorf("normalizeQwenEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}
