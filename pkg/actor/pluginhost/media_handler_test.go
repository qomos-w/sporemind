package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/invoke"
	"github.com/qomos-w/gospore/ref"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

func TestMediaGenerate_ModelOnlyRejected(t *testing.T) {
	a := &Actor{}
	for _, callID := range []string{"image.generate", "video.generate"} {
		payload := []byte(`{"Prompt":"a cat","Model":"gpt-image-2"}`)
		_, err := a.handleMediaGenerate(callID, "app.demo", payload)
		if err == nil {
			t.Fatalf("%s: model-only selection must error", callID)
		}
		if !strings.Contains(err.Error(), "unit") {
			t.Errorf("%s: error should name the unit rule, got: %v", callID, err)
		}
	}
}

func TestMediaGenerate_PromptRequired(t *testing.T) {
	a := &Actor{}
	_, err := a.handleMediaGenerate("image.generate", "app.demo", []byte(`{"Prompt":"  "}`))
	if err == nil || !strings.Contains(err.Error(), "Prompt") {
		t.Fatalf("empty prompt must error naming Prompt, got: %v", err)
	}
}

func TestNormalizeMediaRef(t *testing.T) {
	url := "https://example.com/a.png"
	if got, err := normalizeMediaRef(url); err != nil || got != url {
		t.Errorf("URL ref must pass through, got %q err %v", got, err)
	}
	dataURI := "data:image/png;base64,AAAA"
	if got, err := normalizeMediaRef(dataURI); err != nil || got != dataURI {
		t.Errorf("data URI must pass through, got %q err %v", got, err)
	}
	b64 := "aGVsbG8="
	if got, err := normalizeMediaRef(b64); err != nil || got != b64 {
		t.Errorf("base64 ref must pass through, got %q err %v", got, err)
	}
	// Bare local paths are an undeclared fs.read — reject explicitly.
	if _, err := normalizeMediaRef("C:\\Users\\me\\secret.png"); err == nil {
		t.Error("bare local path must be rejected")
	} else if !strings.Contains(err.Error(), "base64") {
		t.Errorf("rejection should point at base64/URL, got: %v", err)
	}
}

func TestNormalizeMediaRefs_VideoRefsURLOnly(t *testing.T) {
	req := mediaGenRequest{
		Prompt:          "dance",
		ReferenceImages: []string{"https://x/i.png", "aGVsbG8="},
		ReferenceVideos: []string{"https://x/v.mp4"},
		ReferenceAudios: []string{"https://x/a.mp3"},
	}
	refs, err := normalizeMediaRefs(mediaKindVideo, req)
	if err != nil {
		t.Fatalf("valid video refs: %v", err)
	}
	if len(refs.images) != 2 {
		t.Errorf("images = %v, want both refs", refs.images)
	}
	req.ReferenceVideos = []string{"C:\\tmp\\v.mp4"}
	if _, err := normalizeMediaRefs(mediaKindVideo, req); err == nil {
		t.Error("local-path video reference must be rejected")
	}
	req.ReferenceVideos = nil
	req.ReferenceAudios = []string{"aGVsbG8="}
	if _, err := normalizeMediaRefs(mediaKindVideo, req); err == nil {
		t.Error("base64 audio reference must be rejected (URLs only)")
	}
}

func TestSaveAppMediaAt(t *testing.T) {
	appDir := t.TempDir()
	artifact := filepath.Join(appDir, ".sporecode", "build", "plugin-x.exe")
	if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifact, []byte("bin"), 0o644); err != nil {
		t.Fatal(err)
	}
	rel, err := saveAppMediaAt(artifact, "video", []byte("data"), "video/mp4")
	if err != nil {
		t.Fatalf("saveAppMediaAt: %v", err)
	}
	if !strings.HasPrefix(rel, "media/video-") {
		t.Errorf("rel path = %q, want media/video-* prefix", rel)
	}
	ext := strings.TrimPrefix(filepath.Ext(rel), ".")
	if ext != "mp4" && ext != "m4v" {
		t.Errorf("rel path = %q, want an mp4-family extension", rel)
	}
	written, err := os.ReadFile(filepath.Join(appDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("artifact not written: %v", err)
	}
	if string(written) != "data" {
		t.Errorf("artifact content = %q", written)
	}

	// Unknown layout refuses to guess a root.
	if _, err := saveAppMediaAt(filepath.Join(t.TempDir(), "plugin.exe"), "image", []byte("x"), "image/png"); err == nil {
		t.Error("non-anchored artifact path must error")
	}
}

func TestSaveAppMedia_NilLoader(t *testing.T) {
	a := &Actor{} // loader nil
	if _, err := a.saveAppMedia("app.demo", "image", []byte("x"), "image/png"); err == nil {
		t.Error("nil loader must error")
	}
}

// TestSaveAppMedia_StaticRootAnchor pins the zip-install placement path: an
// inventory-installed app's artifact lives in the content-addressed store
// (not <appDir>/.sporecode/build), so media anchors to the static root the
// appmanager pushed in the OnLoad config — even with no live loader entry.
func TestSaveAppMedia_StaticRootAnchor(t *testing.T) {
	root := t.TempDir()
	cfg, err := json.Marshal(map[string]string{
		"httpAddr": "127.0.0.1:0", "sessionSecret": "s", "staticDir": root,
	})
	if err != nil {
		t.Fatal(err)
	}
	a := &Actor{ArtifactLoads: map[string]gen.PluginArtifactLoadReq{
		"app.demo": {OnLoadConfig: cfg},
	}}
	rel, err := a.saveAppMedia("app.demo", "image", []byte("img"), "image/png")
	if err != nil {
		t.Fatalf("saveAppMedia: %v", err)
	}
	if !strings.HasPrefix(rel, "media/image-") {
		t.Fatalf("rel path = %q, want media/image-* prefix", rel)
	}
	written, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("artifact not written under static root: %v", err)
	}
	if string(written) != "img" {
		t.Errorf("artifact content = %q", written)
	}

	// A load record without a staticDir field must not anchor anywhere —
	// the build-layout fallback (or its error) applies.
	bare := &Actor{ArtifactLoads: map[string]gen.PluginArtifactLoadReq{
		"app.demo": {OnLoadConfig: []byte(`{"httpAddr":"127.0.0.1:0"}`)},
	}}
	if _, err := bare.saveAppMedia("app.demo", "image", []byte("x"), "image/png"); err == nil {
		t.Error("load record without staticDir and nil loader must error")
	}
}

func TestParseOnLoadStaticDir(t *testing.T) {
	if got := parseOnLoadStaticDir(nil); got != "" {
		t.Errorf("nil config = %q, want empty", got)
	}
	if got := parseOnLoadStaticDir([]byte("not json")); got != "" {
		t.Errorf("unparseable config = %q, want empty", got)
	}
	if got := parseOnLoadStaticDir([]byte(`{"staticDir":"  C:\\apps\\demo  "}`)); got != `C:\apps\demo` {
		t.Errorf("staticDir = %q, want trimmed path", got)
	}
}

func TestMediaPureHelpers(t *testing.T) {
	if got := parseMediaDuration("5s"); got != 5 {
		t.Errorf("parseMediaDuration(5s) = %d", got)
	}
	if got := parseMediaDuration("bogus"); got != 0 {
		t.Errorf("parseMediaDuration(bogus) = %d, want 0", got)
	}
	if got := mediaImageSize("2K", "16:9"); got != "1536x1024" {
		t.Errorf("mediaImageSize(2K,16:9) = %q", got)
	}
	if got := mediaImageSize("2K", ""); got != "1024x1024" {
		t.Errorf("mediaImageSize(2K,\"\") = %q", got)
	}
	if !isGeminiMediaProvider(" Gemini ") {
		t.Error("isGeminiMediaProvider must be case/space tolerant")
	}
	if !isArkMediaProvider("volcengine") || !isArkMediaProvider("byteplus") {
		t.Error("ark aliases must match")
	}
	if !isQwenMediaProvider(" Qwen ") || !isQwenMediaProvider("dashscope") {
		t.Error("isQwenMediaProvider must be trim/case tolerant across both aliases")
	}
	if isQwenMediaProvider("qwen-video") || isQwenMediaProvider("") {
		t.Error("isQwenMediaProvider must be exact")
	}
	if !isMiniMaxMediaProvider(" MiniMax ") || isMiniMaxMediaProvider("minimaxi") {
		t.Error("isMiniMaxMediaProvider must be trim/case tolerant but exact")
	}
	if got := firstNonEmptyMedia("", "fallback"); got != "fallback" {
		t.Errorf("firstNonEmptyMedia = %q", got)
	}
	if got := mediaExtFor("application/octet-stream", "video"); got != "mp4" && got != "bin" {
		t.Errorf("video fallback ext = %q", got)
	}
	if got := mediaExtFor("image/png", "image"); got != "png" {
		t.Errorf("png ext = %q", got)
	}
}

// TestMediaGenerate_EmptyPinHonorsBinding pins the empty-pin routing: with no
// active media account, the media-page provider-model binding must narrow the
// aggregator resolve (agent-tool parity) instead of pool auto-pick. The
// generated bytes land in saveAppMedia, whose nil-loader failure here is
// expected — the assertions target the resolve request and the gemini wire.
func TestMediaGenerate_EmptyPinHonorsBinding(t *testing.T) {
	var mu sync.Mutex
	var gotPath string
	geminiHits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		geminiHits++
		gotPath = r.URL.Path
		mu.Unlock()
		b64 := "aW1n" // "img"
		fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"inlineData":{"data":"%s","mimeType":"image/png"}}]}}]}`, b64)
	}))
	defer upstream.Close()

	mediaRef := &scriptRef{reply: []byte(`{"Account":{},"BoundProvider":"rolldesk","BoundModel":"gemini-3.1-flash-image-preview"}`)}
	aggRef := &scriptRef{reply: []byte(fmt.Sprintf(
		`{"Model":"gemini-3.1-flash-image-preview","Endpoint":%q,"ProviderName":"rolldesk","Protocol":"gemini","AuthToken":"k"}`,
		upstream.URL+"/v1"))}

	a := &Actor{svcRefs: map[string]ref.Ref{"media": mediaRef, "aiaggregator": aggRef}}

	_, err := a.handleMediaGenerate("image.generate", "app.demo", []byte(`{"Plugin":"app.demo","Prompt":"a cat"}`))
	if err == nil {
		t.Fatal("nil-loader save stage must error")
	}
	if !strings.Contains(err.Error(), "pluginhost: image.generate:") || strings.Contains(err.Error(), "resolve") {
		t.Fatalf("error should be the save-stage failure, got: %v", err)
	}

	var req gen.AIAggregatorImageResolveReq
	aggRef.mu.Lock()
	err2 := json.Unmarshal(aggRef.lastPayload.([]byte), &req)
	aggRef.mu.Unlock()
	if err2 != nil {
		t.Fatalf("decode resolve request: %v", err2)
	}
	if req.Provider != "rolldesk" || req.Model != "gemini-3.1-flash-image-preview" {
		t.Fatalf("resolve request = %+v, want the media-page binding pair", req)
	}
	mu.Lock()
	defer mu.Unlock()
	if geminiHits != 1 {
		t.Fatalf("gemini wire hits = %d, want 1", geminiHits)
	}
	if want := "/v1beta/models/gemini-3.1-flash-image-preview:generateContent"; gotPath != want {
		t.Fatalf("upstream path = %q, want %q (trailing /v1 trimmed)", gotPath, want)
	}
}

// TestMediaGenerate_VideoQwenUsesDashScopeWire locks the pluginhost video
// dispatch for a Qwen provider unit: the resolved protocol must select the
// native DashScope Wan backend (async submit → task poll → mp4 download)
// instead of the GPT-compatible media client. The artifact-save stage has no
// loader in this fixture, so the assertions target the upstream wire.
func TestMediaGenerate_VideoQwenUsesDashScopeWire(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotAsync, gotAuth string
	submits := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/services/aigc/video-generation/video-synthesis":
			submits++
			gotPath = r.URL.Path
			gotAsync = r.Header.Get("X-DashScope-Async")
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-1","task_status":"PENDING"}}`))
		case "/api/v1/tasks/task-1":
			_, _ = w.Write([]byte(`{"output":{"task_id":"task-1","task_status":"SUCCEEDED","video_url":"http://` + r.Host + `/v.mp4"}}`))
		case "/v.mp4":
			_, _ = w.Write([]byte("wan-bytes"))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer upstream.Close()

	aggRef := &scriptRef{reply: []byte(fmt.Sprintf(
		`{"Model":"wan-2.5-t2v-720p","Endpoint":%q,"ProviderName":"qwen","Protocol":"qwen","AuthToken":"k"}`,
		upstream.URL+"/api/v1"))}
	a := &Actor{svcRefs: map[string]ref.Ref{"aiaggregator": aggRef}}

	_, err := a.handleMediaGenerate("video.generate", "app.demo",
		[]byte(`{"Plugin":"app.demo","Prompt":"a cat","Provider":"qwen","Model":"wan-2.5-t2v-720p","Duration":"5s"}`))
	if err == nil {
		t.Fatal("nil-loader save stage must error")
	}
	if !strings.Contains(err.Error(), "pluginhost: video.generate:") {
		t.Fatalf("error should be the save-stage failure, got: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if submits != 1 {
		t.Fatalf("DashScope submits = %d, want 1", submits)
	}
	if want := "/api/v1/services/aigc/video-generation/video-synthesis"; gotPath != want {
		t.Fatalf("upstream path = %q, want %q", gotPath, want)
	}
	if gotAsync != "enable" {
		t.Fatalf("X-DashScope-Async = %q, want enable", gotAsync)
	}
	if gotAuth != "Bearer k" {
		t.Fatalf("authorization = %q, want the resolved unit token", gotAuth)
	}
}

// scriptRef replies with one raw payload per Invoke and records the most
// recent invoke payload for assertions.
type scriptRef struct {
	mu          sync.Mutex
	reply       []byte
	lastPayload any
}

func (r *scriptRef) ID() id.ActorID          { return id.ActorID{} }
func (r *scriptRef) Service() (string, bool) { return "test", true }
func (r *scriptRef) Invoke(_ context.Context, _ string, payload any, _ ...map[string]string) *invoke.Call {
	r.mu.Lock()
	r.lastPayload = payload
	reply := r.reply
	r.mu.Unlock()
	return invoke.NewCall(invoke.CallModeStream, &rawReplyStream{raw: reply})
}

type rawReplyStream struct{ raw []byte }

func (s *rawReplyStream) Recv() (any, error)       { return nil, io.EOF }
func (s *rawReplyStream) RecvRaw() ([]byte, error) { return s.raw, nil }
func (s *rawReplyStream) Close() error             { return nil }

func TestMediaGenRequestWireCasing(t *testing.T) {
	// The host-bridge payload is the SDK wire (PascalCase, matching
	// gen.ImageGenerateReq / gen.VideoGenerateReq JSON tags); pin the decode.
	payload := []byte(`{"Plugin":"app.demo","Prompt":"a cat","Provider":"gemini","Model":"gemini-3.1-flash-image-preview","InputImage":"aGVsbG8=","Duration":"5s"}`)
	a := &Actor{}
	// model-only path not hit here (both set) — decode is exercised by
	// asserting the pinned-unit rejection message names the unit pair.
	_, err := a.handleMediaGenerate("image.generate", "app.demo", payload)
	if err == nil {
		t.Fatal("pinned unit with no services available must error (resolve unavailable)")
	}
	if !strings.Contains(err.Error(), "gemini/gemini-3.1-flash-image-preview") {
		t.Errorf("error should name the pinned pair, got: %v", err)
	}
}

// TestDispatchMediaGenerationMiniMax pins the MiniMax dispatch branch: an
// image unit with provider "minimax" (or an explicit "minimax" protocol, the
// shape resolved units carry) must route through the native minimax
// image_generation wire, not the GPT-compatible media client — the account
// path infers the protocol from the provider name alone.
func TestDispatchMediaGenerationMiniMax(t *testing.T) {
	var srvURL string
	genHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/image_generation":
			genHits++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":      map[string]any{"image_urls": []string{srvURL + "/x.png"}},
				"base_resp": map[string]any{"status_code": 0},
			})
		case "/x.png":
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
		Provider: "minimax", Endpoint: srv.URL, AuthToken: "k", Model: "image-01",
	}, mediaGenRequest{Prompt: "a cat"}, mediaRefs{})
	if err != nil {
		t.Fatalf("dispatchMediaGeneration (provider inference): %v", err)
	}
	if genHits != 1 {
		t.Fatalf("minimax wire hits = %d, want 1 (provider inference)", genHits)
	}
	if len(data) != 3 || mime != "image/png" || provider != "minimax" || model != "image-01" {
		t.Fatalf("result = %d bytes, mime=%q provider=%q model=%q", len(data), mime, provider, model)
	}

	// Explicit protocol pin (the resolved-unit path).
	if _, _, _, _, err = a.dispatchMediaGeneration(context.Background(), mediaKindImage, mediaUnit{
		Protocol: "minimax", Provider: "relay", Endpoint: srv.URL, AuthToken: "k", Model: "image-01",
	}, mediaGenRequest{Prompt: "a cat"}, mediaRefs{}); err != nil {
		t.Fatalf("dispatchMediaGeneration (protocol pin): %v", err)
	}
	if genHits != 2 {
		t.Fatalf("minimax wire hits = %d, want 2 (one per dispatch)", genHits)
	}
}
