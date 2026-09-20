package pluginhost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIsQwenMediaProvider locks the provider-name inference used on the
// active-account path: "qwen" plus its platform aliases route to the native
// DashScope backend, everything else does not.
func TestIsQwenMediaProvider(t *testing.T) {
	cases := map[string]bool{
		"qwen":       true,
		"Qwen":       true,
		" QWEN ":     true,
		"dashscope":  true,
		"bailian":    true,
		"qwen_image": false,
		"minimax":    false,
		"openai":     false,
		"":           false,
	}
	for provider, want := range cases {
		if got := isQwenMediaProvider(provider); got != want {
			t.Errorf("isQwenMediaProvider(%q) = %v, want %v", provider, got, want)
		}
	}
}

// TestDispatchMediaGenerationQwen pins the Qwen dispatch branch: an image unit
// with provider "qwen" (or an explicit "qwen" protocol, the shape resolved
// units carry) must route through the native DashScope task wire — submit,
// poll, download — and not the GPT-compatible media client.
func TestDispatchMediaGenerationQwen(t *testing.T) {
	var srvURL string
	submits, polls := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/services/aigc/image-generation/generation":
			submits++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"output": map[string]any{"task_id": "t1", "task_status": "PENDING"},
			})
		case "/api/v1/tasks/t1":
			polls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"output": map[string]any{
					"task_id":     "t1",
					"task_status": "SUCCEEDED",
					"choices": []any{map[string]any{
						"message": map[string]any{
							"content": []any{map[string]any{"image": srvURL + "/q.png"}},
						},
					}},
				},
			})
		case "/q.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte{1, 2, 3})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	srvURL = srv.URL
	defer srv.Close()

	a := &Actor{}
	// Provider-name inference (the active-account path leaves Protocol empty).
	data, mime, provider, model, err := a.dispatchMediaGeneration(context.Background(), mediaKindImage, mediaUnit{
		Provider: "qwen", Endpoint: srv.URL, AuthToken: "k", Model: "qwen-image-3.0-pro",
	}, mediaGenRequest{Prompt: "a cat"}, mediaRefs{})
	if err != nil {
		t.Fatalf("dispatchMediaGeneration (provider inference): %v", err)
	}
	if submits != 1 || polls != 1 {
		t.Fatalf("qwen wire hits: submits=%d polls=%d, want 1/1", submits, polls)
	}
	if len(data) != 3 || mime != "image/png" || provider != "qwen" || model != "qwen-image-3.0-pro" {
		t.Fatalf("result = %d bytes, mime=%q provider=%q model=%q", len(data), mime, provider, model)
	}

	// Explicit protocol pin (the resolved-unit path).
	if _, _, _, _, err = a.dispatchMediaGeneration(context.Background(), mediaKindImage, mediaUnit{
		Protocol: "qwen", Provider: "relay", Endpoint: srv.URL, AuthToken: "k", Model: "qwen-image-3.0-pro",
	}, mediaGenRequest{Prompt: "a cat"}, mediaRefs{}); err != nil {
		t.Fatalf("dispatchMediaGeneration (protocol pin): %v", err)
	}
	if submits != 2 {
		t.Fatalf("qwen submits = %d, want 2 (one per dispatch)", submits)
	}
}
