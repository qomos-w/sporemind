package videogen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// qwenSubmitted mirrors the DashScope task-creation body so tests can assert
// the wire field names and the size/duration mapping.
type qwenSubmitted struct {
	Model string `json:"model"`
	Input struct {
		Prompt   string `json:"prompt"`
		ImageURL string `json:"img_url"`
		AudioURL string `json:"audio_url"`
	} `json:"input"`
	Parameters struct {
		Size      string `json:"size"`
		Duration  int    `json:"duration"`
		Watermark bool   `json:"watermark"`
	} `json:"parameters"`
}

// TestNormalizeQwenEndpoint pins the endpoint resolution: the native
// /api/v1 base is used as-is, a bare host gains it, and the OpenAI-compatible
// base stored by media accounts is translated to the native one.
func TestNormalizeQwenEndpoint(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", qwenDefaultEndpoint},
		{"https://dashscope.aliyuncs.com/api/v1", "https://dashscope.aliyuncs.com/api/v1"},
		{"https://dashscope.aliyuncs.com/api/v1/", "https://dashscope.aliyuncs.com/api/v1"},
		{"https://dashscope.aliyuncs.com", "https://dashscope.aliyuncs.com/api/v1"},
		{"https://dashscope-intl.aliyuncs.com", "https://dashscope-intl.aliyuncs.com/api/v1"},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", "https://dashscope.aliyuncs.com/api/v1"},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1/", "https://dashscope.aliyuncs.com/api/v1"},
		{"  https://dashscope.aliyuncs.com/api/v1  ", "https://dashscope.aliyuncs.com/api/v1"},
		{"https://relay.example.com/dashscope/api/v1", "https://relay.example.com/dashscope/api/v1"},
	}
	for _, tc := range cases {
		if got := normalizeQwenEndpoint(tc.in); got != tc.want {
			t.Errorf("normalizeQwenEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestQwenSize pins the ratio → Wan size-token mapping; an unsupported ratio
// must yield an empty size so the provider default applies instead of a
// guessed (and possibly rejected) resolution.
func TestQwenSize(t *testing.T) {
	cases := []struct{ ratio, want string }{
		{"16:9", "1280*720"},
		{"9:16", "720*1280"},
		{"1:1", "960*960"},
		{"4:3", "1104*832"},
		{"3:4", "832*1104"},
		{" 16:9 ", "1280*720"},
		{"", ""},
		{"21:9", ""},
	}
	for _, tc := range cases {
		if got := qwenSize(tc.ratio); got != tc.want {
			t.Errorf("qwenSize(%q) = %q, want %q", tc.ratio, got, tc.want)
		}
	}
}

func TestGenerateQwen_SubmitPollAndDownload(t *testing.T) {
	setFastPoll(t)
	polls := 0
	var submitAuth, submitAsync, pollAsync, downloadAuth, rawSubmitBody string
	var submitted qwenSubmitted

	_, dlURL := newLocalhostServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloadAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("mp4-bytes"))
	}))

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1"+qwenVideoSynthesisPath:
			submitAuth = r.Header.Get("Authorization")
			submitAsync = r.Header.Get("X-DashScope-Async")
			raw, _ := io.ReadAll(r.Body)
			rawSubmitBody = string(raw)
			if err := json.Unmarshal(raw, &submitted); err != nil {
				t.Errorf("decode submit body: %v", err)
			}
			_, _ = w.Write([]byte(`{"request_id":"req-1","output":{"task_id":"task-1","task_status":"PENDING"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/tasks/task-1":
			pollAsync = r.Header.Get("X-DashScope-Async")
			polls++
			if polls < 3 {
				status := "PENDING"
				if polls == 2 {
					status = "RUNNING"
				}
				_, _ = w.Write([]byte(`{"request_id":"req-1","output":{"task_id":"task-1","task_status":"` + status + `"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"request_id":"req-1","output":{"task_id":"task-1","task_status":"SUCCEEDED","video_url":"` + dlURL + `/v.mp4"},"usage":{"video_count":1,"video_duration":5}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer api.Close()

	result, err := GenerateQwen(context.Background(), Params{
		Protocol:        "qwen",
		Prompt:          "一只小猫在月光下奔跑",
		Model:           "wan2.5-t2v-preview",
		Endpoint:        api.URL,
		AuthToken:       "dashscope-key",
		AspectRatio:     "16:9",
		Duration:        5,
		ReferenceImages: []string{"https://example.com/first-frame.png"},
		ReferenceAudios: []string{"https://example.com/track.mp3", "https://example.com/ignored.mp3"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if submitAuth != "Bearer dashscope-key" {
		t.Fatalf("submit auth = %q, want Bearer dashscope-key", submitAuth)
	}
	if submitAsync != "enable" {
		t.Fatalf("submit X-DashScope-Async = %q, want enable", submitAsync)
	}
	if pollAsync != "" {
		t.Fatalf("poll X-DashScope-Async = %q, want empty (async marker is a submit-only header)", pollAsync)
	}
	if downloadAuth != "" {
		t.Fatalf("cross-host download auth = %q, want empty (presigned OSS URLs must not receive Authorization)", downloadAuth)
	}
	if submitted.Model != "wan2.5-t2v-preview" {
		t.Errorf("model = %q", submitted.Model)
	}
	if submitted.Input.Prompt != "一只小猫在月光下奔跑" {
		t.Errorf("input.prompt = %q", submitted.Input.Prompt)
	}
	if submitted.Input.ImageURL != "https://example.com/first-frame.png" {
		t.Errorf("input.img_url = %q, want the first reference image", submitted.Input.ImageURL)
	}
	if submitted.Input.AudioURL != "https://example.com/track.mp3" {
		t.Errorf("input.audio_url = %q, want the first reference audio", submitted.Input.AudioURL)
	}
	if submitted.Parameters.Size != "1280*720" {
		t.Errorf("parameters.size = %q, want 1280*720", submitted.Parameters.Size)
	}
	if submitted.Parameters.Duration != 5 {
		t.Errorf("parameters.duration = %d, want 5", submitted.Parameters.Duration)
	}
	if submitted.Parameters.Watermark {
		t.Errorf("parameters.watermark = true, want false")
	}
	// The scalar choice must be on the wire even when it is the zero value: an
	// omitted field would let the provider default decide silently.
	for _, want := range []string{`"size":"1280*720"`, `"duration":5`, `"watermark":false`} {
		if !strings.Contains(rawSubmitBody, want) {
			t.Errorf("submit body missing %s, got: %s", want, rawSubmitBody)
		}
	}

	if polls != 3 {
		t.Fatalf("polls = %d, want 3", polls)
	}
	if string(result.Data) != "mp4-bytes" {
		t.Fatalf("data = %q, want mp4-bytes", result.Data)
	}
	if result.MimeType != "video/mp4" {
		t.Fatalf("mime = %q, want video/mp4", result.MimeType)
	}
	if result.URL != dlURL+"/v.mp4" {
		t.Fatalf("url = %q, want %q", result.URL, dlURL+"/v.mp4")
	}
}

// TestGenerateQwen_DefaultModel pins the documented default model: an empty
// Params.Model is submitted as the Wan 2.5 text-to-video alias.
func TestGenerateQwen_DefaultModel(t *testing.T) {
	setFastPoll(t)
	var submitted qwenSubmitted
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1" + qwenVideoSynthesisPath:
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Errorf("decode submit body: %v", err)
			}
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-default","task_status":"PENDING"}}`))
		case "/api/v1/tasks/task-default":
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-default","task_status":"SUCCEEDED","video_url":"http://` + r.Host + `/v.mp4"}}`))
		case "/v.mp4":
			_, _ = w.Write([]byte("bytes"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer api.Close()

	if _, err := GenerateQwen(context.Background(), Params{Prompt: "a wave", Endpoint: api.URL, AuthToken: "k"}); err != nil {
		t.Fatal(err)
	}
	if submitted.Model != qwenDefaultModel {
		t.Fatalf("model = %q, want %q", submitted.Model, qwenDefaultModel)
	}
	if submitted.Model != "wan-2.5-t2v-720p" {
		t.Fatalf("qwenDefaultModel = %q, want the wan-2.5-t2v-720p default", qwenDefaultModel)
	}
}

func TestGenerateQwen_SameHostDownloadSendsBearer(t *testing.T) {
	setFastPoll(t)
	var downloadAuth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1" + qwenVideoSynthesisPath:
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-2","task_status":"PENDING"}}`))
		case "/api/v1/tasks/task-2":
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-2","task_status":"SUCCEEDED","video_url":"http://` + r.Host + `/api/v1/file/v.mp4"}}`))
		case "/api/v1/file/v.mp4":
			downloadAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte("same-host-bytes"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer api.Close()

	result, err := GenerateQwen(context.Background(), Params{
		Prompt:    "a wave",
		Model:     "wan2.5-t2v-preview",
		Endpoint:  api.URL,
		AuthToken: "dashscope-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	if downloadAuth != "Bearer dashscope-key" {
		t.Fatalf("same-host download auth = %q, want Bearer dashscope-key", downloadAuth)
	}
	if string(result.Data) != "same-host-bytes" {
		t.Fatalf("data = %q, want same-host-bytes", result.Data)
	}
}

func TestGenerateQwen_FailedStatusTruncatesError(t *testing.T) {
	setFastPoll(t)
	longMsg := strings.Repeat("a", 512) + strings.Repeat("b", 88) // 600 bytes total
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1" + qwenVideoSynthesisPath:
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-fail","task_status":"PENDING"}}`))
		case "/api/v1/tasks/task-fail":
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-fail","task_status":"FAILED","code":"InvalidParameter","message":"` + longMsg + `"}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer api.Close()

	_, err := GenerateQwen(context.Background(), Params{
		Prompt:    "x",
		Model:     "wan2.5-t2v-preview",
		Endpoint:  api.URL,
		AuthToken: "dashscope-key",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "task task-fail failed") {
		t.Errorf("error should mention the failed task, got: %v", err)
	}
	if !strings.Contains(err.Error(), "[InvalidParameter]") {
		t.Errorf("error should include the provider error code, got: %v", err)
	}
	want := longMsg[:512] + "..."
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error should include the truncated error message %q, got: %v", want, err)
	}
	if strings.Contains(err.Error(), strings.Repeat("b", 88)) {
		t.Errorf("error must not include the untruncated tail of the provider message, got: %v", err)
	}
}

// TestGenerateQwen_CanceledStatusIsTerminal covers the cancelled spelling
// variants the provider may report; both must fail fast instead of polling
// until the context expires.
func TestGenerateQwen_CanceledStatusIsTerminal(t *testing.T) {
	setFastPoll(t)
	polls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1" + qwenVideoSynthesisPath:
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-canceled","task_status":"PENDING"}}`))
		case "/api/v1/tasks/task-canceled":
			polls++
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-canceled","task_status":"CANCELLED"}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer api.Close()

	_, err := GenerateQwen(context.Background(), Params{Prompt: "x", Model: "m", Endpoint: api.URL, AuthToken: "k"})
	if err == nil || !strings.Contains(err.Error(), "task task-canceled failed") {
		t.Fatalf("expected a terminal cancelled error, got %v", err)
	}
	if polls != 1 {
		t.Fatalf("polls = %d, want 1 (cancelled is terminal)", polls)
	}
}

func TestGenerateQwen_CtxCancellation(t *testing.T) {
	// Use a poll interval longer than the test's runtime so that after the
	// context is cancelled during the first poll, the next loop iteration
	// deterministically selects the ctx.Done() branch instead of the timer.
	prev := pollInterval
	pollInterval = 300 * time.Millisecond
	t.Cleanup(func() { pollInterval = prev })

	ctx, cancel := context.WithCancel(context.Background())
	polls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1" + qwenVideoSynthesisPath:
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-cancel","task_status":"PENDING"}}`))
		case "/api/v1/tasks/task-cancel":
			polls++
			cancel()
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-cancel","task_status":"RUNNING"}}`))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
		}
	}))
	defer api.Close()

	_, err := GenerateQwen(ctx, Params{Prompt: "x", Model: "m", Endpoint: api.URL, AuthToken: "k"})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context canceled error, got %v", err)
	}
	if polls != 1 {
		t.Fatalf("polls = %d, want 1 (no polling after cancellation)", polls)
	}
}

func TestGenerateQwen_SubmitErrorSurfacesProviderMessage(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1"+qwenVideoSynthesisPath {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"InvalidParameter","message":"model not found"}`))
	}))
	defer api.Close()

	_, err := GenerateQwen(context.Background(), Params{Prompt: "x", Model: "nope", Endpoint: api.URL, AuthToken: "k"})
	if err == nil || !strings.Contains(err.Error(), "submit http 400") || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("submit error = %v, want the http status and provider message", err)
	}
}

func TestGenerateQwen_Validation(t *testing.T) {
	if _, err := GenerateQwen(context.Background(), Params{Model: "m"}); err == nil {
		t.Fatal("expected prompt-or-reference-required error")
	}
	_, err := GenerateQwen(context.Background(), Params{
		Prompt:          "x",
		Model:           "m",
		ReferenceVideos: []string{"https://example.com/motion.mp4"},
	})
	if err == nil || !strings.Contains(err.Error(), "reference videos are not supported") {
		t.Fatalf("reference-video error = %v, want an explicit unsupported error", err)
	}
}
