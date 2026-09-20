package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/sporemind/pkg/domain"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func TestHandleVideoGenerate_UsesActiveMediaAccount(t *testing.T) {
	var received struct {
		Model           string   `json:"model"`
		Prompt          string   `json:"prompt"`
		Duration        int      `json:"duration"`
		AspectRatio     string   `json:"aspect_ratio"`
		ReferenceImages []string `json:"referenceImages"`
		ReferenceAudios []string `json:"referenceAudios"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/video/generations" {
			t.Errorf("path = %q, want /video/generations", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer video-key" {
			t.Errorf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"dmlkZW8tYnl0ZXM=","mime_type":"video/mp4"}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{Kind: "video", APIKey: "video-key", Model: "video-model", BaseURL: server.URL})

	path, err := a.handleVideoGenerate(ctx, gen.VideoGenerateReq{
		Prompt:          "a city at night",
		Duration:        "5s",
		AspectRatio:     "16:9",
		ReferenceImages: []string{"image-ref"},
		ReferenceAudios: []string{"audio-ref"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if received.Model != "video-model" || received.Prompt != "a city at night" || received.Duration != 5 || received.AspectRatio != "16:9" {
		t.Fatalf("unexpected request: %+v", received)
	}
	if len(received.ReferenceImages) != 1 || len(received.ReferenceAudios) != 1 {
		t.Fatalf("missing references: %+v", received)
	}
	if filepath.Dir(path) != domain.GeneratedAssetsPath(root) {
		t.Fatalf("generated path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "video-bytes" {
		t.Fatalf("file content = %q", data)
	}
}

func TestHandleImageGenerate_PrefersActiveMediaAccount(t *testing.T) {
	mediaCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaCalls++
		if r.URL.Path != "/images/generations" {
			t.Errorf("path = %q, want /images/generations", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2UtYnl0ZXM=","mime_type":"image/png"}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{Kind: "image", APIKey: "image-key", Model: "image-model", BaseURL: server.URL})

	path, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{Prompt: "a poster"})
	if err != nil {
		t.Fatal(err)
	}
	if mediaCalls != 1 {
		t.Fatalf("media calls = %d, want 1", mediaCalls)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image-bytes" {
		t.Fatalf("file content = %q", data)
	}
}

func TestHandleVideoGenerate_RequiresActiveAccount(t *testing.T) {
	a := &Actor{}
	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(context.Context, ref.Ref, string, any) (any, error) {
			return nil, nil
		}}
	}

	_, err := a.handleVideoGenerate(ctx, gen.VideoGenerateReq{Prompt: "a clip"})
	if err == nil || err.Error() != "generate_video: no active video media account configured and aggregator not available" {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestHandleVideoGenerate_AggregatorFallback locks the acceptance criterion
// that a video model configured on an LLM provider (no media account) stays
// reachable: handleVideoGenerate resolves a video unit through the system
// aggregator and routes it via the videogen dispatcher by the resolved
// Protocol. The Ark protocol path is exercised end to end against a mock
// upstream (submit → poll succeeded → download) so the full fallback chain
// is covered, including auth token and endpoint propagation.
func TestHandleVideoGenerate_AggregatorFallback(t *testing.T) {
	polls := 0
	var server *httptest.Server
	var gotAuth string
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/contents/generations/tasks":
			if r.Method != http.MethodPost {
				t.Errorf("submit method = %s, want POST", r.Method)
			}
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"id":"task-1"}`))
		case "/contents/generations/tasks/task-1":
			polls++
			_, _ = w.Write([]byte(`{"id":"task-1","status":"succeeded","content":{"video_url":"` + server.URL + `/v.mp4"}}`))
		case "/v.mp4":
			_, _ = w.Write([]byte("video-bytes"))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	aggRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	a.aggRefCache = map[string]ref.Ref{systemAggID: aggRef}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false } // no media service
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
			if callID != "aiaggregator.video_resolve" {
				return nil, &unexpectedMediaCall{callID: callID}
			}
			return gen.AIAggregatorVideoResolveResp{
				Model: "dreamina-seedance-2-0-260128", Endpoint: server.URL, ProviderName: "ark",
				Protocol: "ark", AuthToken: "agg-token",
			}, nil
		}}
	}

	path, err := a.handleVideoGenerate(ctx, gen.VideoGenerateReq{
		Prompt:      "a clip of a dancer",
		Duration:    "5s",
		AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer agg-token" {
		t.Fatalf("authorization = %q, want Bearer agg-token", gotAuth)
	}
	if polls < 1 {
		t.Errorf("task was never polled")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "video-bytes" {
		t.Fatalf("file content = %q", data)
	}
}

// TestHandleVideoGenerate_AggregatorFallbackNoVideoUnit locks the failure
// message when neither a media account nor an aggregator video unit exists:
// the error must name both missing pieces so the user knows what to add.
func TestHandleVideoGenerate_AggregatorFallbackNoVideoUnit(t *testing.T) {
	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	aggRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	a.aggRefCache = map[string]ref.Ref{systemAggID: aggRef}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
			if callID != "aiaggregator.video_resolve" {
				return nil, &unexpectedMediaCall{callID: callID}
			}
			return gen.AIAggregatorVideoResolveResp{}, nil // empty: no video unit anywhere
		}}
	}

	_, err := a.handleVideoGenerate(ctx, gen.VideoGenerateReq{Prompt: "a clip"})
	if err == nil || !strings.Contains(err.Error(), "no active video media account configured and no video-generation model available") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestHandleImageGenerate_BindingNarrowsAggregatorFallback locks the media-page
// provider-model binding: when no media account is active but a binding is
// set, the aggregator resolve request carries the bound (Provider, Model)
// pair instead of falling through to first-match.
func TestHandleImageGenerate_BindingNarrowsAggregatorFallback(t *testing.T) {
	var resolveReq gen.AIAggregatorImageResolveReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2UtYnl0ZXM="}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	aggRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	a.aggRefCache = map[string]ref.Ref{systemAggID: aggRef}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	mediaRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return mediaRef, name == "media" }
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, payload any) (any, error) {
			switch callID {
			case "media.active_account":
				// No active account; the provider-model binding is set.
				return gen.MediaActiveAccountResp{BoundProvider: "visioncoder", BoundModel: "grok-imagine-image"}, nil
			case "aiaggregator.image_resolve":
				if req, ok := payload.(gen.AIAggregatorImageResolveReq); ok {
					resolveReq = req
				}
				return gen.AIAggregatorImageResolveResp{
					Model: "grok-imagine-image", Endpoint: server.URL, ProviderName: "visioncoder",
					Protocol: "openai", AuthToken: "bound-token",
				}, nil
			default:
				return nil, &unexpectedMediaCall{callID: callID}
			}
		}}
	}

	path, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{Prompt: "a poster"})
	if err != nil {
		t.Fatal(err)
	}
	if resolveReq.Provider != "visioncoder" || resolveReq.Model != "grok-imagine-image" {
		t.Fatalf("resolve request filters = %+v, want visioncoder/grok-imagine-image", resolveReq)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image-bytes" {
		t.Fatalf("file content = %q", data)
	}
}

func mediaTestContext(endpoint string, account gen.MediaAccount) actor.Context {
	ctx := testutil.HumanCtx(testutil.GenActorID())
	mediaRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) {
		return mediaRef, name == "media"
	}
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
			if callID != "media.active_account" {
				return nil, &unexpectedMediaCall{callID: callID}
			}
			return gen.MediaActiveAccountResp{Account: account}, nil
		}}
	}
	return ctx
}

type unexpectedMediaCall struct {
	callID string
}

func (e *unexpectedMediaCall) Error() string {
	return "unexpected media call: " + e.callID
}

func TestHandleImageGenerate_GeminiUsesGenerateContent(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("x-goog-api-key")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"data":"aW1hZ2UtYnl0ZXM=","mimeType":"image/png"}}]}}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{
		Kind: "image", Provider: "gemini", APIKey: "gemini-key",
		Model: "gemini-3.1-flash-image-preview", BaseURL: server.URL,
	})

	path, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{Prompt: "a poster"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1beta/models/gemini-3.1-flash-image-preview:generateContent" {
		t.Fatalf("path = %q, want native generateContent", gotPath)
	}
	if gotAuth != "gemini-key" {
		t.Fatalf("x-goog-api-key = %q, want gemini-key", gotAuth)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image-bytes" {
		t.Fatalf("file content = %q", data)
	}
}

func TestHandleVideoGenerate_GeminiUsesPredictLongRunning(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("x-goog-api-key")
		// The submit response already carries the completed operation so the
		// test resolves without waiting on the poll interval.
		_, _ = w.Write([]byte(`{"name":"models/veo-3.0-generate-001/operations/op-1","done":true,"response":{"generateVideoResponse":{"generatedSamples":[{"video":{"bytesBase64Encoded":"dmlkZW8tYnl0ZXM=","mimeType":"video/mp4"}}]}}}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{
		Kind: "video", Provider: "gemini", APIKey: "gemini-key",
		Model: "veo-3.0-generate-001", BaseURL: server.URL,
	})

	path, err := a.handleVideoGenerate(ctx, gen.VideoGenerateReq{Prompt: "a city at night", Duration: "5s"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1beta/models/veo-3.0-generate-001:predictLongRunning" {
		t.Fatalf("path = %q, want native predictLongRunning", gotPath)
	}
	if gotAuth != "gemini-key" {
		t.Fatalf("x-goog-api-key = %q, want gemini-key", gotAuth)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "video-bytes" {
		t.Fatalf("file content = %q", data)
	}
}

// TestHandleVideoGenerate_ArkRoutesToGenerateArk locks the Seedance (Ark)
// routing: a "volcengine" account hits the contents/generations async-task API,
// reference image paths are base64-injected and URLs pass through, reference
// videos/audios pass through as URLs, generate_audio defaults to true, and
// watermark stays false.
func TestHandleVideoGenerate_ArkRoutesToGenerateArk(t *testing.T) {
	type mediaURL struct {
		URL string `json:"url"`
	}
	type contentPart struct {
		Type     string    `json:"type"`
		Text     string    `json:"text,omitempty"`
		ImageURL *mediaURL `json:"image_url,omitempty"`
		VideoURL *mediaURL `json:"video_url,omitempty"`
		AudioURL *mediaURL `json:"audio_url,omitempty"`
		Role     string    `json:"role,omitempty"`
	}
	var submitted struct {
		Model         string        `json:"model"`
		GenerateAudio bool          `json:"generate_audio"`
		Ratio         string        `json:"ratio,omitempty"`
		Duration      int           `json:"duration,omitempty"`
		Watermark     bool          `json:"watermark"`
		Content       []contentPart `json:"content"`
	}
	polls := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/contents/generations/tasks":
			if r.Method != http.MethodPost {
				t.Errorf("submit method = %s, want POST", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer ark-key" {
				t.Errorf("authorization = %q, want Bearer ark-key", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"id":"task-1"}`))
		case "/contents/generations/tasks/task-1":
			if r.Method != http.MethodGet {
				t.Errorf("poll method = %s, want GET", r.Method)
			}
			polls++
			_, _ = w.Write([]byte(`{"id":"task-1","status":"succeeded","content":{"video_url":"` + server.URL + `/v.mp4"}}`))
		case "/v.mp4":
			_, _ = w.Write([]byte("video-bytes"))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	source := []byte("img-bytes")
	refPath := filepath.Join(t.TempDir(), "ref.png")
	if err := os.WriteFile(refPath, source, 0o644); err != nil {
		t.Fatal(err)
	}
	imgURL := "https://example.com/img.png"
	videoURL := "https://example.com/motion.mp4"
	audioURL := "https://example.com/track.wav"

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{
		Kind: "video", Provider: "volcengine", APIKey: "ark-key",
		Model: "dreamina-seedance-2-0-260128", BaseURL: server.URL,
	})

	path, err := a.handleVideoGenerate(ctx, gen.VideoGenerateReq{
		Prompt:          "make the dancer follow the beat, using [Image 1], [Video 1], [Audio 1]",
		Duration:        "5s",
		AspectRatio:     "16:9",
		ReferenceImages: []string{refPath, imgURL},
		ReferenceVideos: []string{videoURL},
		ReferenceAudios: []string{audioURL},
	})
	if err != nil {
		t.Fatal(err)
	}

	if submitted.Model != "dreamina-seedance-2-0-260128" {
		t.Errorf("model = %q, want dreamina-seedance-2-0-260128", submitted.Model)
	}
	if !submitted.GenerateAudio {
		t.Errorf("generate_audio = false, want the Seedance 2.0 default true")
	}
	if submitted.Watermark {
		t.Errorf("watermark = true, want false")
	}
	if submitted.Ratio != "16:9" || submitted.Duration != 5 {
		t.Errorf("ratio/duration = %q/%d, want 16:9/5", submitted.Ratio, submitted.Duration)
	}
	if polls < 1 {
		t.Errorf("task was never polled")
	}
	wantB64 := base64.StdEncoding.EncodeToString(source)
	if len(submitted.Content) != 5 {
		t.Fatalf("content parts = %d, want 5 (text + 2 images + video + audio): %+v", len(submitted.Content), submitted.Content)
	}
	if submitted.Content[0].Type != "text" || submitted.Content[0].Text == "" {
		t.Errorf("content[0] = %+v, want text prompt", submitted.Content[0])
	}
	if submitted.Content[1].Type != "image_url" || submitted.Content[1].Role != "reference_image" || submitted.Content[1].ImageURL == nil || submitted.Content[1].ImageURL.URL != wantB64 {
		t.Errorf("content[1] = %+v, want image_url with base64 path payload %q", submitted.Content[1], wantB64)
	}
	if submitted.Content[2].Type != "image_url" || submitted.Content[2].Role != "reference_image" || submitted.Content[2].ImageURL == nil || submitted.Content[2].ImageURL.URL != imgURL {
		t.Errorf("content[2] = %+v, want image_url passthrough %q", submitted.Content[2], imgURL)
	}
	if submitted.Content[3].Type != "video_url" || submitted.Content[3].Role != "reference_video" || submitted.Content[3].VideoURL == nil || submitted.Content[3].VideoURL.URL != videoURL {
		t.Errorf("content[3] = %+v, want video_url passthrough %q", submitted.Content[3], videoURL)
	}
	if submitted.Content[4].Type != "audio_url" || submitted.Content[4].Role != "reference_audio" || submitted.Content[4].AudioURL == nil || submitted.Content[4].AudioURL.URL != audioURL {
		t.Errorf("content[4] = %+v, want audio_url passthrough %q", submitted.Content[4], audioURL)
	}
	if filepath.Dir(path) != domain.GeneratedAssetsPath(root) {
		t.Fatalf("generated path = %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "video-bytes" {
		t.Fatalf("file content = %q", data)
	}
}

// TestHandleVideoGenerate_DoubaoRoutesToArk locks the media panel's brand-facing
// "doubao" provider id onto the same Seedance/Ark backend as "ark": the account
// must hit the contents/generations async-task API (not the GPT-compatible
// mediagen path) and carry the Doubao Seedance 2.0 model through untouched.
func TestHandleVideoGenerate_DoubaoRoutesToArk(t *testing.T) {
	var submitted struct {
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text,omitempty"`
		} `json:"content"`
	}
	submitPath := ""
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/contents/generations/tasks":
			submitPath = r.URL.Path
			if err := json.NewDecoder(r.Body).Decode(&submitted); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"id":"task-9"}`))
		case "/contents/generations/tasks/task-9":
			_, _ = w.Write([]byte(`{"id":"task-9","status":"succeeded","content":{"video_url":"` + server.URL + `/d.mp4"}}`))
		case "/d.mp4":
			_, _ = w.Write([]byte("doubao-video"))
		default:
			t.Errorf("unexpected %s %s (doubao must route through the Ark task API)", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{
		Kind: "video", Provider: "doubao", APIKey: "ark-key",
		Model: "doubao-seedance-2.0-720p-video", BaseURL: server.URL,
	})

	path, err := a.handleVideoGenerate(ctx, gen.VideoGenerateReq{
		Prompt:   "a doubao seedance clip",
		Duration: "5s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if submitPath != "/contents/generations/tasks" {
		t.Fatalf("submit path = %q, want /contents/generations/tasks", submitPath)
	}
	if submitted.Model != "doubao-seedance-2.0-720p-video" {
		t.Errorf("model = %q, want doubao-seedance-2.0-720p-video", submitted.Model)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "doubao-video" {
		t.Fatalf("file content = %q", data)
	}
}

// TestHandleImageGenerate_ProviderNameCaseInsensitive locks the acceptance
// criterion that provider routing is not case-sensitive: a user-supplied
// "Gemini"/"GEMINI" account name must still reach the native generateContent
// backend instead of the GPT-compatible mediagen client.
func TestHandleImageGenerate_ProviderNameCaseInsensitive(t *testing.T) {
	for _, provider := range []string{"Gemini", "GEMINI", " gemini "} {
		t.Run(provider, func(t *testing.T) {
			var gotPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"data":"aW1hZ2UtYnl0ZXM=","mimeType":"image/png"}}]}}]}`))
			}))
			defer server.Close()

			root := t.TempDir()
			a := &Actor{}
			a.cachedProjectRoot.Store(&root)
			ctx := mediaTestContext(server.URL, gen.MediaAccount{
				Kind: "image", Provider: provider, APIKey: "gemini-key",
				Model: "gemini-3.1-flash-image-preview", BaseURL: server.URL,
			})

			if _, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{Prompt: "a poster"}); err != nil {
				t.Fatal(err)
			}
			if gotPath != "/v1beta/models/gemini-3.1-flash-image-preview:generateContent" {
				t.Fatalf("path = %q, want native generateContent (provider %q mis-routed)", gotPath, provider)
			}
		})
	}
}

// TestHandleImageGenerate_DoubaoUsesArkImages locks the acceptance criterion
// that a Doubao (Volcengine Ark) image account routes to the native
// images/generations backend instead of the GPT-compatible mediagen client:
// Bearer auth, response_format "url", watermark off, and the size budget mapped
// to Ark's "WxH" form.
func TestHandleImageGenerate_DoubaoUsesArkImages(t *testing.T) {
	var gotPath, gotAuth string
	var body struct {
		Model          string `json:"model"`
		Prompt         string `json:"prompt"`
		Size           string `json:"size"`
		ResponseFormat string `json:"response_format"`
		Watermark      bool   `json:"watermark"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("path = %q, want /images/generations", r.URL.Path)
		}
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2UtYnl0ZXM="}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{
		Kind: "image", Provider: "doubao", APIKey: "ark-key",
		Model: "doubao-seedream-3.0-t2i", BaseURL: server.URL,
	})

	path, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{Prompt: "a poster", Size: "2K"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/images/generations" {
		t.Fatalf("path = %q, want /images/generations", gotPath)
	}
	if gotAuth != "Bearer ark-key" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if body.Model != "doubao-seedream-3.0-t2i" || body.Prompt != "a poster" {
		t.Fatalf("unexpected body: %+v", body)
	}
	if body.ResponseFormat != "url" {
		t.Fatalf("response_format = %q, want url", body.ResponseFormat)
	}
	if body.Watermark {
		t.Fatal("watermark = true, want false")
	}
	if body.Size != "2048x2048" {
		t.Fatalf("size = %q, want 2048x2048", body.Size)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image-bytes" {
		t.Fatalf("file content = %q", data)
	}
}

// TestHandleImageGenerate_MediaPathMapsSizeEnum locks the acceptance criterion
// that the "1K/2K/4K" enum is translated to a concrete "WxH" size before it
// reaches GPT-compatible media endpoints, so openai_custom/glm accounts with a
// Size request no longer receive an illegal "2K"/"4K" value and fail with 400.
func TestHandleImageGenerate_MediaPathMapsSizeEnum(t *testing.T) {
	var received struct {
		Model       string   `json:"model"`
		Prompt      string   `json:"prompt"`
		Size        string   `json:"size"`
		AspectRatio string   `json:"aspect_ratio"`
		ImageURLs   []string `json:"image_urls"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("path = %q, want /images/generations", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&received)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2UtYnl0ZXM=","mime_type":"image/png"}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{
		Kind: "image", Provider: "openai_custom", APIKey: "key",
		Model: "gpt-image-2", BaseURL: server.URL,
	})

	path, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{
		Prompt: "a wide poster", Size: "2K", AspectRatio: "16:9",
	})
	if err != nil {
		t.Fatal(err)
	}
	if received.Size != "1536x1024" {
		t.Fatalf("size = %q, want mapped 1536x1024 for 2K+16:9", received.Size)
	}
	if received.AspectRatio != "16:9" || received.Prompt != "a wide poster" || received.Model != "gpt-image-2" {
		t.Fatalf("unexpected request: %+v", received)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image-bytes" {
		t.Fatalf("file content = %q", data)
	}
}

// TestHandleImageGenerate_InputImagePathAndURL locks the acceptance criterion
// that InputImage accepts a file path, URL, or base64. A path is read and
// base64-injected (so the tool_use history only ever carries the path, never
// the payload); URLs pass through untouched.
func TestHandleImageGenerate_InputImagePathAndURL(t *testing.T) {
	source := []byte("source-png-bytes")
	refPath := filepath.Join(t.TempDir(), "ref.png")
	if err := os.WriteFile(refPath, source, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		input   string
		wantRef string
	}{
		{"file path is read and base64-injected", refPath, base64.StdEncoding.EncodeToString(source)},
		{"URL passes through unchanged", "https://example.com/ref.png", "https://example.com/ref.png"},
		{"raw base64 passes through unchanged", base64.StdEncoding.EncodeToString(source), base64.StdEncoding.EncodeToString(source)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var received struct {
				ImageURLs []string `json:"image_urls"`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewDecoder(r.Body).Decode(&received)
				_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2UtYnl0ZXM=","mime_type":"image/png"}]}`))
			}))
			defer server.Close()

			root := t.TempDir()
			a := &Actor{}
			a.cachedProjectRoot.Store(&root)
			ctx := mediaTestContext(server.URL, gen.MediaAccount{
				Kind: "image", Provider: "openai_custom", APIKey: "key",
				Model: "gpt-image-2", BaseURL: server.URL,
			})

			if _, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{Prompt: "edit", InputImage: tc.input}); err != nil {
				t.Fatal(err)
			}
			if len(received.ImageURLs) != 1 || received.ImageURLs[0] != tc.wantRef {
				t.Fatalf("image_urls = %v, want [%q]", received.ImageURLs, tc.wantRef)
			}
		})
	}
}

// TestHandleImageGenerate_AggregatorFallback covers the backwards-compatible
// aggregator route: no active media account, so the request resolves an image
// unit through aiaggregator.image_resolve and generates directly via imagegen,
// with the Size enum mapped and InputImage path/base64 resolved.
func TestHandleImageGenerate_AggregatorFallback(t *testing.T) {
	var received struct {
		Model  string `json:"model"`
		Size   string `json:"size"`
		Prompt string `json:"prompt"`
	}
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("path = %q, want /images/generations", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&received)
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2UtYnl0ZXM="}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	aggRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	a.aggRefCache = map[string]ref.Ref{systemAggID: aggRef}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false } // no media service
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
			if callID != "aiaggregator.image_resolve" {
				return nil, &unexpectedMediaCall{callID: callID}
			}
			return gen.AIAggregatorImageResolveResp{
				Model: "gpt-image-2", Endpoint: server.URL, ProviderName: "openai_custom",
				Protocol: "openai", AuthToken: "agg-token",
			}, nil
		}}
	}

	path, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{
		Prompt: "a tall poster", Size: "4K", AspectRatio: "9:16",
	})
	if err != nil {
		t.Fatal(err)
	}
	// gpt-image-2 relays reject every explicit size; the field must be omitted
	// so the server default ("auto") applies (see mapOpenAISize).
	if received.Size != "" {
		t.Fatalf("size = %q, want omitted for gpt-image-2", received.Size)
	}
	if received.Model != "gpt-image-2" || received.Prompt != "a tall poster" {
		t.Fatalf("unexpected request: %+v", received)
	}
	if gotAuth != "Bearer agg-token" {
		t.Fatalf("authorization = %q, want Bearer agg-token", gotAuth)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image-bytes" {
		t.Fatalf("file content = %q", data)
	}
}

// TestHandleImageGenerate_AggregatorFallbackInputImagePath covers the
// aggregator route with a file-path InputImage: the path is read and
// base64-injected so the native openai edits flow can decode it.
func TestHandleImageGenerate_AggregatorFallbackInputImagePath(t *testing.T) {
	source := []byte("edit-me-bytes")
	refPath := filepath.Join(t.TempDir(), "edit.png")
	if err := os.WriteFile(refPath, source, 0o644); err != nil {
		t.Fatal(err)
	}
	wantB64 := base64.StdEncoding.EncodeToString(source)

	var gotFileData []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			t.Errorf("path = %q, want /images/edits", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		if f, _, err := r.FormFile("image"); err == nil {
			buf := make([]byte, 64)
			n, _ := f.Read(buf)
			gotFileData = buf[:n]
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2UtYnl0ZXM="}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	aggRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	a.aggRefCache = map[string]ref.Ref{systemAggID: aggRef}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
			return gen.AIAggregatorImageResolveResp{
				Model: "gpt-image-2", Endpoint: server.URL, Protocol: "openai", AuthToken: "t",
			}, nil
		}}
	}

	if _, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{Prompt: "edit", InputImage: refPath}); err != nil {
		t.Fatal(err)
	}
	if string(gotFileData) != string(source) {
		t.Fatalf("multipart image file = %q, want %q (path was not decoded from %q)", gotFileData, source, wantB64)
	}
}

// TestDecodeImageResolveError locks the fix that a malformed resolve result is
// surfaced as an error instead of silently reporting "no model configured".
func TestDecodeImageResolveError(t *testing.T) {
	if _, err := decodeImageResolve(make(chan int)); err == nil {
		t.Fatal("expected an error for an unmarshalable resolve result")
	}
	if _, err := decodeImageResolve("not-a-resp-object"); err == nil {
		t.Fatal("expected an error for a non-object resolve result")
	}
	resp, err := decodeImageResolve(gen.AIAggregatorImageResolveResp{Model: "m", Endpoint: "e", Protocol: "openai"})
	if err != nil {
		t.Fatalf("typed resolve result should pass through: %v", err)
	}
	if resp.Model != "m" {
		t.Fatalf("model = %q, want m", resp.Model)
	}
}

// TestToolCallTimeoutForMediaTools locks the timeout-layering fix: media
// generation tools reserve the full media generation budget so a 4K generation
// is not cut by the generic 5-minute tool timeout, while the outer budget stays
// at or above the inner mediaGenerationTimeout everywhere.
func TestToolCallTimeoutForMediaTools(t *testing.T) {
	for _, callable := range []string{"image_generate", "video_generate"} {
		got := toolCallTimeoutFor(callable)
		if got != mediaGenerationTimeout {
			t.Fatalf("toolCallTimeoutFor(%q) = %v, want mediaGenerationTimeout %v", callable, got, mediaGenerationTimeout)
		}
		if got < domain.ToolCallTimeout {
			t.Fatalf("toolCallTimeoutFor(%q) = %v is below the generic %v", callable, got, domain.ToolCallTimeout)
		}
	}
	if got := toolCallTimeoutFor("file.write"); got != domain.ToolCallTimeout {
		t.Fatalf("toolCallTimeoutFor(file.write) = %v, want ToolCallTimeout %v", got, domain.ToolCallTimeout)
	}
}

// TestResolveInputImage locks the three-state InputImage normalization: file
// paths are read and base64-encoded, URLs and raw base64 pass through.
func TestResolveInputImage(t *testing.T) {
	source := []byte("ref-bytes")
	refPath := filepath.Join(t.TempDir(), "ref.png")
	if err := os.WriteFile(refPath, source, 0o644); err != nil {
		t.Fatal(err)
	}
	wantB64 := base64.StdEncoding.EncodeToString(source)

	if got := resolveInputImage(""); got != "" {
		t.Fatalf("resolveInputImage(\"\") = %q, want empty", got)
	}
	if got := resolveInputImage(refPath); got != wantB64 {
		t.Fatalf("resolveInputImage(path) = %q, want base64 %q", got, wantB64)
	}
	url := "https://example.com/ref.png"
	if got := resolveInputImage(url); got != url {
		t.Fatalf("resolveInputImage(url) = %q, want passthrough", got)
	}
	if got := resolveInputImage(wantB64); got != wantB64 {
		t.Fatalf("resolveInputImage(base64) = %q, want passthrough", got)
	}
}

// TestInputImageReference locks the mediagen reference-list form of the
// three-state handling.
func TestInputImageReference(t *testing.T) {
	if got := inputImageReference(""); got != nil {
		t.Fatalf("inputImageReference(\"\") = %v, want nil", got)
	}
	source := []byte("ref-bytes")
	refPath := filepath.Join(t.TempDir(), "ref.png")
	if err := os.WriteFile(refPath, source, 0o644); err != nil {
		t.Fatal(err)
	}
	wantB64 := base64.StdEncoding.EncodeToString(source)
	got := inputImageReference(refPath)
	if len(got) != 1 || got[0] != wantB64 {
		t.Fatalf("inputImageReference(path) = %v, want [%q]", got, wantB64)
	}
}

// TestIsGeminiProvider locks case-insensitive provider routing.
func TestIsGeminiProvider(t *testing.T) {
	for provider, want := range map[string]bool{
		"gemini":        true,
		"Gemini":        true,
		"GEMINI":        true,
		" gemini ":      true,
		"openai_custom": false,
		"glm":           false,
		"":              false,
	} {
		if got := isGeminiProvider(provider); got != want {
			t.Fatalf("isGeminiProvider(%q) = %v, want %v", provider, got, want)
		}
	}
}

// TestIsArkProvider locks case-insensitive, space-trimmed routing of the
// accepted Ark aliases ("ark", "volcengine", "byteplus", "doubao" — the last
// is the media panel's brand-facing id for Doubao/Seedance 2.0) to GenerateArk.
func TestIsArkProvider(t *testing.T) {
	for provider, want := range map[string]bool{
		"ark":           true,
		"Ark":           true,
		"ARK":           true,
		" ark ":         true,
		"volcengine":    true,
		"VolcEngine":    true,
		"VOLCENGINE":    true,
		"byteplus":      true,
		"BytePlus":      true,
		"BYTEPLUS":      true,
		" byteplus ":    true,
		"doubao":        true,
		"Doubao":        true,
		"DOUBAO":        true,
		" doubao ":      true,
		"gemini":        false,
		"openai_custom": false,
		"glm":           false,
		"":              false,
	} {
		if got := isArkProvider(provider); got != want {
			t.Fatalf("isArkProvider(%q) = %v, want %v", provider, got, want)
		}
	}
}

// TestIsDoubaoImageProvider locks case-insensitive, space-trimmed routing of
// the Doubao/Ark image aliases ("doubao", "imagex", "volcengine", "byteplus")
// to the native images/generations backend.
func TestIsDoubaoImageProvider(t *testing.T) {
	for provider, want := range map[string]bool{
		"doubao":        true,
		"Doubao":        true,
		"DOUBAO":        true,
		" doubao ":      true,
		"imagex":        true,
		"ImageX":        true,
		"volcengine":    true,
		"byteplus":      true,
		"gemini":        false,
		"minimax":       false,
		"openai_custom": false,
		"glm":           false,
		"":              false,
	} {
		if got := isDoubaoImageProvider(provider); got != want {
			t.Fatalf("isDoubaoImageProvider(%q) = %v, want %v", provider, got, want)
		}
	}
}

// TestMediaImageSize locks the Size-enum → dimensions mapping used for
// GPT-compatible media endpoints, mirroring imagegen's mapOpenAISize.
func TestMediaImageSize(t *testing.T) {
	tests := []struct {
		size   string
		aspect string
		want   string
	}{
		{"1K", "1:1", "1024x1024"},
		{"1K", "16:9", "1536x1024"},
		{"1K", "9:16", "1024x1536"},
		{"2K", "16:9", "1536x1024"},
		{"2K", "9:16", "1024x1536"},
		{"4K", "16:9", "1536x1024"},
		{"4K", "9:16", "1024x1536"},
		{"4K", "", "1024x1024"},
		{"unknown", "16:9", "1536x1024"},
		{"", "9:16", "1024x1536"},
	}
	for _, tc := range tests {
		if got := mediaImageSize(tc.size, tc.aspect); got != tc.want {
			t.Fatalf("mediaImageSize(%q, %q) = %q, want %q", tc.size, tc.aspect, got, tc.want)
		}
	}
}

// TestHandleImageGenerate_ReferenceImagesGeminiMultiPath locks multi-reference
// support on the native gemini media path: every ReferenceImages file path is
// read and base64-injected into one inlineData part, in order, followed by the
// text prompt part.
func TestHandleImageGenerate_ReferenceImagesGeminiMultiPath(t *testing.T) {
	sourceA := []byte("ref-a-bytes")
	sourceB := []byte("ref-b-bytes")
	refA := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(refA, sourceA, 0o644); err != nil {
		t.Fatal(err)
	}
	refB := filepath.Join(t.TempDir(), "b.png")
	if err := os.WriteFile(refB, sourceB, 0o644); err != nil {
		t.Fatal(err)
	}

	type part struct {
		Text       string `json:"text"`
		InlineData *struct {
			Data string `json:"data"`
		} `json:"inlineData"`
	}
	var gotParts []part
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Contents []struct {
				Parts []part `json:"parts"`
			} `json:"contents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		gotParts = body.Contents[0].Parts
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"inlineData":{"data":"aW1hZ2UtYnl0ZXM=","mimeType":"image/png"}}]}}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{
		Kind: "image", Provider: "gemini", APIKey: "gemini-key",
		Model: "gemini-3.1-flash-image-preview", BaseURL: server.URL,
	})

	if _, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{
		Prompt:          "edit both",
		ReferenceImages: []string{refA, refB},
	}); err != nil {
		t.Fatal(err)
	}
	wantA := base64.StdEncoding.EncodeToString(sourceA)
	wantB := base64.StdEncoding.EncodeToString(sourceB)
	if len(gotParts) != 3 {
		t.Fatalf("parts = %d, want 3 (two inlineData + text)", len(gotParts))
	}
	if gotParts[0].InlineData == nil || gotParts[0].InlineData.Data != wantA {
		t.Fatalf("part[0] = %+v, want inlineData %q", gotParts[0], wantA)
	}
	if gotParts[1].InlineData == nil || gotParts[1].InlineData.Data != wantB {
		t.Fatalf("part[1] = %+v, want inlineData %q", gotParts[1], wantB)
	}
	if gotParts[2].Text != "edit both" {
		t.Fatalf("part[2] = %+v, want text prompt", gotParts[2])
	}
}

// TestHandleImageGenerate_ReferenceImagesMediaOpenAIPath locks multi-reference
// support on the GPT-compatible media path: file paths are read and
// base64-injected into image_urls, URL references pass through, and order is
// preserved.
func TestHandleImageGenerate_ReferenceImagesMediaOpenAIPath(t *testing.T) {
	source := []byte("ref-c-bytes")
	refPath := filepath.Join(t.TempDir(), "c.png")
	if err := os.WriteFile(refPath, source, 0o644); err != nil {
		t.Fatal(err)
	}
	wantB64 := base64.StdEncoding.EncodeToString(source)
	urlRef := "https://example.com/ref.png"

	var received struct {
		ImageURLs []string `json:"image_urls"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/generations" {
			t.Errorf("path = %q, want /images/generations", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2UtYnl0ZXM=","mime_type":"image/png"}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{
		Kind: "image", Provider: "openai_custom", APIKey: "key",
		Model: "gpt-image-2", BaseURL: server.URL,
	})

	if _, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{
		Prompt:          "edit",
		ReferenceImages: []string{refPath, urlRef},
	}); err != nil {
		t.Fatal(err)
	}
	if len(received.ImageURLs) != 2 || received.ImageURLs[0] != wantB64 || received.ImageURLs[1] != urlRef {
		t.Fatalf("image_urls = %v, want [%q, %q]", received.ImageURLs, wantB64, urlRef)
	}
}

// TestHandleImageGenerate_InputImageAndReferenceImagesMerge locks the merge
// contract: when both InputImage and ReferenceImages are supplied, the single
// image is resolved and prepended so all references travel as one ordered
// list, matching the "merge (no dedup) is acceptable" acceptance criterion.
func TestHandleImageGenerate_InputImageAndReferenceImagesMerge(t *testing.T) {
	sourceInput := []byte("input-bytes")
	sourceRef := []byte("ref-d-bytes")
	inputPath := filepath.Join(t.TempDir(), "input.png")
	if err := os.WriteFile(inputPath, sourceInput, 0o644); err != nil {
		t.Fatal(err)
	}
	refPath := filepath.Join(t.TempDir(), "ref.png")
	if err := os.WriteFile(refPath, sourceRef, 0o644); err != nil {
		t.Fatal(err)
	}

	var received struct {
		ImageURLs []string `json:"image_urls"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2UtYnl0ZXM=","mime_type":"image/png"}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{
		Kind: "image", Provider: "openai_custom", APIKey: "key",
		Model: "gpt-image-2", BaseURL: server.URL,
	})

	if _, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{
		Prompt:          "edit",
		InputImage:      inputPath,
		ReferenceImages: []string{refPath},
	}); err != nil {
		t.Fatal(err)
	}
	wantInput := base64.StdEncoding.EncodeToString(sourceInput)
	wantRef := base64.StdEncoding.EncodeToString(sourceRef)
	want := []string{wantInput, wantRef}
	if len(received.ImageURLs) != len(want) {
		t.Fatalf("image_urls = %v, want %v", received.ImageURLs, want)
	}
	for i := range want {
		if received.ImageURLs[i] != want[i] {
			t.Fatalf("image_urls = %v, want %v (position %d)", received.ImageURLs, want, i)
		}
	}
}

// TestHandleImageGenerate_ReferenceImagesAggregatorPath locks multi-reference
// support on the aggregator fallback path: both file paths are read,
// base64-injected, and ride the /images/edits multipart body as two image[]
// form files.
func TestHandleImageGenerate_ReferenceImagesAggregatorPath(t *testing.T) {
	sourceA := []byte("agg-a-bytes")
	sourceB := []byte("agg-b-bytes")
	refA := filepath.Join(t.TempDir(), "a.png")
	if err := os.WriteFile(refA, sourceA, 0o644); err != nil {
		t.Fatal(err)
	}
	refB := filepath.Join(t.TempDir(), "b.png")
	if err := os.WriteFile(refB, sourceB, 0o644); err != nil {
		t.Fatal(err)
	}

	var gotFiles [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/edits" {
			t.Errorf("path = %q, want /images/edits", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			return
		}
		for _, key := range []string{"image", "image[]"} {
			for _, fh := range r.MultipartForm.File[key] {
				f, err := fh.Open()
				if err != nil {
					t.Errorf("open form file: %v", err)
					continue
				}
				data, _ := io.ReadAll(f)
				gotFiles = append(gotFiles, data)
				_ = f.Close()
			}
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2UtYnl0ZXM="}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	aggRef := testutil.NewFakeRef(testutil.GenActorID(), nil)
	a.aggRefCache = map[string]ref.Ref{systemAggID: aggRef}

	ctx := testutil.HumanCtx(testutil.GenActorID())
	ctx.LookupServiceFn = func(name string) (ref.Ref, bool) { return nil, false }
	ctx.PlannerFn = func() actor.Planner {
		return fakePlannerForInvoke{callFunc: func(_ context.Context, _ ref.Ref, callID string, _ any) (any, error) {
			if callID != "aiaggregator.image_resolve" {
				return nil, &unexpectedMediaCall{callID: callID}
			}
			return gen.AIAggregatorImageResolveResp{
				Model: "gpt-image-2", Endpoint: server.URL, Protocol: "openai", AuthToken: "t",
			}, nil
		}}
	}

	if _, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{
		Prompt:          "edit",
		ReferenceImages: []string{refA, refB},
	}); err != nil {
		t.Fatal(err)
	}
	if len(gotFiles) != 2 || string(gotFiles[0]) != string(sourceA) || string(gotFiles[1]) != string(sourceB) {
		t.Fatalf("multipart image files = %q, want [%q, %q]", gotFiles, sourceA, sourceB)
	}
}

// TestHandleImageGenerate_EmptyReferenceImages locks no-regression for an
// empty ReferenceImages array: the openai media path receives no image_urls
// and still succeeds, so an empty array cannot turn into a spurious reference.
func TestHandleImageGenerate_EmptyReferenceImages(t *testing.T) {
	var received struct {
		ImageURLs []string `json:"image_urls"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"aW1hZ2UtYnl0ZXM=","mime_type":"image/png"}]}`))
	}))
	defer server.Close()

	root := t.TempDir()
	a := &Actor{}
	a.cachedProjectRoot.Store(&root)
	ctx := mediaTestContext(server.URL, gen.MediaAccount{
		Kind: "image", Provider: "openai_custom", APIKey: "key",
		Model: "gpt-image-2", BaseURL: server.URL,
	})

	if _, err := a.handleImageGenerate(ctx, gen.ImageGenerateReq{
		Prompt:          "a poster",
		ReferenceImages: []string{},
	}); err != nil {
		t.Fatal(err)
	}
	if len(received.ImageURLs) != 0 {
		t.Fatalf("image_urls = %v, want none for an empty ReferenceImages array", received.ImageURLs)
	}
}
