package agent

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// TestIsQwenProvider locks case/space-tolerant provider routing to the native
// DashScope image backend, including its platform aliases.
func TestIsQwenProvider(t *testing.T) {
	for provider, want := range map[string]bool{
		"qwen":          true,
		"Qwen":          true,
		" QWEN ":        true,
		"dashscope":     true,
		"DashScope":     true,
		"bailian":       true,
		"qwen-image":    false,
		"minimax":       false,
		"gemini":        false,
		"openai_custom": false,
		"":              false,
	} {
		if got := isQwenProvider(provider); got != want {
			t.Fatalf("isQwenProvider(%q) = %v, want %v", provider, got, want)
		}
	}
}

// TestHandleImageGenerate_QwenUsesDashScopeTask locks the media-account route
// for a Qwen image account: the provider name alone must select the native
// DashScope task wire (image-generation/generation), never the GPT-compatible
// images endpoint. The submit response carries the finished image so the test
// resolves without waiting on the poll cadence.
func TestHandleImageGenerate_QwenUsesDashScopeTask(t *testing.T) {
	var gotPath, gotAuth, gotAsync string
	var srvURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/services/aigc/image-generation/generation":
			gotPath = r.URL.Path
			gotAuth = r.Header.Get("Authorization")
			gotAsync = r.Header.Get("X-DashScope-Async")
			_, _ = w.Write([]byte(`{"output":{"task_id":"t1","task_status":"SUCCEEDED","choices":[{"message":{"content":[{"image":"` + srvURL + `/out.png"}]}}]}}`))
		case "/out.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("image-bytes"))
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srvURL = server.URL
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{
		Kind: "image", Provider: "qwen", APIKey: "qwen-key",
		Model: "qwen-image-3.0-pro", BaseURL: server.URL,
	})

	path, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{Prompt: "a poster"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/v1/services/aigc/image-generation/generation" {
		t.Fatalf("path = %q, want the DashScope task endpoint", gotPath)
	}
	if gotAuth != "Bearer qwen-key" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotAsync != "enable" {
		t.Fatalf("X-DashScope-Async = %q, want enable", gotAsync)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image-bytes" {
		t.Fatalf("file content = %q", data)
	}
}
