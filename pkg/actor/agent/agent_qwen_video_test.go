package agent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestHandleVideoGenerate_QwenUsesDashScopeWire locks the Qwen (Wan) account
// path: a media account whose provider is qwen must reach the DashScope
// video-synthesis wire (async submit → task poll → mp4 download) instead of the
// GPT-compatible mediagen client, with the prompt, first-frame image, and audio
// track mapped onto the Wan input object.
func TestHandleVideoGenerate_QwenUsesDashScopeWire(t *testing.T) {
	var submitted struct {
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
	var rawSubmit, submitAuth, submitAsync string
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/services/aigc/video-generation/video-synthesis":
			if r.Method != http.MethodPost {
				t.Errorf("submit method = %s, want POST", r.Method)
			}
			submitAuth = r.Header.Get("Authorization")
			submitAsync = r.Header.Get("X-DashScope-Async")
			raw, _ := io.ReadAll(r.Body)
			rawSubmit = string(raw)
			if err := json.Unmarshal(raw, &submitted); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-1","task_status":"PENDING"}}`))
		case "/api/v1/tasks/task-1":
			if r.Method != http.MethodGet {
				t.Errorf("poll method = %s, want GET", r.Method)
			}
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-1","task_status":"SUCCEEDED","video_url":"` + server.URL + `/v.mp4"}}`))
		case "/v.mp4":
			_, _ = w.Write([]byte("wan-video-bytes"))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{
		Kind: "video", Provider: "qwen", APIKey: "dashscope-key",
		Model: "wan2.5-t2v-preview", BaseURL: server.URL + "/api/v1",
	})

	path, err := a.handleVideoGenerate(ctx, gen.VideoGenerateReq{
		Prompt:          "一只小猫在月光下奔跑",
		Duration:        "5s",
		AspectRatio:     "16:9",
		ReferenceImages: []string{"https://example.com/first-frame.png"},
		ReferenceVideos: []string{"https://example.com/motion.mp4"},
		ReferenceAudios: []string{"https://example.com/track.mp3"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if submitAuth != "Bearer dashscope-key" {
		t.Errorf("submit auth = %q, want Bearer dashscope-key", submitAuth)
	}
	if submitAsync != "enable" {
		t.Errorf("X-DashScope-Async = %q, want enable", submitAsync)
	}
	if submitted.Model != "wan2.5-t2v-preview" {
		t.Errorf("model = %q, want the account model", submitted.Model)
	}
	if submitted.Input.Prompt != "一只小猫在月光下奔跑" {
		t.Errorf("input.prompt = %q", submitted.Input.Prompt)
	}
	if submitted.Input.ImageURL != "https://example.com/first-frame.png" {
		t.Errorf("input.img_url = %q, want the first reference image", submitted.Input.ImageURL)
	}
	if submitted.Input.AudioURL != "https://example.com/track.mp3" {
		t.Errorf("input.audio_url = %q, want the reference audio", submitted.Input.AudioURL)
	}
	if submitted.Parameters.Size != "1280*720" || submitted.Parameters.Duration != 5 || submitted.Parameters.Watermark {
		t.Errorf("parameters = %+v, want size 1280*720 / duration 5 / watermark false", submitted.Parameters)
	}
	// The Wan API has no reference-video input: forwarding it would make the
	// caller believe the clip shaped the result.
	if strings.Contains(rawSubmit, "motion.mp4") {
		t.Errorf("reference video must not be forwarded, got: %s", rawSubmit)
	}

	if filepath.Dir(path) != domain.GeneratedAssetsPath(root) {
		t.Fatalf("generated path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "wan-video-bytes" {
		t.Fatalf("file content = %q, want wan-video-bytes", data)
	}
}

func TestIsQwenProvider_VideoRouting(t *testing.T) {
	for _, provider := range []string{"qwen", "Qwen", " dashscope ", "DashScope"} {
		if !isQwenProvider(provider) {
			t.Errorf("isQwenProvider(%q) = false, want true", provider)
		}
	}
	for _, provider := range []string{"", "qwen-video", "dashscopes", "ark", "gemini"} {
		if isQwenProvider(provider) {
			t.Errorf("isQwenProvider(%q) = true, want false", provider)
		}
	}
}
